package app

import (
	"strings"
	"sync"
	"time"

	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	clusterv2channels "github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controller"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	gatewaypkg "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
)

type topGatewayObserver struct {
	top *topCollector
}

func (o topGatewayObserver) OnConnectionOpen(event gatewaypkg.ConnectionEvent) {
	if o.top == nil {
		return
	}
	o.top.addCounter("gateway.connection.open."+safeTopLabel(event.Protocol), 1)
}

func (o topGatewayObserver) OnConnectionClose(event gatewaypkg.ConnectionEvent) {
	if o.top == nil {
		return
	}
	o.top.addCounter("gateway.connection.close."+safeTopLabel(event.Protocol), 1)
}

func (o topGatewayObserver) OnAuth(event gatewaypkg.AuthEvent) {
	if o.top == nil || strings.EqualFold(strings.TrimSpace(event.Status), "ok") {
		return
	}
	o.top.addCounter("gateway.auth.fail", 1)
}

func (o topGatewayObserver) OnFrameIn(event gatewaypkg.FrameEvent) {
	if o.top == nil || event.FrameType != "SEND" {
		return
	}
	o.top.ObserveGatewaySend(event.Protocol, event.Bytes)
}

func (o topGatewayObserver) OnFrameOut(gatewaypkg.FrameEvent) {}

func (o topGatewayObserver) OnFrameHandled(gatewaypkg.FrameHandleEvent) {}

func (o topGatewayObserver) OnSessionError(event gatewaypkg.SessionErrorEvent) {
	if o.top == nil {
		return
	}
	o.top.ObserveGatewaySessionError(event.Protocol, string(event.CloseReason), event.Class)
}

func (o topGatewayObserver) OnAsyncSendQueue(event gatewaypkg.AsyncSendQueueEvent) {
	if o.top == nil {
		return
	}
	o.top.SetQueue("gateway", "async_send", "send", "none", int64(event.Depth), int64(event.Capacity))
}

func (o topGatewayObserver) OnAsyncSendAdmission(event gatewaypkg.AsyncSendAdmissionEvent) {
	if o.top == nil || strings.EqualFold(strings.TrimSpace(event.Result), "ok") {
		return
	}
	o.top.addCounter("pressure.gateway.async_send.send.none.admission_error", 1)
}

func (o topGatewayObserver) OnAsyncSendBatch(gatewaypkg.AsyncSendBatchEvent) {}

func (o topGatewayObserver) OnAsyncSendDispatchWait(event gatewaypkg.AsyncSendDispatchWaitEvent) {
	if o.top == nil {
		return
	}
	o.top.observeDurationMS("pressure.gateway.async_send.send.none.wait", event.Duration)
}

func (o topGatewayObserver) OnAsyncAuthQueue(event gatewaypkg.AsyncAuthQueueEvent) {
	if o.top == nil {
		return
	}
	o.top.SetQueue("gateway", "async_auth", "auth", "none", int64(event.Depth), int64(event.Capacity))
}

func (o topGatewayObserver) OnAsyncAuthAdmission(gatewaypkg.AsyncAuthAdmissionEvent) {}

func (o topGatewayObserver) OnAsyncAuthWait(gatewaypkg.AsyncAuthWaitEvent) {}

func (o topGatewayObserver) OnTransportPressure(gatewaypkg.TransportPressureEvent) {}

type topChannelV2Observer struct {
	top *topCollector
}

func (o topChannelV2Observer) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	if o.top == nil {
		return
	}
	pool := channelV2ReactorPoolLabel(reactorID)
	key := "channelv2.reactor_mailbox." + safeTopLabel(pool) + "." + safeTopLabel(priority)
	o.top.setGauge(key+".depth", int64(depth))
	o.top.setGauge(topPressureKey("channelv2", pool, "mailbox", priority)+".depth", int64(depth))
}

func (o topChannelV2Observer) SetReactorMailboxCapacity(reactorID int, priority string, capacity int) {
	if o.top == nil {
		return
	}
	pool := channelV2ReactorPoolLabel(reactorID)
	key := "channelv2.reactor_mailbox." + safeTopLabel(pool) + "." + safeTopLabel(priority)
	o.top.setGauge(key+".capacity", int64(capacity))
	o.top.setGauge(topPressureKey("channelv2", pool, "mailbox", priority)+".capacity", int64(capacity))
}

func (o topChannelV2Observer) ObserveReactorMailboxAdmission(reactorID int, priority string, result string) {
	if o.top == nil || strings.EqualFold(strings.TrimSpace(result), "ok") {
		return
	}
	o.top.addCounter("pressure.channelv2."+safeTopLabel(channelV2ReactorPoolLabel(reactorID))+".mailbox."+safeTopLabel(priority)+".admission_error", 1)
}

func (o topChannelV2Observer) SetAppendQueuePressure(event reactor.AppendQueuePressureEvent) {
	if o.top == nil {
		return
	}
	o.top.SetQueue("channelv2", channelV2ReactorPoolLabel(event.ReactorID), "append", "none", int64(event.Depth), int64(event.Capacity))
}

func (o topChannelV2Observer) SetWorkerQueueDepth(pool string, depth int) {
	if o.top == nil {
		return
	}
	key := "channelv2.worker." + safeTopLabel(pool)
	o.top.setGauge(key+".queue_depth", int64(depth))
	o.top.setGauge(topPressureKey("channelv2", pool, "worker", "none")+".depth", int64(depth))
}

func (o topChannelV2Observer) SetWorkerQueueCapacity(pool string, capacity int) {
	if o.top == nil {
		return
	}
	key := "channelv2.worker." + safeTopLabel(pool)
	o.top.setGauge(key+".queue_capacity", int64(capacity))
	o.top.setGauge(topPressureKey("channelv2", pool, "worker", "none")+".capacity", int64(capacity))
}

func (o topChannelV2Observer) SetWorkerWorkers(pool string, workers int) {
	if o.top == nil {
		return
	}
	key := "channelv2.worker." + safeTopLabel(pool)
	o.top.setGauge(key+".workers", int64(workers))
	o.top.setGauge(topPressureKey("channelv2", pool, "inflight", "none")+".workers", int64(workers))
}

func (o topChannelV2Observer) ObserveWorkerAdmission(pool string, result string) {
	if o.top == nil || strings.EqualFold(strings.TrimSpace(result), "ok") {
		return
	}
	o.top.addCounter("pressure.channelv2."+safeTopLabel(pool)+".worker.none.admission_error", 1)
}

func (o topChannelV2Observer) ObserveWorkerWait(pool string, kind worker.TaskKind, d time.Duration) {
	if o.top == nil {
		return
	}
	o.top.observeDurationMS("pressure.channelv2."+safeTopLabel(pool)+".worker.none.wait", d)
	_ = kind
}

func (o topChannelV2Observer) ObserveWorkerTask(pool string, kind worker.TaskKind, err error, d time.Duration) {
	if o.top == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.top.observeDurationMS("pressure.channelv2."+safeTopLabel(pool)+"."+safeTopLabel(channelV2WorkerKindLabel(kind))+"."+result+".task", d)
}

func (o topChannelV2Observer) ObserveWorkerBatch(pool string, kind worker.TaskKind, items int, err error) {
	if o.top == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.top.addCounter("channelv2.worker_batch."+safeTopLabel(pool)+"."+safeTopLabel(channelV2WorkerKindLabel(kind))+"."+result, uint64(nonNegativeInt(items)))
}

func (o topChannelV2Observer) SetWorkerInflight(pool string, inflight int) {
	if o.top == nil {
		return
	}
	key := "channelv2.worker." + safeTopLabel(pool)
	o.top.setGauge(key+".inflight", int64(inflight))
	o.top.setGauge(topPressureKey("channelv2", pool, "inflight", "none")+".inflight", int64(inflight))
}

func (o topChannelV2Observer) SetWorkerInflightPeak(pool string, peak int) {
	if o.top == nil {
		return
	}
	o.top.setGauge("channelv2.worker."+safeTopLabel(pool)+".inflight_peak", int64(peak))
}

func (o topChannelV2Observer) SetWorkerAntsPoolUsage(pool string, running int, capacity int, waiting int) {
	if o.top == nil {
		return
	}
	o.SetWorkerInflight(pool, running)
	o.SetWorkerWorkers(pool, capacity)
	o.top.setGauge("channelv2.worker."+safeTopLabel(pool)+".waiting", int64(waiting))
}

func (o topChannelV2Observer) SetChannelRuntimeCount(reactorID int, role ch.Role, count int) {
	if o.top == nil {
		return
	}
	o.top.SetChannelV2RuntimeCount(reactorID, channelV2RoleLabel(role), int64(count))
}

func (o topChannelV2Observer) ObserveChannelActivationRejected(reason string) {
	if o.top == nil {
		return
	}
	o.top.addCounter("channelv2.activation_rejected."+safeTopLabel(reason), 1)
}

func (o topChannelV2Observer) SetFollowerParkedCount(reactorID int, count int) {
	if o.top == nil {
		return
	}
	o.top.SetChannelV2FollowerParked(reactorID, int64(count))
}

func (o topChannelV2Observer) ObserveFollowerRecoveryProbe(result string) {
	if o.top == nil {
		return
	}
	o.top.addCounter("channelv2.follower_recovery."+safeTopLabel(result), 1)
}

func (o topChannelV2Observer) ObservePull(result string, empty bool) {
	if o.top == nil {
		return
	}
	label := safeTopLabel(result)
	if empty {
		label += ".empty"
	}
	o.top.addCounter("channelv2.pull."+label, 1)
}

func (o topChannelV2Observer) ObserveReplicationStage(stage string, result string, d time.Duration) {
	if o.top == nil || !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return
	}
	o.top.observeDurationMS("channelv2.replication."+safeTopLabel(stage), d)
}

func (o topChannelV2Observer) ObserveChannelMetaCache(result string) {
	if o.top == nil {
		return
	}
	o.top.addCounter("channelv2.meta_cache."+safeTopLabel(result), 1)
}

func (o topChannelV2Observer) ObserveAppendBatch(records int, bytes int, wait time.Duration) {
	if o.top == nil {
		return
	}
	o.top.observeValue("channelv2.append.batch.records", float64(nonNegativeInt(records)))
	o.top.observeDurationMS("channelv2.append.batch.wait", wait)
	_ = bytes
}

func (o topChannelV2Observer) ObserveAppendLatency(mode ch.CommitMode, d time.Duration) {
	if o.top == nil {
		return
	}
	o.top.ObserveChannelV2AppendLatency(channelV2CommitModeLabel(mode), d)
}

func (o topChannelV2Observer) ObserveChannelAppendStage(stage string, result string, d time.Duration) {
	if o.top == nil {
		return
	}
	o.top.ObserveChannelV2AppendStage(stage, result, d)
}

func (o topChannelV2Observer) ObserveAppendWaitStage(stage string, mode ch.CommitMode, result string, d time.Duration) {
	if o.top == nil {
		return
	}
	o.top.ObserveChannelV2AppendStage(stage+"_"+channelV2CommitModeLabel(mode), result, d)
}

func (o topChannelV2Observer) ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration) {
	if o.top == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.top.observeDurationMS("channelv2.worker_result."+safeTopLabel(channelV2WorkerKindLabel(kind))+"."+result, d)
}

type topStorageObserver struct {
	top *topCollector
}

func (o topStorageObserver) SetCommitCoordinatorQueueDepth(depth int) {
	if o.top == nil {
		return
	}
	o.top.setGauge(topGaugeStorageCommitDepth, int64(depth))
	o.top.setGauge(topPressureKey(dbRuntimeComponent, dbMessageCommitPool, dbMessageCommitQueue, dbRuntimeQueuePriority)+".depth", int64(depth))
}

func (o topStorageObserver) SetCommitCoordinatorQueue(depth int, capacity int) {
	if o.top == nil {
		return
	}
	o.top.SetStorageCommitQueue(int64(depth), int64(capacity))
}

func (o topStorageObserver) ObserveCommitCoordinatorBatch(event messagedb.CommitCoordinatorBatchEvent) {
	if o.top == nil {
		return
	}
	duration := event.CommitDuration
	if duration <= 0 {
		duration = event.TotalDuration
	}
	o.top.ObserveStorageCommitBatch(event.Records, duration)
}

func (o topStorageObserver) ObserveCommitCoordinatorRequest(event messagedb.CommitCoordinatorRequestEvent) {
	if o.top == nil {
		return
	}
	o.top.ObserveStorageCommitRequest(event.Lane, event.Result, event.Duration)
}

type topDeliveryObserver struct {
	top *topCollector
}

func (o topDeliveryObserver) ObserveFanoutTask(event runtimedelivery.FanoutTaskEvent) {
	if o.top == nil || event.ErrorClass == "" || event.ErrorClass == runtimedelivery.DeliveryErrorClassNone {
		return
	}
	o.top.addCounter(topCounterDeliveryPushErr, 1)
}

func (o topDeliveryObserver) ObserveFanoutResolve(event runtimedelivery.FanoutResolveEvent) {
	if o.top == nil {
		return
	}
	o.top.ObserveDeliveryRoutes(event.Routes)
	if event.ErrorClass != "" && event.ErrorClass != runtimedelivery.DeliveryErrorClassNone {
		o.top.addCounter(topCounterDeliveryPushErr, 1)
	}
}

func (o topDeliveryObserver) ObserveFanoutPush(event runtimedelivery.FanoutPushEvent) {
	if o.top == nil {
		return
	}
	accepted := event.Accepted
	if accepted <= 0 && event.Result == runtimedelivery.DeliveryResultOK {
		accepted = event.Routes
	}
	o.top.ObserveDeliveryPush(event.Result, accepted, event.Duration)
	if event.Retryable > 0 || event.Dropped > 0 || (event.ErrorClass != "" && event.ErrorClass != runtimedelivery.DeliveryErrorClassNone) {
		o.top.addCounter(topCounterDeliveryPushErr, uint64(nonNegativeInt(event.Retryable+event.Dropped)))
	}
}

func (o topDeliveryObserver) ObserveRetry(event runtimedelivery.RetryEvent) {
	if o.top == nil {
		return
	}
	o.top.SetDeliveryRetryQueueDepth(int64(event.QueueDepth))
	if event.ErrorClass != "" && event.ErrorClass != runtimedelivery.DeliveryErrorClassNone {
		o.top.addCounter(topCounterDeliveryPushErr, 1)
	}
}

func (o topDeliveryObserver) ObserveAck(event runtimedelivery.AckEvent) {
	if o.top == nil {
		return
	}
	o.top.SetDeliveryAckBindings(int64(event.PendingCount))
}

func (o topDeliveryObserver) ObserveManagerAdmission(event runtimedelivery.ManagerAdmissionEvent) {
	if o.top == nil {
		return
	}
	o.top.SetQueue("delivery", "manager", "events", "none", int64(event.QueueDepth), 0)
	if event.Result != "" && event.Result != runtimedelivery.DeliveryResultOK {
		o.top.addCounter(topCounterDeliveryPushErr, 1)
	}
}

func (o topDeliveryObserver) ObserveManagerTerminal(event runtimedelivery.ManagerTerminalEvent) {
	if o.top == nil {
		return
	}
	o.top.SetQueue("delivery", "manager", "events", "none", int64(event.QueueDepth), 0)
	if event.ErrorClass != "" && event.ErrorClass != runtimedelivery.DeliveryErrorClassNone {
		o.top.addCounter(topCounterDeliveryPushErr, 1)
	}
}

type topSlotObserver struct {
	top *topCollector
}

func (o topSlotObserver) SetSchedulerWorkers(workers int) {
	if o.top != nil {
		o.top.setGauge(topPressureKey("slot", "scheduler", "inflight", "none")+".workers", int64(workers))
	}
}

func (o topSlotObserver) SetSchedulerInflight(inflight int) {
	if o.top != nil {
		o.top.setGauge(topPressureKey("slot", "scheduler", "inflight", "none")+".inflight", int64(inflight))
	}
}

func (o topSlotObserver) SetSchedulerState(event multiraft.SchedulerStateEvent) {
	if o.top != nil {
		o.top.SetQueue("slot", "scheduler", "scheduler", "none", int64(event.Depth+event.Pending), int64(event.Capacity))
	}
}

func (o topSlotObserver) ObserveSchedulerAdmission(result string) {
	if o.top != nil && isSlotSchedulerAdmissionError(result) {
		o.top.addCounter("pressure.slot.scheduler.scheduler.none.admission_error", 1)
	}
}

func isSlotSchedulerAdmissionError(result string) bool {
	switch strings.ToLower(strings.TrimSpace(result)) {
	case "", "ok", "coalesced", "dirty", "requeued":
		return false
	default:
		return true
	}
}

func (o topSlotObserver) ObserveSchedulerTask(task string, d time.Duration) {
	if o.top != nil {
		o.top.observeDurationMS("pressure.slot.scheduler."+safeTopLabel(task)+".none.task", d)
	}
}

type topControllerRaftObserver struct {
	top *topCollector
}

func (o topControllerRaftObserver) SetStepQueueDepth(depth int, capacity int) {
	if o.top != nil {
		o.top.SetQueue("controller", "raft", "step", "none", int64(depth), int64(capacity))
	}
}

func (o topControllerRaftObserver) ObserveStepEnqueue(result string, d time.Duration) {
	if o.top == nil {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		o.top.addCounter("pressure.controller.raft.step.none.admission_error", 1)
	}
	o.top.observeDurationMS("pressure.controller.raft.step.none.wait", d)
}

type topTransportV2Observer struct {
	top *topCollector
	mu  sync.Mutex

	pendingRPCBySource map[uint64]int
}

func (o *topTransportV2Observer) ObserveTransport(event transportv2.Event) {
	if o == nil || o.top == nil {
		return
	}
	switch event.Name {
	case "pending_rpc":
		o.top.SetInflight("transportv2", "rpc", int64(o.pendingRPCInflight(event)), 0)
	case "peer_pool":
		inflight := event.Inflight
		if inflight == 0 {
			inflight = event.Items
		}
		o.top.SetInflight("transportv2", "peer_pool", int64(inflight), int64(event.Capacity))
	case "scheduler_queue":
		o.top.SetQueue("transportv2", "scheduler", "scheduler", transportV2PriorityLabel(event.Priority), int64(event.Items), int64(event.Capacity))
	case "service_queue":
		o.top.SetQueue("transportv2", "service", transportV2ServiceEventLabel(event), transportV2PriorityLabel(event.Priority), int64(event.Items), int64(event.Capacity))
	case "scheduler_admission":
		if event.Result != "" && event.Result != "ok" {
			o.top.addCounter("pressure.transportv2.scheduler.scheduler."+safeTopLabel(transportV2PriorityLabel(event.Priority))+".admission_error", 1)
		}
	case "service_admission":
		if event.Result != "" && event.Result != "ok" {
			o.top.addCounter("pressure.transportv2.service."+safeTopLabel(transportV2ServiceEventLabel(event))+"."+safeTopLabel(transportV2PriorityLabel(event.Priority))+".admission_error", 1)
		}
	case "scheduler_wait":
		o.top.observeDurationMS("pressure.transportv2.scheduler.scheduler."+safeTopLabel(transportV2PriorityLabel(event.Priority))+".wait", event.Duration)
	case "service_task":
		o.top.observeDurationMS("pressure.transportv2.service."+safeTopLabel(transportV2ServiceEventLabel(event))+"."+safeTopLabel(event.Result)+".task", event.Duration)
	case "service_inflight":
		o.top.SetInflight("transportv2", transportV2ServiceEventLabel(event), int64(event.Inflight), int64(event.Capacity))
	}
}

func (o *topTransportV2Observer) pendingRPCInflight(event transportv2.Event) int {
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
	total := 0
	for _, inflight := range o.pendingRPCBySource {
		total += inflight
	}
	return total
}

type topSendackObserver struct {
	top *topCollector
}

func (o topSendackObserver) SendackWritten(event accessgateway.SendackEvent) {
	if o.top == nil {
		return
	}
	o.top.ObserveGatewaySendack(gatewaySendackReasonLabel(event.Reason), event.Source, event.ErrorClass)
}

var _ gatewaypkg.Observer = topGatewayObserver{}
var _ gatewaypkg.SessionErrorObserver = topGatewayObserver{}
var _ gatewaypkg.AsyncSendObserver = topGatewayObserver{}
var _ gatewaypkg.AsyncSendAdmissionObserver = topGatewayObserver{}
var _ gatewaypkg.AsyncAuthObserver = topGatewayObserver{}
var _ gatewaypkg.TransportPressureObserver = topGatewayObserver{}
var _ accessgateway.SendackObserver = topSendackObserver{}
var _ reactor.Observer = topChannelV2Observer{}
var _ reactor.MailboxPressureObserver = topChannelV2Observer{}
var _ reactor.AppendQueuePressureObserver = topChannelV2Observer{}
var _ reactor.RuntimeObserver = topChannelV2Observer{}
var _ reactor.ReplicationObserver = topChannelV2Observer{}
var _ reactor.ReplicationStageObserver = topChannelV2Observer{}
var _ reactor.AppendWaitStageObserver = topChannelV2Observer{}
var _ worker.InflightObserver = topChannelV2Observer{}
var _ worker.QueueCapacityObserver = topChannelV2Observer{}
var _ worker.AdmissionObserver = topChannelV2Observer{}
var _ worker.WaitObserver = topChannelV2Observer{}
var _ worker.TaskObserver = topChannelV2Observer{}
var _ worker.BatchObserver = topChannelV2Observer{}
var _ worker.AntsPoolObserver = topChannelV2Observer{}
var _ clusterv2channels.MetaCacheObserver = topChannelV2Observer{}
var _ clusterv2channels.AppendStageObserver = topChannelV2Observer{}
var _ messagedb.CommitCoordinatorObserver = topStorageObserver{}
var _ messagedb.CommitCoordinatorQueueObserver = topStorageObserver{}
var _ messagedb.CommitCoordinatorRequestObserver = topStorageObserver{}
var _ runtimedelivery.Observer = topDeliveryObserver{}
var _ runtimedelivery.RetryObserver = topDeliveryObserver{}
var _ runtimedelivery.AckObserver = topDeliveryObserver{}
var _ runtimedelivery.ManagerObserver = topDeliveryObserver{}
var _ multiraft.SchedulerObserver = topSlotObserver{}
var _ cv2.RaftObserver = topControllerRaftObserver{}
var _ transportv2.Observer = (*topTransportV2Observer)(nil)

func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
