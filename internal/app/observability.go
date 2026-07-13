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

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	gatewayadapter "github.com/WuKongIM/WuKongIM/internal/access/gateway"
	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	accessgateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type gatewayMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type channelMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type slotMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type transportMetricsObserver struct {
	metrics *obsmetrics.Registry
	mu      sync.Mutex

	pendingRPCBySource     map[uint64]int
	schedulerQueueBySource map[transportSchedulerQueueSource]obsmetrics.RuntimePressureQueueObservation
}

type transportSchedulerQueueSource struct {
	sourceID uint64
	priority string
}

type controllerRaftMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type controlSnapshotMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type nodeLifecycleMetricsObserver struct {
	metrics             *obsmetrics.Registry
	mu                  sync.Mutex
	scaleInBlockerSeen  map[string]struct{}
	scaleInBlockerOrder []string
}

type storageCommitMetricsObserver struct {
	metrics *obsmetrics.Registry
	workers int
}

type messageEventMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type deliveryMetricsObserver struct {
	metrics *obsmetrics.Registry
	logger  wklog.Logger
}

type conversationListMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type conversationSyncMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type conversationAuthorityMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type presenceMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type multiChannelObserver []reactor.Observer
type multiSlotObserver []multiraft.SchedulerObserver
type multiTransportObserver []transport.Observer
type multiControllerRaftObserver []controller.RaftObserver
type multiControlSnapshotObserver []cluster.ControlSnapshotObserver
type multiSlotReplicaMoveObserver []cluster.SlotReplicaMoveObserver
type multiCommitCoordinatorObserver []messagedb.CommitCoordinatorObserver
type multiMessageEventObserver []cluster.MessageEventObserver
type multiGatewayObserver []accessgateway.Observer
type multiSendackObserver []gatewayadapter.SendackObserver
type multiDeliveryObserver []runtimedelivery.Observer

const (
	dbRuntimeComponent       = "db"
	dbMessageCommitPool      = "message_commit"
	dbMessageCommitQueue     = "commit"
	dbRuntimeQueuePriority   = "none"
	dbMessageCommitWorkerCap = 1

	channelRuntimePressureComponent   = "channel"
	transportRuntimePressureComponent = "transport"
	channelLeaderPullObservationEvery = uint64(16)
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

func (o multiGatewayObserver) OnConnectionOpen(event accessgateway.ConnectionEvent) {
	for _, observer := range o {
		observer.OnConnectionOpen(event)
	}
}

func (o multiGatewayObserver) OnConnectionClose(event accessgateway.ConnectionEvent) {
	for _, observer := range o {
		observer.OnConnectionClose(event)
	}
}

func (o multiGatewayObserver) OnAuth(event accessgateway.AuthEvent) {
	for _, observer := range o {
		observer.OnAuth(event)
	}
}

func (o multiGatewayObserver) OnFrameIn(event accessgateway.FrameEvent) {
	for _, observer := range o {
		observer.OnFrameIn(event)
	}
}

func (o multiGatewayObserver) OnFrameOut(event accessgateway.FrameEvent) {
	for _, observer := range o {
		observer.OnFrameOut(event)
	}
}

func (o multiGatewayObserver) OnFrameHandled(event accessgateway.FrameHandleEvent) {
	for _, observer := range o {
		observer.OnFrameHandled(event)
	}
}

func (o multiGatewayObserver) OnSessionError(event accessgateway.SessionErrorEvent) {
	for _, observer := range o {
		if optional, ok := observer.(accessgateway.SessionErrorObserver); ok {
			optional.OnSessionError(event)
		}
	}
}

func (o multiGatewayObserver) OnAsyncSendQueue(event accessgateway.AsyncSendQueueEvent) {
	for _, observer := range o {
		if optional, ok := observer.(accessgateway.AsyncSendObserver); ok {
			optional.OnAsyncSendQueue(event)
		}
	}
}

func (o multiGatewayObserver) OnAsyncSendAdmission(event accessgateway.AsyncSendAdmissionEvent) {
	for _, observer := range o {
		if optional, ok := observer.(accessgateway.AsyncSendAdmissionObserver); ok {
			optional.OnAsyncSendAdmission(event)
		}
	}
}

func (o multiGatewayObserver) OnAsyncSendBatch(event accessgateway.AsyncSendBatchEvent) {
	for _, observer := range o {
		if optional, ok := observer.(accessgateway.AsyncSendObserver); ok {
			optional.OnAsyncSendBatch(event)
		}
	}
}

func (o multiGatewayObserver) OnAsyncSendDispatchWait(event accessgateway.AsyncSendDispatchWaitEvent) {
	for _, observer := range o {
		if optional, ok := observer.(accessgateway.AsyncSendObserver); ok {
			optional.OnAsyncSendDispatchWait(event)
		}
	}
}

func (o multiGatewayObserver) OnAsyncAuthQueue(event accessgateway.AsyncAuthQueueEvent) {
	for _, observer := range o {
		if optional, ok := observer.(accessgateway.AsyncAuthObserver); ok {
			optional.OnAsyncAuthQueue(event)
		}
	}
}

func (o multiGatewayObserver) OnAsyncAuthAdmission(event accessgateway.AsyncAuthAdmissionEvent) {
	for _, observer := range o {
		if optional, ok := observer.(accessgateway.AsyncAuthObserver); ok {
			optional.OnAsyncAuthAdmission(event)
		}
	}
}

func (o multiGatewayObserver) OnAsyncAuthWait(event accessgateway.AsyncAuthWaitEvent) {
	for _, observer := range o {
		if optional, ok := observer.(accessgateway.AsyncAuthObserver); ok {
			optional.OnAsyncAuthWait(event)
		}
	}
}

func (o multiGatewayObserver) OnTransportPressure(event accessgateway.TransportPressureEvent) {
	for _, observer := range o {
		if optional, ok := observer.(accessgateway.TransportPressureObserver); ok {
			optional.OnTransportPressure(event)
		}
	}
}

func (o multiSendackObserver) SendackWritten(event gatewayadapter.SendackEvent) {
	for _, observer := range o {
		observer.SendackWritten(event)
	}
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
	case messageusecase.ReasonSubscriberNotExist:
		return "subscriber_not_exist"
	case messageusecase.ReasonInBlacklist:
		return "in_blacklist"
	case messageusecase.ReasonNotAllowSend:
		return "not_allow_send"
	case messageusecase.ReasonNotInWhitelist:
		return "not_in_whitelist"
	case messageusecase.ReasonBan:
		return "ban"
	case messageusecase.ReasonDisband:
		return "disband"
	case messageusecase.ReasonSendBan:
		return "send_ban"
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

func (o conversationSyncMetricsObserver) ObserveConversationSync(event accessapi.ConversationSyncObservation) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveSync(event.Result, event.OnlyUnread, event.WithRecents, event.Duration, event.ReturnedItems, event.OverlayItems, event.RecentLoadDuration)
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

func (o conversationAuthorityMetricsObserver) ObserveConversationActiveCache(event conversationactive.CacheObservation) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.SetActiveCache(event.Rows, event.DirtyRows, event.OldestDirtyAge)
	for _, kind := range []metadb.ConversationKind{metadb.ConversationKindNormal, metadb.ConversationKindCMD} {
		o.metrics.Conversation.SetActiveCacheKind(conversationKindMetricLabel(kind), event.RowsByKind[kind], event.DirtyRowsByKind[kind])
	}
}

func (o conversationAuthorityMetricsObserver) ObserveConversationActiveFlush(event conversationactive.FlushObservation) {
	if o.metrics == nil || o.metrics.Conversation == nil {
		return
	}
	o.metrics.Conversation.ObserveActiveFlush(event.Result, event.Selected, event.Flushed, event.Duration)
}

func (o presenceMetricsObserver) ObservePresenceExpiry(result authoritypresence.ExpireResult, duration time.Duration) {
	if o.metrics == nil || o.metrics.Presence == nil {
		return
	}
	o.metrics.Presence.ObserveExpiry(
		presenceTouchResultSuccess,
		duration,
		result.DueBuckets,
		result.Examined,
		result.Expired,
		result.IndexRoutes,
		result.IndexBuckets,
	)
}

func (o presenceMetricsObserver) ObservePresenceTouchFlush(event presenceTouchFlushObservation) {
	if o.metrics == nil || o.metrics.Presence == nil {
		return
	}
	o.metrics.Presence.ObserveTouchFlush(
		event.Result,
		event.Duration,
		event.Drained,
		event.Resolved,
		event.Sent,
		event.Requeued,
		event.Chunks,
		event.TargetGroups,
		event.BudgetReached,
	)
}

func conversationKindMetricLabel(kind metadb.ConversationKind) string {
	switch kind {
	case metadb.ConversationKindNormal:
		return "normal"
	case metadb.ConversationKindCMD:
		return "cmd"
	default:
		return "other"
	}
}

func (o channelMetricsObserver) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.SetReactorMailboxDepth(reactorID, priority, depth)
	o.metrics.RuntimePressure.SetQueueDepth(channelRuntimePressureComponent, channelReactorPoolLabel(reactorID), "mailbox", priority, depth)
}

func (o channelMetricsObserver) SetReactorMailboxCapacity(reactorID int, priority string, capacity int) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetQueueCapacity(channelRuntimePressureComponent, channelReactorPoolLabel(reactorID), "mailbox", priority, capacity)
}

func (o channelMetricsObserver) ObserveReactorMailboxAdmission(reactorID int, priority string, result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.ObserveAdmission(channelRuntimePressureComponent, channelReactorPoolLabel(reactorID), "mailbox", priority, result)
}

func (o channelMetricsObserver) SetAppendQueuePressure(event reactor.AppendQueuePressureEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetQueue(channelRuntimePressureComponent, channelReactorPoolLabel(event.ReactorID), "append", "none", obsmetrics.RuntimePressureQueueObservation{
		Depth:         event.Depth,
		Capacity:      event.Capacity,
		Bytes:         int64(event.Bytes),
		BytesCapacity: int64(event.BytesCapacity),
	})
}

func (o channelMetricsObserver) SetWorkerQueueDepth(pool string, depth int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.SetWorkerQueueDepth(pool, depth)
	o.metrics.RuntimePressure.SetQueueDepth(channelRuntimePressureComponent, pool, "worker", "none", depth)
}

func (o channelMetricsObserver) SetWorkerQueueCapacity(pool string, capacity int) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetQueueCapacity(channelRuntimePressureComponent, pool, "worker", "none", capacity)
}

func (o channelMetricsObserver) SetWorkerWorkers(pool string, workers int) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetPoolWorkers(channelRuntimePressureComponent, pool, workers)
}

func (o channelMetricsObserver) ObserveWorkerAdmission(pool string, result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.ObserveAdmission(channelRuntimePressureComponent, pool, "worker", "none", result)
}

func (o channelMetricsObserver) ObserveWorkerWait(pool string, kind worker.TaskKind, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.ObserveQueueWait(channelRuntimePressureComponent, pool, "worker", "none", "ok", d)
}

func (o channelMetricsObserver) ObserveWorkerTask(pool string, kind worker.TaskKind, err error, d time.Duration) {
	if o.metrics == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.metrics.RuntimePressure.ObserveTaskDuration(channelRuntimePressureComponent, pool, channelWorkerKindLabel(kind), result, d)
}

func (o channelMetricsObserver) ObserveWorkerBatch(pool string, kind worker.TaskKind, items int, err error) {
	if o.metrics == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.metrics.ChannelRuntime.ObserveWorkerBatch(channelWorkerKindLabel(kind), result, items)
}

func (o channelMetricsObserver) SetWorkerInflight(pool string, inflight int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.SetWorkerInflight(pool, inflight)
	o.metrics.RuntimePressure.SetPoolInflight(channelRuntimePressureComponent, pool, inflight)
}

func (o channelMetricsObserver) SetWorkerInflightPeak(pool string, peak int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.SetWorkerInflightPeak(pool, peak)
}

func (o channelMetricsObserver) SetWorkerAntsPoolUsage(pool string, running int, capacity int, waiting int) {
	if o.metrics == nil {
		return
	}
	o.metrics.AntsPool.SetUsage(channelRuntimePressureComponent, pool, running, capacity, waiting)
}

func (o channelMetricsObserver) SetChannelRuntimeCount(reactorID int, role ch.Role, count int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.SetChannelRuntimeCount(reactorID, channelRoleLabel(role), count)
}

func (o channelMetricsObserver) ObserveChannelActivationRejected(reason string) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObserveChannelActivationRejected(reason)
}

func (o channelMetricsObserver) SetFollowerParkedCount(reactorID int, count int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.SetFollowerParkedCount(reactorID, count)
}

func (o channelMetricsObserver) ObserveFollowerRecoveryProbe(result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObserveFollowerRecoveryProbe(result)
}

func (o channelMetricsObserver) ObservePull(result string, empty bool) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObservePull(result, empty)
}

func (o channelMetricsObserver) ObservePullBatch(event ch.PullBatchObservation) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObservePullBatch(
		channelPullBatchResultLabel(event),
		event.Items,
		event.Records,
		event.PayloadBytes,
		event.SubmitDuration,
		event.AwaitDuration,
		event.MaxSequentialAwaitDuration,
		event.TotalDuration,
	)
}

func (o channelMetricsObserver) ObserveLeaderPullStage(_ ch.OpID, stage string, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObserveLeaderPullStage(stage, d)
}

func (o channelMetricsObserver) ObserveLeaderPullCompletedWaiters(_ ch.OpID, count int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObserveLeaderPullCompletedWaiters(count)
}

func (o channelMetricsObserver) LeaderPullObservationSampleEvery() uint64 {
	return channelLeaderPullObservationEvery
}

func (o channelMetricsObserver) ObservePullHintResult(reason channeltransport.PullHintReason, result string, err error) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObservePullHint(channelPullHintReasonLabel(reason), result, channelPullHintErrorLabel(err))
}

func (o channelMetricsObserver) ObservePullHintReceived(reason channeltransport.PullHintReason, stage string, err error) {
	if o.metrics == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.metrics.ChannelRuntime.ObservePullHintReceived(channelPullHintReasonLabel(reason), stage, result, channelPullHintErrorLabel(err))
}

func (o channelMetricsObserver) SetPendingMetaCount(reactorID int, count int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.SetPendingMetaCount(reactorID, count)
}

func (o channelMetricsObserver) ObservePendingMeta(event string, err error) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObservePendingMeta(event, channelPullHintErrorLabel(err))
}

func (o channelMetricsObserver) ObserveNeedMetaPull(result string, err error) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObserveNeedMetaPull(result, channelPullHintErrorLabel(err))
}

func (o channelMetricsObserver) ObserveReplicationStage(stage string, result string, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObserveReplicationStage(stage, result, d)
}

func (o channelMetricsObserver) ObserveChannelMetaCache(result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObserveMetaCache(result)
}

func (o channelMetricsObserver) ObserveAppendBatch(records int, bytes int, wait time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObserveAppendBatch(records, bytes, wait)
}

func (o channelMetricsObserver) ObserveAppendLatency(mode ch.CommitMode, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObserveAppendLatency(channelCommitModeLabel(mode), d)
}

func (o channelMetricsObserver) ObserveChannelAppendStage(stage string, result string, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObserveAppendStage(stage, result, d)
}

func (o channelMetricsObserver) ObserveAppendWaitStage(stage string, mode ch.CommitMode, result string, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelRuntime.ObserveAppendWaitStage(stage, channelCommitModeLabel(mode), result, d)
}

func (o channelMetricsObserver) ObserveAppendWaitCanceled(snapshot reactor.AppendWaitCancelSnapshot) {
	if o.metrics == nil {
		return
	}
	log.Print(channelAppendWaitCancelLogLine(snapshot))
}

func channelAppendWaitCancelLogLine(snapshot reactor.AppendWaitCancelSnapshot) string {
	return fmt.Sprintf(
		"internal/app: channel append waiter canceled reactor=%d key=%s channel_id=%s channel_type=%d op=%d commit_mode=%s role=%s leader=%d epoch=%d leader_epoch=%d leo=%d hw=%d target=%d store_submitted=%t store_completed=%t follower_pull_served=%t ack_offset_observed=%t hw_advanced=%t waiters=%d pending_appends=%d pending_append_order=%d append_queue_pending=%d append_queue_records=%d append_queue_bytes=%d append_inflight=%t append_inflight_op=%d append_inflight_waiters=%d append_store_blocked=%t pull_waiters=%d follower_states=%q err=%v",
		snapshot.ReactorID,
		snapshot.Key,
		snapshot.ChannelID.ID,
		snapshot.ChannelID.Type,
		snapshot.OpID,
		channelCommitModeLabel(snapshot.CommitMode),
		channelRoleLabel(snapshot.Role),
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

func (o channelMetricsObserver) ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration) {
	if o.metrics == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.metrics.ChannelRuntime.ObserveWorkerResult(channelWorkerKindLabel(kind), result, d, channelPullHintErrorLabel(err))
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

func (o slotMetricsObserver) ObserveSlotProposal(slotID multiraft.SlotID, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.Slot.ObserveProposal(uint32(slotID), d)
}

func (o slotMetricsObserver) ObserveSlotProposalAdmission(_ multiraft.SlotID, class multiraft.ProposalClass, result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.Slot.ObserveProposalAdmission(string(class), result)
}

func (o slotMetricsObserver) ObserveSlotLeaderChange(slotID multiraft.SlotID, from, to multiraft.NodeID) {
	o.ObserveSlotLeaderChangeWithCause(slotID, from, to, multiraft.LeaderChangeCauseElection)
}

func (o slotMetricsObserver) ObserveSlotLeaderChangeWithCause(slotID multiraft.SlotID, _, _ multiraft.NodeID, cause multiraft.LeaderChangeCause) {
	if o.metrics == nil {
		return
	}
	if cause == multiraft.LeaderChangeCausePlannedTransfer {
		o.metrics.Slot.ObserveLeaderChangeCause(uint32(slotID), string(cause))
		return
	}
	o.metrics.Slot.ObserveLeaderChange(uint32(slotID))
}

func (o slotMetricsObserver) SetSlotApplyState(slotID multiraft.SlotID, commitIndex, appliedIndex uint64) {
	if o.metrics == nil {
		return
	}
	o.metrics.Slot.SetApplyGap(uint32(slotID), slotApplyGap(commitIndex, appliedIndex))
}

func slotApplyGap(commitIndex, appliedIndex uint64) uint64 {
	if commitIndex <= appliedIndex {
		return 0
	}
	return commitIndex - appliedIndex
}

func (o *transportMetricsObserver) ObserveTransport(event transport.Event) {
	if o.metrics == nil {
		return
	}
	switch event.Name {
	case "sent_bytes":
		o.metrics.Transport.ObserveSentBytes(transportFrameKindLabel(event.Kind), event.Bytes)
	case "received_bytes":
		o.metrics.Transport.ObserveReceivedBytes(transportFrameKindLabel(event.Kind), event.Bytes)
	case "write_batch":
		o.metrics.Transport.ObserveWriteBatch(event.Items, event.Bytes, event.Capacity)
	case "pending_rpc":
		o.metrics.RuntimePressure.SetPoolInflight(transportRuntimePressureComponent, "rpc", o.transportPendingRPCInflight(event))
	case "peer_pool":
		inflight := event.Inflight
		if inflight == 0 {
			inflight = event.Items
		}
		o.metrics.RuntimePressure.SetPoolWorkers(transportRuntimePressureComponent, "peer_pool", event.Capacity)
		o.metrics.RuntimePressure.SetPoolInflight(transportRuntimePressureComponent, "peer_pool", inflight)
	case "scheduler_queue":
		priority := transportPriorityLabel(event.Priority)
		o.metrics.RuntimePressure.SetQueue(transportRuntimePressureComponent, "scheduler", "scheduler", priority, o.transportSchedulerQueue(priority, event))
	case "service_queue":
		o.metrics.RuntimePressure.SetQueue(transportRuntimePressureComponent, "service", transportServiceEventLabel(event), transportPriorityLabel(event.Priority), transportQueueObservation(event))
	case "scheduler_admission":
		o.metrics.RuntimePressure.ObserveAdmission(transportRuntimePressureComponent, "scheduler", "scheduler", transportPriorityLabel(event.Priority), event.Result)
	case "service_admission":
		o.metrics.RuntimePressure.ObserveAdmission(transportRuntimePressureComponent, "service", transportServiceEventLabel(event), transportPriorityLabel(event.Priority), event.Result)
	case "scheduler_wait":
		o.metrics.RuntimePressure.ObserveQueueWait(transportRuntimePressureComponent, "scheduler", "scheduler", transportPriorityLabel(event.Priority), event.Result, event.Duration)
	case "service_task":
		queue := transportServiceEventLabel(event)
		o.metrics.RuntimePressure.ObserveTaskDuration(transportRuntimePressureComponent, "service", queue, event.Result, event.Duration)
		o.metrics.Transport.ObserveRPC(queue, transportRPCResultLabel(event.Result), event.Duration)
	case "service_inflight":
		pool := transportServiceEventLabel(event)
		if event.Capacity > 0 {
			o.metrics.RuntimePressure.SetPoolWorkers(transportRuntimePressureComponent, pool, event.Capacity)
		}
		o.metrics.RuntimePressure.SetPoolInflight(transportRuntimePressureComponent, pool, event.Inflight)
		if event.PoolCapacity > 0 {
			o.metrics.AntsPool.SetUsage(transportRuntimePressureComponent, "service_executor", event.PoolRunning, event.PoolCapacity, event.PoolWaiting)
		}
	case "controller_raft_queue":
		o.metrics.RuntimePressure.SetQueue(transportRuntimePressureComponent, "controller_raft", "send", transportPriorityLabel(event.Priority), transportQueueObservation(event))
	case "controller_raft_admission":
		o.metrics.RuntimePressure.ObserveAdmission(transportRuntimePressureComponent, "controller_raft", "send", transportPriorityLabel(event.Priority), event.Result)
	case "controller_raft_task":
		o.metrics.RuntimePressure.ObserveTaskDuration(transportRuntimePressureComponent, "controller_raft", "send", event.Result, event.Duration)
	}
}

func (o *transportMetricsObserver) transportPendingRPCInflight(event transport.Event) int {
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

func (o *transportMetricsObserver) transportSchedulerQueue(priority string, event transport.Event) obsmetrics.RuntimePressureQueueObservation {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.schedulerQueueBySource == nil {
		o.schedulerQueueBySource = make(map[transportSchedulerQueueSource]obsmetrics.RuntimePressureQueueObservation)
	}
	key := transportSchedulerQueueSource{sourceID: event.SourceID, priority: priority}
	switch event.Result {
	case "closed", "stopped":
		delete(o.schedulerQueueBySource, key)
	default:
		o.schedulerQueueBySource[key] = transportQueueObservation(event)
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

func (o controllerRaftMetricsObserver) SetApplyState(commitIndex, appliedIndex uint64) {
	if o.metrics == nil {
		return
	}
	o.metrics.Controller.SetApplyGap(controllerApplyGap(commitIndex, appliedIndex))
}

func (o controllerRaftMetricsObserver) ObserveControllerRaftStatus(status managementusecase.ControllerRaftStatus) {
	if o.metrics == nil {
		return
	}
	o.metrics.Controller.SetControllerRaftMembership(len(status.Voters), len(status.Learners))
}

func (o controllerRaftMetricsObserver) ObserveControllerVoterPromotionAttempt(result string) {
	if o.metrics == nil {
		return
	}
	o.metrics.Controller.ObserveControllerVoterPromotionAttempt(result)
}

func (o controllerRaftMetricsObserver) ObserveControllerVoterPromotionBlocker(reason string) {
	if o.metrics == nil {
		return
	}
	o.metrics.Controller.ObserveControllerVoterPromotionBlocker(reason)
}

func (o controllerRaftMetricsObserver) ObserveControllerVoterPromotionPhase(phase string, d time.Duration) {
	if o.metrics == nil {
		return
	}
	o.metrics.Controller.ObserveControllerVoterPromotionPhase(phase, d)
}

func controllerApplyGap(commitIndex, appliedIndex uint64) uint64 {
	if commitIndex <= appliedIndex {
		return 0
	}
	return commitIndex - appliedIndex
}

func (o controlSnapshotMetricsObserver) ObserveControlSnapshot(snapshot control.Snapshot) {
	if o.metrics == nil {
		return
	}
	o.metrics.Controller.SetStateRevision(snapshot.Revision)
	o.metrics.Controller.SetLeaderPresent(snapshot.ControllerID != 0)
	alive, suspect, dead := controllerNodeCounts(snapshot.Nodes)
	o.metrics.Controller.SetNodeCounts(alive, suspect, dead)
	activeTasks, failedTasks := controllerTaskCounts(snapshot.Tasks)
	o.metrics.Controller.SetTaskActive(activeTasks)
	o.metrics.Controller.SetTaskFailed(failedTasks)
	o.metrics.Controller.SetSlotLeaderSkew(controllerPreferredLeaderSkew(snapshot.Nodes, snapshot.Slots))
	o.metrics.NodeLifecycle.SetDiscoveryMembershipRevision(snapshot.Revision)
	o.metrics.NodeLifecycle.SetLifecycleNodes(controllerLifecycleCounts(snapshot.Nodes))
	freshnessCounts, ageMax := controllerHealthFreshnessCounts(snapshot.Nodes)
	o.metrics.NodeLifecycle.SetHealthFreshnessNodes(freshnessCounts)
	o.metrics.NodeLifecycle.SetHealthReportAgeMax(ageMax)
	o.metrics.NodeLifecycle.SetOnboardingTasks(controllerOnboardingTaskCounts(snapshot.Tasks))
}

func controllerNodeCounts(nodes []control.Node) (alive, suspect, dead int) {
	for _, node := range nodes {
		switch node.Status {
		case control.NodeAlive:
			alive++
		case control.NodeSuspect:
			suspect++
		case control.NodeDown:
			dead++
		}
	}
	return alive, suspect, dead
}

func controllerTaskCounts(tasks []control.ReconcileTask) (map[string]int, map[string]int) {
	active := make(map[string]int)
	failed := make(map[string]int)
	for _, task := range tasks {
		kind := string(task.Kind)
		if kind == "" {
			continue
		}
		if task.Status == control.TaskStatusFailed {
			failed[kind]++
			continue
		}
		active[kind]++
	}
	return active, failed
}

func controllerLifecycleCounts(nodes []control.Node) map[obsmetrics.NodeLifecycleKey]int {
	counts := make(map[obsmetrics.NodeLifecycleKey]int)
	for _, node := range nodes {
		counts[obsmetrics.NodeLifecycleKey{
			JoinState: string(controllerEffectiveJoinState(node.JoinState)),
			Status:    string(node.Status),
		}]++
	}
	return counts
}

func controllerHealthFreshnessCounts(nodes []control.Node) (map[obsmetrics.NodeHealthFreshnessKey]int, map[obsmetrics.NodeHealthFreshnessKey]float64) {
	counts := make(map[obsmetrics.NodeHealthFreshnessKey]int)
	ageMax := make(map[obsmetrics.NodeHealthFreshnessKey]float64)
	for _, node := range nodes {
		key := obsmetrics.NodeHealthFreshnessKey{
			Freshness: string(node.Health.Freshness),
			Status:    string(node.Health.Status),
		}
		if key.Freshness == "" {
			key.Freshness = string(control.NodeHealthMissing)
		}
		if key.Status == "" {
			key.Status = string(node.Status)
		}
		counts[key]++
		age := node.Health.ReportAge.Seconds()
		if existing, ok := ageMax[key]; !ok || age > existing {
			ageMax[key] = age
		}
	}
	return counts, ageMax
}

func controllerOnboardingTaskCounts(tasks []control.ReconcileTask) map[string]int {
	counts := map[string]int{
		string(control.TaskStatusPending): 0,
		string(control.TaskStatusRunning): 0,
		string(control.TaskStatusFailed):  0,
	}
	for _, task := range tasks {
		if task.Kind != control.TaskKindSlotReplicaMove {
			continue
		}
		counts[string(task.Status)]++
	}
	return counts
}

func controllerEffectiveJoinState(state control.NodeJoinState) control.NodeJoinState {
	if state == "" {
		return control.NodeJoinStateActive
	}
	return state
}

const nodeLifecycleScaleInBlockerDedupLimit = 4096

func (o *nodeLifecycleMetricsObserver) ObserveScaleInStatus(status managementusecase.NodeScaleInStatusResponse) {
	if o == nil {
		return
	}
	if o.metrics == nil || o.metrics.NodeLifecycle == nil {
		return
	}
	for _, reason := range status.BlockedReasons {
		if !o.markScaleInBlockerObserved(status.NodeID, status.StateRevision, reason) {
			continue
		}
		o.metrics.NodeLifecycle.ObserveScaleInBlocker(reason)
	}
}

func (o *nodeLifecycleMetricsObserver) ObserveNodeLifecycleAttempt(operation, result string) {
	if o == nil {
		return
	}
	if o.metrics == nil || o.metrics.NodeLifecycle == nil {
		return
	}
	o.metrics.NodeLifecycle.ObserveLifecycleAttempt(operation, result)
}

func (o *nodeLifecycleMetricsObserver) markScaleInBlockerObserved(nodeID, stateRevision uint64, reason string) bool {
	if o == nil {
		return false
	}
	key := fmt.Sprintf("%d/%d/%s", nodeID, stateRevision, reason)
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.scaleInBlockerSeen[key]; ok {
		return false
	}
	if o.scaleInBlockerSeen == nil {
		o.scaleInBlockerSeen = make(map[string]struct{}, nodeLifecycleScaleInBlockerDedupLimit)
	}
	if len(o.scaleInBlockerOrder) >= nodeLifecycleScaleInBlockerDedupLimit {
		oldest := o.scaleInBlockerOrder[0]
		copy(o.scaleInBlockerOrder, o.scaleInBlockerOrder[1:])
		o.scaleInBlockerOrder = o.scaleInBlockerOrder[:len(o.scaleInBlockerOrder)-1]
		delete(o.scaleInBlockerSeen, oldest)
	}
	o.scaleInBlockerSeen[key] = struct{}{}
	o.scaleInBlockerOrder = append(o.scaleInBlockerOrder, key)
	return true
}

func controllerPreferredLeaderSkew(nodes []control.Node, slots []control.SlotAssignment) int {
	counts := make(map[uint64]int)
	for _, node := range nodes {
		if node.Status == control.NodeAlive && controlNodeHasRole(node.Roles, control.RoleData) {
			counts[node.NodeID] = 0
		}
	}
	if len(counts) == 0 {
		return 0
	}
	for _, slot := range slots {
		if _, ok := counts[slot.PreferredLeader]; ok {
			counts[slot.PreferredLeader]++
		}
	}
	min, max := 0, 0
	first := true
	for _, count := range counts {
		if first {
			min, max = count, count
			first = false
			continue
		}
		if count < min {
			min = count
		}
		if count > max {
			max = count
		}
	}
	return max - min
}

func controlNodeHasRole(roles []control.Role, role control.Role) bool {
	for _, candidate := range roles {
		if candidate == role {
			return true
		}
	}
	return false
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

func (o messageEventMetricsObserver) ObserveMessageEventAppend(event cluster.MessageEventAppendObservation) {
	if o.metrics == nil {
		return
	}
	o.metrics.Message.ObserveEventAppend(event.Path, event.EventType, event.Result, event.Duration)
}

func (o messageEventMetricsObserver) ObserveMessageEventAppendStage(event cluster.MessageEventAppendStageObservation) {
	if o.metrics == nil {
		return
	}
	o.metrics.Message.ObserveEventAppendStage(event.Path, event.Result, event.Stage, event.Duration)
}

func (o messageEventMetricsObserver) ObserveMessageEventPropose(event cluster.MessageEventProposeObservation) {
	if o.metrics == nil {
		return
	}
	o.metrics.Message.ObserveEventPropose(event.Path, event.Result, event.BatchSize, event.Duration)
}

func (o messageEventMetricsObserver) ObserveMessageEventProposeStage(event cluster.MessageEventProposeStageObservation) {
	if o.metrics == nil {
		return
	}
	o.metrics.Message.ObserveEventProposeStage(event.Path, event.Result, event.Stage, event.Duration)
}

func (o messageEventMetricsObserver) SetMessageEventStreamCache(event cluster.MessageEventStreamCacheObservation) {
	if o.metrics == nil {
		return
	}
	o.metrics.Message.SetEventStreamCache(obsmetrics.MessageEventStreamCacheObservation{
		Sessions:     event.Sessions,
		OpenLanes:    event.OpenLanes,
		PayloadBytes: event.PayloadBytes,
		MaxSessions:  event.MaxSessions,
	})
}

func (o deliveryMetricsObserver) ObserveFanoutTask(event runtimedelivery.FanoutTaskEvent) {
	if o.metrics != nil {
		o.metrics.Delivery.ObserveFanoutTask(deliveryNodeLabel(event.TargetNodeID), event.Result, event.Duration)
		o.observeError(event.ErrorClass)
	}
	if event.ErrorClass != "" && event.ErrorClass != runtimedelivery.DeliveryErrorClassNone {
		o.loggerOrNop().Warn("delivery fanout task failed",
			wklog.Event("internal.app.delivery.fanout_task_failed"),
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
			wklog.Event("internal.app.delivery.fanout_resolve_failed"),
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
			wklog.Event("internal.app.delivery.fanout_push_failed"),
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
			wklog.Event("internal.app.delivery.retry_failed"),
			wklog.String("retryEvent", event.Event),
			wklog.Result(event.Result),
			wklog.String("errorClass", event.ErrorClass),
			wklog.Attempt(event.Attempt),
			wklog.Int("queueDepth", event.QueueDepth),
		)
	}
}

func (o deliveryMetricsObserver) ObserveAck(event runtimedelivery.AckEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.Delivery.SetAckBindings(event.PendingCount)
}

func (o deliveryMetricsObserver) ObserveManagerAdmission(event runtimedelivery.ManagerAdmissionEvent) {
	if o.metrics != nil {
		o.metrics.Delivery.ObserveEventQueue(event.Result)
	}
	if event.Result != runtimedelivery.DeliveryResultOK {
		o.loggerOrNop().Warn("delivery manager admission failed",
			wklog.Event("internal.app.delivery.manager_admission_failed"),
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
			wklog.Event("internal.app.delivery.manager_terminal_failed"),
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
	if a == nil {
		return nil
	}
	var observers []runtimedelivery.Observer
	if a.metrics != nil || a.logger != nil {
		var logger wklog.Logger
		if a.logger != nil {
			logger = a.logger.Named("delivery")
		}
		observers = append(observers, deliveryMetricsObserver{metrics: a.metrics, logger: logger})
	}
	if collector, ok := a.topProvider.(*topCollector); ok {
		observers = append(observers, topDeliveryObserver{top: collector})
	}
	return combineDeliveryObservers(observers...)
}

func (o deliveryMetricsObserver) loggerOrNop() wklog.Logger {
	if o.logger == nil {
		return wklog.NewNop()
	}
	return o.logger
}

func combineChannelObservers(first, second reactor.Observer) reactor.Observer {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiChannelObserver{first, second}
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

func combineTransportObservers(first, second transport.Observer) transport.Observer {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiTransportObserver{first, second}
}

func combineControllerRaftObservers(first, second controller.RaftObserver) controller.RaftObserver {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiControllerRaftObserver{first, second}
}

func combineControlSnapshotObservers(first, second cluster.ControlSnapshotObserver) cluster.ControlSnapshotObserver {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiControlSnapshotObserver{first, second}
}

func combineSlotReplicaMoveObservers(first, second cluster.SlotReplicaMoveObserver) cluster.SlotReplicaMoveObserver {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiSlotReplicaMoveObserver{first, second}
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

func combineMessageEventObservers(first, second cluster.MessageEventObserver) cluster.MessageEventObserver {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiMessageEventObserver{first, second}
}

func combineDeliveryObservers(observers ...runtimedelivery.Observer) runtimedelivery.Observer {
	filtered := make([]runtimedelivery.Observer, 0, len(observers))
	for _, observer := range observers {
		if observer != nil {
			filtered = append(filtered, observer)
		}
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return multiDeliveryObserver(filtered)
	}
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

func channelReactorPoolLabel(reactorID int) string {
	if reactorID < 0 {
		return "reactor_unknown"
	}
	return "reactor_" + strconv.Itoa(reactorID)
}

func transportQueueObservation(event transport.Event) obsmetrics.RuntimePressureQueueObservation {
	return obsmetrics.RuntimePressureQueueObservation{
		Depth:         event.Items,
		Capacity:      event.Capacity,
		Bytes:         int64(event.Bytes),
		BytesCapacity: event.BytesCapacity,
	}
}

func transportServiceEventLabel(event transport.Event) string {
	if alias := strings.TrimSpace(event.ServiceAlias); alias != "" {
		return alias
	}
	return transportServiceQueueLabel(event.ServiceID)
}

func transportServiceQueueLabel(serviceID uint16) string {
	return "service_" + strconv.Itoa(int(serviceID))
}

func transportRPCResultLabel(result string) string {
	if result == "" {
		return "ok"
	}
	return result
}

func transportPriorityLabel(priority transport.Priority) string {
	switch priority {
	case transport.PriorityRaft:
		return "raft"
	case transport.PriorityControl:
		return "control"
	case transport.PriorityRPC:
		return "rpc"
	case transport.PriorityBulk:
		return "bulk"
	default:
		return "none"
	}
}

func transportFrameKindLabel(kind transport.FrameKind) string {
	switch kind {
	case transport.FrameKindData:
		return "data"
	case transport.FrameKindNotify:
		return "notify"
	case transport.FrameKindRPCRequest:
		return "rpc_request"
	case transport.FrameKindRPCResponse:
		return "rpc_response"
	case transport.FrameKindControl:
		return "control"
	default:
		return "unknown"
	}
}

func (o multiChannelObserver) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	for _, observer := range o {
		observer.SetReactorMailboxDepth(reactorID, priority, depth)
	}
}

func (o multiChannelObserver) SetReactorMailboxCapacity(reactorID int, priority string, capacity int) {
	for _, observer := range o {
		mailboxObserver, ok := observer.(reactor.MailboxPressureObserver)
		if ok {
			mailboxObserver.SetReactorMailboxCapacity(reactorID, priority, capacity)
		}
	}
}

func (o multiChannelObserver) ObserveReactorMailboxAdmission(reactorID int, priority string, result string) {
	for _, observer := range o {
		mailboxObserver, ok := observer.(reactor.MailboxPressureObserver)
		if ok {
			mailboxObserver.ObserveReactorMailboxAdmission(reactorID, priority, result)
		}
	}
}

func (o multiChannelObserver) SetAppendQueuePressure(event reactor.AppendQueuePressureEvent) {
	for _, observer := range o {
		appendObserver, ok := observer.(reactor.AppendQueuePressureObserver)
		if ok {
			appendObserver.SetAppendQueuePressure(event)
		}
	}
}

func (o multiChannelObserver) SetWorkerQueueDepth(pool string, depth int) {
	for _, observer := range o {
		observer.SetWorkerQueueDepth(pool, depth)
	}
}

func (o multiChannelObserver) SetWorkerQueueCapacity(pool string, capacity int) {
	for _, observer := range o {
		queueObserver, ok := observer.(worker.QueueCapacityObserver)
		if ok {
			queueObserver.SetWorkerQueueCapacity(pool, capacity)
		}
	}
}

func (o multiChannelObserver) SetWorkerWorkers(pool string, workers int) {
	for _, observer := range o {
		queueObserver, ok := observer.(worker.QueueCapacityObserver)
		if ok {
			queueObserver.SetWorkerWorkers(pool, workers)
		}
	}
}

func (o multiChannelObserver) ObserveWorkerAdmission(pool string, result string) {
	for _, observer := range o {
		admissionObserver, ok := observer.(worker.AdmissionObserver)
		if ok {
			admissionObserver.ObserveWorkerAdmission(pool, result)
		}
	}
}

func (o multiChannelObserver) ObserveWorkerWait(pool string, kind worker.TaskKind, d time.Duration) {
	for _, observer := range o {
		waitObserver, ok := observer.(worker.WaitObserver)
		if ok {
			waitObserver.ObserveWorkerWait(pool, kind, d)
		}
	}
}

func (o multiChannelObserver) ObserveWorkerTask(pool string, kind worker.TaskKind, err error, d time.Duration) {
	for _, observer := range o {
		taskObserver, ok := observer.(worker.TaskObserver)
		if ok {
			taskObserver.ObserveWorkerTask(pool, kind, err, d)
		}
	}
}

func (o multiChannelObserver) ObserveWorkerBatch(pool string, kind worker.TaskKind, items int, err error) {
	for _, observer := range o {
		batchObserver, ok := observer.(worker.BatchObserver)
		if ok {
			batchObserver.ObserveWorkerBatch(pool, kind, items, err)
		}
	}
}

func (o multiChannelObserver) SetWorkerInflight(pool string, inflight int) {
	for _, observer := range o {
		inflightObserver, ok := observer.(worker.InflightObserver)
		if ok {
			inflightObserver.SetWorkerInflight(pool, inflight)
		}
	}
}

func (o multiChannelObserver) SetWorkerInflightPeak(pool string, peak int) {
	for _, observer := range o {
		inflightObserver, ok := observer.(worker.InflightObserver)
		if ok {
			inflightObserver.SetWorkerInflightPeak(pool, peak)
		}
	}
}

func (o multiChannelObserver) SetWorkerAntsPoolUsage(pool string, running int, capacity int, waiting int) {
	for _, observer := range o {
		antsObserver, ok := observer.(worker.AntsPoolObserver)
		if ok {
			antsObserver.SetWorkerAntsPoolUsage(pool, running, capacity, waiting)
		}
	}
}

func (o multiChannelObserver) SetChannelRuntimeCount(reactorID int, role ch.Role, count int) {
	for _, observer := range o {
		runtimeObserver, ok := observer.(reactor.RuntimeObserver)
		if ok {
			runtimeObserver.SetChannelRuntimeCount(reactorID, role, count)
		}
	}
}

func (o multiChannelObserver) ObserveChannelActivationRejected(reason string) {
	for _, observer := range o {
		runtimeObserver, ok := observer.(reactor.RuntimeObserver)
		if ok {
			runtimeObserver.ObserveChannelActivationRejected(reason)
		}
	}
}

func (o multiChannelObserver) SetFollowerParkedCount(reactorID int, count int) {
	for _, observer := range o {
		replicationObserver, ok := observer.(reactor.ReplicationObserver)
		if ok {
			replicationObserver.SetFollowerParkedCount(reactorID, count)
		}
	}
}

func (o multiChannelObserver) ObserveFollowerRecoveryProbe(result string) {
	for _, observer := range o {
		replicationObserver, ok := observer.(reactor.ReplicationObserver)
		if ok {
			replicationObserver.ObserveFollowerRecoveryProbe(result)
		}
	}
}

func (o multiChannelObserver) ObservePull(result string, empty bool) {
	for _, observer := range o {
		replicationObserver, ok := observer.(reactor.ReplicationObserver)
		if ok {
			replicationObserver.ObservePull(result, empty)
		}
	}
}

func (o multiChannelObserver) ObservePullBatch(event ch.PullBatchObservation) {
	for _, observer := range o {
		pullBatchObserver, ok := observer.(reactor.PullBatchObserver)
		if ok {
			pullBatchObserver.ObservePullBatch(event)
		}
	}
}

func (o multiChannelObserver) ObserveLeaderPullStage(opID ch.OpID, stage string, d time.Duration) {
	for _, observer := range o {
		leaderPullObserver, ok := observer.(reactor.LeaderPullObserver)
		if ok && channelLeaderPullObservationSelected(observer, opID) {
			leaderPullObserver.ObserveLeaderPullStage(opID, stage, d)
		}
	}
}

func (o multiChannelObserver) ObserveLeaderPullCompletedWaiters(opID ch.OpID, count int) {
	for _, observer := range o {
		leaderPullObserver, ok := observer.(reactor.LeaderPullObserver)
		if ok && channelLeaderPullObservationSelected(observer, opID) {
			leaderPullObserver.ObserveLeaderPullCompletedWaiters(opID, count)
		}
	}
}

func (o multiChannelObserver) LeaderPullObservationEnabled() bool {
	for _, observer := range o {
		if channelLeaderPullObservationEnabled(observer) {
			return true
		}
	}
	return false
}

func (o multiChannelObserver) LeaderPullObservationSampleEvery() uint64 {
	var selected uint64
	for _, observer := range o {
		if !channelLeaderPullObservationEnabled(observer) {
			continue
		}
		every := channelLeaderPullSampleEvery(observer)
		if selected == 0 {
			selected = every
			continue
		}
		selected = greatestCommonDivisor(selected, every)
	}
	return selected
}

func channelLeaderPullObservationEnabled(observer reactor.Observer) bool {
	if capability, ok := observer.(interface{ LeaderPullObservationEnabled() bool }); ok {
		return capability.LeaderPullObservationEnabled()
	}
	_, ok := observer.(reactor.LeaderPullObserver)
	return ok
}

func channelLeaderPullSampleEvery(observer reactor.Observer) uint64 {
	if sampler, ok := observer.(interface{ LeaderPullObservationSampleEvery() uint64 }); ok {
		if every := sampler.LeaderPullObservationSampleEvery(); every > 0 {
			return every
		}
	}
	return 1
}

func channelLeaderPullObservationSelected(observer reactor.Observer, opID ch.OpID) bool {
	if !channelLeaderPullObservationEnabled(observer) {
		return false
	}
	every := channelLeaderPullSampleEvery(observer)
	return every <= 1 || opID == 0 || uint64(opID)%every == 0
}

func greatestCommonDivisor(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func (o multiChannelObserver) ObservePullHintResult(reason channeltransport.PullHintReason, result string, err error) {
	for _, observer := range o {
		pullHintObserver, ok := observer.(reactor.PullHintResultObserver)
		if ok {
			pullHintObserver.ObservePullHintResult(reason, result, err)
		}
	}
}

func (o multiChannelObserver) ObservePullHintReceived(reason channeltransport.PullHintReason, stage string, err error) {
	for _, observer := range o {
		pullHintReceiveObserver, ok := observer.(interface {
			ObservePullHintReceived(channeltransport.PullHintReason, string, error)
		})
		if ok {
			pullHintReceiveObserver.ObservePullHintReceived(reason, stage, err)
		}
	}
}

func (o multiChannelObserver) SetPendingMetaCount(reactorID int, count int) {
	for _, observer := range o {
		pendingMetaObserver, ok := observer.(reactor.PendingMetaObserver)
		if ok {
			pendingMetaObserver.SetPendingMetaCount(reactorID, count)
		}
	}
}

func (o multiChannelObserver) ObservePendingMeta(event string, err error) {
	for _, observer := range o {
		pendingMetaObserver, ok := observer.(reactor.PendingMetaObserver)
		if ok {
			pendingMetaObserver.ObservePendingMeta(event, err)
		}
	}
}

func (o multiChannelObserver) ObserveNeedMetaPull(result string, err error) {
	for _, observer := range o {
		pendingMetaObserver, ok := observer.(reactor.PendingMetaObserver)
		if ok {
			pendingMetaObserver.ObserveNeedMetaPull(result, err)
		}
	}
}

func (o multiChannelObserver) ObserveReplicationStage(stage string, result string, d time.Duration) {
	for _, observer := range o {
		replicationStageObserver, ok := observer.(reactor.ReplicationStageObserver)
		if ok {
			replicationStageObserver.ObserveReplicationStage(stage, result, d)
		}
	}
}

func (o multiChannelObserver) ObserveChannelMetaCache(result string) {
	for _, observer := range o {
		metaCacheObserver, ok := observer.(clusterchannels.MetaCacheObserver)
		if ok {
			metaCacheObserver.ObserveChannelMetaCache(result)
		}
	}
}

func (o multiChannelObserver) ObserveAppendBatch(records int, bytes int, wait time.Duration) {
	for _, observer := range o {
		observer.ObserveAppendBatch(records, bytes, wait)
	}
}

func (o multiChannelObserver) ObserveAppendLatency(mode ch.CommitMode, d time.Duration) {
	for _, observer := range o {
		observer.ObserveAppendLatency(mode, d)
	}
}

func (o multiChannelObserver) ObserveChannelAppendStage(stage string, result string, d time.Duration) {
	for _, observer := range o {
		appendStageObserver, ok := observer.(clusterchannels.AppendStageObserver)
		if ok {
			appendStageObserver.ObserveChannelAppendStage(stage, result, d)
		}
	}
}

func (o multiChannelObserver) ObserveAppendWaitStage(stage string, mode ch.CommitMode, result string, d time.Duration) {
	for _, observer := range o {
		appendWaitObserver, ok := observer.(reactor.AppendWaitStageObserver)
		if ok {
			appendWaitObserver.ObserveAppendWaitStage(stage, mode, result, d)
		}
	}
}

func (o multiChannelObserver) ObserveAppendWaitCanceled(snapshot reactor.AppendWaitCancelSnapshot) {
	for _, observer := range o {
		appendCancelObserver, ok := observer.(reactor.AppendWaitCancelObserver)
		if ok {
			appendCancelObserver.ObserveAppendWaitCanceled(snapshot)
		}
	}
}

func (o multiChannelObserver) ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration) {
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

func (o multiSlotObserver) ObserveSlotProposal(slotID multiraft.SlotID, d time.Duration) {
	for _, observer := range o {
		proposalObserver, ok := observer.(multiraft.ProposalObserver)
		if ok {
			proposalObserver.ObserveSlotProposal(slotID, d)
		}
	}
}

func (o multiSlotObserver) ObserveSlotProposalAdmission(slotID multiraft.SlotID, class multiraft.ProposalClass, result string) {
	for _, observer := range o {
		admissionObserver, ok := observer.(multiraft.ProposalAdmissionObserver)
		if ok {
			admissionObserver.ObserveSlotProposalAdmission(slotID, class, result)
		}
	}
}

func (o multiSlotObserver) ObserveSlotLeaderChange(slotID multiraft.SlotID, from, to multiraft.NodeID) {
	o.ObserveSlotLeaderChangeWithCause(slotID, from, to, multiraft.LeaderChangeCauseElection)
}

func (o multiSlotObserver) ObserveSlotLeaderChangeWithCause(slotID multiraft.SlotID, from, to multiraft.NodeID, cause multiraft.LeaderChangeCause) {
	for _, observer := range o {
		if causeObserver, ok := observer.(multiraft.LeaderChangeCauseObserver); ok {
			causeObserver.ObserveSlotLeaderChangeWithCause(slotID, from, to, cause)
			continue
		}
		if leaderChangeObserver, ok := observer.(multiraft.LeaderChangeObserver); ok {
			leaderChangeObserver.ObserveSlotLeaderChange(slotID, from, to)
		}
	}
}

func (o multiSlotObserver) SetSlotApplyState(slotID multiraft.SlotID, commitIndex, appliedIndex uint64) {
	for _, observer := range o {
		applyObserver, ok := observer.(multiraft.ApplyStateObserver)
		if ok {
			applyObserver.SetSlotApplyState(slotID, commitIndex, appliedIndex)
		}
	}
}

func (o multiTransportObserver) ObserveTransport(event transport.Event) {
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

func (o multiControllerRaftObserver) SetApplyState(commitIndex, appliedIndex uint64) {
	for _, observer := range o {
		applyObserver, ok := observer.(controller.ApplyStateObserver)
		if ok {
			applyObserver.SetApplyState(commitIndex, appliedIndex)
		}
	}
}

func (o multiControlSnapshotObserver) ObserveControlSnapshot(snapshot control.Snapshot) {
	for _, observer := range o {
		if observer != nil {
			observer.ObserveControlSnapshot(snapshot)
		}
	}
}

func (o multiSlotReplicaMoveObserver) ObserveSlotReplicaMovePhase(step, result string, d time.Duration) {
	for _, observer := range o {
		if observer != nil {
			observer.ObserveSlotReplicaMovePhase(step, result, d)
		}
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

func (o multiMessageEventObserver) ObserveMessageEventAppend(event cluster.MessageEventAppendObservation) {
	for _, observer := range o {
		if observer != nil {
			observer.ObserveMessageEventAppend(event)
		}
	}
}

func (o multiMessageEventObserver) ObserveMessageEventAppendStage(event cluster.MessageEventAppendStageObservation) {
	for _, observer := range o {
		if observer == nil {
			continue
		}
		stageObserver, ok := observer.(cluster.MessageEventAppendStageObserver)
		if ok {
			stageObserver.ObserveMessageEventAppendStage(event)
		}
	}
}

func (o multiMessageEventObserver) ObserveMessageEventPropose(event cluster.MessageEventProposeObservation) {
	for _, observer := range o {
		if observer != nil {
			observer.ObserveMessageEventPropose(event)
		}
	}
}

func (o multiMessageEventObserver) ObserveMessageEventProposeStage(event cluster.MessageEventProposeStageObservation) {
	for _, observer := range o {
		if observer == nil {
			continue
		}
		stageObserver, ok := observer.(cluster.MessageEventProposeStageObserver)
		if ok {
			stageObserver.ObserveMessageEventProposeStage(event)
		}
	}
}

func (o multiMessageEventObserver) SetMessageEventStreamCache(event cluster.MessageEventStreamCacheObservation) {
	for _, observer := range o {
		if observer != nil {
			observer.SetMessageEventStreamCache(event)
		}
	}
}

func (o multiDeliveryObserver) ObserveFanoutTask(event runtimedelivery.FanoutTaskEvent) {
	for _, observer := range o {
		observer.ObserveFanoutTask(event)
	}
}

func (o multiDeliveryObserver) ObserveFanoutResolve(event runtimedelivery.FanoutResolveEvent) {
	for _, observer := range o {
		observer.ObserveFanoutResolve(event)
	}
}

func (o multiDeliveryObserver) ObserveFanoutPush(event runtimedelivery.FanoutPushEvent) {
	for _, observer := range o {
		observer.ObserveFanoutPush(event)
	}
}

func (o multiDeliveryObserver) ObserveRetry(event runtimedelivery.RetryEvent) {
	for _, observer := range o {
		retryObserver, ok := observer.(runtimedelivery.RetryObserver)
		if ok {
			retryObserver.ObserveRetry(event)
		}
	}
}

func (o multiDeliveryObserver) ObserveAck(event runtimedelivery.AckEvent) {
	for _, observer := range o {
		ackObserver, ok := observer.(runtimedelivery.AckObserver)
		if ok {
			ackObserver.ObserveAck(event)
		}
	}
}

func (o multiDeliveryObserver) ObserveManagerAdmission(event runtimedelivery.ManagerAdmissionEvent) {
	for _, observer := range o {
		managerObserver, ok := observer.(runtimedelivery.ManagerObserver)
		if ok {
			managerObserver.ObserveManagerAdmission(event)
		}
	}
}

func (o multiDeliveryObserver) ObserveManagerTerminal(event runtimedelivery.ManagerTerminalEvent) {
	for _, observer := range o {
		managerObserver, ok := observer.(runtimedelivery.ManagerObserver)
		if ok {
			managerObserver.ObserveManagerTerminal(event)
		}
	}
}

func channelCommitModeLabel(mode ch.CommitMode) string {
	switch mode {
	case ch.CommitModeLocal:
		return "local"
	case ch.CommitModeQuorum:
		return "quorum"
	default:
		return "unknown"
	}
}

func channelPullBatchResultLabel(event ch.PullBatchObservation) string {
	switch {
	case event.Errors <= 0:
		return "ok"
	case event.Items > 0 && event.Errors >= event.Items:
		return "err"
	default:
		return "partial"
	}
}

func channelWorkerKindLabel(kind worker.TaskKind) string {
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
	case worker.TaskMetaResolve:
		return "meta_resolve"
	case worker.TaskColdMetaResolve:
		return "cold_meta_resolve"
	case worker.TaskColdStoreLoad:
		return "cold_store_load"
	default:
		return "unknown"
	}
}

func channelRoleLabel(role ch.Role) string {
	switch role {
	case ch.RoleLeader:
		return "leader"
	case ch.RoleFollower:
		return "follower"
	default:
		return "unknown"
	}
}

func channelPullHintReasonLabel(reason channeltransport.PullHintReason) string {
	switch reason {
	case channeltransport.PullHintReasonAppend:
		return "append"
	case channeltransport.PullHintReasonResume:
		return "resume"
	default:
		return "unknown"
	}
}

func channelPullHintErrorLabel(err error) string {
	message := ""
	if err != nil {
		message = err.Error()
	}
	switch {
	case err == nil:
		return "none"
	case ch.ErrorMatches(err, ch.ErrNotReady):
		return "not_ready"
	case ch.ErrorMatches(err, ch.ErrStaleMeta):
		return "stale_meta"
	case ch.ErrorMatches(err, ch.ErrChannelNotFound):
		return "channel_not_found"
	case ch.ErrorMatches(err, ch.ErrNotLeader):
		return "not_leader"
	case ch.ErrorMatches(err, ch.ErrNotReplica):
		return "not_replica"
	case ch.ErrorMatches(err, ch.ErrBackpressured):
		return "backpressured"
	case ch.ErrorMatches(err, ch.ErrInvalidConfig):
		return "invalid_config"
	case ch.ErrorMatches(err, ch.ErrClosed):
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
	case errors.Is(err, messageusecase.ErrBackpressured) || strings.Contains(message, messageusecase.ErrBackpressured.Error()) || ch.ErrorMessageMatches(message, ch.ErrBackpressured):
		return "backpressured"
	case errors.Is(err, messageusecase.ErrRouteNotReady) || strings.Contains(message, messageusecase.ErrRouteNotReady.Error()) || ch.ErrorMessageMatches(message, ch.ErrNotReady):
		return "route_not_ready"
	case errors.Is(err, messageusecase.ErrStaleRoute) || strings.Contains(message, messageusecase.ErrStaleRoute.Error()) || ch.ErrorMessageMatches(message, ch.ErrStaleMeta):
		return "stale_route"
	case errors.Is(err, messageusecase.ErrNotLeader) || strings.Contains(message, messageusecase.ErrNotLeader.Error()) || ch.ErrorMessageMatches(message, ch.ErrNotLeader):
		return "not_leader"
	case errors.Is(err, messageusecase.ErrChannelNotFound) || strings.Contains(message, messageusecase.ErrChannelNotFound.Error()) || ch.ErrorMessageMatches(message, ch.ErrChannelNotFound):
		return "channel_not_found"
	case errors.Is(err, messageusecase.ErrAppendResultMissing) || strings.Contains(message, messageusecase.ErrAppendResultMissing.Error()):
		return "short_result"
	case ch.ErrorMatches(err, ch.ErrInvalidConfig) || errors.Is(err, cluster.ErrInvalidConfig) || strings.Contains(message, cluster.ErrInvalidConfig.Error()):
		return "invalid_config"
	case ch.ErrorMatches(err, ch.ErrClosed) || errors.Is(err, cluster.ErrStopping) || strings.Contains(message, cluster.ErrStopping.Error()):
		return "closed"
	case ch.ErrorMatches(err, ch.ErrTooManyChannels):
		return "too_many_channels"
	case errors.Is(err, cluster.ErrNotStarted) || strings.Contains(message, cluster.ErrNotStarted.Error()):
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
var _ accessapi.ConversationSyncObserver = conversationSyncMetricsObserver{}
var _ conversationAuthorityObserver = conversationAuthorityMetricsObserver{}
var _ reactor.Observer = channelMetricsObserver{}
var _ reactor.MailboxPressureObserver = channelMetricsObserver{}
var _ reactor.AppendQueuePressureObserver = channelMetricsObserver{}
var _ reactor.RuntimeObserver = channelMetricsObserver{}
var _ reactor.ReplicationObserver = channelMetricsObserver{}
var _ reactor.ReplicationStageObserver = channelMetricsObserver{}
var _ reactor.PullBatchObserver = channelMetricsObserver{}
var _ reactor.LeaderPullObserver = channelMetricsObserver{}
var _ reactor.PullHintResultObserver = channelMetricsObserver{}
var _ reactor.PendingMetaObserver = channelMetricsObserver{}
var _ reactor.AppendWaitCancelObserver = channelMetricsObserver{}
var _ worker.InflightObserver = channelMetricsObserver{}
var _ worker.QueueCapacityObserver = channelMetricsObserver{}
var _ worker.AdmissionObserver = channelMetricsObserver{}
var _ worker.WaitObserver = channelMetricsObserver{}
var _ worker.TaskObserver = channelMetricsObserver{}
var _ worker.AntsPoolObserver = channelMetricsObserver{}
var _ clusterchannels.MetaCacheObserver = channelMetricsObserver{}
var _ clusterchannels.AppendStageObserver = channelMetricsObserver{}
var _ multiraft.SchedulerObserver = slotMetricsObserver{}
var _ multiraft.ProposalObserver = slotMetricsObserver{}
var _ multiraft.ProposalAdmissionObserver = slotMetricsObserver{}
var _ multiraft.ApplyStateObserver = slotMetricsObserver{}
var _ transport.Observer = (*transportMetricsObserver)(nil)
var _ controller.RaftObserver = controllerRaftMetricsObserver{}
var _ controller.ApplyStateObserver = controllerRaftMetricsObserver{}
var _ reactor.Observer = multiChannelObserver{}
var _ reactor.MailboxPressureObserver = multiChannelObserver{}
var _ worker.AntsPoolObserver = multiChannelObserver{}
var _ reactor.AppendQueuePressureObserver = multiChannelObserver{}
var _ reactor.RuntimeObserver = multiChannelObserver{}
var _ reactor.ReplicationObserver = multiChannelObserver{}
var _ reactor.ReplicationStageObserver = multiChannelObserver{}
var _ reactor.PullBatchObserver = multiChannelObserver{}
var _ reactor.LeaderPullObserver = multiChannelObserver{}
var _ reactor.PullHintResultObserver = multiChannelObserver{}
var _ reactor.PendingMetaObserver = multiChannelObserver{}
var _ transport.Observer = multiTransportObserver{}
var _ accessgateway.SessionErrorObserver = multiGatewayObserver{}
var _ reactor.AppendWaitCancelObserver = multiChannelObserver{}
var _ worker.InflightObserver = multiChannelObserver{}
var _ worker.QueueCapacityObserver = multiChannelObserver{}
var _ worker.AdmissionObserver = multiChannelObserver{}
var _ worker.WaitObserver = multiChannelObserver{}
var _ worker.TaskObserver = multiChannelObserver{}
var _ clusterchannels.MetaCacheObserver = multiChannelObserver{}
var _ clusterchannels.AppendStageObserver = multiChannelObserver{}
var _ multiraft.SchedulerObserver = multiSlotObserver{}
var _ multiraft.ProposalObserver = multiSlotObserver{}
var _ multiraft.ProposalAdmissionObserver = multiSlotObserver{}
var _ multiraft.ApplyStateObserver = multiSlotObserver{}
var _ controller.RaftObserver = multiControllerRaftObserver{}
var _ controller.ApplyStateObserver = multiControllerRaftObserver{}
var _ messagedb.CommitCoordinatorObserver = storageCommitMetricsObserver{}
var _ messagedb.CommitCoordinatorQueueObserver = storageCommitMetricsObserver{}
var _ messagedb.CommitCoordinatorRequestObserver = storageCommitMetricsObserver{}
var _ messagedb.CommitCoordinatorObserver = multiCommitCoordinatorObserver{}
var _ messagedb.CommitCoordinatorQueueObserver = multiCommitCoordinatorObserver{}
var _ messagedb.CommitCoordinatorRequestObserver = multiCommitCoordinatorObserver{}
var _ runtimedelivery.Observer = multiDeliveryObserver{}
var _ runtimedelivery.RetryObserver = multiDeliveryObserver{}
var _ runtimedelivery.AckObserver = deliveryMetricsObserver{}
var _ runtimedelivery.AckObserver = multiDeliveryObserver{}
var _ runtimedelivery.ManagerObserver = multiDeliveryObserver{}
