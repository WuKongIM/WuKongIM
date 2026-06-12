package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	channelservice "github.com/WuKongIM/WuKongIM/pkg/channelv2/service"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const forwardAppendRecoveryTimeout = 100 * time.Millisecond

const (
	appendStageForwardAppend       = "forward_append"
	appendStageForwardAppendRPC    = "forward_append_rpc"
	appendStageForwardAppendRemote = "forward_append_remote"
)

type channelRuntime interface {
	ch.Cluster
	channeltransport.Server
}

// AppendStageObserver receives low-cardinality client append stage latencies.
type AppendStageObserver interface {
	ObserveChannelAppendStage(stage string, result string, d time.Duration)
}

// ForwardClient forwards client append calls to the authoritative channel leader.
type ForwardClient interface {
	// ForwardAppend forwards one append request to node.
	ForwardAppend(context.Context, ch.NodeID, ch.AppendRequest) (ch.AppendResult, error)
	// ForwardAppendBatch forwards one append batch request to node.
	ForwardAppendBatch(context.Context, ch.NodeID, ch.AppendBatchRequest) (ch.AppendBatchResult, error)
	// ForwardLastVisible forwards one last-visible message read to node.
	ForwardLastVisible(context.Context, ch.NodeID, LastVisibleRequest) (LastVisibleResponse, error)
}

// LastVisibleRequest reads the newest committed channel message above a visibility floor.
type LastVisibleRequest struct {
	// ChannelID identifies the channel-owned message log.
	ChannelID ch.ChannelID
	// VisibleAfterSeq hides messages at or below this sequence.
	VisibleAfterSeq uint64
	// ExpectedLeader is the channel leader resolved by the origin node.
	ExpectedLeader ch.NodeID
	// ExpectedChannelEpoch is the channel epoch resolved by the origin node.
	ExpectedChannelEpoch uint64
	// ExpectedLeaderEpoch is the leader epoch resolved by the origin node.
	ExpectedLeaderEpoch uint64
}

// LastVisibleResponse contains a routed last-visible message read result.
type LastVisibleResponse struct {
	// Message is set when Found is true.
	Message ch.Message
	// Found reports whether a visible message exists.
	Found bool
}

// Config wires a ChannelV2 service wrapper.
type Config struct {
	// Runtime optionally supplies an already constructed ChannelV2 runtime.
	Runtime any
	// LocalNode is this node's ChannelV2 node ID when constructing Runtime.
	LocalNode ch.NodeID
	// ReactorCount is the number of ChannelV2 reactor partitions.
	ReactorCount int
	// StoreAppendWorkers caps blocking leader append store workers. Zero keeps the ChannelV2 runtime default.
	StoreAppendWorkers int
	// StoreApplyWorkers caps blocking follower apply store workers. Zero keeps the ChannelV2 runtime default.
	StoreApplyWorkers int
	// RPCWorkers caps blocking ChannelV2 replication RPC workers. Zero keeps the ChannelV2 runtime default.
	RPCWorkers int
	// MailboxSize bounds each ChannelV2 reactor mailbox.
	MailboxSize int
	// MaxChannels bounds loaded ChannelV2 runtimes on this node. Zero keeps unlimited behavior.
	MaxChannels int
	// AppendBatchMaxRecords is the queued ChannelV2 record count that triggers a store append flush.
	AppendBatchMaxRecords int
	// AppendBatchMaxWait is the maximum age of the oldest queued ChannelV2 append before flushing.
	AppendBatchMaxWait time.Duration
	// FollowerRecoveryProbeInterval is the base delay for parked follower recovery probes. Zero uses the runtime default.
	FollowerRecoveryProbeInterval time.Duration
	// FollowerRecoveryProbeJitter spreads parked follower recovery probes across this bounded window.
	FollowerRecoveryProbeJitter time.Duration
	// Observer receives lightweight ChannelV2 reactor and worker metrics.
	Observer reactor.Observer
	// Store opens ChannelV2 stores when constructing Runtime.
	Store channelstore.Factory
	// Transport sends ChannelV2 replication RPCs when constructing Runtime.
	Transport channeltransport.Client
	// MetaSource resolves authoritative channel metadata.
	MetaSource ChannelMetaSource
	// Forward sends client append calls to the resolved channel leader.
	Forward ForwardClient
}

// Service wraps ChannelV2 and exposes both client and replication surfaces.
type Service struct {
	runtime    channelRuntime
	localNode  ch.NodeID
	metaSource ChannelMetaSource
	ensurer    ChannelMetaEnsurer
	forward    ForwardClient
	store      channelstore.Factory
	metaCache  channelMetaCache
	observer   any
}

// NewService creates a Service from cfg.
func NewService(cfg Config) (*Service, error) {
	runtime := cfg.Runtime
	if cfg.Forward == nil {
		if forward, ok := cfg.Transport.(ForwardClient); ok {
			cfg.Forward = forward
		}
	}
	if runtime == nil {
		cluster, err := channelservice.New(channelservice.Config{
			LocalNode:                     cfg.LocalNode,
			ReactorCount:                  cfg.ReactorCount,
			StoreAppendWorkers:            cfg.StoreAppendWorkers,
			StoreApplyWorkers:             cfg.StoreApplyWorkers,
			RPCWorkers:                    cfg.RPCWorkers,
			MailboxSize:                   cfg.MailboxSize,
			MaxChannels:                   cfg.MaxChannels,
			AppendBatchMaxRecords:         cfg.AppendBatchMaxRecords,
			AppendBatchMaxWait:            cfg.AppendBatchMaxWait,
			FollowerRecoveryProbeInterval: cfg.FollowerRecoveryProbeInterval,
			FollowerRecoveryProbeJitter:   cfg.FollowerRecoveryProbeJitter,
			Store:                         cfg.Store,
			Transport:                     cfg.Transport,
			Observer:                      cfg.Observer,
		})
		if err != nil {
			return nil, err
		}
		runtime = cluster
	}
	combined, ok := runtime.(channelRuntime)
	if !ok {
		return nil, fmt.Errorf("channels: runtime must implement channelv2.Cluster and channelv2/transport.Server")
	}
	ensurer, _ := cfg.MetaSource.(ChannelMetaEnsurer)
	return &Service{runtime: combined, localNode: cfg.LocalNode, metaSource: cfg.MetaSource, ensurer: ensurer, forward: cfg.Forward, store: cfg.Store, observer: cfg.Observer}, nil
}

// Runtime returns the ChannelV2 public cluster surface.
func (s *Service) Runtime() ch.Cluster { return s.runtime }

// Server returns the ChannelV2 replication server surface.
func (s *Service) Server() channeltransport.Server { return s.runtime }

// ApplyMeta applies authoritative metadata to the local ChannelV2 runtime.
func (s *Service) ApplyMeta(meta ch.Meta) error { return s.runtime.ApplyMeta(meta) }

// Append appends one message.
func (s *Service) Append(ctx context.Context, req ch.AppendRequest) (ch.AppendResult, error) {
	res, err, usedCache := s.appendOnce(ctx, req)
	if err == nil || !usedCache || !retryableMetaCacheError(err) {
		return res, err
	}
	s.metaCache.invalidate(req.ChannelID)
	s.observeMetaCache("invalidate")
	return s.appendFresh(ctx, req)
}

// AppendBatch appends messages to one channel.
func (s *Service) AppendBatch(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	res, err, usedCache := s.appendBatchOnce(ctx, req)
	if err == nil || !usedCache || !retryableMetaCacheError(err) {
		return res, err
	}
	s.metaCache.invalidate(req.ChannelID)
	s.observeMetaCache("invalidate")
	return s.appendBatchFresh(ctx, req)
}

// ResolveAppendAuthority resolves the current append authority using append metadata admission.
func (s *Service) ResolveAppendAuthority(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	started := time.Now()
	meta, ok, err := s.resolveAppendMetaFresh(ctx, id)
	s.observeAppendStage("meta_resolve", err, time.Since(started))
	if err != nil {
		return ch.Meta{}, err
	}
	if !ok || meta.Leader == 0 {
		return ch.Meta{}, ch.ErrNotReady
	}
	return meta, nil
}

// Tick advances ChannelV2 background work.
func (s *Service) Tick(ctx context.Context) error { return s.runtime.Tick(ctx) }

// Close closes the ChannelV2 runtime.
func (s *Service) Close() error { return s.runtime.Close() }

// ReadChannelLastVisible reads the newest visible message from the authoritative channel leader.
func (s *Service) ReadChannelLastVisible(ctx context.Context, id ch.ChannelID, visibleAfterSeq uint64) (ch.Message, bool, error) {
	meta, ok, err := s.resolveReadMeta(ctx, id)
	if err != nil {
		return ch.Message{}, false, err
	}
	if !ok || meta.Leader == 0 {
		return ch.Message{}, false, ch.ErrNotReady
	}
	if meta.Leader != s.localNode {
		if s.forward == nil {
			return ch.Message{}, false, ch.ErrNotLeader
		}
		resp, err := s.forward.ForwardLastVisible(ctx, meta.Leader, LastVisibleRequest{
			ChannelID:            id,
			VisibleAfterSeq:      visibleAfterSeq,
			ExpectedLeader:       meta.Leader,
			ExpectedChannelEpoch: meta.Epoch,
			ExpectedLeaderEpoch:  meta.LeaderEpoch,
		})
		if err != nil {
			return ch.Message{}, false, err
		}
		resp.Message.Payload = append([]byte(nil), resp.Message.Payload...)
		return resp.Message, resp.Found, nil
	}
	return s.readLocalLastVisible(ctx, id, visibleAfterSeq)
}

func (s *Service) handleForwardLastVisible(ctx context.Context, req LastVisibleRequest) (LastVisibleResponse, error) {
	meta, ok, err := s.resolveReadMeta(ctx, req.ChannelID)
	if err != nil && !canFallbackLastVisibleOnMissingMeta(s.localNode, req, err) {
		return LastVisibleResponse{}, err
	}
	if err != nil && canFallbackLastVisibleOnMissingMeta(s.localNode, req, err) {
		msg, ok, readErr := s.readLocalLastVisible(ctx, req.ChannelID, req.VisibleAfterSeq)
		return LastVisibleResponse{Message: msg, Found: ok}, readErr
	}
	if !ok || meta.Leader == 0 {
		return LastVisibleResponse{}, ch.ErrNotReady
	}
	if meta.Leader != s.localNode {
		return LastVisibleResponse{}, ch.ErrNotLeader
	}
	if req.ExpectedLeader != 0 && req.ExpectedLeader != s.localNode {
		return LastVisibleResponse{}, ch.ErrNotLeader
	}
	if metaOlderThanRequest(meta, req) {
		return LastVisibleResponse{}, ch.ErrStaleMeta
	}
	msg, ok, err := s.readLocalLastVisible(ctx, req.ChannelID, req.VisibleAfterSeq)
	return LastVisibleResponse{Message: msg, Found: ok}, err
}

func (s *Service) readLocalLastVisible(ctx context.Context, id ch.ChannelID, visibleAfterSeq uint64) (ch.Message, bool, error) {
	if s == nil || s.store == nil {
		return ch.Message{}, false, ch.ErrNotReady
	}
	store, err := s.store.ChannelStore(ch.ChannelKeyForID(id), id)
	if err != nil {
		return ch.Message{}, false, err
	}
	read, err := store.ReadCommitted(ctx, channelstore.ReadCommittedRequest{
		FromSeq:  maxUint64(),
		MaxSeq:   maxUint64(),
		Limit:    1,
		MaxBytes: maxInt(),
		Reverse:  true,
	})
	if err != nil {
		return ch.Message{}, false, err
	}
	for _, msg := range read.Messages {
		if msg.MessageSeq <= visibleAfterSeq {
			continue
		}
		msg.Payload = append([]byte(nil), msg.Payload...)
		return msg, true, nil
	}
	return ch.Message{}, false, nil
}

func canFallbackLastVisibleOnMissingMeta(local ch.NodeID, req LastVisibleRequest, err error) bool {
	return (channelErrorMatches(err, ch.ErrChannelNotFound) || errors.Is(err, metadb.ErrNotFound)) &&
		req.ExpectedLeader == local &&
		req.ExpectedChannelEpoch != 0 &&
		req.ExpectedLeaderEpoch != 0
}

func metaOlderThanRequest(meta ch.Meta, req LastVisibleRequest) bool {
	return (req.ExpectedChannelEpoch != 0 && meta.Epoch < req.ExpectedChannelEpoch) ||
		(req.ExpectedLeaderEpoch != 0 && meta.LeaderEpoch < req.ExpectedLeaderEpoch)
}

func (s *Service) appendOnce(ctx context.Context, req ch.AppendRequest) (ch.AppendResult, error, bool) {
	started := time.Now()
	meta, ok, usedCache, err := s.resolveAppendMetaCached(ctx, req.ChannelID)
	s.observeAppendStage("meta_resolve", err, time.Since(started))
	if err != nil {
		return ch.AppendResult{}, err, usedCache
	}
	res, err := s.appendWithMeta(ctx, req, meta, ok)
	return res, err, usedCache
}

func (s *Service) appendFresh(ctx context.Context, req ch.AppendRequest) (ch.AppendResult, error) {
	started := time.Now()
	meta, ok, err := s.resolveAppendMetaFresh(ctx, req.ChannelID)
	s.observeAppendStage("meta_resolve", err, time.Since(started))
	if err != nil {
		return ch.AppendResult{}, err
	}
	return s.appendWithMeta(ctx, req, meta, ok)
}

func (s *Service) appendWithMeta(ctx context.Context, req ch.AppendRequest, meta ch.Meta, ok bool) (ch.AppendResult, error) {
	if ok {
		if meta.Leader != 0 && meta.Leader != s.localNode {
			if s.forward == nil {
				return ch.AppendResult{}, ch.ErrNotLeader
			}
			started := time.Now()
			res, err := s.forward.ForwardAppend(ctx, meta.Leader, req)
			forwardRPC := time.Since(started)
			s.observeAppendStage(appendStageForwardAppendRPC, err, forwardRPC)
			s.observeAppendStage(appendStageForwardAppend, err, forwardRPC)
			if err != nil {
				recoverStarted := time.Now()
				batch, recovered := s.recoverForwardAppendBatch(ctx, meta, ch.AppendBatchRequest{
					ChannelID:            req.ChannelID,
					Messages:             []ch.Message{req.Message},
					CommitMode:           req.CommitMode,
					ExpectedChannelEpoch: req.ExpectedChannelEpoch,
					ExpectedLeaderEpoch:  req.ExpectedLeaderEpoch,
				}, err)
				s.observeAppendStage("forward_append_recover", recoveredAppendError(recovered, err), time.Since(recoverStarted))
				if recovered && len(batch.Items) == 1 && batch.Items[0].Err == nil {
					item := batch.Items[0]
					return ch.AppendResult{MessageID: item.MessageID, MessageSeq: item.MessageSeq, Message: item.Message}, nil
				}
			}
			return res, err
		}
		started := time.Now()
		err := s.runtime.ApplyMeta(meta)
		s.observeAppendStage("meta_apply", err, time.Since(started))
		if err != nil {
			return ch.AppendResult{}, err
		}
	}
	started := time.Now()
	res, err := s.runtime.Append(ctx, req)
	s.observeAppendStage("runtime_append", err, time.Since(started))
	return res, err
}

func (s *Service) appendBatchOnce(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error, bool) {
	started := time.Now()
	meta, ok, usedCache, err := s.resolveAppendMetaCached(ctx, req.ChannelID)
	s.observeAppendStage("meta_resolve", err, time.Since(started))
	if err != nil {
		return ch.AppendBatchResult{}, err, usedCache
	}
	res, err := s.appendBatchWithMeta(ctx, req, meta, ok)
	return res, err, usedCache
}

func (s *Service) appendBatchFresh(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	started := time.Now()
	meta, ok, err := s.resolveAppendMetaFresh(ctx, req.ChannelID)
	s.observeAppendStage("meta_resolve", err, time.Since(started))
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	return s.appendBatchWithMeta(ctx, req, meta, ok)
}

func (s *Service) appendBatchWithMeta(ctx context.Context, req ch.AppendBatchRequest, meta ch.Meta, ok bool) (ch.AppendBatchResult, error) {
	if ok {
		if meta.Leader != 0 && meta.Leader != s.localNode {
			if s.forward == nil {
				return ch.AppendBatchResult{}, ch.ErrNotLeader
			}
			started := time.Now()
			res, err := s.forward.ForwardAppendBatch(ctx, meta.Leader, req)
			forwardRPC := time.Since(started)
			s.observeAppendStage(appendStageForwardAppendRPC, err, forwardRPC)
			s.observeAppendStage(appendStageForwardAppend, err, forwardRPC)
			if err != nil {
				recoverStarted := time.Now()
				recovered, ok := s.recoverForwardAppendBatch(ctx, meta, req, err)
				s.observeAppendStage("forward_append_recover", recoveredAppendError(ok, err), time.Since(recoverStarted))
				if ok {
					return recovered, nil
				}
			}
			return res, err
		}
		started := time.Now()
		err := s.runtime.ApplyMeta(meta)
		s.observeAppendStage("meta_apply", err, time.Since(started))
		if err != nil {
			return ch.AppendBatchResult{}, err
		}
	}
	started := time.Now()
	res, err := s.runtime.AppendBatch(ctx, req)
	s.observeAppendStage("runtime_append", err, time.Since(started))
	return res, err
}

func (s *Service) recoverForwardAppendBatch(_ context.Context, meta ch.Meta, req ch.AppendBatchRequest, forwardErr error) (ch.AppendBatchResult, bool) {
	if !recoverableForwardDeadline(forwardErr) || !localNodeIsReplica(meta, s.localNode) {
		return ch.AppendBatchResult{}, false
	}
	lookup, ok := s.runtime.(ch.CommittedMessageLookup)
	if !ok {
		return ch.AppendBatchResult{}, false
	}
	recoverCtx, cancel := context.WithTimeout(context.Background(), forwardAppendRecoveryTimeout)
	defer cancel()
	items := make([]ch.AppendBatchItemResult, len(req.Messages))
	recovered := false
	for i, msg := range req.Messages {
		items[i].Err = forwardErr
		if msg.MessageID == 0 {
			continue
		}
		committed, ok, err := lookup.LookupCommittedMessage(recoverCtx, req.ChannelID, msg.MessageID)
		if err != nil {
			if recovered {
				return ch.AppendBatchResult{Items: items}, true
			}
			return ch.AppendBatchResult{}, false
		}
		if !ok {
			continue
		}
		items[i] = ch.AppendBatchItemResult{
			MessageID:  committed.MessageID,
			MessageSeq: committed.MessageSeq,
			Message:    committed,
		}
		recovered = true
	}
	if !recovered {
		return ch.AppendBatchResult{}, false
	}
	return ch.AppendBatchResult{Items: items}, true
}

func recoverableForwardDeadline(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || (err != nil && strings.Contains(err.Error(), context.DeadlineExceeded.Error()))
}

func localNodeIsReplica(meta ch.Meta, local ch.NodeID) bool {
	for _, replica := range meta.Replicas {
		if replica == local {
			return true
		}
	}
	return false
}

func recoveredAppendError(recovered bool, err error) error {
	if recovered {
		return nil
	}
	return err
}

func (s *Service) resolveAppendMetaCached(ctx context.Context, id ch.ChannelID) (ch.Meta, bool, bool, error) {
	if s == nil {
		return ch.Meta{}, false, false, nil
	}
	if meta, ok := s.metaCache.get(id); ok {
		s.observeMetaCache("hit")
		return meta, true, true, nil
	}
	if s.ensurer != nil || s.metaSource != nil {
		s.observeMetaCache("miss")
	}
	meta, ok, err := s.resolveAppendMetaFresh(ctx, id)
	return meta, ok, ok && cacheableAppendMeta(id, meta), err
}

func (s *Service) resolveAppendMetaFresh(ctx context.Context, id ch.ChannelID) (ch.Meta, bool, error) {
	meta, ok, err := s.resolveAppendMeta(ctx, id)
	if err != nil || !ok {
		return meta, ok, err
	}
	if cacheableAppendMeta(id, meta) {
		s.metaCache.put(id, meta)
	}
	return meta, ok, nil
}

func retryableMetaCacheError(err error) bool {
	return channelErrorMatches(err, ch.ErrStaleMeta) ||
		channelErrorMatches(err, ch.ErrChannelNotFound) ||
		channelErrorMatches(err, ch.ErrNotLeader) ||
		channelErrorMatches(err, ch.ErrNotReplica) ||
		channelErrorMatches(err, ch.ErrNotReady)
}

func channelErrorMatches(err error, sentinel error) bool {
	return errors.Is(err, sentinel) || (err != nil && sentinel != nil && strings.Contains(err.Error(), sentinel.Error()))
}

func (s *Service) observeMetaCache(result string) {
	if s == nil || s.observer == nil {
		return
	}
	observer, ok := s.observer.(MetaCacheObserver)
	if !ok {
		return
	}
	observer.ObserveChannelMetaCache(result)
}

func (s *Service) observeAppendStage(stage string, err error, d time.Duration) {
	if s == nil || s.observer == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	observer, ok := s.observer.(AppendStageObserver)
	if !ok {
		return
	}
	observer.ObserveChannelAppendStage(stage, result, d)
}

func (s *Service) resolveAppendMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, bool, error) {
	if s == nil {
		return ch.Meta{}, false, nil
	}
	if s.ensurer != nil {
		meta, err := s.ensurer.EnsureChannelMeta(ctx, id)
		if err != nil {
			return ch.Meta{}, true, err
		}
		return normalizeAppendMeta(id, meta)
	}
	if s.metaSource == nil {
		return ch.Meta{}, false, nil
	}
	meta, err := s.metaSource.ResolveChannelMeta(ctx, id)
	if err != nil {
		return ch.Meta{}, true, err
	}
	return normalizeAppendMeta(id, meta)
}

func (s *Service) resolveReadMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, bool, error) {
	if s == nil {
		return ch.Meta{}, false, nil
	}
	if s.metaSource == nil {
		return ch.Meta{}, false, nil
	}
	meta, err := s.metaSource.ResolveChannelMeta(ctx, id)
	if err != nil {
		return ch.Meta{}, true, err
	}
	return normalizeAppendMeta(id, meta)
}

func normalizeAppendMeta(id ch.ChannelID, meta ch.Meta) (ch.Meta, bool, error) {
	if meta.ID == (ch.ChannelID{}) {
		meta.ID = id
	}
	if meta.Key == "" {
		meta.Key = ch.ChannelKeyForID(meta.ID)
	}
	if meta.ID != id || meta.Key != ch.ChannelKeyForID(id) {
		return ch.Meta{}, true, ch.ErrStaleMeta
	}
	return meta, true, nil
}

func maxUint64() uint64 {
	return ^uint64(0)
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
