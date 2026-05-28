package app

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
	accessgateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

type gatewayMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type channelV2MetricsObserver struct {
	metrics *obsmetrics.Registry
}

type multiChannelV2Observer []reactor.Observer

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

func combineChannelV2Observers(first, second reactor.Observer) reactor.Observer {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiChannelV2Observer{first, second}
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

var _ accessgateway.Observer = gatewayMetricsObserver{}
var _ accessgateway.AsyncSendObserver = gatewayMetricsObserver{}
var _ reactor.Observer = channelV2MetricsObserver{}
var _ reactor.Observer = multiChannelV2Observer{}
