package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type appChannelCluster struct {
	service channel.MetaRollbackService
	runtime channel.Runtime
	closers []func() error

	localNodeID    uint64
	remoteAppender remoteChannelAppender

	closeOnce sync.Once
	closeErr  error

	applyMu    sync.Mutex
	applyLocks map[channel.ChannelKey]*appChannelApplyLock

	metricsMu      sync.Mutex
	metrics        *obsmetrics.Registry
	activeChannels map[channel.ChannelKey]struct{}
	logger         wklog.Logger
}

type appChannelApplyLock struct {
	mu   sync.Mutex
	refs int
}

type remoteChannelAppender interface {
	AppendToLeader(ctx context.Context, nodeID uint64, req channel.AppendRequest) (channel.AppendResult, error)
}

func newAppChannelCluster(
	store *channelstore.Engine,
	rt channelruntime.Runtime,
	transport *channeltransport.Transport,
	messageIDs channel.MessageIDGenerator,
	localNodeID uint64,
	logger wklog.Logger,
) (*appChannelCluster, error) {
	if store == nil || rt == nil || transport == nil || messageIDs == nil {
		return nil, channel.ErrInvalidConfig
	}
	service, err := channelhandler.New(channelhandler.Config{
		Runtime:    rt,
		Store:      store,
		MessageIDs: messageIDs,
	})
	if err != nil {
		return nil, err
	}
	transport.BindFetchService(rt)
	return &appChannelCluster{
		service:     service,
		runtime:     appChannelRuntimeControl{runtime: rt},
		localNodeID: localNodeID,
		logger:      logger,
		closers: []func() error{
			transport.Close,
			rt.Close,
		},
	}, nil
}

func (c *appChannelCluster) appendLogger() wklog.Logger {
	if c == nil || c.logger == nil {
		return wklog.NewNop()
	}
	return c.logger.Named("channel.append")
}

func (c *appChannelCluster) ApplyMeta(meta channel.Meta) error {
	if c == nil || c.service == nil || c.runtime == nil {
		return channel.ErrInvalidConfig
	}
	key := meta.Key
	if key == "" {
		key = channelhandler.KeyFromChannelID(meta.ID)
		meta.Key = key
	}
	unlock := c.lockApplyMeta(key)
	defer unlock()

	previous, ok := c.service.MetaSnapshot(key)
	if err := c.ApplyRoutingMeta(meta); err != nil {
		return err
	}
	var runtimeErr error
	if meta.Status == channel.StatusDeleted {
		runtimeErr = c.RemoveLocalRuntime(key)
	} else {
		runtimeErr = c.EnsureLocalRuntime(meta)
	}
	if runtimeErr == nil {
		return nil
	}
	c.service.RestoreMeta(key, previous, ok)
	return runtimeErr
}

func (c *appChannelCluster) lockApplyMeta(key channel.ChannelKey) func() {
	c.applyMu.Lock()
	if c.applyLocks == nil {
		c.applyLocks = make(map[channel.ChannelKey]*appChannelApplyLock)
	}
	lock, ok := c.applyLocks[key]
	if !ok {
		lock = &appChannelApplyLock{}
		c.applyLocks[key] = lock
	}
	lock.refs++
	c.applyMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()

		c.applyMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(c.applyLocks, key)
		}
		c.applyMu.Unlock()
	}
}

func (c *appChannelCluster) Append(ctx context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
	if c == nil || c.service == nil {
		return channel.AppendResult{}, channel.ErrInvalidConfig
	}
	start := time.Now()
	result, err := c.service.Append(ctx, req)
	if err == nil {
		sendtrace.Record(sendtrace.Event{
			Stage:       sendtrace.StageChannelAppendLocal,
			At:          start,
			Duration:    sendtrace.Elapsed(start, time.Now()),
			NodeID:      c.localNodeID,
			ChannelKey:  string(channelhandler.KeyFromChannelID(req.ChannelID)),
			ClientMsgNo: req.Message.ClientMsgNo,
			MessageSeq:  result.MessageSeq,
		})
	}
	if errors.Is(err, channel.ErrNotLeader) || errors.Is(err, channel.ErrStaleMeta) {
		// A known remote leader can still serve the append after local runtime
		// refresh removes this node from replicas.
		if forwarded, forwardErr, ok := c.forwardAppendToLeader(ctx, req); ok {
			result, err = forwarded, forwardErr
		}
	}
	c.observeAppend(err, time.Since(start))
	return result, err
}

func (c *appChannelCluster) Fetch(ctx context.Context, req channel.FetchRequest) (channel.FetchResult, error) {
	if c == nil || c.service == nil {
		return channel.FetchResult{}, channel.ErrInvalidConfig
	}
	start := time.Now()
	result, err := c.service.Fetch(ctx, req)
	c.observeFetch(time.Since(start))
	return result, err
}

func (c *appChannelCluster) Status(id channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	if c == nil || c.service == nil {
		return channel.ChannelRuntimeStatus{}, channel.ErrInvalidConfig
	}
	return c.service.Status(id)
}

func (c *appChannelCluster) MetaSnapshot(key channel.ChannelKey) (channel.Meta, bool) {
	if c == nil || c.service == nil {
		return channel.Meta{}, false
	}
	return c.service.MetaSnapshot(key)
}

func (c *appChannelCluster) RestoreMeta(key channel.ChannelKey, meta channel.Meta, ok bool) {
	if c == nil || c.service == nil {
		return
	}
	c.service.RestoreMeta(key, meta, ok)
}

func (c *appChannelCluster) RemoveLocal(key channel.ChannelKey) error {
	return c.RemoveLocalRuntime(key)
}

func (c *appChannelCluster) ApplyRoutingMeta(meta channel.Meta) error {
	if c == nil {
		return channel.ErrInvalidConfig
	}
	key := meta.Key
	if key == "" {
		key = channelhandler.KeyFromChannelID(meta.ID)
		meta.Key = key
	}
	if c.service == nil {
		return channel.ErrInvalidConfig
	}
	return c.service.ApplyMeta(meta)
}

func (c *appChannelCluster) EnsureLocalRuntime(meta channel.Meta) error {
	if c == nil || c.runtime == nil {
		return channel.ErrInvalidConfig
	}
	key := meta.Key
	if key == "" {
		key = channelhandler.KeyFromChannelID(meta.ID)
		meta.Key = key
	}
	if err := c.runtime.UpsertMeta(meta); err != nil {
		return err
	}
	c.setChannelActive(key, true)
	return nil
}

func (c *appChannelCluster) RemoveLocalRuntime(key channel.ChannelKey) error {
	if c == nil {
		return nil
	}
	var err error
	if c.runtime != nil {
		err = c.runtime.RemoveChannel(key)
		if errors.Is(err, channel.ErrChannelNotFound) {
			err = nil
		}
	}
	c.setChannelActive(key, false)
	return err
}

func (c *appChannelCluster) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		for _, closeFn := range c.closers {
			if closeFn == nil {
				continue
			}
			c.closeErr = errors.Join(c.closeErr, closeFn())
		}
		c.metricsMu.Lock()
		c.activeChannels = nil
		if c.metrics != nil {
			c.metrics.Channel.SetActiveChannels(0)
		}
		c.metricsMu.Unlock()
	})
	return c.closeErr
}

func (c *appChannelCluster) observeAppend(err error, dur time.Duration) {
	if c == nil || c.metrics == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	c.metrics.Channel.ObserveAppend(result, dur)
}

func (c *appChannelCluster) observeFetch(dur time.Duration) {
	if c == nil || c.metrics == nil {
		return
	}
	c.metrics.Channel.ObserveFetch(dur)
}

func (c *appChannelCluster) setChannelActive(key channel.ChannelKey, active bool) {
	if c == nil || c.metrics == nil {
		return
	}
	c.metricsMu.Lock()
	defer c.metricsMu.Unlock()
	if c.activeChannels == nil {
		c.activeChannels = make(map[channel.ChannelKey]struct{})
	}
	if active {
		c.activeChannels[key] = struct{}{}
	} else {
		delete(c.activeChannels, key)
	}
	c.metrics.Channel.SetActiveChannels(len(c.activeChannels))
}

func (c *appChannelCluster) forwardAppendToLeader(ctx context.Context, req channel.AppendRequest) (channel.AppendResult, error, bool) {
	if c == nil || c.remoteAppender == nil {
		return channel.AppendResult{}, nil, false
	}
	meta, ok := c.service.MetaSnapshot(channelhandler.KeyFromChannelID(req.ChannelID))
	if !ok || meta.Leader == 0 {
		c.appendLogger().Debug("skip forwarding channel append without leader metadata",
			wklog.Event("app.channel.append.forward.skipped"),
			wklog.NodeID(c.localNodeID),
			wklog.ChannelID(req.ChannelID.ID),
			wklog.ChannelType(int64(req.ChannelID.Type)),
			wklog.String("clientMsgNo", req.Message.ClientMsgNo),
			wklog.Reason("missing_leader_metadata"),
		)
		return channel.AppendResult{}, nil, false
	}
	leaderID := uint64(meta.Leader)
	if leaderID == 0 || leaderID == c.localNodeID {
		c.appendLogger().Debug("skip forwarding channel append because local node is still selected as leader",
			wklog.Event("app.channel.append.forward.skipped"),
			wklog.NodeID(c.localNodeID),
			wklog.ChannelID(req.ChannelID.ID),
			wklog.ChannelType(int64(req.ChannelID.Type)),
			wklog.String("clientMsgNo", req.Message.ClientMsgNo),
			wklog.Reason("leader_is_local"),
		)
		return channel.AppendResult{}, nil, false
	}
	c.appendLogger().Debug("forwarding channel append to leader",
		wklog.Event("app.channel.append.forward.triggered"),
		wklog.NodeID(c.localNodeID),
		wklog.LeaderNodeID(leaderID),
		wklog.ChannelID(req.ChannelID.ID),
		wklog.ChannelType(int64(req.ChannelID.Type)),
		wklog.String("clientMsgNo", req.Message.ClientMsgNo),
	)
	startedAt := time.Now()
	result, err := c.remoteAppender.AppendToLeader(ctx, leaderID, req)
	completedFields := []wklog.Field{
		wklog.Event("app.channel.append.forward.completed"),
		wklog.NodeID(c.localNodeID),
		wklog.LeaderNodeID(leaderID),
		wklog.ChannelID(req.ChannelID.ID),
		wklog.ChannelType(int64(req.ChannelID.Type)),
		wklog.String("clientMsgNo", req.Message.ClientMsgNo),
		wklog.Duration("duration", time.Since(startedAt)),
	}
	if err != nil {
		completedFields = append(completedFields, wklog.Error(err))
	}
	c.appendLogger().Debug("completed forwarded channel append", completedFields...)
	if err == nil {
		sendtrace.Record(sendtrace.Event{
			Stage:       sendtrace.StageChannelAppendForward,
			At:          startedAt,
			Duration:    sendtrace.Elapsed(startedAt, time.Now()),
			NodeID:      c.localNodeID,
			PeerNodeID:  leaderID,
			ChannelKey:  string(meta.Key),
			ClientMsgNo: req.Message.ClientMsgNo,
			MessageSeq:  result.MessageSeq,
		})
	}
	return result, err, true
}

type appChannelRuntimeControl struct {
	runtime channelruntime.Runtime
}

func (c appChannelRuntimeControl) UpsertMeta(meta channel.Meta) error {
	if c.runtime == nil {
		return channel.ErrInvalidConfig
	}
	if err := c.runtime.EnsureChannel(meta); err != nil {
		if errors.Is(err, channelruntime.ErrChannelExists) {
			return c.runtime.ApplyMeta(meta)
		}
		return err
	}
	return nil
}

func (c appChannelRuntimeControl) RemoveChannel(key channel.ChannelKey) error {
	if c.runtime == nil {
		return channel.ErrInvalidConfig
	}
	if err := c.runtime.RemoveChannel(key); err != nil && !errors.Is(err, channel.ErrChannelNotFound) {
		return err
	}
	return nil
}

func (c appChannelRuntimeControl) Close() error {
	if c.runtime == nil {
		return nil
	}
	return c.runtime.Close()
}

func containsNodeID(values []channel.NodeID, target channel.NodeID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
