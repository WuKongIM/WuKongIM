package app

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	accessgateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

type gatewayMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type channelV2MetricsObserver struct {
	metrics *obsmetrics.Registry
}

type storageCommitMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type multiChannelV2Observer []reactor.Observer
type multiCommitCoordinatorObserver []messagedb.CommitCoordinatorObserver

// channelV2MetaCacheObserver receives ChannelV2 metadata cache observations.
type channelV2MetaCacheObserver interface {
	ObserveChannelMetaCache(result string)
}

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
	o.metrics.Gateway.Auth(event.Status, event.Duration)
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
}

func (o channelV2MetricsObserver) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetReactorMailboxDepth(reactorID, priority, depth)
}

func (o channelV2MetricsObserver) SetWorkerQueueDepth(pool string, depth int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetWorkerQueueDepth(pool, depth)
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

func (o storageCommitMetricsObserver) SetCommitCoordinatorQueueDepth(depth int) {
	if o.metrics == nil {
		return
	}
	o.metrics.Storage.SetCommitQueueDepth("message", depth)
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

func combineCommitCoordinatorObservers(first, second messagedb.CommitCoordinatorObserver) messagedb.CommitCoordinatorObserver {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiCommitCoordinatorObserver{first, second}
}

func (o multiChannelV2Observer) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	for _, observer := range o {
		observer.SetReactorMailboxDepth(reactorID, priority, depth)
	}
}

func (o multiChannelV2Observer) SetWorkerQueueDepth(pool string, depth int) {
	for _, observer := range o {
		observer.SetWorkerQueueDepth(pool, depth)
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

func (o multiChannelV2Observer) ObserveChannelMetaCache(result string) {
	for _, observer := range o {
		metaCacheObserver, ok := observer.(channelV2MetaCacheObserver)
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

func (o multiChannelV2Observer) ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration) {
	for _, observer := range o {
		observer.ObserveWorkerResult(kind, err, d)
	}
}

func (o multiCommitCoordinatorObserver) SetCommitCoordinatorQueueDepth(depth int) {
	for _, observer := range o {
		observer.SetCommitCoordinatorQueueDepth(depth)
	}
}

func (o multiCommitCoordinatorObserver) ObserveCommitCoordinatorBatch(event messagedb.CommitCoordinatorBatchEvent) {
	for _, observer := range o {
		observer.ObserveCommitCoordinatorBatch(event)
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

var _ accessgateway.Observer = gatewayMetricsObserver{}
var _ accessgateway.AsyncSendObserver = gatewayMetricsObserver{}
var _ reactor.Observer = channelV2MetricsObserver{}
var _ reactor.RuntimeObserver = channelV2MetricsObserver{}
var _ reactor.ReplicationObserver = channelV2MetricsObserver{}
var _ channelV2MetaCacheObserver = channelV2MetricsObserver{}
var _ reactor.Observer = multiChannelV2Observer{}
var _ reactor.RuntimeObserver = multiChannelV2Observer{}
var _ reactor.ReplicationObserver = multiChannelV2Observer{}
var _ channelV2MetaCacheObserver = multiChannelV2Observer{}
var _ messagedb.CommitCoordinatorObserver = storageCommitMetricsObserver{}
var _ messagedb.CommitCoordinatorObserver = multiCommitCoordinatorObserver{}
