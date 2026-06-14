package app

import (
	"strings"

	accessgateway "github.com/WuKongIM/WuKongIM/internalv2/access/gateway"
	gatewaypkg "github.com/WuKongIM/WuKongIM/pkg/gateway"
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
var _ gatewaypkg.AsyncSendObserver = topGatewayObserver{}
var _ gatewaypkg.AsyncSendAdmissionObserver = topGatewayObserver{}
var _ gatewaypkg.AsyncAuthObserver = topGatewayObserver{}
var _ gatewaypkg.TransportPressureObserver = topGatewayObserver{}
var _ accessgateway.SendackObserver = topSendackObserver{}
