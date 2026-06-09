package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	gatewayadapter "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	messageusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	clusterv2channels "github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	accessgateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type gatewayMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type channelV2MetricsObserver struct {
	metrics *obsmetrics.Registry
}

type slotMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type transportV2MetricsObserver struct {
	metrics *obsmetrics.Registry
	mu      sync.Mutex

	pendingRPCBySource     map[uint64]int
	schedulerQueueBySource map[transportV2SchedulerQueueSource]obsmetrics.RuntimePressureQueueObservation
}

type transportV2SchedulerQueueSource struct {
	sourceID uint64
	priority string
}

type controllerRaftMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type storageCommitMetricsObserver struct {
	metrics *obsmetrics.Registry
	workers int
}

type deliveryMetricsObserver struct {
	metrics *obsmetrics.Registry
	logger  wklog.Logger
}

type conversationListMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type conversationAuthorityMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type authorityMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type multiChannelV2Observer []reactor.Observer
type multiSlotObserver []multiraft.SchedulerObserver
type multiTransportV2Observer []transportv2.Observer
type multiControllerRaftObserver []cv2raft.Observer
type multiCommitCoordinatorObserver []messagedb.CommitCoordinatorObserver

const (
	dbRuntimeComponent       = "db"
	dbMessageCommitPool      = "message_commit"
	dbMessageCommitQueue     = "commit"
	dbRuntimeQueuePriority   = "none"
	dbMessageCommitWorkerCap = 1
)

func (o gatewayMetricsObserver) OnConnectionOpen(event accessgateway.ConnectionEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.ConnectionOpened(event.Protocol)
}

func (o gatewayMetricsObserver) OnConnectionClose(event accessgateway.ConnectionEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.ConnectionClosed(event.Protocol)
	o.metrics.Gateway.ConnectionClosedReason(event.Protocol, string(event.CloseReason))
}

func (o gatewayMetricsObserver) OnAuth(event accessgateway.AuthEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.Auth(event.Status, event.Failure, event.Duration)
}

func (o gatewayMetricsObserver) OnFrameIn(event accessgateway.FrameEvent) {
	if o.metrics == nil || event.FrameType != "SEND" {
		return
	}
	o.metrics.Gateway.MessageReceived(event.Protocol, event.Bytes)
}

func (o gatewayMetricsObserver) OnFrameOut(event accessgateway.FrameEvent) {
	if o.metrics == nil || event.FrameType != "RECV" {
		return
	}
	o.metrics.Gateway.MessageDelivered(event.Protocol, event.Bytes)
}

func (o gatewayMetricsObserver) OnFrameHandled(event accessgateway.FrameHandleEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.FrameHandled(event.FrameType, event.Duration)
}

func (o gatewayMetricsObserver) OnAsyncSendQueue(event accessgateway.AsyncSendQueueEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.SetAsyncSendQueue(event.Depth, event.Capacity)
	o.metrics.RuntimePressure.SetQueue("gateway", "async_send", "send", "none", obsmetrics.RuntimePressureQueueObservation{
		Depth:    event.Depth,
		Capacity: event.Capacity,
	})
}

func (o gatewayMetricsObserver) OnAsyncSendAdmission(event accessgateway.AsyncSendAdmissionEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.ObserveAdmission("gateway", "async_send", "send", "none", event.Result)
}

func (o gatewayMetricsObserver) OnAsyncSendBatch(event accessgateway.AsyncSendBatchEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.ObserveAsyncSendBatch(event.Records, event.Bytes, event.Wait)
}

func (o gatewayMetricsObserver) OnAsyncSendDispatchWait(event accessgateway.AsyncSendDispatchWaitEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.ObserveAsyncSendDispatchWait(event.Protocol, event.Duration)
	o.metrics.RuntimePressure.ObserveQueueWait("gateway", "async_send", "send", "none", "ok", event.Duration)
}

func (o gatewayMetricsObserver) OnAsyncAuthQueue(event accessgateway.AsyncAuthQueueEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetPoolWorkers("gateway", "async_auth", event.Workers)
	o.metrics.RuntimePressure.SetQueue("gateway", "async_auth", "auth", "none", obsmetrics.RuntimePressureQueueObservation{
		Depth:    event.Depth,
		Capacity: event.Capacity,
	})
}

func (o gatewayMetricsObserver) OnAsyncAuthAdmission(event accessgateway.AsyncAuthAdmissionEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.ObserveAdmission("gateway", "async_auth", "auth", "none", event.Result)
}

func (o gatewayMetricsObserver) OnAsyncAuthWait(event accessgateway.AsyncAuthWaitEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.ObserveQueueWait("gateway", "async_auth", "auth", "none", "ok", event.Duration)
}

func (o gatewayMetricsObserver) OnTransportPressure(event accessgateway.TransportPressureEvent) {
	if o.metrics == nil {
		return
	}
	pool := fallbackRuntimePressureLabel(event.Name, "gnet")
	queue := fallbackRuntimePressureLabel(event.Queue, "transport")
	o.metrics.RuntimePressure.SetQueue("gateway", pool, queue, "none", obsmetrics.RuntimePressureQueueObservation{
		Depth:         event.Depth,
		Capacity:      event.Capacity,
		Bytes:         event.Bytes,
		BytesCapacity: event.BytesCapacity,
	})
	if event.Result != "" {
		o.metrics.RuntimePressure.ObserveAdmission("gateway", pool, queue, "none", event.Result)
	}
}

func (o gatewayMetricsObserver) SendackWritten(event gatewayadapter.SendackEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Gateway.Sendack(gatewaySendackReasonLabel(event.Reason), event.Source, event.ErrorClass)
}

func gatewaySendackReasonLabel(reason messageusecase.Reason) string {
	switch reason {
	case messageusecase.ReasonSuccess:
		return "success"
	case messageusecase.ReasonInvalidRequest:
		return "invalid_request"
	case messageusecase.ReasonAuthFail:
		return "auth_fail"
	case messageusecase.ReasonChannelNotExist:
		return "channel_not_exist"
	case messageusecase.ReasonNodeNotMatch:
		return "node_not_match"
	case messageusecase.ReasonSystemError:
		return "system_error"
	case messageusecase.ReasonUnsupported:
		return "unsupported"
	default:
		return "unknown"
	}
}

func (o conversationListMetricsObserver) ObserveConversationList(event accessapi.ConversationListObservation) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveList(event.Result, event.More, event.Duration, event.ReturnedItems, event.SparseItems, event.LastMessageLoads, event.LastMessageErrors, event.ActiveIndexStaleSkips)
}

func (o conversationAuthorityMetricsObserver) ObserveConversationAuthorityAdmit(event conversationAuthorityAdmitEvent) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveAuthorityAdmit(event.Result)
}

func (o conversationAuthorityMetricsObserver) ObserveConversationAuthorityCachePressure(event conversationAuthorityCachePressureEvent) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveAuthorityCachePressure(event.Phase, event.Result)
}

func (o conversationAuthorityMetricsObserver) ObserveConversationAuthorityList(event conversationAuthorityListEvent) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveAuthorityList(event.Result)
}

func (o conversationAuthorityMetricsObserver) ObserveConversationAuthorityHandoff(event conversationAuthorityHandoffEvent) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveAuthorityHandoff(event.Result)
}

func (o authorityMetricsObserver) ObserveAuthoritySenderRoute(event authoritySenderRouteEvent) {
	if o.metrics == nil || o.metrics.Authority == nil {
		return
	}
	o.metrics.Authority.ObserveSenderRoute(event.Result)
}

func (o authorityMetricsObserver) ObserveAuthorityRecipientQueue(event authorityRecipientQueueEvent) {
	if o.metrics == nil || o.metrics.Authority == nil {
		return
	}
	o.metrics.Authority.ObserveRecipientQueue(event.Result)
}

func (o authorityMetricsObserver) ObserveAuthorityRecipientDispatch(event authorityRecipientDispatchEvent) {
	if o.metrics == nil || o.metrics.Authority == nil {
		return
	}
	o.metrics.Authority.ObserveRecipientDispatch(event.Phase, event.Result, event.Duration)
}

func (o channelV2MetricsObserver) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetReactorMailboxDepth(reactorID, priority, depth)
	o.metrics.RuntimePressure.SetQueueDepth("channelv2", channelV2ReactorPoolLabel(reactorID), "mailbox", priority, depth)
}

func (o channelV2MetricsObserver) SetReactorMailboxCapacity(reactorID int, priority string, capacity int) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetQueueCapacity("channelv2", channelV2ReactorPoolLabel(reactorID), "mailbox", priority, capacity)
}

func (o channelV2MetricsObserver) ObserveReactorMailboxAdmission(reactorID int, priority string, result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.ObserveAdmission("channelv2", channelV2ReactorPoolLabel(reactorID), "mailbox", priority, result)
}

func (o channelV2MetricsObserver) SetAppendQueuePressure(event reactor.AppendQueuePressureEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetQueue("channelv2", channelV2ReactorPoolLabel(event.ReactorID), "append", "none", obsmetrics.RuntimePressureQueueObservation{
		Depth:         event.Depth,
		Capacity:      event.Capacity,
		Bytes:         int64(event.Bytes),
		BytesCapacity: int64(event.BytesCapacity),
	})
}

func (o channelV2MetricsObserver) SetWorkerQueueDepth(pool string, depth int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetWorkerQueueDepth(pool, depth)
	o.metrics.RuntimePressure.SetQueueDepth("channelv2", pool, "worker", "none", depth)
}

func (o channelV2MetricsObserver) SetWorkerQueueCapacity(pool string, capacity int) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetQueueCapacity("channelv2", pool, "worker", "none", capacity)
}

func (o channelV2MetricsObserver) SetWorkerWorkers(pool string, workers int) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetPoolWorkers("channelv2", pool, workers)
}

func (o channelV2MetricsObserver) ObserveWorkerAdmission(pool string, result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.ObserveAdmission("channelv2", pool, "worker", "none", result)
}

func (o channelV2MetricsObserver) ObserveWorkerWait(pool string, kind worker.TaskKind, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.ObserveQueueWait("channelv2", pool, "worker", "none", "ok", d)
}

func (o channelV2MetricsObserver) ObserveWorkerTask(pool string, kind worker.TaskKind, err error, d time.Duration) {
	if o.metrics == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.metrics.RuntimePressure.ObserveTaskDuration("channelv2", pool, channelV2WorkerKindLabel(kind), result, d)
}

func (o channelV2MetricsObserver) ObserveWorkerBatch(pool string, kind worker.TaskKind, items int, err error) {
	if o.metrics == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.metrics.ChannelV2.ObserveWorkerBatch(channelV2WorkerKindLabel(kind), result, items)
}

func (o channelV2MetricsObserver) SetWorkerInflight(pool string, inflight int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetWorkerInflight(pool, inflight)
	o.metrics.RuntimePressure.SetPoolInflight("channelv2", pool, inflight)
}

func (o channelV2MetricsObserver) SetWorkerInflightPeak(pool string, peak int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetWorkerInflightPeak(pool, peak)
}

func (o channelV2MetricsObserver) SetChannelRuntimeCount(reactorID int, role ch.Role, count int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetChannelRuntimeCount(reactorID, channelV2RoleLabel(role), count)
}

func (o channelV2MetricsObserver) ObserveChannelActivationRejected(reason string) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveChannelActivationRejected(reason)
}

func (o channelV2MetricsObserver) SetFollowerParkedCount(reactorID int, count int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetFollowerParkedCount(reactorID, count)
}

func (o channelV2MetricsObserver) ObserveFollowerRecoveryProbe(result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveFollowerRecoveryProbe(result)
}

func (o channelV2MetricsObserver) ObservePull(result string, empty bool) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObservePull(result, empty)
}

func (o channelV2MetricsObserver) ObservePullHintResult(reason transport.PullHintReason, result string, err error) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObservePullHint(channelV2PullHintReasonLabel(reason), result, channelV2PullHintErrorLabel(err))
}

func (o channelV2MetricsObserver) ObservePullHintReceived(reason transport.PullHintReason, stage string, err error) {
	if o.metrics == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.metrics.ChannelV2.ObservePullHintReceived(channelV2PullHintReasonLabel(reason), stage, result, channelV2PullHintErrorLabel(err))
}

func (o channelV2MetricsObserver) SetPendingMetaCount(reactorID int, count int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetPendingMetaCount(reactorID, count)
}

func (o channelV2MetricsObserver) ObservePendingMeta(event string, err error) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObservePendingMeta(event, channelV2PullHintErrorLabel(err))
}

func (o channelV2MetricsObserver) ObserveNeedMetaPull(result string, err error) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveNeedMetaPull(result, channelV2PullHintErrorLabel(err))
}

func (o channelV2MetricsObserver) ObserveReplicationStage(stage string, result string, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveReplicationStage(stage, result, d)
}

func (o channelV2MetricsObserver) ObserveChannelMetaCache(result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveMetaCache(result)
}

func (o channelV2MetricsObserver) ObserveAppendBatch(records int, bytes int, wait time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveAppendBatch(records, bytes, wait)
}

func (o channelV2MetricsObserver) ObserveAppendLatency(mode ch.CommitMode, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveAppendLatency(channelV2CommitModeLabel(mode), d)
}

func (o channelV2MetricsObserver) ObserveChannelAppendStage(stage string, result string, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveAppendStage(stage, result, d)
}

func (o channelV2MetricsObserver) ObserveAppendWaitStage(stage string, mode ch.CommitMode, result string, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.ObserveAppendWaitStage(stage, channelV2CommitModeLabel(mode), result, d)
}

func (o channelV2MetricsObserver) ObserveAppendWaitCanceled(snapshot reactor.AppendWaitCancelSnapshot) {
	if o.metrics == nil {
		return
	}
	log.Print(channelV2AppendWaitCancelLogLine(snapshot))
}

func channelV2AppendWaitCancelLogLine(snapshot reactor.AppendWaitCancelSnapshot) string {
	return fmt.Sprintf(
		"internalv2/app: channelv2 append waiter canceled reactor=%d key=%s channel_id=%s channel_type=%d op=%d commit_mode=%s role=%s leader=%d epoch=%d leader_epoch=%d leo=%d hw=%d target=%d store_submitted=%t store_completed=%t follower_pull_served=%t ack_offset_observed=%t hw_advanced=%t waiters=%d pending_appends=%d pending_append_order=%d append_queue_pending=%d append_queue_records=%d append_queue_bytes=%d append_inflight=%t append_inflight_op=%d append_inflight_waiters=%d append_store_blocked=%t pull_waiters=%d follower_states=%q err=%v",
		snapshot.ReactorID,
		snapshot.Key,
		snapshot.ChannelID.ID,
		snapshot.ChannelID.Type,
		snapshot.OpID,
		channelV2CommitModeLabel(snapshot.CommitMode),
		channelV2RoleLabel(snapshot.Role),
		snapshot.Leader,
		snapshot.Epoch,
		snapshot.LeaderEpoch,
		snapshot.LEO,
		snapshot.HW,
		snapshot.TargetOffset,
		snapshot.StoreSubmitted,
		snapshot.StoreCompleted,
		snapshot.FollowerPullServed,
		snapshot.AckOffsetObserved,
		snapshot.HWAdvanced,
		snapshot.Waiters,
		snapshot.PendingAppends,
		snapshot.PendingAppendOrder,
		snapshot.AppendQueuePending,
		snapshot.AppendQueueRecords,
		snapshot.AppendQueueBytes,
		snapshot.AppendInflight,
		snapshot.AppendInflightOpID,
		snapshot.AppendInflightWaiters,
		snapshot.AppendStoreBlocked,
		snapshot.PullWaiters,
		snapshot.FollowerStates,
		snapshot.Err,
	)
}

func (o channelV2MetricsObserver) ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration) {
	if o.metrics == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.metrics.ChannelV2.ObserveWorkerResult(channelV2WorkerKindLabel(kind), result, d)
}

func (o slotMetricsObserver) SetSchedulerWorkers(workers int) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetPoolWorkers("slot", "scheduler", workers)
}

func (o slotMetricsObserver) SetSchedulerInflight(inflight int) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetPoolInflight("slot", "scheduler", inflight)
}

func (o slotMetricsObserver) SetSchedulerState(event multiraft.SchedulerStateEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetQueue("slot", "scheduler", "scheduler", "none", obsmetrics.RuntimePressureQueueObservation{
		Depth:    event.Depth + event.Pending,
		Capacity: event.Capacity,
	})
}

func (o slotMetricsObserver) ObserveSchedulerAdmission(result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.ObserveAdmission("slot", "scheduler", "scheduler", "none", result)
}

func (o slotMetricsObserver) ObserveSchedulerTask(task string, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.ObserveTaskDuration("slot", "scheduler", task, "ok", d)
}

func (o *transportV2MetricsObserver) ObserveTransport(event transportv2.Event) {
	if o.metrics == nil {
		return
	}
	switch event.Name {
	case "pending_rpc":
		o.metrics.RuntimePressure.SetPoolInflight("transportv2", "rpc", o.transportV2PendingRPCInflight(event))
	case "peer_pool":
		inflight := event.Inflight
		if inflight == 0 {
			inflight = event.Items
		}
		o.metrics.RuntimePressure.SetPoolWorkers("transportv2", "peer_pool", event.Capacity)
		o.metrics.RuntimePressure.SetPoolInflight("transportv2", "peer_pool", inflight)
	case "scheduler_queue":
		priority := transportV2PriorityLabel(event.Priority)
		o.metrics.RuntimePressure.SetQueue("transportv2", "scheduler", "scheduler", priority, o.transportV2SchedulerQueue(priority, event))
	case "service_queue":
		o.metrics.RuntimePressure.SetQueue("transportv2", "service", transportV2ServiceQueueLabel(event.ServiceID), transportV2PriorityLabel(event.Priority), transportV2QueueObservation(event))
	case "scheduler_admission":
		o.metrics.RuntimePressure.ObserveAdmission("transportv2", "scheduler", "scheduler", transportV2PriorityLabel(event.Priority), event.Result)
	case "service_admission":
		o.metrics.RuntimePressure.ObserveAdmission("transportv2", "service", transportV2ServiceQueueLabel(event.ServiceID), transportV2PriorityLabel(event.Priority), event.Result)
	case "scheduler_wait":
		o.metrics.RuntimePressure.ObserveQueueWait("transportv2", "scheduler", "scheduler", transportV2PriorityLabel(event.Priority), event.Result, event.Duration)
	case "service_task":
		queue := transportV2ServiceQueueLabel(event.ServiceID)
		o.metrics.RuntimePressure.ObserveTaskDuration("transportv2", "service", queue, event.Result, event.Duration)
	case "service_inflight":
		pool := transportV2ServiceQueueLabel(event.ServiceID)
		if event.Capacity > 0 {
			o.metrics.RuntimePressure.SetPoolWorkers("transportv2", pool, event.Capacity)
		}
		o.metrics.RuntimePressure.SetPoolInflight("transportv2", pool, event.Inflight)
	}
}

func (o *transportV2MetricsObserver) transportV2PendingRPCInflight(event transportv2.Event) int {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.pendingRPCBySource == nil {
		o.pendingRPCBySource = make(map[uint64]int)
	}
	if event.Inflight <= 0 {
		delete(o.pendingRPCBySource, event.SourceID)
	} else {
		o.pendingRPCBySource[event.SourceID] = event.Inflight
	}
	var total int
	for _, inflight := range o.pendingRPCBySource {
		total += inflight
	}
	return total
}

func (o *transportV2MetricsObserver) transportV2SchedulerQueue(priority string, event transportv2.Event) obsmetrics.RuntimePressureQueueObservation {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.schedulerQueueBySource == nil {
		o.schedulerQueueBySource = make(map[transportV2SchedulerQueueSource]obsmetrics.RuntimePressureQueueObservation)
	}
	key := transportV2SchedulerQueueSource{sourceID: event.SourceID, priority: priority}
	switch event.Result {
	case "closed", "stopped":
		delete(o.schedulerQueueBySource, key)
	default:
		o.schedulerQueueBySource[key] = transportV2QueueObservation(event)
	}

	var total obsmetrics.RuntimePressureQueueObservation
	for source, observation := range o.schedulerQueueBySource {
		if source.priority != priority {
			continue
		}
		total.Depth += observation.Depth
		total.Capacity += observation.Capacity
		total.Bytes += observation.Bytes
		total.BytesCapacity += observation.BytesCapacity
	}
	return total
}

func (o controllerRaftMetricsObserver) SetStepQueueDepth(depth int, capacity int) {
	if o.metrics == nil {
		return
	}
	o.metrics.Controller.SetControllerRaftStepQueue(depth, capacity)
}

func (o controllerRaftMetricsObserver) ObserveStepEnqueue(result string, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.Controller.ObserveControllerRaftStepEnqueue(result, d)
}

func (o storageCommitMetricsObserver) SetCommitCoordinatorQueueDepth(depth int) {
	o.SetCommitCoordinatorQueue(depth, 0)
}

func (o storageCommitMetricsObserver) SetCommitCoordinatorQueue(depth int, capacity int) {
	if o.metrics == nil {
		return
	}
	o.metrics.Storage.SetCommitQueueDepth("message", depth)
	o.metrics.RuntimePressure.SetPoolWorkers(dbRuntimeComponent, dbMessageCommitPool, o.workerCount())
	o.metrics.RuntimePressure.SetQueue(dbRuntimeComponent, dbMessageCommitPool, dbMessageCommitQueue, dbRuntimeQueuePriority, obsmetrics.RuntimePressureQueueObservation{
		Depth:    depth,
		Capacity: capacity,
	})
}

func (o storageCommitMetricsObserver) ObserveCommitCoordinatorBatch(event messagedb.CommitCoordinatorBatchEvent) {
	if o.metrics == nil {
		return
	}
	result := "ok"
	if event.Err != nil {
		result = "err"
	}
	o.metrics.Storage.ObserveCommitBatch("message", result, obsmetrics.StorageCommitBatchObservation{
		Requests:        event.Requests,
		Records:         event.Records,
		Bytes:           event.Bytes,
		CollectDuration: event.CollectDuration,
		BuildDuration:   event.BuildDuration,
		CommitDuration:  event.CommitDuration,
		PublishDuration: event.PublishDuration,
		TotalDuration:   event.TotalDuration,
	})
	o.metrics.RuntimePressure.SetPoolWorkers(dbRuntimeComponent, dbMessageCommitPool, o.workerCount())
	o.metrics.RuntimePressure.ObserveTaskDuration(dbRuntimeComponent, dbMessageCommitPool, dbMessageCommitQueue, result, event.TotalDuration)
}

func (o storageCommitMetricsObserver) workerCount() int {
	if o.workers <= 0 {
		return dbMessageCommitWorkerCap
	}
	return o.workers
}

func (o storageCommitMetricsObserver) ObserveCommitCoordinatorRequest(event messagedb.CommitCoordinatorRequestEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Storage.ObserveCommitRequest("message", event.Lane, event.Result, obsmetrics.StorageCommitRequestObservation{
		Records:  event.Records,
		Bytes:    event.Bytes,
		Duration: event.Duration,
	})
	o.metrics.RuntimePressure.ObserveAdmission(dbRuntimeComponent, dbMessageCommitPool, dbMessageCommitQueue, dbRuntimeQueuePriority, event.Result)
	o.metrics.RuntimePressure.ObserveQueueWait(dbRuntimeComponent, dbMessageCommitPool, dbMessageCommitQueue, dbRuntimeQueuePriority, event.Result, event.Duration)
}

func (o deliveryMetricsObserver) ObserveFanoutTask(event runtimedelivery.FanoutTaskEvent) {
	if o.metrics != nil {
		o.metrics.Delivery.ObserveFanoutTask(deliveryNodeLabel(event.TargetNodeID), event.Result, event.Duration)
		o.observeError(event.ErrorClass)
	}
	if event.ErrorClass != "" && event.ErrorClass != runtimedelivery.DeliveryErrorClassNone {
		o.loggerOrNop().Warn("delivery fanout task failed",
			wklog.Event("internalv2.app.delivery.fanout_task_failed"),
			wklog.TargetNodeID(event.TargetNodeID),
			wklog.Int("partitionID", int(event.PartitionID)),
			wklog.String("path", event.Path),
			wklog.Result(event.Result),
			wklog.String("errorClass", event.ErrorClass),
			wklog.Duration("duration", event.Duration),
		)
	}
}

func (o deliveryMetricsObserver) ObserveFanoutResolve(event runtimedelivery.FanoutResolveEvent) {
	if o.metrics != nil {
		o.metrics.Delivery.ObserveResolve(deliveryChannelTypeLabel(event.ChannelType), event.Result, event.Duration, event.Pages, event.Routes)
		o.observeError(event.ErrorClass)
	}
	if event.ErrorClass != "" && event.ErrorClass != runtimedelivery.DeliveryErrorClassNone {
		o.loggerOrNop().Warn("delivery fanout resolve failed",
			wklog.Event("internalv2.app.delivery.fanout_resolve_failed"),
			wklog.ChannelType(int64(event.ChannelType)),
			wklog.Result(event.Result),
			wklog.String("errorClass", event.ErrorClass),
			wklog.Duration("duration", event.Duration),
			wklog.Int("pages", event.Pages),
			wklog.Int("uids", event.UIDs),
		)
	}
}

func (o deliveryMetricsObserver) ObserveFanoutPush(event runtimedelivery.FanoutPushEvent) {
	if o.metrics != nil {
		o.metrics.Delivery.ObservePushRPC(deliveryNodeLabel(event.OwnerNodeID), event.Result, event.Duration, event.Routes)
		o.observeError(event.ErrorClass)
	}
	if event.ErrorClass != "" && event.ErrorClass != runtimedelivery.DeliveryErrorClassNone {
		o.loggerOrNop().Warn("delivery fanout push failed",
			wklog.Event("internalv2.app.delivery.fanout_push_failed"),
			wklog.Uint64("ownerNodeID", event.OwnerNodeID),
			wklog.Result(event.Result),
			wklog.String("errorClass", event.ErrorClass),
			wklog.Duration("duration", event.Duration),
			wklog.Int("routes", event.Routes),
			wklog.Int("accepted", event.Accepted),
			wklog.Int("retryable", event.Retryable),
			wklog.Int("dropped", event.Dropped),
		)
	}
}

func (o deliveryMetricsObserver) ObserveRetry(event runtimedelivery.RetryEvent) {
	if o.metrics != nil {
		o.metrics.Delivery.ObserveRetry(event.Event, event.Result)
		o.metrics.Delivery.SetRetryQueueDepth(event.QueueDepth)
		o.observeError(event.ErrorClass)
	}
	if event.Result == runtimedelivery.DeliveryResultDropped ||
		event.Result == runtimedelivery.DeliveryResultOverflow ||
		event.Result == runtimedelivery.DeliveryResultMaxAttempts ||
		(event.ErrorClass != "" && event.ErrorClass != runtimedelivery.DeliveryErrorClassNone && event.Event == runtimedelivery.DeliveryRetryEventDrop) {
		o.loggerOrNop().Warn("delivery retry failed",
			wklog.Event("internalv2.app.delivery.retry_failed"),
			wklog.String("retryEvent", event.Event),
			wklog.Result(event.Result),
			wklog.String("errorClass", event.ErrorClass),
			wklog.Attempt(event.Attempt),
			wklog.Int("queueDepth", event.QueueDepth),
		)
	}
}

func (o deliveryMetricsObserver) ObserveManagerAdmission(event runtimedelivery.ManagerAdmissionEvent) {
	if o.metrics != nil {
		o.metrics.Delivery.ObserveEventQueue(event.Result)
	}
	if event.Result != runtimedelivery.DeliveryResultOK {
		o.loggerOrNop().Warn("delivery manager admission failed",
			wklog.Event("internalv2.app.delivery.manager_admission_failed"),
			wklog.Result(event.Result),
			wklog.Int("queueDepth", event.QueueDepth),
		)
	}
}

func (o deliveryMetricsObserver) ObserveManagerTerminal(event runtimedelivery.ManagerTerminalEvent) {
	if o.metrics != nil {
		o.observeError(event.ErrorClass)
	}
	if event.ErrorClass != "" && event.ErrorClass != runtimedelivery.DeliveryErrorClassNone {
		o.loggerOrNop().Warn("delivery manager terminal failed",
			wklog.Event("internalv2.app.delivery.manager_terminal_failed"),
			wklog.Result(event.Result),
			wklog.String("errorClass", event.ErrorClass),
			wklog.Int("queueDepth", event.QueueDepth),
		)
	}
}

func (o deliveryMetricsObserver) observeError(class string) {
	if class == "" || class == runtimedelivery.DeliveryErrorClassNone {
		return
	}
	o.metrics.Delivery.ObserveError(class)
}

func (a *App) deliveryObserver() runtimedelivery.Observer {
	if a == nil || (a.metrics == nil && a.logger == nil) {
		return nil
	}
	var logger wklog.Logger
	if a.logger != nil {
		logger = a.logger.Named("delivery")
	}
	return deliveryMetricsObserver{metrics: a.metrics, logger: logger}
}

func (o deliveryMetricsObserver) loggerOrNop() wklog.Logger {
	if o.logger == nil {
		return wklog.NewNop()
	}
	return o.logger
}

func combineChannelV2Observers(first, second reactor.Observer) reactor.Observer {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiChannelV2Observer{first, second}
}

func combineSlotObservers(first, second multiraft.SchedulerObserver) multiraft.SchedulerObserver {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiSlotObserver{first, second}
}

func combineTransportV2Observers(first, second transportv2.Observer) transportv2.Observer {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiTransportV2Observer{first, second}
}

func combineControllerRaftObservers(first, second cv2raft.Observer) cv2raft.Observer {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiControllerRaftObserver{first, second}
}

func combineCommitCoordinatorObservers(first, second messagedb.CommitCoordinatorObserver) messagedb.CommitCoordinatorObserver {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiCommitCoordinatorObserver{first, second}
}

func deliveryNodeLabel(nodeID uint64) string {
	return strconv.FormatUint(nodeID, 10)
}

func deliveryChannelTypeLabel(channelType uint8) string {
	switch channelType {
	case frame.ChannelTypePerson:
		return "person"
	case frame.ChannelTypeGroup:
		return "group"
	default:
		return strconv.Itoa(int(channelType))
	}
}

func fallbackRuntimePressureLabel(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func channelV2ReactorPoolLabel(reactorID int) string {
	if reactorID < 0 {
		return "reactor_unknown"
	}
	return "reactor_" + strconv.Itoa(reactorID)
}

func transportV2QueueObservation(event transportv2.Event) obsmetrics.RuntimePressureQueueObservation {
	return obsmetrics.RuntimePressureQueueObservation{
		Depth:         event.Items,
		Capacity:      event.Capacity,
		Bytes:         int64(event.Bytes),
		BytesCapacity: event.BytesCapacity,
	}
}

func transportV2ServiceQueueLabel(serviceID uint16) string {
	return "service_" + strconv.Itoa(int(serviceID))
}

func transportV2PriorityLabel(priority transportv2.Priority) string {
	switch priority {
	case transportv2.PriorityRaft:
		return "raft"
	case transportv2.PriorityControl:
		return "control"
	case transportv2.PriorityRPC:
		return "rpc"
	case transportv2.PriorityBulk:
		return "bulk"
	default:
		return "none"
	}
}

func (o multiChannelV2Observer) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	for _, observer := range o {
		observer.SetReactorMailboxDepth(reactorID, priority, depth)
	}
}

func (o multiChannelV2Observer) SetReactorMailboxCapacity(reactorID int, priority string, capacity int) {
	for _, observer := range o {
		mailboxObserver, ok := observer.(reactor.MailboxPressureObserver)
		if ok {
			mailboxObserver.SetReactorMailboxCapacity(reactorID, priority, capacity)
		}
	}
}

func (o multiChannelV2Observer) ObserveReactorMailboxAdmission(reactorID int, priority string, result string) {
	for _, observer := range o {
		mailboxObserver, ok := observer.(reactor.MailboxPressureObserver)
		if ok {
			mailboxObserver.ObserveReactorMailboxAdmission(reactorID, priority, result)
		}
	}
}

func (o multiChannelV2Observer) SetAppendQueuePressure(event reactor.AppendQueuePressureEvent) {
	for _, observer := range o {
		appendObserver, ok := observer.(reactor.AppendQueuePressureObserver)
		if ok {
			appendObserver.SetAppendQueuePressure(event)
		}
	}
}

func (o multiChannelV2Observer) SetWorkerQueueDepth(pool string, depth int) {
	for _, observer := range o {
		observer.SetWorkerQueueDepth(pool, depth)
	}
}

func (o multiChannelV2Observer) SetWorkerQueueCapacity(pool string, capacity int) {
	for _, observer := range o {
		queueObserver, ok := observer.(worker.QueueCapacityObserver)
		if ok {
			queueObserver.SetWorkerQueueCapacity(pool, capacity)
		}
	}
}

func (o multiChannelV2Observer) SetWorkerWorkers(pool string, workers int) {
	for _, observer := range o {
		queueObserver, ok := observer.(worker.QueueCapacityObserver)
		if ok {
			queueObserver.SetWorkerWorkers(pool, workers)
		}
	}
}

func (o multiChannelV2Observer) ObserveWorkerAdmission(pool string, result string) {
	for _, observer := range o {
		admissionObserver, ok := observer.(worker.AdmissionObserver)
		if ok {
			admissionObserver.ObserveWorkerAdmission(pool, result)
		}
	}
}

func (o multiChannelV2Observer) ObserveWorkerWait(pool string, kind worker.TaskKind, d time.Duration) {
	for _, observer := range o {
		waitObserver, ok := observer.(worker.WaitObserver)
		if ok {
			waitObserver.ObserveWorkerWait(pool, kind, d)
		}
	}
}

func (o multiChannelV2Observer) ObserveWorkerTask(pool string, kind worker.TaskKind, err error, d time.Duration) {
	for _, observer := range o {
		taskObserver, ok := observer.(worker.TaskObserver)
		if ok {
			taskObserver.ObserveWorkerTask(pool, kind, err, d)
		}
	}
}

func (o multiChannelV2Observer) ObserveWorkerBatch(pool string, kind worker.TaskKind, items int, err error) {
	for _, observer := range o {
		batchObserver, ok := observer.(worker.BatchObserver)
		if ok {
			batchObserver.ObserveWorkerBatch(pool, kind, items, err)
		}
	}
}

func (o multiChannelV2Observer) SetWorkerInflight(pool string, inflight int) {
	for _, observer := range o {
		inflightObserver, ok := observer.(worker.InflightObserver)
		if ok {
			inflightObserver.SetWorkerInflight(pool, inflight)
		}
	}
}

func (o multiChannelV2Observer) SetWorkerInflightPeak(pool string, peak int) {
	for _, observer := range o {
		inflightObserver, ok := observer.(worker.InflightObserver)
		if ok {
			inflightObserver.SetWorkerInflightPeak(pool, peak)
		}
	}
}

func (o multiChannelV2Observer) SetChannelRuntimeCount(reactorID int, role ch.Role, count int) {
	for _, observer := range o {
		runtimeObserver, ok := observer.(reactor.RuntimeObserver)
		if ok {
			runtimeObserver.SetChannelRuntimeCount(reactorID, role, count)
		}
	}
}

func (o multiChannelV2Observer) ObserveChannelActivationRejected(reason string) {
	for _, observer := range o {
		runtimeObserver, ok := observer.(reactor.RuntimeObserver)
		if ok {
			runtimeObserver.ObserveChannelActivationRejected(reason)
		}
	}
}

func (o multiChannelV2Observer) SetFollowerParkedCount(reactorID int, count int) {
	for _, observer := range o {
		replicationObserver, ok := observer.(reactor.ReplicationObserver)
		if ok {
			replicationObserver.SetFollowerParkedCount(reactorID, count)
		}
	}
}

func (o multiChannelV2Observer) ObserveFollowerRecoveryProbe(result string) {
	for _, observer := range o {
		replicationObserver, ok := observer.(reactor.ReplicationObserver)
		if ok {
			replicationObserver.ObserveFollowerRecoveryProbe(result)
		}
	}
}

func (o multiChannelV2Observer) ObservePull(result string, empty bool) {
	for _, observer := range o {
		replicationObserver, ok := observer.(reactor.ReplicationObserver)
		if ok {
			replicationObserver.ObservePull(result, empty)
		}
	}
}

func (o multiChannelV2Observer) ObservePullHintResult(reason transport.PullHintReason, result string, err error) {
	for _, observer := range o {
		pullHintObserver, ok := observer.(reactor.PullHintResultObserver)
		if ok {
			pullHintObserver.ObservePullHintResult(reason, result, err)
		}
	}
}

func (o multiChannelV2Observer) ObservePullHintReceived(reason transport.PullHintReason, stage string, err error) {
	for _, observer := range o {
		pullHintReceiveObserver, ok := observer.(interface {
			ObservePullHintReceived(transport.PullHintReason, string, error)
		})
		if ok {
			pullHintReceiveObserver.ObservePullHintReceived(reason, stage, err)
		}
	}
}

func (o multiChannelV2Observer) SetPendingMetaCount(reactorID int, count int) {
	for _, observer := range o {
		pendingMetaObserver, ok := observer.(reactor.PendingMetaObserver)
		if ok {
			pendingMetaObserver.SetPendingMetaCount(reactorID, count)
		}
	}
}

func (o multiChannelV2Observer) ObservePendingMeta(event string, err error) {
	for _, observer := range o {
		pendingMetaObserver, ok := observer.(reactor.PendingMetaObserver)
		if ok {
			pendingMetaObserver.ObservePendingMeta(event, err)
		}
	}
}

func (o multiChannelV2Observer) ObserveNeedMetaPull(result string, err error) {
	for _, observer := range o {
		pendingMetaObserver, ok := observer.(reactor.PendingMetaObserver)
		if ok {
			pendingMetaObserver.ObserveNeedMetaPull(result, err)
		}
	}
}

func (o multiChannelV2Observer) ObserveReplicationStage(stage string, result string, d time.Duration) {
	for _, observer := range o {
		replicationStageObserver, ok := observer.(reactor.ReplicationStageObserver)
		if ok {
			replicationStageObserver.ObserveReplicationStage(stage, result, d)
		}
	}
}

func (o multiChannelV2Observer) ObserveChannelMetaCache(result string) {
	for _, observer := range o {
		metaCacheObserver, ok := observer.(clusterv2channels.MetaCacheObserver)
		if ok {
			metaCacheObserver.ObserveChannelMetaCache(result)
		}
	}
}

func (o multiChannelV2Observer) ObserveAppendBatch(records int, bytes int, wait time.Duration) {
	for _, observer := range o {
		observer.ObserveAppendBatch(records, bytes, wait)
	}
}

func (o multiChannelV2Observer) ObserveAppendLatency(mode ch.CommitMode, d time.Duration) {
	for _, observer := range o {
		observer.ObserveAppendLatency(mode, d)
	}
}

func (o multiChannelV2Observer) ObserveChannelAppendStage(stage string, result string, d time.Duration) {
	for _, observer := range o {
		appendStageObserver, ok := observer.(clusterv2channels.AppendStageObserver)
		if ok {
			appendStageObserver.ObserveChannelAppendStage(stage, result, d)
		}
	}
}

func (o multiChannelV2Observer) ObserveAppendWaitStage(stage string, mode ch.CommitMode, result string, d time.Duration) {
	for _, observer := range o {
		appendWaitObserver, ok := observer.(reactor.AppendWaitStageObserver)
		if ok {
			appendWaitObserver.ObserveAppendWaitStage(stage, mode, result, d)
		}
	}
}

func (o multiChannelV2Observer) ObserveAppendWaitCanceled(snapshot reactor.AppendWaitCancelSnapshot) {
	for _, observer := range o {
		appendCancelObserver, ok := observer.(reactor.AppendWaitCancelObserver)
		if ok {
			appendCancelObserver.ObserveAppendWaitCanceled(snapshot)
		}
	}
}

func (o multiChannelV2Observer) ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration) {
	for _, observer := range o {
		observer.ObserveWorkerResult(kind, err, d)
	}
}

func (o multiSlotObserver) SetSchedulerWorkers(workers int) {
	for _, observer := range o {
		observer.SetSchedulerWorkers(workers)
	}
}

func (o multiSlotObserver) SetSchedulerInflight(inflight int) {
	for _, observer := range o {
		observer.SetSchedulerInflight(inflight)
	}
}

func (o multiSlotObserver) SetSchedulerState(event multiraft.SchedulerStateEvent) {
	for _, observer := range o {
		observer.SetSchedulerState(event)
	}
}

func (o multiSlotObserver) ObserveSchedulerAdmission(result string) {
	for _, observer := range o {
		observer.ObserveSchedulerAdmission(result)
	}
}

func (o multiSlotObserver) ObserveSchedulerTask(task string, d time.Duration) {
	for _, observer := range o {
		observer.ObserveSchedulerTask(task, d)
	}
}

func (o multiTransportV2Observer) ObserveTransport(event transportv2.Event) {
	for _, observer := range o {
		if observer != nil {
			observer.ObserveTransport(event)
		}
	}
}

func (o multiControllerRaftObserver) SetStepQueueDepth(depth int, capacity int) {
	for _, observer := range o {
		observer.SetStepQueueDepth(depth, capacity)
	}
}

func (o multiControllerRaftObserver) ObserveStepEnqueue(result string, d time.Duration) {
	for _, observer := range o {
		observer.ObserveStepEnqueue(result, d)
	}
}

func (o multiCommitCoordinatorObserver) SetCommitCoordinatorQueueDepth(depth int) {
	o.SetCommitCoordinatorQueue(depth, 0)
}

func (o multiCommitCoordinatorObserver) SetCommitCoordinatorQueue(depth int, capacity int) {
	for _, observer := range o {
		queueObserver, ok := observer.(messagedb.CommitCoordinatorQueueObserver)
		if ok {
			queueObserver.SetCommitCoordinatorQueue(depth, capacity)
			continue
		}
		observer.SetCommitCoordinatorQueueDepth(depth)
	}
}

func (o multiCommitCoordinatorObserver) ObserveCommitCoordinatorBatch(event messagedb.CommitCoordinatorBatchEvent) {
	for _, observer := range o {
		observer.ObserveCommitCoordinatorBatch(event)
	}
}

func (o multiCommitCoordinatorObserver) ObserveCommitCoordinatorRequest(event messagedb.CommitCoordinatorRequestEvent) {
	for _, observer := range o {
		requestObserver, ok := observer.(messagedb.CommitCoordinatorRequestObserver)
		if !ok {
			continue
		}
		requestObserver.ObserveCommitCoordinatorRequest(event)
	}
}

func channelV2CommitModeLabel(mode ch.CommitMode) string {
	switch mode {
	case ch.CommitModeLocal:
		return "local"
	case ch.CommitModeQuorum:
		return "quorum"
	default:
		return "unknown"
	}
}

func channelV2WorkerKindLabel(kind worker.TaskKind) string {
	switch kind {
	case worker.TaskFunc:
		return "func"
	case worker.TaskStoreAppend:
		return "store_append"
	case worker.TaskStoreApply:
		return "store_apply"
	case worker.TaskStoreReadLog:
		return "store_read_log"
	case worker.TaskRPCPull:
		return "rpc_pull"
	case worker.TaskRPCAck:
		return "rpc_ack"
	case worker.TaskRPCNotify:
		return "rpc_notify"
	case worker.TaskStoreCheckpoint:
		return "store_checkpoint"
	case worker.TaskRPCPullHint:
		return "rpc_pull_hint"
	default:
		return "unknown"
	}
}

func channelV2RoleLabel(role ch.Role) string {
	switch role {
	case ch.RoleLeader:
		return "leader"
	case ch.RoleFollower:
		return "follower"
	default:
		return "unknown"
	}
}

func channelV2PullHintReasonLabel(reason transport.PullHintReason) string {
	switch reason {
	case transport.PullHintReasonAppend:
		return "append"
	case transport.PullHintReasonResume:
		return "resume"
	default:
		return "unknown"
	}
}

func channelV2PullHintErrorLabel(err error) string {
	message := ""
	if err != nil {
		message = err.Error()
	}
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, ch.ErrNotReady) || strings.Contains(message, ch.ErrNotReady.Error()):
		return "not_ready"
	case errors.Is(err, ch.ErrStaleMeta) || strings.Contains(message, ch.ErrStaleMeta.Error()):
		return "stale_meta"
	case errors.Is(err, ch.ErrChannelNotFound) || strings.Contains(message, ch.ErrChannelNotFound.Error()):
		return "channel_not_found"
	case errors.Is(err, ch.ErrNotLeader) || strings.Contains(message, ch.ErrNotLeader.Error()):
		return "not_leader"
	case errors.Is(err, ch.ErrNotReplica) || strings.Contains(message, ch.ErrNotReplica.Error()):
		return "not_replica"
	case errors.Is(err, ch.ErrBackpressured) || strings.Contains(message, ch.ErrBackpressured.Error()):
		return "backpressured"
	case errors.Is(err, ch.ErrInvalidConfig) || strings.Contains(message, ch.ErrInvalidConfig.Error()):
		return "invalid_config"
	case errors.Is(err, ch.ErrClosed) || strings.Contains(message, ch.ErrClosed.Error()):
		return "closed"
	case errors.Is(err, context.Canceled) || strings.Contains(message, "context canceled"):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "deadline exceeded"):
		return "timeout"
	case strings.Contains(message, "remote error"):
		return "remote_error"
	default:
		return "other"
	}
}

func messageAppendErrorLabel(err error) string {
	message := ""
	if err != nil {
		message = err.Error()
	}
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, messageusecase.ErrBackpressured) || strings.Contains(message, messageusecase.ErrBackpressured.Error()) || strings.Contains(message, ch.ErrBackpressured.Error()):
		return "backpressured"
	case errors.Is(err, messageusecase.ErrRouteNotReady) || strings.Contains(message, messageusecase.ErrRouteNotReady.Error()) || strings.Contains(message, ch.ErrNotReady.Error()):
		return "route_not_ready"
	case errors.Is(err, messageusecase.ErrStaleRoute) || strings.Contains(message, messageusecase.ErrStaleRoute.Error()) || strings.Contains(message, ch.ErrStaleMeta.Error()):
		return "stale_route"
	case errors.Is(err, messageusecase.ErrNotLeader) || strings.Contains(message, messageusecase.ErrNotLeader.Error()) || strings.Contains(message, ch.ErrNotLeader.Error()):
		return "not_leader"
	case errors.Is(err, messageusecase.ErrChannelNotFound) || strings.Contains(message, messageusecase.ErrChannelNotFound.Error()) || strings.Contains(message, ch.ErrChannelNotFound.Error()):
		return "channel_not_found"
	case errors.Is(err, messageusecase.ErrAppendResultMissing) || strings.Contains(message, messageusecase.ErrAppendResultMissing.Error()):
		return "short_result"
	case errors.Is(err, ch.ErrInvalidConfig) || errors.Is(err, clusterv2.ErrInvalidConfig) || strings.Contains(message, ch.ErrInvalidConfig.Error()) || strings.Contains(message, clusterv2.ErrInvalidConfig.Error()):
		return "invalid_config"
	case errors.Is(err, ch.ErrClosed) || errors.Is(err, clusterv2.ErrStopping) || strings.Contains(message, ch.ErrClosed.Error()) || strings.Contains(message, clusterv2.ErrStopping.Error()):
		return "closed"
	case errors.Is(err, ch.ErrTooManyChannels) || strings.Contains(message, ch.ErrTooManyChannels.Error()):
		return "too_many_channels"
	case errors.Is(err, clusterv2.ErrNotStarted) || strings.Contains(message, clusterv2.ErrNotStarted.Error()):
		return "not_started"
	case errors.Is(err, context.Canceled) || strings.Contains(message, "context canceled"):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "deadline exceeded"):
		return "timeout"
	case strings.Contains(message, "remote error"):
		return "remote_error"
	case errors.Is(err, messageusecase.ErrAppendFailed) || strings.Contains(message, messageusecase.ErrAppendFailed.Error()):
		return "append_failed"
	default:
		return "other"
	}
}

var _ accessgateway.Observer = gatewayMetricsObserver{}
var _ accessgateway.AsyncSendObserver = gatewayMetricsObserver{}
var _ accessgateway.AsyncAuthObserver = gatewayMetricsObserver{}
var _ accessgateway.AsyncSendAdmissionObserver = gatewayMetricsObserver{}
var _ accessgateway.TransportPressureObserver = gatewayMetricsObserver{}
var _ gatewayadapter.SendackObserver = gatewayMetricsObserver{}
var _ conversationAuthorityObserver = conversationAuthorityMetricsObserver{}
var _ reactor.Observer = channelV2MetricsObserver{}
var _ reactor.MailboxPressureObserver = channelV2MetricsObserver{}
var _ reactor.AppendQueuePressureObserver = channelV2MetricsObserver{}
var _ reactor.RuntimeObserver = channelV2MetricsObserver{}
var _ reactor.ReplicationObserver = channelV2MetricsObserver{}
var _ reactor.ReplicationStageObserver = channelV2MetricsObserver{}
var _ reactor.PullHintResultObserver = channelV2MetricsObserver{}
var _ reactor.PendingMetaObserver = channelV2MetricsObserver{}
var _ reactor.AppendWaitCancelObserver = channelV2MetricsObserver{}
var _ worker.InflightObserver = channelV2MetricsObserver{}
var _ worker.QueueCapacityObserver = channelV2MetricsObserver{}
var _ worker.AdmissionObserver = channelV2MetricsObserver{}
var _ worker.WaitObserver = channelV2MetricsObserver{}
var _ worker.TaskObserver = channelV2MetricsObserver{}
var _ clusterv2channels.MetaCacheObserver = channelV2MetricsObserver{}
var _ clusterv2channels.AppendStageObserver = channelV2MetricsObserver{}
var _ multiraft.SchedulerObserver = slotMetricsObserver{}
var _ transportv2.Observer = (*transportV2MetricsObserver)(nil)
var _ cv2raft.Observer = controllerRaftMetricsObserver{}
var _ reactor.Observer = multiChannelV2Observer{}
var _ reactor.MailboxPressureObserver = multiChannelV2Observer{}
var _ reactor.AppendQueuePressureObserver = multiChannelV2Observer{}
var _ reactor.RuntimeObserver = multiChannelV2Observer{}
var _ reactor.ReplicationObserver = multiChannelV2Observer{}
var _ reactor.ReplicationStageObserver = multiChannelV2Observer{}
var _ reactor.PullHintResultObserver = multiChannelV2Observer{}
var _ reactor.PendingMetaObserver = multiChannelV2Observer{}
var _ transportv2.Observer = multiTransportV2Observer{}
var _ reactor.AppendWaitCancelObserver = multiChannelV2Observer{}
var _ worker.InflightObserver = multiChannelV2Observer{}
var _ worker.QueueCapacityObserver = multiChannelV2Observer{}
var _ worker.AdmissionObserver = multiChannelV2Observer{}
var _ worker.WaitObserver = multiChannelV2Observer{}
var _ worker.TaskObserver = multiChannelV2Observer{}
var _ clusterv2channels.MetaCacheObserver = multiChannelV2Observer{}
var _ clusterv2channels.AppendStageObserver = multiChannelV2Observer{}
var _ multiraft.SchedulerObserver = multiSlotObserver{}
var _ cv2raft.Observer = multiControllerRaftObserver{}
var _ messagedb.CommitCoordinatorObserver = storageCommitMetricsObserver{}
var _ messagedb.CommitCoordinatorQueueObserver = storageCommitMetricsObserver{}
var _ messagedb.CommitCoordinatorRequestObserver = storageCommitMetricsObserver{}
var _ messagedb.CommitCoordinatorObserver = multiCommitCoordinatorObserver{}
var _ messagedb.CommitCoordinatorQueueObserver = multiCommitCoordinatorObserver{}
var _ messagedb.CommitCoordinatorRequestObserver = multiCommitCoordinatorObserver{}
