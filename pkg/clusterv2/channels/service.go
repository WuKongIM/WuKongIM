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
)

const forwardAppendRecoveryTimeout = 100 * time.Millisecond

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
	return &Service{runtime: combined, localNode: cfg.LocalNode, metaSource: cfg.MetaSource, ensurer: ensurer, forward: cfg.Forward, observer: cfg.Observer}, nil
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

// Tick advances ChannelV2 background work.
func (s *Service) Tick(ctx context.Context) error { return s.runtime.Tick(ctx) }

// Close closes the ChannelV2 runtime.
func (s *Service) Close() error { return s.runtime.Close() }

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
			s.observeAppendStage("forward_append", err, time.Since(started))
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
			s.observeAppendStage("forward_append", err, time.Since(started))
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
