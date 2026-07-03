package app

import (
	"context"
	"errors"
	"hash/fnv"
	"io"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/contracts/deliveryevents"
	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	deliverytagruntime "github.com/WuKongIM/WuKongIM/internal/runtime/deliverytag"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	errRemoteAckNotifierRequired       = errors.New("app: remote ack notifier required")
	errRemoteOfflineNotifierRequired   = errors.New("app: remote offline notifier required")
	errCommittedDispatcherStopped      = errors.New("app: committed dispatcher stopped")
	errMessageScopedDeliveryRequired   = errors.New("app: message scoped committed delivery required")
	errMessageScopedOwnerRequired      = errors.New("app: message scoped committed owner required")
	errMessageScopedNodeClientRequired = errors.New("app: message scoped committed node client required")
)

const (
	committedRouteRetryAttempts = 3
	committedRouteRetryBackoff  = 20 * time.Millisecond

	// Increased from 512 to 1000 to reduce backpressure in high-throughput scenarios
	committedDispatchDefaultQueueDepth = 1000
	committedDispatchMinShards         = 8  // Increased from 4 to 8
	committedDispatchMaxShards         = 64 // Increased from 32 to 64

	// deliveryPushRouteChunkSize bounds the route list carried by one remote push RPC.
	deliveryPushRouteChunkSize = 1000

	deliveryTagRPCStatusOK              = "ok"
	deliveryTagRPCStatusRetryable       = "retryable"
	deliveryTagRPCResultTagNotCurrent   = "tag_not_current"
	deliveryTagRPCResultVersionMismatch = "tag_version_mismatch"
)

type asyncCommittedDispatcherConfig struct {
	// LocalNodeID identifies the current node for committed owner routing.
	LocalNodeID uint64
	// Logger records committed routing diagnostics without failing the send path.
	Logger wklog.Logger
	// ChannelLog resolves the channel owner for committed side effects.
	ChannelLog interface {
		Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error)
	}
	// Delivery receives local realtime fanout submissions.
	Delivery committedDeliverySubmitter
	// Conversation receives committed messages for conversation projection.
	Conversation committedConversationSubmitter
	// NodeClient forwards committed side effects to remote owner nodes.
	NodeClient committedNodeSubmitter
	// Metrics observes queue depth, enqueue results, and overflow events.
	Metrics committedDispatchMetrics
	// ShardCount controls FIFO worker lanes; zero uses bounded runtime defaults.
	ShardCount int
	// QueueDepth controls each shard buffer; zero uses the internal default.
	QueueDepth int
	// DisableWorkersForTest leaves queues unconsumed so overflow paths are deterministic.
	DisableWorkersForTest bool
}

type committedDispatchMetrics interface {
	SetCommittedDispatchQueueDepth(shard string, depth int)
	ObserveCommittedDispatchEnqueue(shard, result string)
	ObserveCommittedDispatchOverflow(shard string)
}

type asyncCommittedDispatcher struct {
	localNodeID uint64
	logger      wklog.Logger
	channelLog  interface {
		Status(id channel.ChannelID) (channel.ChannelRuntimeStatus, error)
	}
	delivery     committedDeliverySubmitter
	conversation committedConversationSubmitter
	nodeClient   committedNodeSubmitter
	metrics      committedDispatchMetrics

	mu                    sync.Mutex
	cancel                context.CancelFunc
	done                  chan struct{}
	running               bool
	stopping              bool
	wg                    sync.WaitGroup
	shards                []chan committedDispatchItem
	fallbacks             chan committedDispatchItem
	disableWorkersForTest bool
}

type committedDispatchItem struct {
	ctx context.Context
	env deliveryruntime.CommittedEnvelope
}

func newAsyncCommittedDispatcher(cfg asyncCommittedDispatcherConfig) *asyncCommittedDispatcher {
	shardCount := cfg.ShardCount
	if shardCount <= 0 {
		shardCount = runtime.GOMAXPROCS(0)
		if shardCount < committedDispatchMinShards {
			shardCount = committedDispatchMinShards
		}
		if shardCount > committedDispatchMaxShards {
			shardCount = committedDispatchMaxShards
		}
	}
	queueDepth := cfg.QueueDepth
	if queueDepth <= 0 {
		queueDepth = committedDispatchDefaultQueueDepth
	}
	shards := make([]chan committedDispatchItem, shardCount)
	for i := range shards {
		shards[i] = make(chan committedDispatchItem, queueDepth)
	}
	return &asyncCommittedDispatcher{
		localNodeID:           cfg.LocalNodeID,
		logger:                cfg.Logger,
		channelLog:            cfg.ChannelLog,
		delivery:              cfg.Delivery,
		conversation:          cfg.Conversation,
		nodeClient:            cfg.NodeClient,
		metrics:               cfg.Metrics,
		shards:                shards,
		fallbacks:             make(chan committedDispatchItem, queueDepth),
		disableWorkersForTest: cfg.DisableWorkersForTest,
	}
}

func (d *asyncCommittedDispatcher) Start(ctx context.Context) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancel != nil {
		return nil
	}
	if d.running || d.stopping {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.done = make(chan struct{})
	d.running = true
	if d.conversation != nil {
		d.wg.Add(1)
		go d.runConversationFallback(runCtx, d.fallbacks)
	}
	if d.disableWorkersForTest {
		go d.closeCommittedDispatchDone(d.done)
		return nil
	}
	for i, shard := range d.shards {
		shardName := strconv.Itoa(i)
		d.wg.Add(1)
		go d.runShard(runCtx, shardName, shard)
	}
	go d.closeCommittedDispatchDone(d.done)
	return nil
}

func (d *asyncCommittedDispatcher) Stop() error {
	return d.StopContext(context.Background())
}

// StopContext stops workers and bounds shutdown with ctx; queued events are abandoned for committed replay.
func (d *asyncCommittedDispatcher) StopContext(ctx context.Context) error {
	if d == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	d.mu.Lock()
	cancel := d.cancel
	done := d.done
	if cancel == nil && d.stopping && done != nil {
		d.mu.Unlock()
		return d.waitCommittedDispatchDone(ctx, done)
	}
	d.cancel = nil
	d.running = false
	if cancel != nil {
		d.stopping = true
	}
	d.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	d.abandonQueuedCommitted()
	return d.waitCommittedDispatchDone(ctx, done)
}

func (d *asyncCommittedDispatcher) waitCommittedDispatchDone(ctx context.Context, done chan struct{}) error {
	select {
	case <-done:
		d.finishCommittedDispatchStop(done)
		return nil
	case <-ctx.Done():
		go func() {
			<-done
			d.finishCommittedDispatchStop(done)
		}()
		return ctx.Err()
	}
}

func (d *asyncCommittedDispatcher) closeCommittedDispatchDone(done chan struct{}) {
	d.wg.Wait()
	close(done)
}

func (d *asyncCommittedDispatcher) finishCommittedDispatchStop(done chan struct{}) {
	d.mu.Lock()
	if d.done == done {
		d.done = nil
		d.stopping = false
	}
	d.mu.Unlock()
	d.recordAllCommittedDispatchDepths()
}

// abandonQueuedCommitted drops buffered side effects during shutdown; channel log replay is the durable fallback.
func (d *asyncCommittedDispatcher) abandonQueuedCommitted() {
	for _, queue := range d.shards {
		for {
			select {
			case <-queue:
			default:
				goto next
			}
		}
	next:
	}
	for {
		select {
		case <-d.fallbacks:
		default:
			return
		}
	}
}

func (d *asyncCommittedDispatcher) runShard(ctx context.Context, shardName string, queue <-chan committedDispatchItem) {
	defer d.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-queue:
			select {
			case <-ctx.Done():
				return
			default:
			}
			d.recordCommittedDispatchDepth(shardName, len(queue))
			d.routeCommitted(committedDispatchRouteContext(ctx, item.ctx), item.env)
		}
	}
}

func (d *asyncCommittedDispatcher) runConversationFallback(ctx context.Context, queue <-chan committedDispatchItem) {
	defer d.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-queue:
			select {
			case <-ctx.Done():
				return
			default:
			}
			d.submitConversationFallback(committedDispatchRouteContext(ctx, item.ctx), item.env)
		}
	}
}

func (d *asyncCommittedDispatcher) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if d == nil {
		return nil
	}
	if d.delivery == nil && d.conversation == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cloned := event.Clone()
	env := committedEnvelopeFromMessageEvent(cloned)
	if len(env.MessageScopedUIDs) > 0 {
		d.mu.Lock()
		if !d.running || d.stopping {
			d.mu.Unlock()
			return errCommittedDispatcherStopped
		}
		d.wg.Add(1)
		d.mu.Unlock()
		defer d.wg.Done()
		return d.routeMessageScopedCommitted(ctx, env)
	}
	ctx = context.WithoutCancel(ctx)
	idx := d.committedDispatchShard(cloned.Message)
	shardName := strconv.Itoa(idx)
	d.mu.Lock()
	if !d.running || d.stopping {
		d.mu.Unlock()
		return errCommittedDispatcherStopped
	}
	queue := d.shards[idx]
	enqueueResult := "ok"
	var overflow bool
	var depth int
	var fallbackScheduled bool
	select {
	case queue <- committedDispatchItem{ctx: ctx, env: env}:
		depth = len(queue)
	default:
		enqueueResult = "overflow"
		overflow = true
		depth = len(queue)
		fallbackScheduled = d.enqueueConversationFallbackLocked(ctx, env)
	}
	d.mu.Unlock()
	d.recordCommittedDispatchEnqueue(shardName, enqueueResult)
	if overflow {
		d.recordCommittedDispatchOverflow(shardName)
	}
	d.recordCommittedDispatchDepth(shardName, depth)
	if overflow && !fallbackScheduled {
		d.logCommittedRoute(env, "conversation_fallback_dropped", 0, nil)
	}
	return nil
}

func (d *asyncCommittedDispatcher) routeMessageScopedCommitted(ctx context.Context, env deliveryruntime.CommittedEnvelope) error {
	if d.delivery == nil {
		d.logCommittedRoute(env, "message_scoped_delivery_required", 0, errMessageScopedDeliveryRequired)
		return errMessageScopedDeliveryRequired
	}
	if d.channelLog == nil {
		d.logCommittedRoute(env, "message_scoped_no_channel_log", 0, errMessageScopedOwnerRequired)
		return errMessageScopedOwnerRequired
	}

	var lastErr error
	for attempt := 0; attempt < committedRouteRetryAttempts; attempt++ {
		status, err := d.channelLog.Status(channel.ChannelID{
			ID:   env.ChannelID,
			Type: env.ChannelType,
		})
		if err == nil && status.Leader != 0 {
			ownerNodeID := uint64(status.Leader)
			if ownerNodeID == d.localNodeID {
				d.logCommittedRoute(env, "message_scoped_local_owner", ownerNodeID, nil)
				return d.submitLocalStrict(ctx, env)
			}
			if d.nodeClient == nil {
				d.logCommittedRoute(env, "message_scoped_node_client_required", ownerNodeID, errMessageScopedNodeClientRequired)
				return errMessageScopedNodeClientRequired
			}
			if err := d.nodeClient.SubmitCommitted(ctx, ownerNodeID, env); err == nil {
				d.logCommittedRoute(env, "message_scoped_remote_owner", ownerNodeID, nil)
				return nil
			} else {
				lastErr = err
				d.logCommittedRoute(env, "message_scoped_remote_owner_submit_failed", ownerNodeID, err)
				if errors.Is(err, accessnode.ErrMessageScopedDeliverySubmitUnsupported) {
					return err
				}
			}
		} else {
			if err != nil {
				lastErr = err
				d.logCommittedRoute(env, "message_scoped_status_failed", 0, err)
			} else {
				lastErr = errMessageScopedOwnerRequired
				d.logCommittedRoute(env, "message_scoped_no_owner", 0, lastErr)
			}
		}
		if attempt < committedRouteRetryAttempts-1 {
			if !sleepCommittedRouteRetry(ctx, time.Duration(attempt+1)*committedRouteRetryBackoff) {
				return ctx.Err()
			}
		}
	}
	if lastErr == nil {
		lastErr = errMessageScopedOwnerRequired
	}
	return lastErr
}

func (d *asyncCommittedDispatcher) enqueueConversationFallbackLocked(ctx context.Context, env deliveryruntime.CommittedEnvelope) bool {
	if d.conversation == nil || d.fallbacks == nil {
		return false
	}
	select {
	case d.fallbacks <- committedDispatchItem{ctx: ctx, env: env}:
		return true
	default:
		return false
	}
}

func (d *asyncCommittedDispatcher) committedDispatchShard(msg channel.Message) int {
	if len(d.shards) <= 1 {
		return 0
	}
	hash := fnv.New64a()
	_, _ = io.WriteString(hash, msg.ChannelID)
	_, _ = hash.Write([]byte{msg.ChannelType})
	return int(hash.Sum64() % uint64(len(d.shards)))
}

func (d *asyncCommittedDispatcher) recordCommittedDispatchDepth(shard string, depth int) {
	if d.metrics != nil {
		d.metrics.SetCommittedDispatchQueueDepth(shard, depth)
	}
}

func (d *asyncCommittedDispatcher) recordCommittedDispatchEnqueue(shard, result string) {
	if d.metrics != nil {
		d.metrics.ObserveCommittedDispatchEnqueue(shard, result)
	}
}

func (d *asyncCommittedDispatcher) recordCommittedDispatchOverflow(shard string) {
	if d.metrics != nil {
		d.metrics.ObserveCommittedDispatchOverflow(shard)
	}
}

func (d *asyncCommittedDispatcher) recordAllCommittedDispatchDepths() {
	for i, shard := range d.shards {
		d.recordCommittedDispatchDepth(strconv.Itoa(i), len(shard))
	}
}

type committedDispatchValueContext struct {
	control context.Context
	values  context.Context
}

func committedDispatchRouteContext(control, values context.Context) context.Context {
	if control == nil {
		control = context.Background()
	}
	if values == nil {
		return control
	}
	return committedDispatchValueContext{control: control, values: values}
}

func (c committedDispatchValueContext) Deadline() (time.Time, bool) {
	return c.control.Deadline()
}

func (c committedDispatchValueContext) Done() <-chan struct{} {
	return c.control.Done()
}

func (c committedDispatchValueContext) Err() error {
	return c.control.Err()
}

func (c committedDispatchValueContext) Value(key any) any {
	if v := c.values.Value(key); v != nil {
		return v
	}
	return c.control.Value(key)
}

func committedEnvelopeFromMessageEvent(event messageevents.MessageCommitted) deliveryruntime.CommittedEnvelope {
	return deliveryruntime.CommittedEnvelope{
		Message:                        event.Message,
		SenderSessionID:                event.SenderSessionID,
		MessageScopedUIDs:              append([]string(nil), event.MessageScopedUIDs...),
		CMDConversationIntentSubmitted: event.CMDConversationIntentSubmitted,
	}
}

type deliveryRuntimeCommittedSubmitter struct {
	target interface {
		SubmitCommitted(context.Context, messageevents.MessageCommitted) error
	}
}

func (s deliveryRuntimeCommittedSubmitter) SubmitCommitted(ctx context.Context, env deliveryruntime.CommittedEnvelope) error {
	if s.target == nil {
		return nil
	}
	return s.target.SubmitCommitted(ctx, messageevents.MessageCommitted{
		Message:                        env.Message,
		SenderSessionID:                env.SenderSessionID,
		MessageScopedUIDs:              append([]string(nil), env.MessageScopedUIDs...),
		CMDConversationIntentSubmitted: env.CMDConversationIntentSubmitted,
	})
}

func (d *asyncCommittedDispatcher) routeCommitted(ctx context.Context, env deliveryruntime.CommittedEnvelope) {
	if d.channelLog == nil {
		d.logCommittedRoute(env, "no_channel_log", d.localNodeID, nil)
		d.submitLocal(ctx, env)
		return
	}

	var (
		lastRouteErr   error
		lastRouteStage string
		lastOwnerNode  uint64
	)
	for attempt := 0; attempt < committedRouteRetryAttempts; attempt++ {
		status, err := d.channelLog.Status(channel.ChannelID{
			ID:   env.ChannelID,
			Type: env.ChannelType,
		})
		if err == nil && status.Leader != 0 {
			ownerNodeID := uint64(status.Leader)
			if ownerNodeID == d.localNodeID {
				d.logCommittedRoute(env, "local_owner", ownerNodeID, nil)
				d.submitLocal(ctx, env)
				return
			}
			if d.nodeClient != nil {
				if err := d.nodeClient.SubmitCommitted(ctx, ownerNodeID, env); err == nil {
					d.logCommittedRoute(env, "remote_owner", ownerNodeID, nil)
					return
				} else {
					lastRouteErr = err
					lastRouteStage = "remote_owner_submit_failed"
					lastOwnerNode = ownerNodeID
				}
			}
		} else if err != nil {
			lastRouteErr = err
			lastRouteStage = "status_failed"
			lastOwnerNode = 0
		}
		if attempt < committedRouteRetryAttempts-1 {
			if !sleepCommittedRouteRetry(ctx, time.Duration(attempt+1)*committedRouteRetryBackoff) {
				return
			}
		}
	}
	if lastRouteErr != nil {
		d.logCommittedRoute(env, lastRouteStage, lastOwnerNode, lastRouteErr)
	}
	d.logCommittedRoute(env, "conversation_fallback", 0, nil)
	d.submitConversationFallback(ctx, env)
}

func sleepCommittedRouteRetry(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (d *asyncCommittedDispatcher) logCommittedRoute(env deliveryruntime.CommittedEnvelope, stage string, ownerNodeID uint64, err error) {
	if d.logger == nil {
		return
	}
	if err == nil && !wklog.DebugEnabled(d.logger) {
		return
	}
	fields := []wklog.Field{
		wklog.Event("delivery.diag.committed_route"),
		wklog.String("stage", stage),
		wklog.String("channelID", env.ChannelID),
		wklog.Int("channelType", int(env.ChannelType)),
		wklog.Uint64("messageID", env.MessageID),
		wklog.Uint64("messageSeq", env.MessageSeq),
	}
	if ownerNodeID != 0 {
		fields = append(fields, wklog.Uint64("ownerNodeID", ownerNodeID))
	}
	if err != nil {
		fields = append(fields, wklog.Error(err))
		d.logger.Warn("committed message routing observed failure", fields...)
		return
	}
	d.logger.Debug("committed message routed", fields...)
}

func (d *asyncCommittedDispatcher) submitLocal(ctx context.Context, env deliveryruntime.CommittedEnvelope) {
	if d.delivery != nil {
		_ = d.delivery.SubmitCommitted(ctx, env)
	}
	d.submitConversation(ctx, env.Message)
}

func (d *asyncCommittedDispatcher) submitLocalStrict(ctx context.Context, env deliveryruntime.CommittedEnvelope) error {
	if d.delivery == nil {
		return errMessageScopedDeliveryRequired
	}
	if err := d.delivery.SubmitCommitted(ctx, env); err != nil {
		return err
	}
	d.submitConversation(ctx, env.Message)
	return nil
}

func (d *asyncCommittedDispatcher) submitConversation(ctx context.Context, msg channel.Message) {
	if d.conversation != nil {
		_ = d.conversation.SubmitCommitted(ctx, msg)
	}
}

func (d *asyncCommittedDispatcher) submitConversationFallback(ctx context.Context, env deliveryruntime.CommittedEnvelope) {
	d.submitConversation(ctx, env.Message)
}

type localDeliveryResolver struct {
	subscribers        deliveryusecase.SubscriberResolver
	authority          presence.Authoritative
	uidObserver        resolvedUIDObserver
	collectOfflineUIDs bool
	pageSize           int
	metrics            deliveryRoutingMetrics
	logger             wklog.Logger
}

// resolvedUIDPage contains one resolver UID page before presence expansion.
type resolvedUIDPage struct {
	Envelope deliveryruntime.CommittedEnvelope
	UIDs     []string
}

// resolvedUIDObserver observes resolver UID pages for non-routing side effects.
type resolvedUIDObserver interface {
	OnResolvedUIDPage(context.Context, resolvedUIDPage)
}

// deliveryTagTopologyReader reads the authority shape that affects tag UID partitioning.
type deliveryTagTopologyReader interface {
	CurrentDeliveryTagTopology(ctx context.Context, uids []string) (deliverytagruntime.PartitionTopologyVersion, error)
}

type deliveryTagTopologyValidator interface {
	ValidateCurrentDeliveryTagTopology(ctx context.Context, topology deliverytagruntime.PartitionTopologyVersion) (bool, error)
}

type deliveryTagCluster interface {
	SlotForKey(key string) multiraft.SlotID
	HashSlotTableVersion() uint64
	LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error)
	ListSlotAssignments(ctx context.Context) ([]controllermeta.SlotAssignment, error)
}

type cachedDeliveryTagAssignments interface {
	// ListCachedAssignments returns the node-local controller assignment snapshot without refreshing the controller leader.
	ListCachedAssignments() []controllermeta.SlotAssignment
}

// tagDeliveryResolver resolves routes from leader-built delivery tag partitions.
type tagDeliveryResolver struct {
	localNodeID        uint64
	tags               *deliverytagruntime.Manager
	subscribers        deliveryusecase.SubscriberResolver
	authority          presence.Authoritative
	uidObserver        resolvedUIDObserver
	collectOfflineUIDs bool
	topology           deliveryTagTopologyReader
	pageSize           int
	metrics            deliveryRoutingMetrics
	logger             wklog.Logger
}

type deliveryTagAuthority struct {
	tags *deliverytagruntime.Manager
}

func (a deliveryTagAuthority) GetDeliveryTag(_ context.Context, req accessnode.DeliveryTagRequest) (accessnode.DeliveryTagResponse, error) {
	if a.tags == nil {
		return accessnode.DeliveryTagResponse{Status: deliveryTagRPCStatusRetryable}, nil
	}
	tag, ok := a.tags.LookupTag(req.TagKey)
	if !ok {
		return accessnode.DeliveryTagResponse{Status: deliveryTagRPCStatusRetryable, Result: deliveryTagRPCResultTagNotCurrent}, nil
	}
	return accessnode.DeliveryTagResponse{Status: deliveryTagRPCStatusOK, Tag: tag}, nil
}

func (a deliveryTagAuthority) UpdateDeliveryTag(_ context.Context, req accessnode.DeliveryTagRequest) (accessnode.DeliveryTagResponse, error) {
	if a.tags == nil {
		return accessnode.DeliveryTagResponse{Status: deliveryTagRPCStatusRetryable}, nil
	}
	tag, _ := a.tags.StoreFollowerPartition(req.Tag)
	if tag.Key == "" {
		tag = req.Tag
	}
	return accessnode.DeliveryTagResponse{Status: deliveryTagRPCStatusOK, Tag: tag}, nil
}

type deliveryTagTopologyReaderAdapter struct {
	cluster deliveryTagCluster
}

func (r deliveryTagTopologyReaderAdapter) CurrentDeliveryTagTopology(ctx context.Context, uids []string) (deliverytagruntime.PartitionTopologyVersion, error) {
	if r.cluster == nil {
		return deliverytagruntime.PartitionTopologyVersion{}, nil
	}
	slotSet := make(map[uint32]struct{})
	for _, uid := range uids {
		slotSet[uint32(r.cluster.SlotForKey(uid))] = struct{}{}
	}
	slotIDs := make([]uint32, 0, len(slotSet))
	for slotID := range slotSet {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	assignmentBySlot := currentDeliveryTagAssignmentBySlot(ctx, r.cluster, slotIDs)
	refs := make([]deliverytagruntime.SlotAuthorityRef, 0, len(slotIDs))
	for _, slotID := range slotIDs {
		leaderID, err := r.cluster.LeaderOf(multiraft.SlotID(slotID))
		if err != nil {
			return deliverytagruntime.PartitionTopologyVersion{}, err
		}
		assignment := assignmentBySlot[slotID]
		refs = append(refs, deliverytagruntime.SlotAuthorityRef{
			SlotID:         slotID,
			LeaderNodeID:   uint64(leaderID),
			ConfigEpoch:    assignment.ConfigEpoch,
			BalanceVersion: assignment.BalanceVersion,
		})
	}
	return deliverytagruntime.PartitionTopologyVersion{
		HashSlotTableVersion: r.cluster.HashSlotTableVersion(),
		SlotAuthorityRefs:    refs,
	}, nil
}

func (r deliveryTagTopologyReaderAdapter) ValidateCurrentDeliveryTagTopology(ctx context.Context, topology deliverytagruntime.PartitionTopologyVersion) (bool, error) {
	if r.cluster == nil {
		return true, nil
	}
	if topology.HashSlotTableVersion != r.cluster.HashSlotTableVersion() {
		return false, nil
	}
	if len(topology.SlotAuthorityRefs) == 0 {
		return true, nil
	}
	slotIDs := make([]uint32, 0, len(topology.SlotAuthorityRefs))
	for _, ref := range topology.SlotAuthorityRefs {
		slotIDs = append(slotIDs, ref.SlotID)
	}
	assignmentBySlot := currentDeliveryTagAssignmentBySlot(ctx, r.cluster, slotIDs)
	for _, ref := range topology.SlotAuthorityRefs {
		leaderID, err := r.cluster.LeaderOf(multiraft.SlotID(ref.SlotID))
		if err != nil {
			return false, err
		}
		assignment, ok := assignmentBySlot[ref.SlotID]
		if !ok {
			return false, nil
		}
		if uint64(leaderID) != ref.LeaderNodeID || assignment.ConfigEpoch != ref.ConfigEpoch || assignment.BalanceVersion != ref.BalanceVersion {
			return false, nil
		}
	}
	return true, nil
}

func currentDeliveryTagAssignmentBySlot(ctx context.Context, cluster deliveryTagCluster, requiredSlotIDs []uint32) map[uint32]controllermeta.SlotAssignment {
	if len(requiredSlotIDs) == 0 || cluster == nil {
		return nil
	}
	if cached, ok := cluster.(cachedDeliveryTagAssignments); ok {
		assignmentBySlot := deliveryTagAssignmentBySlot(cached.ListCachedAssignments(), requiredSlotIDs)
		if deliveryTagAssignmentMapCoversSlots(assignmentBySlot, requiredSlotIDs) {
			return assignmentBySlot
		}
	}
	assignments, _ := cluster.ListSlotAssignments(ctx)
	return deliveryTagAssignmentBySlot(assignments, requiredSlotIDs)
}

func deliveryTagAssignmentBySlot(assignments []controllermeta.SlotAssignment, requiredSlotIDs []uint32) map[uint32]controllermeta.SlotAssignment {
	if len(assignments) == 0 || len(requiredSlotIDs) == 0 {
		return nil
	}
	assignmentBySlot := make(map[uint32]controllermeta.SlotAssignment, len(requiredSlotIDs))
	for _, assignment := range assignments {
		if !deliveryTagSlotRequired(assignment.SlotID, requiredSlotIDs) {
			continue
		}
		assignmentBySlot[assignment.SlotID] = assignment
		if len(assignmentBySlot) == len(requiredSlotIDs) {
			break
		}
	}
	return assignmentBySlot
}

func deliveryTagSlotRequired(slotID uint32, requiredSlotIDs []uint32) bool {
	for _, requiredSlotID := range requiredSlotIDs {
		if slotID == requiredSlotID {
			return true
		}
	}
	return false
}

func deliveryTagAssignmentMapCoversSlots(assignmentBySlot map[uint32]controllermeta.SlotAssignment, requiredSlotIDs []uint32) bool {
	if len(assignmentBySlot) == 0 {
		return false
	}
	for _, slotID := range requiredSlotIDs {
		if _, ok := assignmentBySlot[slotID]; !ok {
			return false
		}
	}
	return true
}

type localResolveToken struct {
	snapshot    deliveryusecase.SnapshotToken
	env         deliveryruntime.CommittedEnvelope
	channelType uint8
	pending     []deliveryruntime.RouteKey
	done        bool
}

type tagResolveToken struct {
	tag deliverytagruntime.DeliveryTag
	env deliveryruntime.CommittedEnvelope
	// routableUIDs contains the tag UIDs this resolver should expand for the current delivery.
	routableUIDs []string
	nextIndex    int
	channelType  uint8
	ephemeral    bool
	pending      []deliveryruntime.RouteKey
	done         bool
}

type deliveryRoutingMetrics interface {
	ObserveResolve(channelType, result string, dur time.Duration, pages, routes int)
	ObservePushRPC(targetNode, result string, dur time.Duration, routes int)
}

func beginSubscriberSnapshot(ctx context.Context, resolver deliveryusecase.SubscriberResolver, id channel.ChannelID, messageScopedUIDs []string) (deliveryusecase.SnapshotToken, error) {
	if len(messageScopedUIDs) == 0 {
		return resolver.BeginSnapshot(ctx, id)
	}
	return resolver.BeginSnapshotWithRequest(ctx, id, deliveryusecase.SubscriberSnapshotRequest{
		MessageScopedUIDs: append([]string(nil), messageScopedUIDs...),
	})
}

func (r localDeliveryResolver) BeginResolve(ctx context.Context, key deliveryruntime.ChannelKey, env deliveryruntime.CommittedEnvelope) (any, error) {
	if r.subscribers == nil {
		return nil, nil
	}
	startedAt := time.Now()
	snapshot, err := beginSubscriberSnapshot(ctx, r.subscribers, channel.ChannelID{
		ID:   key.ChannelID,
		Type: key.ChannelType,
	}, env.MessageScopedUIDs)
	if err != nil {
		r.recordResolveMetric(deliveryChannelTypeLabel(key.ChannelType), "error", time.Since(startedAt), 0, 0)
		return nil, err
	}
	return &localResolveToken{snapshot: snapshot, env: env, channelType: key.ChannelType}, nil
}

func (r localDeliveryResolver) ResolvePage(ctx context.Context, token any, cursor string, limit int) (page deliveryruntime.ResolvePageResult, err error) {
	startedAt := time.Now()
	pages := 0
	channelType := "unknown"
	defer func() {
		result := "ok"
		if err != nil {
			result = "error"
		}
		r.recordResolveMetric(channelType, result, time.Since(startedAt), pages, len(page.Routes))
	}()
	if r.subscribers == nil || r.authority == nil {
		return deliveryruntime.ResolvePageResult{Done: true}, nil
	}
	if limit <= 0 {
		limit = r.pageSize
	}
	if limit <= 0 {
		limit = 128
	}

	resolveToken, ok := token.(*localResolveToken)
	if !ok {
		return deliveryruntime.ResolvePageResult{Done: true}, nil
	}
	channelType = deliveryChannelTypeLabel(resolveToken.channelType)

	out := make([]deliveryruntime.RouteKey, 0, localResolveRouteCap(limit, resolveToken.channelType))
	var offlineUIDs []string
	if len(resolveToken.pending) > 0 {
		taken := limit
		if taken > len(resolveToken.pending) {
			taken = len(resolveToken.pending)
		}
		out = append(out, resolveToken.pending[:taken]...)
		resolveToken.pending = resolveToken.pending[taken:]
		if len(out) == limit || resolveToken.done {
			return deliveryruntime.ResolvePageResult{
				Routes:     out,
				NextCursor: cursor,
				Done:       resolveToken.done && len(resolveToken.pending) == 0,
			}, nil
		}
	}

	pageSize := r.pageSize
	if pageSize <= 0 {
		pageSize = 128
	}

	for len(out) < limit {
		if resolveToken.done {
			return deliveryruntime.ResolvePageResult{Routes: out, OfflineUIDs: offlineUIDs, NextCursor: cursor, Done: true}, nil
		}

		pages++
		uids, nextCursor, done, err := r.subscribers.NextPage(ctx, resolveToken.snapshot, cursor, pageSize)
		if err != nil {
			return deliveryruntime.ResolvePageResult{}, err
		}
		cursor = nextCursor
		resolveToken.done = done
		if len(uids) == 0 {
			if done {
				return deliveryruntime.ResolvePageResult{Routes: out, OfflineUIDs: offlineUIDs, NextCursor: cursor, Done: true}, nil
			}
			continue
		}
		notifyResolvedUIDPage(ctx, r.uidObserver, resolveToken.env, uids)

		var (
			expandedCount int
			missing       []string
			overflow      []deliveryruntime.RouteKey
		)
		offlineBefore := len(offlineUIDs)
		remaining := limit - len(out)
		debugEnabled := wklog.DebugEnabled(r.logger)
		if debugEnabled {
			missing = make([]string, 0, len(uids))
		}
		if resolveToken.channelType == frame.ChannelTypePerson {
			for _, uid := range uids {
				routes, err := r.authority.EndpointsByUID(ctx, uid)
				if err != nil {
					return deliveryruntime.ResolvePageResult{}, err
				}
				if len(routes) == 0 && debugEnabled {
					missing = append(missing, uid)
				}
				if len(routes) == 0 && r.collectOfflineUIDs {
					offlineUIDs = append(offlineUIDs, uid)
				}
				expandedCount += len(routes)
				out, overflow = appendResolvedPresenceRoutes(out, overflow, routes, &remaining)
			}
		} else {
			endpointsByUID, err := r.authority.EndpointsByUIDs(ctx, uids)
			if err != nil {
				return deliveryruntime.ResolvePageResult{}, err
			}
			for _, uid := range uids {
				routes := endpointsByUID[uid]
				if len(routes) == 0 && debugEnabled {
					missing = append(missing, uid)
				}
				if len(routes) == 0 && r.collectOfflineUIDs {
					offlineUIDs = append(offlineUIDs, uid)
				}
				expandedCount += len(routes)
				out, overflow = appendResolvedPresenceRoutes(out, overflow, routes, &remaining)
			}
		}
		if debugEnabled {
			fields := []wklog.Field{
				wklog.Event("delivery.diag.resolve_page"),
				wklog.String("cursor", cursor),
				wklog.Int("uids", len(uids)),
				wklog.Int("routes", expandedCount),
			}
			if len(missing) > 0 {
				fields = append(fields, wklog.String("missingUIDs", strings.Join(missing, ",")))
				r.logger.Debug("delivery resolver found missing authoritative endpoints", fields...)
			} else {
				r.logger.Debug("delivery resolver expanded authoritative endpoints", fields...)
			}
		}

		if len(overflow) > 0 {
			resolveToken.pending = append(resolveToken.pending[:0], overflow...)
			return deliveryruntime.ResolvePageResult{Routes: out, OfflineUIDs: offlineUIDs, NextCursor: cursor}, nil
		}
		if done {
			return deliveryruntime.ResolvePageResult{Routes: out, OfflineUIDs: offlineUIDs, NextCursor: cursor, Done: true}, nil
		}
		if len(offlineUIDs) > offlineBefore {
			return deliveryruntime.ResolvePageResult{Routes: out, OfflineUIDs: offlineUIDs, NextCursor: cursor}, nil
		}
		if expandedCount == 0 {
			continue
		}
	}
	return deliveryruntime.ResolvePageResult{Routes: out, OfflineUIDs: offlineUIDs, NextCursor: cursor}, nil
}

// appendResolvedPresenceRoutes appends resolved endpoints to the current page and spills excess routes to overflow.
func appendResolvedPresenceRoutes(out, overflow []deliveryruntime.RouteKey, routes []presence.Route, remaining *int) ([]deliveryruntime.RouteKey, []deliveryruntime.RouteKey) {
	for _, route := range routes {
		resolved := deliveryruntime.RouteKey{
			UID:       route.UID,
			NodeID:    route.NodeID,
			BootID:    route.BootID,
			SessionID: route.SessionID,
		}
		if remaining != nil && *remaining > 0 {
			out = append(out, resolved)
			*remaining = *remaining - 1
			continue
		}
		overflow = append(overflow, resolved)
	}
	return out, overflow
}

// localResolveRouteCap bounds the optimistic route buffer for common small fanout paths.
func localResolveRouteCap(limit int, channelType uint8) int {
	if limit <= 0 {
		return 0
	}
	if channelType == frame.ChannelTypePerson {
		return min(limit, 2)
	}
	return min(limit, 16)
}

func (r localDeliveryResolver) recordResolveMetric(channelType, result string, dur time.Duration, pages, routes int) {
	if r.metrics == nil {
		return
	}
	r.metrics.ObserveResolve(channelType, result, dur, pages, routes)
}

func (r tagDeliveryResolver) BeginResolve(ctx context.Context, key deliveryruntime.ChannelKey, env deliveryruntime.CommittedEnvelope) (any, error) {
	if r.tags == nil || r.subscribers == nil || deliveryTagPartitionBypassed(key.ChannelType) {
		return localDeliveryResolver{
			subscribers:        r.subscribers,
			authority:          r.authority,
			uidObserver:        r.uidObserver,
			collectOfflineUIDs: r.collectOfflineUIDs,
			pageSize:           r.pageSize,
			metrics:            r.metrics,
			logger:             r.logger,
		}.BeginResolve(ctx, key, env)
	}
	startedAt := time.Now()
	channelKey := deliveryTagChannelKey(key)
	snapshot, err := beginSubscriberSnapshot(ctx, r.subscribers, channel.ChannelID{ID: key.ChannelID, Type: key.ChannelType}, env.MessageScopedUIDs)
	if err != nil {
		r.recordResolveMetric(deliveryChannelTypeLabel(key.ChannelType), "error", time.Since(startedAt), 0, 0)
		return nil, err
	}
	source := snapshot.Source()
	tag, err := r.leaderTagFromSnapshot(ctx, key, channelKey, snapshot)
	if err != nil {
		r.recordResolveMetric(deliveryChannelTypeLabel(key.ChannelType), "error", time.Since(startedAt), 0, 0)
		return nil, err
	}
	return &tagResolveToken{
		tag:          tag,
		env:          env,
		routableUIDs: deliveryTagRoutableUIDs(tag),
		channelType:  key.ChannelType,
		ephemeral:    !source.ReusableTagState,
	}, nil
}

func deliveryTagPartitionBypassed(channelType uint8) bool {
	// Person-channel subscriber sets are derived and tiny; resolving them on the
	// current committed owner avoids tag partitions tied to unrelated Slot owners.
	return channelType == frame.ChannelTypePerson
}

func (r tagDeliveryResolver) ResolvePage(ctx context.Context, token any, cursor string, limit int) (page deliveryruntime.ResolvePageResult, err error) {
	if _, local := token.(*localResolveToken); local {
		return localDeliveryResolver{
			subscribers:        r.subscribers,
			authority:          r.authority,
			uidObserver:        r.uidObserver,
			collectOfflineUIDs: r.collectOfflineUIDs,
			pageSize:           r.pageSize,
			metrics:            r.metrics,
			logger:             r.logger,
		}.ResolvePage(ctx, token, cursor, limit)
	}

	startedAt := time.Now()
	pages := 0
	channelType := "unknown"
	defer func() {
		result := "ok"
		if err != nil {
			result = "error"
		}
		r.recordResolveMetric(channelType, result, time.Since(startedAt), pages, len(page.Routes))
	}()
	if r.authority == nil {
		return deliveryruntime.ResolvePageResult{Done: true}, nil
	}
	if limit <= 0 {
		limit = r.pageSize
	}
	if limit <= 0 {
		limit = 128
	}
	resolveToken, ok := token.(*tagResolveToken)
	if !ok {
		return localDeliveryResolver{
			subscribers:        r.subscribers,
			authority:          r.authority,
			uidObserver:        r.uidObserver,
			collectOfflineUIDs: r.collectOfflineUIDs,
			pageSize:           r.pageSize,
			metrics:            r.metrics,
			logger:             r.logger,
		}.ResolvePage(ctx, token, cursor, limit)
	}
	channelType = deliveryChannelTypeLabel(resolveToken.channelType)
	if !resolveToken.ephemeral {
		if refreshed, hit, reason := r.tags.LookupLocalPartitionRef(deliverytagruntime.TagRef{
			ChannelKey:                      resolveToken.tag.ChannelKey,
			TagKey:                          resolveToken.tag.Key,
			TagVersion:                      resolveToken.tag.TagVersion,
			SubscriberMutationVersion:       resolveToken.tag.SubscriberMutationVersion,
			SourceChannelKey:                resolveToken.tag.SourceChannelKey,
			SourceSubscriberMutationVersion: resolveToken.tag.SourceSubscriberMutationVersion,
			Topology:                        resolveToken.tag.Topology,
		}); hit {
			resolveToken.tag = refreshed
		} else if reason == deliverytagruntime.LookupStaleRequest || reason == deliverytagruntime.LookupTagKeyMismatch {
			resolveToken.tag = refreshed
			resolveToken.routableUIDs = deliveryTagRoutableUIDs(refreshed)
			resolveToken.nextIndex = deliveryTagIndexAfterCursor(resolveToken.routableUIDs, cursor)
		}
	}
	if resolveToken.done {
		return deliveryruntime.ResolvePageResult{NextCursor: cursor, Done: true}, nil
	}
	out := make([]deliveryruntime.RouteKey, 0, localResolveRouteCap(limit, resolveToken.channelType))
	var offlineUIDs []string
	if len(resolveToken.pending) > 0 {
		taken := limit
		if taken > len(resolveToken.pending) {
			taken = len(resolveToken.pending)
		}
		out = append(out, resolveToken.pending[:taken]...)
		resolveToken.pending = resolveToken.pending[taken:]
		if len(out) == limit || len(resolveToken.pending) > 0 {
			return deliveryruntime.ResolvePageResult{Routes: out, NextCursor: cursor}, nil
		}
	}

	pageSize := r.pageSize
	if pageSize <= 0 {
		pageSize = 128
	}
	for len(out) < limit {
		uids, nextIndex, pageDone := nextDeliveryTagUIDPageAt(resolveToken.routableUIDs, resolveToken.nextIndex, minInt(pageSize, limit-len(out)))
		pages++
		resolveToken.nextIndex = nextIndex
		if len(uids) > 0 {
			cursor = uids[len(uids)-1]
		}
		if len(uids) == 0 {
			resolveToken.done = pageDone
			return deliveryruntime.ResolvePageResult{
				Routes:      out,
				OfflineUIDs: offlineUIDs,
				NextCursor:  cursor,
				Done:        pageDone && len(resolveToken.pending) == 0,
			}, nil
		}
		notifyResolvedUIDPage(ctx, r.uidObserver, resolveToken.env, uids)
		offlineBefore := len(offlineUIDs)
		expanded, overflow, offline, err := r.expandTagUIDs(ctx, uids, limit-len(out), resolveToken.channelType)
		if err != nil {
			return deliveryruntime.ResolvePageResult{}, err
		}
		offlineUIDs = append(offlineUIDs, offline...)
		out = append(out, expanded...)
		if len(overflow) > 0 {
			resolveToken.pending = append(resolveToken.pending[:0], overflow...)
			return deliveryruntime.ResolvePageResult{Routes: out, OfflineUIDs: offlineUIDs, NextCursor: cursor}, nil
		}
		if pageDone {
			resolveToken.done = true
			return deliveryruntime.ResolvePageResult{Routes: out, OfflineUIDs: offlineUIDs, NextCursor: cursor, Done: true}, nil
		}
		if len(offlineUIDs) > offlineBefore {
			return deliveryruntime.ResolvePageResult{Routes: out, OfflineUIDs: offlineUIDs, NextCursor: cursor}, nil
		}
		if len(expanded) == 0 {
			continue
		}
	}
	return deliveryruntime.ResolvePageResult{Routes: out, OfflineUIDs: offlineUIDs, NextCursor: cursor}, nil
}

func (r tagDeliveryResolver) leaderTagFromSnapshot(ctx context.Context, key deliveryruntime.ChannelKey, channelKey string, snapshot deliveryusecase.SnapshotToken) (deliverytagruntime.DeliveryTag, error) {
	source := snapshot.Source()
	if deliveryTagCanUseCachedTagFastPath(source) {
		if ref, ok := r.tags.CurrentRef(channelKey); ok {
			if validator, ok := r.topology.(deliveryTagTopologyValidator); ok {
				valid, err := validator.ValidateCurrentDeliveryTagTopology(ctx, ref.Topology)
				if err == nil && valid {
					if tag, hit, reason := r.tags.LookupLocalPartitionRef(deliverytagruntime.TagRef{
						ChannelKey:                      channelKey,
						TagKey:                          ref.TagKey,
						TagVersion:                      ref.TagVersion,
						SubscriberMutationVersion:       source.SubscriberMutationVersion,
						SourceChannelKey:                deliveryTagSourceChannelKey(source),
						SourceSubscriberMutationVersion: source.SourceSubscriberMutationVersion,
						Topology:                        ref.Topology,
					}); hit || reason == deliverytagruntime.LookupStaleRequest {
						return tag, nil
					}
				}
			}
		}
	}
	uids, err := r.collectSnapshotUIDs(ctx, snapshot)
	if err != nil {
		return deliverytagruntime.DeliveryTag{}, err
	}
	topology, err := r.currentTagTopology(ctx, uids)
	if err != nil {
		return deliverytagruntime.DeliveryTag{}, err
	}
	if !source.ReusableTagState {
		tag, _ := r.tags.BuildEphemeralTag(deliverytagruntime.BuildRequest{
			ChannelKey:                      channelKey,
			SubscriberMutationVersion:       source.SubscriberMutationVersion,
			SourceChannelKey:                deliveryTagSourceChannelKey(source),
			SourceSubscriberMutationVersion: source.SourceSubscriberMutationVersion,
			Topology:                        topology,
			Partitions:                      r.partitionDeliveryTagUIDs(ctx, uids, topology),
		})
		return tag, nil
	}
	if ref, ok := r.tags.CurrentRef(channelKey); ok {
		if tag, hit, reason := r.tags.LookupLocalPartitionRef(deliverytagruntime.TagRef{
			ChannelKey:                      channelKey,
			TagKey:                          ref.TagKey,
			TagVersion:                      ref.TagVersion,
			SubscriberMutationVersion:       source.SubscriberMutationVersion,
			SourceChannelKey:                deliveryTagSourceChannelKey(source),
			SourceSubscriberMutationVersion: source.SourceSubscriberMutationVersion,
			Topology:                        topology,
		}); hit {
			return tag, nil
		} else if reason == deliverytagruntime.LookupStaleRequest {
			return tag, nil
		}
	}
	tag, _ := r.tags.BuildLeaderTag(deliverytagruntime.BuildRequest{
		ChannelKey:                      channelKey,
		SubscriberMutationVersion:       source.SubscriberMutationVersion,
		SourceChannelKey:                deliveryTagSourceChannelKey(source),
		SourceSubscriberMutationVersion: source.SourceSubscriberMutationVersion,
		Topology:                        topology,
		Partitions:                      r.partitionDeliveryTagUIDs(ctx, uids, topology),
	})
	return tag, nil
}

func deliveryTagCanUseCachedTagFastPath(source deliveryusecase.SubscriberSource) bool {
	if !source.ReusableTagState {
		return false
	}
	if source.Kind == deliveryusecase.SubscriberSourceKindDerived {
		return true
	}
	return source.SubscriberMutationVersion != 0 || source.SourceSubscriberMutationVersion != 0
}

func (r tagDeliveryResolver) collectSnapshotUIDs(ctx context.Context, snapshot deliveryusecase.SnapshotToken) ([]string, error) {
	pageSize := r.pageSize
	if pageSize <= 0 {
		pageSize = 128
	}
	cursor := ""
	uids := make([]string, 0, pageSize)
	for {
		page, next, done, err := r.subscribers.NextPage(ctx, snapshot, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		uids = append(uids, page...)
		if done {
			return uniqueStringsForDeliveryTag(uids), nil
		}
		if next == "" || next == cursor {
			return uniqueStringsForDeliveryTag(uids), nil
		}
		cursor = next
	}
}

func (r tagDeliveryResolver) currentTagTopology(ctx context.Context, uids []string) (deliverytagruntime.PartitionTopologyVersion, error) {
	if r.topology == nil {
		return deliverytagruntime.PartitionTopologyVersion{}, nil
	}
	return r.topology.CurrentDeliveryTagTopology(ctx, uids)
}

func (r tagDeliveryResolver) partitionDeliveryTagUIDs(_ context.Context, uids []string, topology deliverytagruntime.PartitionTopologyVersion) []deliverytagruntime.NodePartition {
	byNode := make(map[uint64][]string)
	for index, uid := range uids {
		nodeID := r.localNodeID
		if len(topology.SlotAuthorityRefs) > 0 {
			nodeID = topology.SlotAuthorityRefs[index%len(topology.SlotAuthorityRefs)].LeaderNodeID
		}
		if nodeID == 0 {
			nodeID = r.localNodeID
		}
		byNode[nodeID] = append(byNode[nodeID], uid)
	}
	nodeIDs := make([]uint64, 0, len(byNode))
	for nodeID := range byNode {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	out := make([]deliverytagruntime.NodePartition, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		out = append(out, deliverytagruntime.NodePartition{NodeID: nodeID, UIDs: byNode[nodeID]})
	}
	return out
}

func (r tagDeliveryResolver) expandTagUIDs(ctx context.Context, uids []string, limit int, channelType uint8) ([]deliveryruntime.RouteKey, []deliveryruntime.RouteKey, []string, error) {
	if len(uids) == 0 || limit <= 0 {
		return nil, nil, nil, nil
	}
	remaining := limit
	out := make([]deliveryruntime.RouteKey, 0, localResolveRouteCap(limit, channelType))
	var overflow []deliveryruntime.RouteKey
	var offlineUIDs []string
	if channelType == frame.ChannelTypePerson {
		for _, uid := range uids {
			routes, err := r.authority.EndpointsByUID(ctx, uid)
			if err != nil {
				return nil, nil, nil, err
			}
			if len(routes) == 0 && r.collectOfflineUIDs {
				offlineUIDs = append(offlineUIDs, uid)
			}
			out, overflow = appendResolvedPresenceRoutes(out, overflow, routes, &remaining)
		}
		return out, overflow, offlineUIDs, nil
	}
	endpointsByUID, err := r.authority.EndpointsByUIDs(ctx, uids)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, uid := range uids {
		routes := endpointsByUID[uid]
		if len(routes) == 0 && r.collectOfflineUIDs {
			offlineUIDs = append(offlineUIDs, uid)
		}
		out, overflow = appendResolvedPresenceRoutes(out, overflow, routes, &remaining)
	}
	return out, overflow, offlineUIDs, nil
}

func notifyResolvedUIDPage(ctx context.Context, observer resolvedUIDObserver, env deliveryruntime.CommittedEnvelope, uids []string) {
	if observer == nil || len(uids) == 0 {
		return
	}
	if env.CMDConversationIntentSubmitted && len(env.MessageScopedUIDs) > 0 {
		return
	}
	observer.OnResolvedUIDPage(ctx, resolvedUIDPage{
		Envelope: env,
		UIDs:     append([]string(nil), uids...),
	})
}

func (r tagDeliveryResolver) recordResolveMetric(channelType, result string, dur time.Duration, pages, routes int) {
	if r.metrics == nil {
		return
	}
	r.metrics.ObserveResolve(channelType, result, dur, pages, routes)
}

func nextDeliveryTagUIDPageAt(uids []string, start int, limit int) ([]string, int, bool) {
	if limit <= 0 {
		return nil, start, false
	}
	if start < 0 {
		start = 0
	}
	if start >= len(uids) {
		return nil, len(uids), true
	}
	end := start + limit
	if end > len(uids) {
		end = len(uids)
	}
	return uids[start:end], end, end >= len(uids)
}

// deliveryTagRoutableUIDs returns every UID available in the current node-local tag body.
func deliveryTagRoutableUIDs(tag deliverytagruntime.DeliveryTag) []string {
	if len(tag.Partitions) == 0 {
		return nil
	}
	if len(tag.Partitions) == 1 {
		return tag.Partitions[0].UIDs
	}
	total := 0
	for _, partition := range tag.Partitions {
		total += len(partition.UIDs)
	}
	uids := make([]string, 0, total)
	for _, partition := range tag.Partitions {
		uids = append(uids, partition.UIDs...)
	}
	return uids
}

func deliveryTagIndexAfterCursor(uids []string, cursor string) int {
	if cursor == "" {
		return 0
	}
	for i, uid := range uids {
		if uid == cursor {
			return i + 1
		}
	}
	return 0
}

func deliveryTagChannelKey(key deliveryruntime.ChannelKey) string {
	return strconv.Itoa(int(key.ChannelType)) + ":" + key.ChannelID
}

func deliveryTagSourceChannelKey(source deliveryusecase.SubscriberSource) string {
	if source.SourceChannelID == "" {
		return ""
	}
	return strconv.Itoa(int(source.SourceChannelType)) + ":" + source.SourceChannelID
}

func uniqueStringsForDeliveryTag(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

type localDeliveryPush struct {
	online        online.Registry
	localNodeID   uint64
	gatewayBootID uint64
	logger        wklog.Logger
}

func (p localDeliveryPush) Push(_ context.Context, cmd deliveryruntime.PushCommand) (deliveryruntime.PushResult, error) {
	return p.pushEnvelope(cmd.Envelope, cmd.Routes), nil
}

func (p localDeliveryPush) pushEnvelope(env deliveryruntime.CommittedEnvelope, routes []deliveryruntime.RouteKey) deliveryruntime.PushResult {
	result := deliveryruntime.PushResult{}
	var sharedFrame frame.Frame
	var framesByUID map[string]frame.Frame
	if env.ChannelType == frame.ChannelTypePerson && len(routes) > 1 {
		frameCacheCap := len(routes)
		if frameCacheCap > 2 {
			frameCacheCap = 2
		}
		framesByUID = make(map[string]frame.Frame, frameCacheCap)
	}
	for _, route := range routes {
		switch {
		case isSenderDeliveryRoute(env, route):
			result.Dropped = append(result.Dropped, route)
		case p.localNodeID != 0 && route.NodeID != p.localNodeID:
			result.Dropped = append(result.Dropped, route)
		case p.gatewayBootID != 0 && route.BootID != p.gatewayBootID:
			result.Dropped = append(result.Dropped, route)
		default:
			conn, ok := p.online.Connection(route.SessionID)
			if !ok || conn.UID != route.UID || conn.State != online.LocalRouteStateActive || conn.Session == nil {
				result.Dropped = append(result.Dropped, route)
				continue
			}
			f := sharedFrame
			if env.ChannelType == frame.ChannelTypePerson {
				if framesByUID == nil {
					f = buildRealtimeRecvPacket(env.Message, route.UID)
				} else {
					f = framesByUID[route.UID]
					if f == nil {
						f = buildRealtimeRecvPacket(env.Message, route.UID)
						framesByUID[route.UID] = f
					}
				}
			} else if f == nil {
				f = buildRealtimeRecvPacket(env.Message, "")
				sharedFrame = f
			}
			if err := conn.Session.WriteFrame(f); err != nil {
				result.Retryable = append(result.Retryable, route)
				continue
			}
			result.Accepted = append(result.Accepted, route)
		}
	}
	if wklog.DebugEnabled(p.logger) {
		p.logger.Debug("local delivery push finished",
			wklog.Event("delivery.diag.local_push"),
			wklog.String("channelID", env.ChannelID),
			wklog.Int("channelType", int(env.ChannelType)),
			wklog.Uint64("messageID", env.MessageID),
			wklog.Uint64("messageSeq", env.MessageSeq),
			wklog.Int("accepted", len(result.Accepted)),
			wklog.Int("retryable", len(result.Retryable)),
			wklog.Int("dropped", len(result.Dropped)),
		)
	}
	return result
}

type distributedDeliveryPush struct {
	localNodeID uint64
	local       localDeliveryPush
	client      deliveryPushClient
	codec       codec.Protocol
	metrics     deliveryRoutingMetrics
	logger      wklog.Logger
}

type deliveryPushClient interface {
	PushBatch(ctx context.Context, nodeID uint64, cmd accessnode.DeliveryPushCommand) (accessnode.DeliveryPushResponse, error)
	PushBatchItems(ctx context.Context, nodeID uint64, cmd accessnode.DeliveryPushBatchCommand) (accessnode.DeliveryPushResponse, error)
}

func (p distributedDeliveryPush) Push(ctx context.Context, cmd deliveryruntime.PushCommand) (deliveryruntime.PushResult, error) {
	if p.codec == nil {
		p.codec = codec.New()
	}
	if len(cmd.Routes) == 1 {
		return p.pushSingleRoute(ctx, cmd)
	}

	localRoutes := make([]deliveryruntime.RouteKey, 0, len(cmd.Routes))
	remoteRoutes := make(map[uint64][]deliveryruntime.RouteKey)
	result := deliveryruntime.PushResult{}
	for _, route := range cmd.Routes {
		if isSenderDeliveryRoute(cmd.Envelope, route) {
			result.Dropped = append(result.Dropped, route)
			continue
		}
		if route.NodeID == p.localNodeID {
			localRoutes = append(localRoutes, route)
			continue
		}
		remoteRoutes[route.NodeID] = append(remoteRoutes[route.NodeID], route)
	}

	if len(localRoutes) > 0 {
		localResult := p.local.pushEnvelope(cmd.Envelope, localRoutes)
		result.Accepted = append(result.Accepted, localResult.Accepted...)
		result.Retryable = append(result.Retryable, localResult.Retryable...)
		result.Dropped = append(result.Dropped, localResult.Dropped...)
	}
	if len(remoteRoutes) == 0 {
		return result, nil
	}

	nodeIDs := sortedDeliveryNodeIDs(remoteRoutes)
	if p.client == nil {
		for _, nodeID := range nodeIDs {
			result.Retryable = append(result.Retryable, remoteRoutes[nodeID]...)
		}
		return result, nil
	}

	var sharedFrame []byte
	if cmd.Envelope.ChannelType != frame.ChannelTypePerson {
		encoded, err := p.encodeDeliveryFrame(cmd.Envelope, "")
		if err != nil {
			return deliveryruntime.PushResult{}, err
		}
		sharedFrame = encoded
	}

	remoteResults, err := p.pushRemoteNodes(ctx, cmd.Envelope, remoteRoutes, nodeIDs, sharedFrame)
	if err != nil {
		return deliveryruntime.PushResult{}, err
	}
	for _, remoteResult := range remoteResults {
		result.Accepted = append(result.Accepted, remoteResult.Accepted...)
		result.Retryable = append(result.Retryable, remoteResult.Retryable...)
		result.Dropped = append(result.Dropped, remoteResult.Dropped...)
	}
	return result, nil
}

func (p distributedDeliveryPush) pushSingleRoute(ctx context.Context, cmd deliveryruntime.PushCommand) (deliveryruntime.PushResult, error) {
	route := cmd.Routes[0]
	if isSenderDeliveryRoute(cmd.Envelope, route) {
		return deliveryruntime.PushResult{Dropped: cmd.Routes}, nil
	}
	if route.NodeID == p.localNodeID {
		return p.local.pushEnvelope(cmd.Envelope, cmd.Routes), nil
	}
	if p.client == nil {
		return deliveryruntime.PushResult{Retryable: cmd.Routes}, nil
	}
	return p.pushRemoteNode(ctx, cmd.Envelope, route.NodeID, cmd.Routes, nil)
}

func isSenderDeliveryRoute(env deliveryruntime.CommittedEnvelope, route deliveryruntime.RouteKey) bool {
	if env.SenderSessionID == 0 || route.SessionID != env.SenderSessionID {
		return false
	}
	return env.FromUID == "" || route.UID == env.FromUID
}

type remoteDeliveryPushResult struct {
	index  int
	result deliveryruntime.PushResult
	err    error
}

func (p distributedDeliveryPush) pushRemoteNodes(ctx context.Context, env deliveryruntime.CommittedEnvelope, remoteRoutes map[uint64][]deliveryruntime.RouteKey, nodeIDs []uint64, sharedFrame []byte) ([]deliveryruntime.PushResult, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	if len(nodeIDs) == 1 {
		nodeID := nodeIDs[0]
		result, err := p.pushRemoteNode(ctx, env, nodeID, remoteRoutes[nodeID], sharedFrame)
		if err != nil {
			return nil, err
		}
		return []deliveryruntime.PushResult{result}, nil
	}

	results := make([]deliveryruntime.PushResult, len(nodeIDs))
	workers := min(4, len(nodeIDs))
	jobs := make(chan int)
	out := make(chan remoteDeliveryPushResult, len(nodeIDs))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				nodeID := nodeIDs[index]
				result, err := p.pushRemoteNode(ctx, env, nodeID, remoteRoutes[nodeID], sharedFrame)
				out <- remoteDeliveryPushResult{index: index, result: result, err: err}
			}
		}()
	}
	for i := range nodeIDs {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(out)

	var firstErr error
	for item := range out {
		if item.err != nil && firstErr == nil {
			firstErr = item.err
		}
		results[item.index] = item.result
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

func (p distributedDeliveryPush) pushRemoteNode(ctx context.Context, env deliveryruntime.CommittedEnvelope, nodeID uint64, routes []deliveryruntime.RouteKey, sharedFrame []byte) (deliveryruntime.PushResult, error) {
	if env.ChannelType == frame.ChannelTypePerson || len(routes) <= deliveryPushRouteChunkSize {
		return p.pushRemoteNodeChunk(ctx, env, nodeID, routes, sharedFrame)
	}

	result := deliveryruntime.PushResult{}
	for start := 0; start < len(routes); start += deliveryPushRouteChunkSize {
		end := start + deliveryPushRouteChunkSize
		if end > len(routes) {
			end = len(routes)
		}
		chunkResult, err := p.pushRemoteNodeChunk(ctx, env, nodeID, routes[start:end], sharedFrame)
		if err != nil {
			return deliveryruntime.PushResult{}, err
		}
		result.Accepted = append(result.Accepted, chunkResult.Accepted...)
		result.Retryable = append(result.Retryable, chunkResult.Retryable...)
		result.Dropped = append(result.Dropped, chunkResult.Dropped...)
	}
	return result, nil
}

func (p distributedDeliveryPush) pushRemoteNodeChunk(ctx context.Context, env deliveryruntime.CommittedEnvelope, nodeID uint64, routes []deliveryruntime.RouteKey, sharedFrame []byte) (deliveryruntime.PushResult, error) {
	items, err := p.deliveryPushItems(env, routes, sharedFrame)
	if err != nil {
		return deliveryruntime.PushResult{}, err
	}
	startedAt := time.Now()
	resp, err := p.client.PushBatchItems(ctx, nodeID, accessnode.DeliveryPushBatchCommand{
		OwnerNodeID: p.localNodeID,
		Items:       items,
	})
	if err != nil {
		if p.logger != nil {
			p.logger.Warn("remote delivery push failed",
				wklog.Event("delivery.diag.remote_push"),
				wklog.String("channelID", env.ChannelID),
				wklog.Int("channelType", int(env.ChannelType)),
				wklog.Uint64("messageID", env.MessageID),
				wklog.Uint64("messageSeq", env.MessageSeq),
				wklog.Uint64("targetNodeID", nodeID),
				wklog.Int("items", len(items)),
				wklog.Int("routes", len(routes)),
				wklog.Error(err),
			)
		}
		legacyResult := p.pushLegacyDeliveryItems(ctx, nodeID, items, routes)
		p.recordPushRPCMetric(nodeID, "error", time.Since(startedAt), len(routes))
		return legacyResult, nil
	}
	if wklog.DebugEnabled(p.logger) {
		p.logger.Debug("remote delivery push finished",
			wklog.Event("delivery.diag.remote_push"),
			wklog.String("channelID", env.ChannelID),
			wklog.Int("channelType", int(env.ChannelType)),
			wklog.Uint64("messageID", env.MessageID),
			wklog.Uint64("messageSeq", env.MessageSeq),
			wklog.Uint64("targetNodeID", nodeID),
			wklog.Int("items", len(items)),
			wklog.Int("routes", len(routes)),
			wklog.Int("accepted", deliveryPushResponseAcceptedCount(resp)),
			wklog.Int("retryable", len(resp.Retryable)),
			wklog.Int("dropped", len(resp.Dropped)),
		)
	}
	result := deliveryruntime.PushResult{}
	mergeDeliveryPushResponse(&result, routes, resp)
	missing := unreportedDeliveryRoutes(routes, resp)
	if len(missing) > 0 {
		if p.logger != nil {
			p.logger.Warn("remote delivery push response omitted routes; falling back to legacy push",
				wklog.Event("delivery.diag.remote_push_legacy_fallback"),
				wklog.String("channelID", env.ChannelID),
				wklog.Int("channelType", int(env.ChannelType)),
				wklog.Uint64("messageID", env.MessageID),
				wklog.Uint64("messageSeq", env.MessageSeq),
				wklog.Uint64("targetNodeID", nodeID),
				wklog.Int("missingRoutes", len(missing)),
			)
		}
		legacyResult := p.pushLegacyDeliveryItems(ctx, nodeID, items, missing)
		result.Accepted = append(result.Accepted, legacyResult.Accepted...)
		result.Retryable = append(result.Retryable, legacyResult.Retryable...)
		result.Dropped = append(result.Dropped, legacyResult.Dropped...)
	}
	p.recordPushRPCMetric(nodeID, deliveryPushRPCMetricResult(resp, missing), time.Since(startedAt), len(routes))
	return result, nil
}

func sortedDeliveryNodeIDs(routes map[uint64][]deliveryruntime.RouteKey) []uint64 {
	nodeIDs := make([]uint64, 0, len(routes))
	for nodeID := range routes {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	return nodeIDs
}

func (p distributedDeliveryPush) recordPushRPCMetric(nodeID uint64, result string, dur time.Duration, routes int) {
	if p.metrics == nil {
		return
	}
	p.metrics.ObservePushRPC(strconv.FormatUint(nodeID, 10), result, dur, routes)
}

func deliveryPushRPCMetricResult(resp accessnode.DeliveryPushResponse, missing []deliveryruntime.RouteKey) string {
	if len(resp.Retryable) > 0 || len(resp.Dropped) > 0 || len(missing) > 0 {
		return "partial"
	}
	return "ok"
}

func deliveryPushResponseAcceptedCount(resp accessnode.DeliveryPushResponse) int {
	if resp.AcceptedCountSet {
		return int(resp.AcceptedCount)
	}
	return len(resp.Accepted)
}

func (p distributedDeliveryPush) deliveryPushItems(env deliveryruntime.CommittedEnvelope, routes []deliveryruntime.RouteKey, sharedFrame []byte) ([]accessnode.DeliveryPushItem, error) {
	if env.ChannelType != frame.ChannelTypePerson {
		item, err := p.deliveryPushItemWithFrame(env, routes, sharedFrame)
		if err != nil {
			return nil, err
		}
		return []accessnode.DeliveryPushItem{item}, nil
	}
	if len(routes) == 1 {
		item, err := p.deliveryPushItem(env, routes, routes[0].UID)
		if err != nil {
			return nil, err
		}
		return []accessnode.DeliveryPushItem{item}, nil
	}

	routesByView := make(map[string][]deliveryruntime.RouteKey)
	for _, route := range routes {
		view := recipientChannelView(env.Message, route.UID)
		routesByView[view] = append(routesByView[view], route)
	}
	items := make([]accessnode.DeliveryPushItem, 0, len(routesByView))
	for _, groupedRoutes := range routesByView {
		item, err := p.deliveryPushItem(env, groupedRoutes, groupedRoutes[0].UID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (p distributedDeliveryPush) deliveryPushItem(env deliveryruntime.CommittedEnvelope, routes []deliveryruntime.RouteKey, recipientUID string) (accessnode.DeliveryPushItem, error) {
	frameBytes, err := p.encodeDeliveryFrame(env, recipientUID)
	if err != nil {
		return accessnode.DeliveryPushItem{}, err
	}
	return p.deliveryPushItemWithFrame(env, routes, frameBytes)
}

func (p distributedDeliveryPush) encodeDeliveryFrame(env deliveryruntime.CommittedEnvelope, recipientUID string) ([]byte, error) {
	f := buildRealtimeRecvPacket(env.Message, recipientUID)
	return p.codec.EncodeFrame(f, frame.LatestVersion)
}

func (p distributedDeliveryPush) deliveryPushItemWithFrame(env deliveryruntime.CommittedEnvelope, routes []deliveryruntime.RouteKey, frameBytes []byte) (accessnode.DeliveryPushItem, error) {
	if frameBytes == nil {
		var err error
		frameBytes, err = p.encodeDeliveryFrame(env, "")
		if err != nil {
			return accessnode.DeliveryPushItem{}, err
		}
	}
	return accessnode.DeliveryPushItem{
		ChannelID:   env.ChannelID,
		ChannelType: env.ChannelType,
		MessageID:   env.MessageID,
		MessageSeq:  env.MessageSeq,
		Routes:      append([]deliveryruntime.RouteKey(nil), routes...),
		Frame:       append([]byte(nil), frameBytes...),
	}, nil
}

func (p distributedDeliveryPush) pushLegacyDeliveryItems(ctx context.Context, nodeID uint64, items []accessnode.DeliveryPushItem, missing []deliveryruntime.RouteKey) deliveryruntime.PushResult {
	missingCounts := deliveryRouteCounts(missing)
	result := deliveryruntime.PushResult{}
	for _, item := range items {
		routes := takeDeliveryRoutes(item.Routes, missingCounts)
		if len(routes) == 0 {
			continue
		}
		resp, err := p.client.PushBatch(ctx, nodeID, accessnode.DeliveryPushCommand{
			OwnerNodeID: p.localNodeID,
			ChannelID:   item.ChannelID,
			ChannelType: item.ChannelType,
			MessageID:   item.MessageID,
			MessageSeq:  item.MessageSeq,
			Routes:      append([]deliveryruntime.RouteKey(nil), routes...),
			Frame:       append([]byte(nil), item.Frame...),
		})
		if err != nil {
			result.Retryable = append(result.Retryable, routes...)
			continue
		}
		mergeDeliveryPushResponse(&result, routes, resp)
		result.Retryable = append(result.Retryable, unreportedDeliveryRoutes(routes, resp)...)
	}
	return result
}

func mergeDeliveryPushResponse(result *deliveryruntime.PushResult, routes []deliveryruntime.RouteKey, resp accessnode.DeliveryPushResponse) {
	accepted, _ := acceptedAndUnreportedDeliveryRoutes(routes, resp)
	result.Accepted = append(result.Accepted, accepted...)
	result.Retryable = append(result.Retryable, resp.Retryable...)
	result.Dropped = append(result.Dropped, resp.Dropped...)
}

func unreportedDeliveryRoutes(routes []deliveryruntime.RouteKey, resp accessnode.DeliveryPushResponse) []deliveryruntime.RouteKey {
	_, missing := acceptedAndUnreportedDeliveryRoutes(routes, resp)
	return missing
}

func acceptedAndUnreportedDeliveryRoutes(routes []deliveryruntime.RouteKey, resp accessnode.DeliveryPushResponse) ([]deliveryruntime.RouteKey, []deliveryruntime.RouteKey) {
	if resp.AcceptedCountSet {
		if len(resp.Retryable) == 0 && len(resp.Dropped) == 0 {
			if resp.AcceptedCount >= uint64(len(routes)) {
				return routes, nil
			}
			acceptedCount := int(resp.AcceptedCount)
			return routes[:acceptedCount], routes[acceptedCount:]
		}
		rejected := deliveryRouteCounts(resp.Retryable)
		for _, route := range resp.Dropped {
			rejected[route]++
		}
		candidates := make([]deliveryruntime.RouteKey, 0, len(routes))
		for _, route := range routes {
			if rejected[route] > 0 {
				rejected[route]--
				continue
			}
			candidates = append(candidates, route)
		}
		if resp.AcceptedCount > uint64(len(candidates)) {
			return candidates, nil
		}
		acceptedCount := int(resp.AcceptedCount)
		return candidates[:acceptedCount], candidates[acceptedCount:]
	}

	reported := deliveryRouteCounts(resp.Accepted)
	for _, route := range resp.Retryable {
		reported[route]++
	}
	for _, route := range resp.Dropped {
		reported[route]++
	}
	missing := make([]deliveryruntime.RouteKey, 0)
	for _, route := range routes {
		if reported[route] > 0 {
			reported[route]--
			continue
		}
		missing = append(missing, route)
	}
	return resp.Accepted, missing
}

func deliveryRouteCounts(routes []deliveryruntime.RouteKey) map[deliveryruntime.RouteKey]int {
	counts := make(map[deliveryruntime.RouteKey]int, len(routes))
	for _, route := range routes {
		counts[route]++
	}
	return counts
}

func takeDeliveryRoutes(routes []deliveryruntime.RouteKey, counts map[deliveryruntime.RouteKey]int) []deliveryruntime.RouteKey {
	taken := make([]deliveryruntime.RouteKey, 0)
	for _, route := range routes {
		if counts[route] == 0 {
			continue
		}
		counts[route]--
		taken = append(taken, route)
	}
	return taken
}

type ackRouting struct {
	localNodeID uint64
	local       routeAcker
	remoteAcks  *deliveryruntime.AckIndex
	notifier    deliveryOwnerNotifier
}

func (r ackRouting) AckRoute(ctx context.Context, cmd message.RouteAckCommand) error {
	event := deliveryRouteAckFromMessage(cmd)
	if r.remoteAcks != nil {
		if binding, ok := r.remoteAcks.LookupRoute(event.UID, event.SessionID, event.MessageID); ok {
			if binding.OwnerNodeID != 0 && binding.OwnerNodeID != r.localNodeID {
				if r.notifier == nil {
					return errRemoteAckNotifierRequired
				}
				if err := r.notifier.NotifyAck(ctx, binding.OwnerNodeID, event); err != nil {
					return err
				}
				r.remoteAcks.RemoveRoute(binding.Route.UID, event.SessionID, event.MessageID)
				return nil
			}
			if r.local == nil {
				r.remoteAcks.RemoveRoute(binding.Route.UID, event.SessionID, event.MessageID)
				return nil
			}
			if err := r.local.AckRoute(ctx, event); err != nil {
				return err
			}
			r.remoteAcks.RemoveRoute(binding.Route.UID, event.SessionID, event.MessageID)
			return nil
		}
	}
	if r.local == nil {
		return nil
	}
	return r.local.AckRoute(ctx, event)
}

type offlineRouting struct {
	localNodeID uint64
	local       sessionCloser
	remoteAcks  *deliveryruntime.AckIndex
	notifier    deliveryOwnerNotifier
}

func (r offlineRouting) SessionClosed(ctx context.Context, cmd message.SessionClosedCommand) error {
	var err error
	event := deliverySessionClosedFromMessage(cmd)
	localBindings := make([]deliveryruntime.AckBinding, 0)
	if r.remoteAcks != nil {
		ownerBindings := make(map[uint64][]deliveryruntime.AckBinding)
		for _, binding := range r.remoteAcks.LookupSessionRoute(event.UID, event.SessionID) {
			if binding.OwnerNodeID == 0 || binding.OwnerNodeID == r.localNodeID {
				localBindings = append(localBindings, binding)
				continue
			}
			ownerBindings[binding.OwnerNodeID] = append(ownerBindings[binding.OwnerNodeID], binding)
		}
		for ownerNodeID, bindings := range ownerBindings {
			if r.notifier == nil {
				err = errors.Join(err, errRemoteOfflineNotifierRequired)
				continue
			}
			notifyErr := r.notifier.NotifyOffline(ctx, ownerNodeID, event)
			err = errors.Join(err, notifyErr)
			if notifyErr != nil {
				continue
			}
			for _, binding := range bindings {
				r.remoteAcks.RemoveRoute(binding.Route.UID, binding.SessionID, binding.MessageID)
			}
		}
	}
	if r.local != nil {
		localErr := r.local.SessionClosed(ctx, event)
		err = errors.Join(err, localErr)
		if localErr == nil && r.remoteAcks != nil {
			for _, binding := range localBindings {
				r.remoteAcks.RemoveRoute(binding.Route.UID, binding.SessionID, binding.MessageID)
			}
		}
	} else if r.remoteAcks != nil {
		for _, binding := range localBindings {
			r.remoteAcks.RemoveRoute(binding.Route.UID, binding.SessionID, binding.MessageID)
		}
	}
	return err
}

func deliveryRouteAckFromMessage(cmd message.RouteAckCommand) deliveryevents.RouteAck {
	return deliveryevents.RouteAck{
		UID:        cmd.UID,
		SessionID:  cmd.SessionID,
		MessageID:  cmd.MessageID,
		MessageSeq: cmd.MessageSeq,
	}
}

func deliverySessionClosedFromMessage(cmd message.SessionClosedCommand) deliveryevents.SessionClosed {
	return deliveryevents.SessionClosed{
		UID:       cmd.UID,
		SessionID: cmd.SessionID,
	}
}

type routeAcker interface {
	AckRoute(ctx context.Context, cmd deliveryevents.RouteAck) error
}

type sessionCloser interface {
	SessionClosed(ctx context.Context, cmd deliveryevents.SessionClosed) error
}

type deliveryOwnerNotifier interface {
	NotifyAck(ctx context.Context, nodeID uint64, cmd deliveryevents.RouteAck) error
	NotifyOffline(ctx context.Context, nodeID uint64, cmd deliveryevents.SessionClosed) error
}

type committedNodeSubmitter interface {
	SubmitCommitted(ctx context.Context, nodeID uint64, env deliveryruntime.CommittedEnvelope) error
}

type committedDeliverySubmitter interface {
	SubmitCommitted(ctx context.Context, env deliveryruntime.CommittedEnvelope) error
}

type committedConversationSubmitter interface {
	SubmitCommitted(ctx context.Context, msg channel.Message) error
}

func buildRealtimeRecvPacket(msg channel.Message, recipientUID string) *frame.RecvPacket {
	framer := msg.Framer
	framer.FrameType = frame.RECV
	sourceChannelID, _ := channelid.FromCommandChannel(msg.ChannelID)

	packet := &frame.RecvPacket{
		Framer:      framer,
		Setting:     msg.Setting,
		MsgKey:      msg.MsgKey,
		Expire:      msg.Expire,
		MessageID:   int64(msg.MessageID),
		MessageSeq:  msg.MessageSeq,
		ClientMsgNo: msg.ClientMsgNo,
		StreamNo:    msg.StreamNo,
		StreamId:    msg.StreamID,
		StreamFlag:  msg.StreamFlag,
		Timestamp:   msg.Timestamp,
		ChannelID:   sourceChannelID,
		ChannelType: msg.ChannelType,
		Topic:       msg.Topic,
		FromUID:     msg.FromUID,
		Payload:     append([]byte(nil), msg.Payload...),
		ClientSeq:   msg.ClientSeq,
	}
	if msg.ChannelType == frame.ChannelTypePerson && recipientUID != "" {
		packet.ChannelID = recipientChannelView(msg, recipientUID)
		packet.ChannelType = frame.ChannelTypePerson
	}
	return packet
}

func recipientChannelView(msg channel.Message, recipientUID string) string {
	sourceChannelID, _ := channelid.FromCommandChannel(msg.ChannelID)
	if recipientUID == "" {
		return sourceChannelID
	}
	left, right, err := deliveryusecase.DecodePersonChannel(sourceChannelID)
	if err != nil {
		return msg.FromUID
	}
	switch recipientUID {
	case left:
		return right
	case right:
		return left
	default:
		return msg.FromUID
	}
}
