package app

import (
	"context"
	"errors"
	"sync"
	"time"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultCommittedReplayCursorName = "committed"
	defaultCommittedReplayBatchSize  = 512
	defaultCommittedReplayMaxBytes   = 4 << 20
	defaultCommittedReplayInterval   = 30 * time.Second
)

type committedReplayChannel struct {
	// Key is the durable channel log key used by the channel store.
	Key channel.ChannelKey
	// ID is the decoded channel identity used by channel runtime status APIs.
	ID channel.ChannelID
}

type committedReplayLog interface {
	ListCommittedReplayChannels(context.Context) ([]committedReplayChannel, error)
	LoadCommittedDispatchCursor(context.Context, channel.ChannelKey, string) (uint64, bool, error)
	StoreCommittedDispatchCursor(context.Context, channel.ChannelKey, string, uint64) error
	CommittedSeq(context.Context, channel.ChannelKey, channel.ChannelID) (uint64, error)
	LoadCommittedMessages(context.Context, channel.ChannelKey, channel.ChannelID, uint64, int, int) ([]channel.Message, error)
}

type committedReplayDelivery interface {
	SubmitCommitted(context.Context, deliveryruntime.CommittedEnvelope) error
}

type committedReplayConversation interface {
	SubmitCommitted(context.Context, channel.Message) error
	Flush(context.Context) error
}

type committedReplayMetrics interface {
	SetCommittedReplayLag(channelType string, lag uint64)
	ObserveCommittedReplayPass(result string, dur time.Duration)
}

type committedReplayerConfig struct {
	// Log is the durable source of committed messages and replay cursors.
	Log committedReplayLog
	// Delivery receives replayed committed messages for realtime fanout.
	Delivery committedReplayDelivery
	// Conversation receives replayed committed messages and must flush before cursor advance.
	Conversation committedReplayConversation
	// CursorName separates replay lanes in the per-channel cursor keyspace.
	CursorName string
	// BatchSize bounds messages processed per channel pass.
	BatchSize int
	// MaxBytes bounds bytes loaded per channel pass.
	MaxBytes int
	// Interval controls the steady-state background replay cadence.
	Interval time.Duration
	// Logger records replay failures without failing the send hot path.
	Logger wklog.Logger
	// Metrics observes replay pass duration and per-channel committed lag.
	Metrics committedReplayMetrics
}

// committedReplayer repairs missed async side effects from the durable channel log.
type committedReplayer struct {
	cfg      committedReplayerConfig
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	stopping bool
}

func newCommittedReplayer(cfg committedReplayerConfig) *committedReplayer {
	if cfg.CursorName == "" {
		cfg.CursorName = defaultCommittedReplayCursorName
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultCommittedReplayBatchSize
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultCommittedReplayMaxBytes
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultCommittedReplayInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = wklog.NewNop()
	}
	return &committedReplayer{cfg: cfg}
}

// Start launches the lightweight replay loop. It does not block ingress startup;
// any missed committed side effects are replayed from the durable channel log.
func (r *committedReplayer) Start(ctx context.Context) error {
	if r == nil || r.cfg.Log == nil {
		return channel.ErrInvalidConfig
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return nil
	}
	if r.stopping {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.done = make(chan struct{})
	go r.run(runCtx, r.done)
	return nil
}

func (r *committedReplayer) Stop() error {
	return r.StopContext(context.Background())
}

// StopContext cancels replay and bounds shutdown with the caller context.
func (r *committedReplayer) StopContext(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	if cancel == nil && r.stopping && done != nil {
		r.mu.Unlock()
		return r.waitStop(ctx, done)
	}
	r.cancel = nil
	if cancel != nil {
		r.stopping = true
	}
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	return r.waitStop(ctx, done)
}

func (r *committedReplayer) waitStop(ctx context.Context, done chan struct{}) error {
	select {
	case <-done:
		r.finishStop(done)
		return nil
	case <-ctx.Done():
		go func() {
			<-done
			r.finishStop(done)
		}()
		return ctx.Err()
	}
}

func (r *committedReplayer) finishStop(done chan struct{}) {
	r.mu.Lock()
	if r.done == done {
		r.done = nil
		r.stopping = false
	}
	r.mu.Unlock()
}

func (r *committedReplayer) run(ctx context.Context, done chan<- struct{}) {
	defer close(done)

	r.runOnceAndLog(ctx)
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnceAndLog(ctx)
		}
	}
}

func (r *committedReplayer) runOnceAndLog(ctx context.Context) {
	if err := r.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		r.cfg.Logger.Warn("committed replay pass failed", wklog.Error(err))
	}
}

// RunOnce replays committed messages after the persisted cursor and advances the
// cursor only after all side effects in the batch have been accepted and flushed.
func (r *committedReplayer) RunOnce(ctx context.Context) (err error) {
	startedAt := time.Now()
	defer func() {
		r.recordReplayPass(committedReplayPassResult(err), time.Since(startedAt))
	}()
	if r == nil || r.cfg.Log == nil {
		return channel.ErrInvalidConfig
	}
	if ctx == nil {
		ctx = context.Background()
	}
	channels, err := r.cfg.Log.ListCommittedReplayChannels(ctx)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.replayChannel(ctx, ch); err != nil {
			return err
		}
	}
	return nil
}

func (r *committedReplayer) replayChannel(ctx context.Context, ch committedReplayChannel) error {
	cursor, ok, err := r.cfg.Log.LoadCommittedDispatchCursor(ctx, ch.Key, r.cfg.CursorName)
	if err != nil {
		return err
	}
	if !ok {
		cursor = 0
	}
	for {
		committedSeq, err := r.cfg.Log.CommittedSeq(ctx, ch.Key, ch.ID)
		if err != nil {
			return err
		}
		r.recordReplayLag(ch.ID.Type, replayLag(cursor, committedSeq))
		if committedSeq == 0 || cursor >= committedSeq {
			return nil
		}

		messages, err := r.cfg.Log.LoadCommittedMessages(ctx, ch.Key, ch.ID, cursor+1, r.cfg.BatchSize, r.cfg.MaxBytes)
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}
		lastSeq := cursor
		for _, msg := range messages {
			if err := ctx.Err(); err != nil {
				return err
			}
			if msg.MessageSeq <= cursor {
				continue
			}
			if msg.MessageSeq != lastSeq+1 {
				return channel.ErrCorruptState
			}
			if msg.MessageSeq > committedSeq {
				break
			}
			if err := r.submitMessage(ctx, msg); err != nil {
				return err
			}
			lastSeq = msg.MessageSeq
		}
		if lastSeq == cursor {
			return nil
		}
		if r.cfg.Conversation != nil {
			if err := r.cfg.Conversation.Flush(ctx); err != nil {
				return err
			}
		}
		if err := r.cfg.Log.StoreCommittedDispatchCursor(ctx, ch.Key, r.cfg.CursorName, lastSeq); err != nil {
			return err
		}
		cursor = lastSeq
		if len(messages) < r.cfg.BatchSize {
			return nil
		}
	}
}

func (r *committedReplayer) submitMessage(ctx context.Context, msg channel.Message) error {
	if r.cfg.Delivery != nil {
		if err := r.cfg.Delivery.SubmitCommitted(ctx, deliveryruntime.CommittedEnvelope{Message: msg}); err != nil {
			return err
		}
	}
	if r.cfg.Conversation != nil {
		if err := r.cfg.Conversation.SubmitCommitted(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

func (r *committedReplayer) recordReplayLag(channelType uint8, lag uint64) {
	if r == nil || r.cfg.Metrics == nil {
		return
	}
	r.cfg.Metrics.SetCommittedReplayLag(deliveryChannelTypeLabel(channelType), lag)
}

func (r *committedReplayer) recordReplayPass(result string, dur time.Duration) {
	if r == nil || r.cfg.Metrics == nil {
		return
	}
	r.cfg.Metrics.ObserveCommittedReplayPass(result, dur)
}

func committedReplayPassResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}

func replayLag(cursor, committedSeq uint64) uint64 {
	if committedSeq <= cursor {
		return 0
	}
	return committedSeq - cursor
}

type channelStoreCommittedReplayLog struct {
	// engine owns the durable structured channel message rows and cursors.
	engine *channelstore.Engine
	// status reports the local channel runtime committed frontier.
	status interface {
		Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error)
	}
	// localNodeID limits replay to channels led by this node.
	localNodeID uint64
}

func (l channelStoreCommittedReplayLog) ListCommittedReplayChannels(ctx context.Context) ([]committedReplayChannel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keys, err := l.engine.ListChannelKeys()
	if err != nil {
		return nil, err
	}
	channels := make([]committedReplayChannel, 0, len(keys))
	for _, key := range keys {
		id, err := channelhandler.ParseChannelKey(key)
		if err != nil {
			return nil, err
		}
		channels = append(channels, committedReplayChannel{Key: key, ID: id})
	}
	return channels, nil
}

func (l channelStoreCommittedReplayLog) LoadCommittedDispatchCursor(_ context.Context, key channel.ChannelKey, name string) (uint64, bool, error) {
	id, err := channelhandler.ParseChannelKey(key)
	if err != nil {
		return 0, false, err
	}
	return l.engine.ForChannel(key, id).LoadCommittedDispatchCursor(name)
}

func (l channelStoreCommittedReplayLog) StoreCommittedDispatchCursor(_ context.Context, key channel.ChannelKey, name string, seq uint64) error {
	id, err := channelhandler.ParseChannelKey(key)
	if err != nil {
		return err
	}
	return l.engine.ForChannel(key, id).StoreCommittedDispatchCursor(name, seq)
}

func (l channelStoreCommittedReplayLog) CommittedSeq(_ context.Context, _ channel.ChannelKey, id channel.ChannelID) (uint64, error) {
	if l.status == nil {
		return 0, nil
	}
	status, err := l.status.Status(id)
	if err != nil {
		return 0, nil
	}
	if l.localNodeID != 0 && uint64(status.Leader) != l.localNodeID {
		return 0, nil
	}
	return status.CommittedSeq, nil
}

func (l channelStoreCommittedReplayLog) LoadCommittedMessages(_ context.Context, key channel.ChannelKey, id channel.ChannelID, fromSeq uint64, limit int, maxBytes int) ([]channel.Message, error) {
	return l.engine.ForChannel(key, id).ListMessagesBySeq(fromSeq, limit, maxBytes, false)
}
