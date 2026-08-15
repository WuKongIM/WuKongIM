package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	channelservice "github.com/WuKongIM/WuKongIM/pkg/channel/service"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const forwardAppendRecoveryTimeout = 100 * time.Millisecond

const channelMetaApplyLockCount = 256

const (
	appendStageForwardAppend       = "forward_append"
	appendStageForwardAppendRPC    = "forward_append_rpc"
	appendStageForwardAppendRemote = "forward_append_remote"
)

type channelRuntime interface {
	ch.Cluster
	channeltransport.Server
}

// conversationRuntimeProbe reads current Leader HW without forcing one
// durable checkpoint write per quorum-committed append.
type conversationRuntimeProbe interface {
	RuntimeProbe(context.Context, ch.RuntimeSelector) (ch.RuntimeProbeResult, error)
}

// AppendStageObserver receives low-cardinality client append stage latencies.
type AppendStageObserver interface {
	ObserveChannelAppendStage(stage string, result string, d time.Duration)
}

// ConversationHydrationObserver receives bounded directory hydration costs.
type ConversationHydrationObserver interface {
	ObserveConversationHydrationBatch(result string, items, remoteCalls, localReads int, duration time.Duration)
}

// ForwardClient forwards client append calls to the authoritative channel leader.
type ForwardClient interface {
	// ForwardAppend forwards one append request to node.
	ForwardAppend(context.Context, ch.NodeID, ch.AppendRequest) (ch.AppendResult, error)
	// ForwardAppendBatch forwards one append batch request to node.
	ForwardAppendBatch(context.Context, ch.NodeID, ch.AppendBatchRequest) (ch.AppendBatchResult, error)
	// ForwardLastVisible forwards one last-visible message read to node.
	ForwardLastVisible(context.Context, ch.NodeID, LastVisibleRequest) (LastVisibleResponse, error)
	// ForwardConversationHeads forwards one aligned conversation-head batch to node.
	ForwardConversationHeads(context.Context, ch.NodeID, ConversationHeadsRequest) (ConversationHeadsResponse, error)
	// ForwardCommittedReads forwards one aligned committed-message batch to node.
	ForwardCommittedReads(context.Context, ch.NodeID, CommittedReadsRequest) (CommittedReadsResponse, error)
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
	// HeadUID requests the complete conversation-head tuple for this user.
	// Empty preserves the narrow last-visible read.
	HeadUID string
	// ExpectedMinISR lets a leader whose metadata follower is briefly behind
	// distinguish single-replica committed LEO from a quorum checkpoint.
	ExpectedMinISR int
}

// LastVisibleResponse contains a routed last-visible message read result.
type LastVisibleResponse struct {
	// Message is set when Found is true.
	Message ch.Message
	// Found reports whether a visible message exists.
	Found bool
	// LastCommittedSeq is the channel commit boundary used for badge math.
	LastCommittedSeq uint64
	// RetentionThroughSeq is the effective logical compaction floor.
	RetentionThroughSeq uint64
	// CurrentUserLastSendSeq is the latest sender-index sequence at or below
	// LastCommittedSeq.
	CurrentUserLastSendSeq uint64
}

// ConversationHead is the bounded leader-owned state needed to construct one
// membership-backed conversation.
type ConversationHead struct {
	// LastCommittedSeq is the authoritative badge-count upper boundary.
	LastCommittedSeq uint64
	// RetentionThroughSeq is the logical message compaction floor.
	RetentionThroughSeq uint64
	// CurrentUserLastSendSeq is the user's latest committed sender-index entry.
	CurrentUserLastSendSeq uint64
	// Message is the newest membership-visible message when Found is true.
	Message ch.Message
	// Found reports whether Message is present above all visibility floors.
	Found bool
}

// ConversationHeadRequest carries the origin node's route fence for one
// channel in a same-leader conversation-head batch.
type ConversationHeadRequest struct {
	// ChannelID identifies the channel-owned message log.
	ChannelID ch.ChannelID
	// RetentionThroughSeq is the origin's slot-authoritative compaction floor.
	RetentionThroughSeq uint64
	// ExpectedLeader fences the request to the resolved Channel Leader.
	ExpectedLeader ch.NodeID
	// ExpectedChannelEpoch rejects a stale channel generation.
	ExpectedChannelEpoch uint64
	// ExpectedLeaderEpoch rejects a stale Channel Leader term.
	ExpectedLeaderEpoch uint64
	// ExpectedMinISR preserves quorum commit semantics during metadata lag.
	ExpectedMinISR int
}

// ConversationHeadsRequest reads one user's head tuple for channels that the
// origin grouped onto the same leader.
type ConversationHeadsRequest struct {
	// UID selects the sender-index sequence used for every aligned item.
	UID string
	// Items contains channel reads already grouped to one exact leader.
	Items []ConversationHeadRequest
}

// ConversationHeadResult is aligned with one requested channel. Routing and
// channel lifecycle failures stay item-scoped.
type ConversationHeadResult struct {
	// Head contains the bounded leader-owned conversation state on success.
	Head ConversationHead
	// Err is item-scoped so transient failures do not discard sibling results.
	Err error
}

// ConversationHeadsResponse preserves request item ordering.
type ConversationHeadsResponse struct {
	// Items is positionally aligned with ConversationHeadsRequest.Items.
	Items []ConversationHeadResult
}

// CommittedRead describes one client-visible committed-message read.
type CommittedRead struct {
	// ChannelID identifies the channel-owned message log.
	ChannelID ch.ChannelID
	// Request contains the bounded committed range and direction.
	Request channelstore.ReadCommittedRequest
}

// CommittedReadRequest carries one read and the origin node's route fence.
type CommittedReadRequest struct {
	CommittedRead
	// RetentionThroughSeq is the origin's slot-authoritative compaction floor.
	RetentionThroughSeq uint64
	// ExpectedLeader fences the request to the resolved Channel Leader.
	ExpectedLeader ch.NodeID
	// ExpectedChannelEpoch rejects a stale channel generation.
	ExpectedChannelEpoch uint64
	// ExpectedLeaderEpoch rejects a stale Channel Leader term.
	ExpectedLeaderEpoch uint64
	// ExpectedMinISR preserves quorum commit semantics during metadata lag.
	ExpectedMinISR int
}

// CommittedReadsRequest contains reads already grouped onto one exact leader.
type CommittedReadsRequest struct {
	// Items contains reads already grouped to one exact Channel Leader.
	Items []CommittedReadRequest
}

// CommittedReadResult is aligned with one requested channel.
type CommittedReadResult struct {
	// Read contains the committed message page on success.
	Read channelstore.ReadCommittedResult
	// Err is item-scoped so one channel failure does not discard siblings.
	Err error
}

// CommittedReadsResponse preserves request item ordering.
type CommittedReadsResponse struct {
	// Items is positionally aligned with CommittedReadsRequest.Items.
	Items []CommittedReadResult
}

// Config wires a Channel service wrapper.
type Config struct {
	// Runtime optionally supplies an already constructed Channel runtime.
	Runtime any
	// LocalNode is this node's Channel node ID when constructing Runtime.
	LocalNode ch.NodeID
	// ReactorCount is the number of Channel reactor partitions.
	ReactorCount int
	// StoreAppendWorkers caps blocking leader append store workers. Zero keeps the Channel runtime default.
	StoreAppendWorkers int
	// StoreAppendBatchMaxWait overrides store-append worker cross-channel coalescing wait. Zero keeps the Channel worker default.
	StoreAppendBatchMaxWait time.Duration
	// StoreApplyWorkers caps blocking follower apply store workers. Zero keeps the Channel runtime default.
	StoreApplyWorkers int
	// RPCWorkers caps blocking Channel replication RPC workers. Zero keeps the Channel runtime default.
	RPCWorkers int
	// RPCBatchMaxItems caps same-target Channel Pull or PullHint items in one
	// blocking transport call. Zero keeps the Channel runtime default.
	RPCBatchMaxItems int
	// MailboxSize bounds each Channel reactor mailbox.
	MailboxSize int
	// MaxChannels bounds loaded Channel runtimes on this node. Zero keeps unlimited behavior.
	MaxChannels int
	// AppendBatchMaxRecords is the queued Channel record count that triggers a store append flush.
	AppendBatchMaxRecords int
	// AppendBatchMaxWait is the maximum age of the oldest queued Channel append before flushing.
	AppendBatchMaxWait time.Duration
	// AppendBatchAdaptiveFlush enables a shorter cold-channel flush delay before the normal append batch window.
	AppendBatchAdaptiveFlush bool
	// AppendBatchColdMaxWait is the cold-channel flush delay used when AppendBatchAdaptiveFlush is enabled.
	AppendBatchColdMaxWait time.Duration
	// FollowerRecoveryProbeInterval is the base delay for parked follower recovery probes. Zero uses the runtime default.
	FollowerRecoveryProbeInterval time.Duration
	// FollowerRecoveryProbeJitter spreads parked follower recovery probes across this bounded window.
	FollowerRecoveryProbeJitter time.Duration
	// Observer receives lightweight Channel reactor and worker metrics.
	Observer reactor.Observer
	// AppendAdmissionGuard can reject local leader appends before Channel reactor admission.
	AppendAdmissionGuard ch.AppendAdmissionGuard
	// Store opens Channel stores when constructing Runtime.
	Store channelstore.Factory
	// Transport sends Channel replication RPCs when constructing Runtime.
	Transport channeltransport.Client
	// MetaSource resolves authoritative channel metadata.
	MetaSource ChannelMetaSource
	// Forward sends client append calls to the resolved channel leader.
	Forward ForwardClient
	// MigrationStore exposes Slot-backed migration task and fence commands.
	MigrationStore *MigrationStore
}

// Service wraps Channel and exposes both client and replication surfaces.
type Service struct {
	runtime    channelRuntime
	localNode  ch.NodeID
	metaSource ChannelMetaSource
	ensurer    ChannelMetaEnsurer
	forward    ForwardClient
	store      channelstore.Factory
	metaCache  channelMetaCache
	// metaApplyLocks serialize complete metadata application per channel shard.
	// They prevent a delayed cached apply from following a newer explicit apply.
	metaApplyLocks [channelMetaApplyLockCount]sync.Mutex
	observer       any
	migration      *MigrationStore
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
			StoreAppendBatchMaxWait:       cfg.StoreAppendBatchMaxWait,
			StoreApplyWorkers:             cfg.StoreApplyWorkers,
			RPCWorkers:                    cfg.RPCWorkers,
			RPCBatchMaxItems:              cfg.RPCBatchMaxItems,
			MailboxSize:                   cfg.MailboxSize,
			MaxChannels:                   cfg.MaxChannels,
			AppendBatchMaxRecords:         cfg.AppendBatchMaxRecords,
			AppendBatchMaxWait:            cfg.AppendBatchMaxWait,
			AppendBatchAdaptiveFlush:      cfg.AppendBatchAdaptiveFlush,
			AppendBatchColdMaxWait:        cfg.AppendBatchColdMaxWait,
			FollowerRecoveryProbeInterval: cfg.FollowerRecoveryProbeInterval,
			FollowerRecoveryProbeJitter:   cfg.FollowerRecoveryProbeJitter,
			AppendAdmissionGuard:          cfg.AppendAdmissionGuard,
			Store:                         cfg.Store,
			Transport:                     cfg.Transport,
			MetaResolver:                  cfg.MetaSource,
			Observer:                      cfg.Observer,
		})
		if err != nil {
			return nil, err
		}
		runtime = cluster
	}
	combined, ok := runtime.(channelRuntime)
	if !ok {
		return nil, fmt.Errorf("channels: runtime must implement channel.Cluster and channel/transport.Server")
	}
	ensurer, _ := cfg.MetaSource.(ChannelMetaEnsurer)
	return &Service{runtime: combined, localNode: cfg.LocalNode, metaSource: cfg.MetaSource, ensurer: ensurer, forward: cfg.Forward, store: cfg.Store, observer: cfg.Observer, migration: cfg.MigrationStore}, nil
}

// Runtime returns the Channel public cluster surface.
func (s *Service) Runtime() ch.Cluster { return s.runtime }

// Server returns the Channel replication server surface.
func (s *Service) Server() channeltransport.Server { return s.runtime }

// MigrationStore returns the Slot-backed migration facade, when configured.
func (s *Service) MigrationStore() *MigrationStore {
	if s == nil {
		return nil
	}
	return s.migration
}

// ApplyMeta applies authoritative metadata to the local Channel runtime and
// advances the append cache through the same per-channel ordering boundary.
func (s *Service) ApplyMeta(meta ch.Meta) error { return s.applyRuntimeMeta(meta, true) }

// Append appends one message.
func (s *Service) Append(ctx context.Context, req ch.AppendRequest) (ch.AppendResult, error) {
	res, err, usedMeta, usedCache := s.appendOnce(ctx, req)
	if err == nil || !usedCache || !retryableMetaCacheError(err) {
		return res, err
	}
	if s.metaCache.invalidateUsedMeta(req.ChannelID, usedMeta) {
		s.observeMetaCache("invalidate")
	}
	return s.appendFresh(ctx, req)
}

// AppendBatch appends messages to one channel.
func (s *Service) AppendBatch(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	res, err, usedMeta, usedCache := s.appendBatchOnce(ctx, req)
	if err == nil || !usedCache || !retryableMetaCacheError(err) {
		return res, err
	}
	if s.metaCache.invalidateUsedMeta(req.ChannelID, usedMeta) {
		s.observeMetaCache("invalidate")
	}
	return s.appendBatchFresh(ctx, req)
}

// ResolveAppendAuthority resolves the current append authority using append metadata admission.
func (s *Service) ResolveAppendAuthority(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	started := time.Now()
	meta, ok, _, err := s.resolveAppendMetaCached(ctx, id)
	s.observeAppendStage("meta_resolve", err, time.Since(started))
	if err != nil {
		return ch.Meta{}, err
	}
	if !ok || !cacheableAppendMeta(id, meta) {
		return ch.Meta{}, unavailableAppendMetaError(meta)
	}
	return meta, nil
}

// InvalidateAppendAuthority removes the cached authority only when it still
// matches the failed route version observed by the caller.
func (s *Service) InvalidateAppendAuthority(id ch.ChannelID, leader ch.NodeID, epoch uint64, leaderEpoch uint64, routeGeneration uint64) {
	if s == nil {
		return
	}
	if s.metaCache.invalidateAuthority(id, leader, epoch, leaderEpoch, routeGeneration) {
		s.observeMetaCache("invalidate")
	}
}

// Tick advances Channel background work.
func (s *Service) Tick(ctx context.Context) error { return s.runtime.Tick(ctx) }

// Close stops metadata-create admission before closing the Channel runtime.
func (s *Service) Close() error {
	var errs []error
	if closer, ok := s.metaSource.(interface{ Close() error }); ok {
		errs = append(errs, closer.Close())
	}
	if s.runtime != nil {
		errs = append(errs, s.runtime.Close())
	}
	return errors.Join(errs...)
}

// ReadChannelLastVisible reads the newest visible message from the authoritative channel leader.
func (s *Service) ReadChannelLastVisible(ctx context.Context, id ch.ChannelID, visibleAfterSeq uint64) (ch.Message, bool, error) {
	meta, ok, err := s.resolveReadMeta(ctx, id)
	if err != nil {
		return ch.Message{}, false, err
	}
	if !ok || meta.Leader == 0 {
		return ch.Message{}, false, ch.ErrNotReady
	}
	visibleAfterSeq = maxUint64Value(visibleAfterSeq, meta.RetentionThroughSeq)
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

// ReadConversationHead reads committed head, retention, latest ordinary
// message, and the current user's latest send from the authoritative leader.
func (s *Service) ReadConversationHead(ctx context.Context, id ch.ChannelID, uid string) (ConversationHead, error) {
	if uid == "" {
		return ConversationHead{}, ch.ErrInvalidConfig
	}
	meta, ok, err := s.resolveReadMeta(ctx, id)
	if err != nil {
		return ConversationHead{}, err
	}
	if !ok || meta.Leader == 0 {
		return ConversationHead{}, ch.ErrNotReady
	}
	if meta.Status == ch.StatusDeleting || meta.Status == ch.StatusDeleted {
		return ConversationHead{}, ch.ErrChannelNotFound
	}
	if meta.Leader != s.localNode {
		if s.forward == nil {
			return ConversationHead{}, ch.ErrNotLeader
		}
		resp, err := s.forward.ForwardLastVisible(ctx, meta.Leader, LastVisibleRequest{
			ChannelID:            id,
			VisibleAfterSeq:      meta.RetentionThroughSeq,
			ExpectedLeader:       meta.Leader,
			ExpectedChannelEpoch: meta.Epoch,
			ExpectedLeaderEpoch:  meta.LeaderEpoch,
			HeadUID:              uid,
			ExpectedMinISR:       meta.MinISR,
		})
		if err != nil {
			return ConversationHead{}, err
		}
		resp.Message.Payload = append([]byte(nil), resp.Message.Payload...)
		return conversationHeadFromResponse(resp), nil
	}
	result := s.readLocalConversationHeads(ctx, uid, []ConversationHeadRequest{{
		ChannelID:            id,
		RetentionThroughSeq:  meta.RetentionThroughSeq,
		ExpectedLeader:       meta.Leader,
		ExpectedChannelEpoch: meta.Epoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedMinISR:       meta.MinISR,
	}})[0]
	return result.Head, result.Err
}

// ReadConversationHeads resolves the current route for every channel, groups
// remote reads by exact leader, and returns one result aligned with every ID.
func (s *Service) ReadConversationHeads(ctx context.Context, ids []ch.ChannelID, uid string) ([]ConversationHeadResult, error) {
	started := time.Now()
	resultLabel := "ok"
	remoteCalls := 0
	localReads := 0
	defer func() {
		s.observeConversationHydrationBatch(resultLabel, len(ids), remoteCalls, localReads, time.Since(started))
	}()
	if uid == "" {
		resultLabel = "error"
		return nil, ch.ErrInvalidConfig
	}
	results := make([]ConversationHeadResult, len(ids))
	if len(ids) == 0 {
		return results, nil
	}
	type remoteItem struct {
		index   int
		request ConversationHeadRequest
	}
	localItems := make([]remoteItem, 0, len(ids))
	remoteByLeader := make(map[ch.NodeID][]remoteItem)
	for index, id := range ids {
		if err := ctx.Err(); err != nil {
			resultLabel = "error"
			return nil, err
		}
		meta, ok, err := s.resolveReadMeta(ctx, id)
		if err != nil {
			results[index].Err = err
			continue
		}
		if !ok || meta.Leader == 0 {
			results[index].Err = ch.ErrNotReady
			continue
		}
		if meta.Status == ch.StatusDeleting || meta.Status == ch.StatusDeleted {
			results[index].Err = ch.ErrChannelNotFound
			continue
		}
		if meta.Leader == s.localNode {
			localItems = append(localItems, remoteItem{index: index, request: ConversationHeadRequest{
				ChannelID:            id,
				RetentionThroughSeq:  meta.RetentionThroughSeq,
				ExpectedLeader:       meta.Leader,
				ExpectedChannelEpoch: meta.Epoch,
				ExpectedLeaderEpoch:  meta.LeaderEpoch,
				ExpectedMinISR:       meta.MinISR,
			}})
			continue
		}
		remoteByLeader[meta.Leader] = append(remoteByLeader[meta.Leader], remoteItem{index: index, request: ConversationHeadRequest{
			ChannelID:            id,
			RetentionThroughSeq:  meta.RetentionThroughSeq,
			ExpectedLeader:       meta.Leader,
			ExpectedChannelEpoch: meta.Epoch,
			ExpectedLeaderEpoch:  meta.LeaderEpoch,
			ExpectedMinISR:       meta.MinISR,
		}})
	}
	if len(localItems) > 0 {
		requests := make([]ConversationHeadRequest, len(localItems))
		for index, item := range localItems {
			requests[index] = item.request
		}
		localResults := s.readLocalConversationHeads(ctx, uid, requests)
		localReads += len(localItems)
		for index, item := range localItems {
			results[item.index] = localResults[index]
		}
	}
	for leader, items := range remoteByLeader {
		if s.forward == nil {
			for _, item := range items {
				results[item.index].Err = ch.ErrNotLeader
			}
			continue
		}
		request := ConversationHeadsRequest{UID: uid, Items: make([]ConversationHeadRequest, len(items))}
		for index, item := range items {
			request.Items[index] = item.request
		}
		response, err := s.forward.ForwardConversationHeads(ctx, leader, request)
		remoteCalls++
		if err != nil {
			for _, item := range items {
				results[item.index].Err = err
			}
			continue
		}
		if len(response.Items) != len(items) {
			for _, item := range items {
				results[item.index].Err = ch.ErrInvalidConfig
			}
			continue
		}
		localReads += len(items)
		for index, item := range items {
			result := response.Items[index]
			result.Head.Message.Payload = append([]byte(nil), result.Head.Message.Payload...)
			results[item.index] = result
		}
	}
	return results, nil
}

func (s *Service) observeConversationHydrationBatch(result string, items, remoteCalls, localReads int, duration time.Duration) {
	if s == nil || s.observer == nil {
		return
	}
	observer, ok := s.observer.(ConversationHydrationObserver)
	if !ok {
		return
	}
	observer.ObserveConversationHydrationBatch(result, items, remoteCalls, localReads, duration)
}

func (s *Service) handleForwardConversationHeads(ctx context.Context, req ConversationHeadsRequest) (ConversationHeadsResponse, error) {
	if req.UID == "" {
		return ConversationHeadsResponse{}, ch.ErrInvalidConfig
	}
	response := ConversationHeadsResponse{Items: make([]ConversationHeadResult, len(req.Items))}
	localItems := make([]ConversationHeadRequest, 0, len(req.Items))
	localIndexes := make([]int, 0, len(req.Items))
	for index, item := range req.Items {
		meta, ok, err := s.resolveReadMeta(ctx, item.ChannelID)
		if err != nil && !canFallbackConversationHeadOnMissingMeta(s.localNode, item, err) {
			response.Items[index].Err = err
			continue
		}
		if err != nil {
			localItems = append(localItems, item)
			localIndexes = append(localIndexes, index)
			continue
		}
		if !ok || meta.Leader == 0 {
			response.Items[index].Err = ch.ErrNotReady
			continue
		}
		if meta.Status == ch.StatusDeleting || meta.Status == ch.StatusDeleted {
			response.Items[index].Err = ch.ErrChannelNotFound
			continue
		}
		if meta.Leader != s.localNode || (item.ExpectedLeader != 0 && item.ExpectedLeader != s.localNode) {
			response.Items[index].Err = ch.ErrNotLeader
			continue
		}
		if (item.ExpectedChannelEpoch != 0 && meta.Epoch < item.ExpectedChannelEpoch) ||
			(item.ExpectedLeaderEpoch != 0 && meta.LeaderEpoch < item.ExpectedLeaderEpoch) {
			response.Items[index].Err = ch.ErrStaleMeta
			continue
		}
		item.RetentionThroughSeq = maxUint64Value(item.RetentionThroughSeq, meta.RetentionThroughSeq)
		item.ExpectedChannelEpoch = meta.Epoch
		item.ExpectedLeaderEpoch = meta.LeaderEpoch
		item.ExpectedMinISR = meta.MinISR
		localItems = append(localItems, item)
		localIndexes = append(localIndexes, index)
	}
	localResults := s.readLocalConversationHeads(ctx, req.UID, localItems)
	for index, result := range localResults {
		response.Items[localIndexes[index]] = result
	}
	return response, nil
}

// ReadCommittedBatch resolves every channel route, groups remote reads by
// exact leader, and preserves the caller's item ordering.
func (s *Service) ReadCommittedBatch(ctx context.Context, reads []CommittedRead) ([]CommittedReadResult, error) {
	results := make([]CommittedReadResult, len(reads))
	if len(reads) == 0 {
		return results, nil
	}
	type remoteItem struct {
		index   int
		request CommittedReadRequest
	}
	remoteByLeader := make(map[ch.NodeID][]remoteItem)
	for index, read := range reads {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		meta, ok, err := s.resolveReadMeta(ctx, read.ChannelID)
		if err != nil {
			results[index].Err = err
			continue
		}
		if !ok || meta.Leader == 0 {
			results[index].Err = ch.ErrNotReady
			continue
		}
		if meta.Status == ch.StatusDeleting || meta.Status == ch.StatusDeleted {
			results[index].Err = ch.ErrChannelNotFound
			continue
		}
		if meta.Leader == s.localNode {
			results[index].Read, results[index].Err = s.readLocalCommitted(ctx, read, meta.RetentionThroughSeq, meta.MinISR)
			continue
		}
		remoteByLeader[meta.Leader] = append(remoteByLeader[meta.Leader], remoteItem{index: index, request: CommittedReadRequest{
			CommittedRead:        read,
			RetentionThroughSeq:  meta.RetentionThroughSeq,
			ExpectedLeader:       meta.Leader,
			ExpectedChannelEpoch: meta.Epoch,
			ExpectedLeaderEpoch:  meta.LeaderEpoch,
			ExpectedMinISR:       meta.MinISR,
		}})
	}
	for leader, items := range remoteByLeader {
		if s.forward == nil {
			for _, item := range items {
				results[item.index].Err = ch.ErrNotLeader
			}
			continue
		}
		request := CommittedReadsRequest{Items: make([]CommittedReadRequest, len(items))}
		for index, item := range items {
			request.Items[index] = item.request
		}
		response, err := s.forward.ForwardCommittedReads(ctx, leader, request)
		if err != nil {
			for _, item := range items {
				results[item.index].Err = err
			}
			continue
		}
		if len(response.Items) != len(items) {
			for _, item := range items {
				results[item.index].Err = ch.ErrInvalidConfig
			}
			continue
		}
		for index, item := range items {
			result := response.Items[index]
			result.Read.Messages = cloneMessages(result.Read.Messages)
			results[item.index] = result
		}
	}
	return results, nil
}

func (s *Service) handleForwardCommittedReads(ctx context.Context, req CommittedReadsRequest) (CommittedReadsResponse, error) {
	response := CommittedReadsResponse{Items: make([]CommittedReadResult, len(req.Items))}
	for index, item := range req.Items {
		meta, ok, err := s.resolveReadMeta(ctx, item.ChannelID)
		if err != nil && !canFallbackCommittedReadOnMissingMeta(s.localNode, item, err) {
			response.Items[index].Err = err
			continue
		}
		if err != nil {
			response.Items[index].Read, response.Items[index].Err = s.readLocalCommitted(ctx, item.CommittedRead, item.RetentionThroughSeq, item.ExpectedMinISR)
			continue
		}
		if !ok || meta.Leader == 0 {
			response.Items[index].Err = ch.ErrNotReady
			continue
		}
		if meta.Status == ch.StatusDeleting || meta.Status == ch.StatusDeleted {
			response.Items[index].Err = ch.ErrChannelNotFound
			continue
		}
		if meta.Leader != s.localNode || (item.ExpectedLeader != 0 && item.ExpectedLeader != s.localNode) {
			response.Items[index].Err = ch.ErrNotLeader
			continue
		}
		if (item.ExpectedChannelEpoch != 0 && meta.Epoch < item.ExpectedChannelEpoch) ||
			(item.ExpectedLeaderEpoch != 0 && meta.LeaderEpoch < item.ExpectedLeaderEpoch) {
			response.Items[index].Err = ch.ErrStaleMeta
			continue
		}
		retentionThroughSeq := maxUint64Value(item.RetentionThroughSeq, meta.RetentionThroughSeq)
		response.Items[index].Read, response.Items[index].Err = s.readLocalCommitted(ctx, item.CommittedRead, retentionThroughSeq, meta.MinISR)
	}
	return response, nil
}

func (s *Service) readLocalCommitted(ctx context.Context, read CommittedRead, retentionThroughSeq uint64, minISR int) (channelstore.ReadCommittedResult, error) {
	if s == nil || s.store == nil {
		return channelstore.ReadCommittedResult{}, ch.ErrNotReady
	}
	store, err := s.store.ChannelStore(ch.ChannelKeyForID(read.ChannelID), read.ChannelID)
	if err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	defer func() { _ = store.Close() }()
	state, err := store.Load(ctx)
	if err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	committed := state.HW
	if minISR <= 1 {
		committed = state.LEO
	}
	retention, err := store.LoadRetentionState(ctx)
	if err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	request := read.Request
	request.MinSeq = maxUint64Value(request.MinSeq, nextSeq(maxUint64Value(retentionThroughSeq, retention.LocalRetentionThroughSeq)))
	if request.MaxSeq == 0 || request.MaxSeq > committed {
		request.MaxSeq = committed
	}
	if !request.Reverse && request.FromSeq > committed {
		return channelstore.ReadCommittedResult{NextSeq: request.FromSeq}, nil
	}
	if request.Reverse && request.FromSeq > committed {
		request.FromSeq = committed
	}
	result, err := store.ReadCommitted(ctx, request)
	if err != nil {
		return channelstore.ReadCommittedResult{}, err
	}
	result.Messages = cloneMessages(result.Messages)
	return result, nil
}

func canFallbackCommittedReadOnMissingMeta(local ch.NodeID, req CommittedReadRequest, err error) bool {
	return (channelErrorMatches(err, ch.ErrChannelNotFound) || errors.Is(err, metadb.ErrNotFound)) &&
		req.ExpectedLeader == local && req.ExpectedChannelEpoch != 0 && req.ExpectedLeaderEpoch != 0
}

func cloneMessages(messages []ch.Message) []ch.Message {
	cloned := make([]ch.Message, len(messages))
	copy(cloned, messages)
	for index := range cloned {
		cloned[index].Payload = append([]byte(nil), cloned[index].Payload...)
	}
	return cloned
}

func (s *Service) handleForwardLastVisible(ctx context.Context, req LastVisibleRequest) (LastVisibleResponse, error) {
	meta, ok, err := s.resolveReadMeta(ctx, req.ChannelID)
	if err != nil && !canFallbackLastVisibleOnMissingMeta(s.localNode, req, err) {
		return LastVisibleResponse{}, err
	}
	if err != nil && canFallbackLastVisibleOnMissingMeta(s.localNode, req, err) {
		if req.HeadUID != "" {
			result := s.readLocalConversationHeads(ctx, req.HeadUID, []ConversationHeadRequest{{
				ChannelID:            req.ChannelID,
				RetentionThroughSeq:  req.VisibleAfterSeq,
				ExpectedLeader:       req.ExpectedLeader,
				ExpectedChannelEpoch: req.ExpectedChannelEpoch,
				ExpectedLeaderEpoch:  req.ExpectedLeaderEpoch,
				ExpectedMinISR:       req.ExpectedMinISR,
			}})[0]
			return lastVisibleResponseFromHead(result.Head), result.Err
		}
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
	visibleAfterSeq := maxUint64Value(req.VisibleAfterSeq, meta.RetentionThroughSeq)
	if req.HeadUID != "" {
		result := s.readLocalConversationHeads(ctx, req.HeadUID, []ConversationHeadRequest{{
			ChannelID:            req.ChannelID,
			RetentionThroughSeq:  visibleAfterSeq,
			ExpectedLeader:       meta.Leader,
			ExpectedChannelEpoch: meta.Epoch,
			ExpectedLeaderEpoch:  meta.LeaderEpoch,
			ExpectedMinISR:       meta.MinISR,
		}})[0]
		return lastVisibleResponseFromHead(result.Head), result.Err
	}
	msg, ok, err := s.readLocalLastVisible(ctx, req.ChannelID, visibleAfterSeq)
	return LastVisibleResponse{Message: msg, Found: ok}, err
}

func canFallbackConversationHeadOnMissingMeta(local ch.NodeID, req ConversationHeadRequest, err error) bool {
	return (channelErrorMatches(err, ch.ErrChannelNotFound) || errors.Is(err, metadb.ErrNotFound)) &&
		req.ExpectedLeader == local && req.ExpectedChannelEpoch != 0 && req.ExpectedLeaderEpoch != 0
}

func (s *Service) readLocalConversationHeads(ctx context.Context, uid string, requests []ConversationHeadRequest) []ConversationHeadResult {
	results := make([]ConversationHeadResult, len(requests))
	if len(requests) == 0 {
		return results
	}
	liveHW, itemErrors, err := s.liveConversationHW(ctx, requests)
	if err != nil {
		for index := range results {
			results[index].Err = err
		}
		return results
	}
	for index, request := range requests {
		if itemErr := itemErrors[request.ChannelID]; itemErr != nil {
			results[index].Err = itemErr
			continue
		}
		committed, hasLiveHW := liveHW[request.ChannelID]
		results[index].Head, results[index].Err = s.readLocalConversationHead(
			ctx, request.ChannelID, uid, request.RetentionThroughSeq, request.ExpectedMinISR, committed, hasLiveHW,
		)
	}
	return results
}

func (s *Service) liveConversationHW(ctx context.Context, requests []ConversationHeadRequest) (map[ch.ChannelID]uint64, map[ch.ChannelID]error, error) {
	probeRuntime, ok := s.runtime.(conversationRuntimeProbe)
	if !ok {
		return nil, nil, nil
	}
	ids := make([]ch.ChannelID, 0, len(requests))
	expected := make(map[ch.ChannelID]ConversationHeadRequest, len(requests))
	for _, request := range requests {
		if request.ExpectedMinISR <= 1 {
			continue
		}
		if _, exists := expected[request.ChannelID]; exists {
			continue
		}
		expected[request.ChannelID] = request
		ids = append(ids, request.ChannelID)
	}
	if len(ids) == 0 {
		return nil, nil, nil
	}
	probe, err := probeRuntime.RuntimeProbe(ctx, ch.RuntimeSelector{ChannelIDs: ids})
	if err != nil {
		return nil, nil, err
	}
	liveHW := make(map[ch.ChannelID]uint64, len(probe.Channels))
	itemErrors := make(map[ch.ChannelID]error)
	for _, channel := range probe.Channels {
		request, exists := expected[channel.ChannelID]
		if !exists {
			continue
		}
		switch {
		case channel.Role != ch.RoleLeader:
			itemErrors[channel.ChannelID] = ch.ErrNotLeader
		case channel.Status != ch.StatusActive:
			itemErrors[channel.ChannelID] = ch.ErrNotReady
		case request.ExpectedChannelEpoch != 0 && channel.ChannelEpoch != request.ExpectedChannelEpoch:
			itemErrors[channel.ChannelID] = ch.ErrStaleMeta
		case request.ExpectedLeaderEpoch != 0 && channel.LeaderEpoch != request.ExpectedLeaderEpoch:
			itemErrors[channel.ChannelID] = ch.ErrStaleMeta
		default:
			liveHW[channel.ChannelID] = channel.HW
		}
	}
	return liveHW, itemErrors, nil
}

func (s *Service) readLocalConversationHead(ctx context.Context, id ch.ChannelID, uid string, retentionThroughSeq uint64, minISR int, liveCommitted uint64, hasLiveCommitted bool) (ConversationHead, error) {
	if s == nil || s.store == nil || uid == "" {
		return ConversationHead{}, ch.ErrNotReady
	}
	store, err := s.store.ChannelStore(ch.ChannelKeyForID(id), id)
	if err != nil {
		return ConversationHead{}, err
	}
	defer func() { _ = store.Close() }()
	state, err := store.Load(ctx)
	if err != nil {
		return ConversationHead{}, err
	}
	committed := state.HW
	if minISR <= 1 {
		committed = state.LEO
	} else if hasLiveCommitted {
		committed = maxUint64Value(committed, liveCommitted)
	}
	retention, err := store.LoadRetentionState(ctx)
	if err != nil {
		return ConversationHead{}, err
	}
	retentionThroughSeq = maxUint64Value(retentionThroughSeq, retention.LocalRetentionThroughSeq)
	head := ConversationHead{LastCommittedSeq: committed, RetentionThroughSeq: retentionThroughSeq}
	if committed == 0 {
		return head, nil
	}
	lookup, ok := store.(channelstore.SenderSequenceLookup)
	if !ok {
		return ConversationHead{}, ch.ErrInvalidConfig
	}
	if seq, found, lookupErr := lookup.GetLastSenderMessageSeq(ctx, uid, committed); lookupErr != nil {
		return ConversationHead{}, lookupErr
	} else if found {
		head.CurrentUserLastSendSeq = seq
	}
	message, found, err := readLastOrdinaryCommitted(ctx, store, committed, retentionThroughSeq)
	if err != nil {
		return ConversationHead{}, err
	}
	head.Message = message
	head.Found = found
	return head, nil
}

func readLastOrdinaryCommitted(ctx context.Context, store channelstore.ChannelStore, committed, retentionThroughSeq uint64) (ch.Message, bool, error) {
	from := committed
	for from > retentionThroughSeq {
		read, err := store.ReadCommitted(ctx, channelstore.ReadCommittedRequest{
			FromSeq: from, MaxSeq: committed, MinSeq: nextSeq(retentionThroughSeq),
			Limit: 64, MaxBytes: maxInt(), Reverse: true,
		})
		if err != nil {
			return ch.Message{}, false, err
		}
		for _, message := range read.Messages {
			if !message.SyncOnce {
				message.Payload = append([]byte(nil), message.Payload...)
				return message, true, nil
			}
		}
		if read.NextSeq == 0 || read.NextSeq >= from || read.NextSeq <= retentionThroughSeq {
			break
		}
		from = read.NextSeq
	}
	return ch.Message{}, false, nil
}

func nextSeq(seq uint64) uint64 {
	if seq == ^uint64(0) {
		return seq
	}
	return seq + 1
}

func lastVisibleResponseFromHead(head ConversationHead) LastVisibleResponse {
	return LastVisibleResponse{
		Message: head.Message, Found: head.Found,
		LastCommittedSeq:       head.LastCommittedSeq,
		RetentionThroughSeq:    head.RetentionThroughSeq,
		CurrentUserLastSendSeq: head.CurrentUserLastSendSeq,
	}
}

func conversationHeadFromResponse(resp LastVisibleResponse) ConversationHead {
	return ConversationHead{
		Message: resp.Message, Found: resp.Found,
		LastCommittedSeq:       resp.LastCommittedSeq,
		RetentionThroughSeq:    resp.RetentionThroughSeq,
		CurrentUserLastSendSeq: resp.CurrentUserLastSendSeq,
	}
}

func (s *Service) readLocalLastVisible(ctx context.Context, id ch.ChannelID, visibleAfterSeq uint64) (ch.Message, bool, error) {
	if s == nil || s.store == nil {
		return ch.Message{}, false, ch.ErrNotReady
	}
	store, err := s.store.ChannelStore(ch.ChannelKeyForID(id), id)
	if err != nil {
		return ch.Message{}, false, err
	}
	defer func() {
		_ = store.Close()
	}()
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

func (s *Service) appendOnce(ctx context.Context, req ch.AppendRequest) (ch.AppendResult, error, ch.Meta, bool) {
	started := time.Now()
	meta, ok, usedCache, err := s.resolveAppendMetaCached(ctx, req.ChannelID)
	s.observeAppendStage("meta_resolve", err, time.Since(started))
	if err != nil {
		return ch.AppendResult{}, err, meta, usedCache
	}
	res, err := s.appendWithMeta(ctx, req, meta, ok)
	return res, err, meta, usedCache
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
		appendable := cacheableAppendMeta(req.ChannelID, meta)
		if meta.Leader != 0 && meta.Leader != s.localNode {
			if !appendable {
				return ch.AppendResult{}, unavailableAppendMetaError(meta)
			}
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
		err := s.applyRuntimeMeta(meta, false)
		s.observeAppendStage("meta_apply", err, time.Since(started))
		if err != nil {
			return ch.AppendResult{}, err
		}
		if !appendable {
			return ch.AppendResult{}, unavailableAppendMetaError(meta)
		}
	}
	started := time.Now()
	res, err := s.runtime.Append(ctx, req)
	s.observeAppendStage("runtime_append", err, time.Since(started))
	return res, err
}

func (s *Service) appendBatchOnce(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error, ch.Meta, bool) {
	started := time.Now()
	meta, ok, usedCache, err := s.resolveAppendMetaCached(ctx, req.ChannelID)
	s.observeAppendStage("meta_resolve", err, time.Since(started))
	if err != nil {
		return ch.AppendBatchResult{}, err, meta, usedCache
	}
	res, err := s.appendBatchWithMeta(ctx, req, meta, ok)
	return res, err, meta, usedCache
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
		appendable := cacheableAppendMeta(req.ChannelID, meta)
		if meta.Leader != 0 && meta.Leader != s.localNode {
			if !appendable {
				return ch.AppendBatchResult{}, unavailableAppendMetaError(meta)
			}
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
		err := s.applyRuntimeMeta(meta, false)
		s.observeAppendStage("meta_apply", err, time.Since(started))
		if err != nil {
			return ch.AppendBatchResult{}, err
		}
		if !appendable {
			return ch.AppendBatchResult{}, unavailableAppendMetaError(meta)
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

func (s *Service) applyRuntimeMeta(meta ch.Meta, authoritative bool) error {
	if s == nil || s.runtime == nil {
		return ch.ErrNotReady
	}
	if meta.Key == "" {
		meta.Key = ch.ChannelKeyForID(meta.ID)
	}
	if !validAppendMetaIdentity(meta.ID, meta) {
		return s.runtime.ApplyMeta(meta)
	}
	lock := &s.metaApplyLocks[channelMetaApplyLockIndex(meta.ID)]
	lock.Lock()
	defer lock.Unlock()

	candidate := cloneMeta(meta)
	selected, _ := s.metaCache.preferCurrent(meta.ID, candidate)
	if err := s.runtime.ApplyMeta(selected); err != nil {
		return err
	}
	if authoritative {
		// Keep the original authoritative candidate as provenance. A retained
		// newer floor selected for runtime safety must not impersonate an exact
		// refresh and reactivate an invalidated cache entry.
		s.metaCache.installIfNewer(candidate.ID, candidate)
	}
	return nil
}

func channelMetaApplyLockIndex(id ch.ChannelID) int {
	const (
		offset64 = uint64(14695981039346656037)
		prime64  = uint64(1099511628211)
	)
	hash := offset64
	for index := 0; index < len(id.ID); index++ {
		hash ^= uint64(id.ID[index])
		hash *= prime64
	}
	hash ^= uint64(id.Type)
	hash *= prime64
	return int(hash % channelMetaApplyLockCount)
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
	if err != nil {
		return meta, ok, false, err
	}
	return meta, ok, ok && cacheableAppendMeta(id, meta), nil
}

func (s *Service) resolveAppendMetaFresh(ctx context.Context, id ch.ChannelID) (ch.Meta, bool, error) {
	meta, ok, err := s.resolveAppendMeta(ctx, id)
	if err != nil || !ok {
		return meta, ok, err
	}
	var freshEnough bool
	meta, freshEnough = s.metaCache.selectResolved(id, meta)
	if !freshEnough {
		return meta, true, ch.ErrStaleMeta
	}
	return meta, ok, nil
}

func unavailableAppendMetaError(meta ch.Meta) error {
	if meta.Status == ch.StatusDeleting || meta.Status == ch.StatusDeleted {
		return ch.ErrChannelNotFound
	}
	return ch.ErrNotReady
}

func retryableMetaCacheError(err error) bool {
	return channelErrorMatches(err, ch.ErrStaleMeta) ||
		channelErrorMatches(err, ch.ErrChannelNotFound) ||
		channelErrorMatches(err, ch.ErrNotLeader) ||
		channelErrorMatches(err, ch.ErrNotReplica) ||
		channelErrorMatches(err, ch.ErrNotReady) ||
		channelErrorMatches(err, ch.ErrWriteFenced)
}

func channelErrorMatches(err error, sentinel error) bool {
	return ch.ErrorMatches(err, sentinel)
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

func maxUint64Value(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
