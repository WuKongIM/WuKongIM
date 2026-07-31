package app

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type onlineDeliveryObserver struct {
	app *App
}

func (a *App) onlineDeliveryObserver() *onlineDeliveryObserver {
	if a == nil {
		return nil
	}
	if a.metrics == nil {
		if _, ok := a.topProvider.(*topCollector); !ok && a.logger == nil {
			return nil
		}
	}
	return &onlineDeliveryObserver{app: a}
}

func (o *onlineDeliveryObserver) ObservePlanAdmission(event runtimedelivery.PlanAdmissionEvent) {
	if o == nil || o.app == nil {
		return
	}
	if o.app.metrics != nil {
		o.app.metrics.Delivery.SetRecipientWorkerQueue(event.QueueDepth, event.QueueCapacity)
		o.app.metrics.Delivery.ObserveRecipientWorkerAdmission(string(event.Result), event.Duration)
	}
	if collector, ok := o.app.topProvider.(*topCollector); ok {
		collector.SetDeliveryRecipientQueue(int64(event.QueueDepth), int64(event.QueueCapacity))
		if event.Result != runtimedelivery.ObservationResultAccepted && event.Result != runtimedelivery.ObservationResultOK {
			collector.addCounter(topCounterDeliveryPushErr, 1)
		}
	}
}

func (o *onlineDeliveryObserver) ObservePlanTerminal(event runtimedelivery.PlanTerminalEvent) {
	if o == nil || o.app == nil {
		return
	}
	if o.app.metrics != nil {
		o.app.metrics.Delivery.ObserveRecipientWorkerProcess(string(event.Result), event.Recipients, event.Duration)
	}
	if collector, ok := o.app.topProvider.(*topCollector); ok && event.Result != runtimedelivery.ObservationResultOK {
		collector.addCounter(topCounterDeliveryPushErr, 1)
	}
	if event.Result != runtimedelivery.ObservationResultOK {
		o.app.deliveryLogger().Warn("online delivery plan incomplete",
			wklog.Event("internal.app.delivery.plan_incomplete"),
			wklog.Result(string(event.Result)),
			wklog.String("phase", string(event.Failure.Phase)),
			wklog.String("mode", onlineDeliveryModeLabel(event.Mode)),
			wklog.Int("recipients", event.Recipients),
			wklog.UID(event.Failure.RecipientUID),
			wklog.Uint64("ownerNodeID", event.Failure.OwnerNodeID),
			wklog.Error(event.Failure.Err),
		)
	}
}

func (o *onlineDeliveryObserver) SetRuntimePressure(event runtimedelivery.RuntimePressureEvent) {
	if o == nil || o.app == nil {
		return
	}
	if o.app.metrics != nil {
		o.app.metrics.Delivery.SetRecipientWorkerQueue(event.QueueDepth, event.QueueCapacity)
		o.app.metrics.Delivery.SetRecipientWorkerPressure(event.Inflight, event.Workers)
	}
	if collector, ok := o.app.topProvider.(*topCollector); ok {
		collector.SetDeliveryRecipientQueue(int64(event.QueueDepth), int64(event.QueueCapacity))
	}
}

func (o *onlineDeliveryObserver) ObserveOwnerPush(event runtimedelivery.OwnerPushEvent) {
	if o == nil || o.app == nil {
		return
	}
	if o.app.metrics != nil {
		o.app.metrics.Delivery.ObservePushRPC(deliveryNodeLabel(event.OwnerNodeID), string(event.Result), event.Duration, event.Routes)
	}
	if collector, ok := o.app.topProvider.(*topCollector); ok {
		collector.ObserveDeliveryPush(string(event.Result), event.Accepted, event.Duration)
		if event.Retryable > 0 || event.Dropped > 0 {
			collector.addCounter(topCounterDeliveryPushErr, uint64(event.Retryable+event.Dropped))
		}
	}
}

func (o *onlineDeliveryObserver) ObserveAck(event runtimedelivery.AckEvent) {
	if o == nil || o.app == nil {
		return
	}
	if o.app.metrics != nil {
		o.app.metrics.Delivery.SetAckBindings(event.PendingCount)
	}
	if collector, ok := o.app.topProvider.(*topCollector); ok {
		collector.SetDeliveryAckBindings(int64(event.PendingCount))
	}
}

func (o *onlineDeliveryObserver) ObserveAckBatch(event runtimedelivery.AckBatchEvent) {
	if o == nil || o.app == nil || o.app.metrics == nil {
		return
	}
	o.app.metrics.Delivery.ObserveAckBatch(
		event.Phase, event.Outcome, event.Items, event.Shards, event.Rejected, event.Rollback, event.Duration,
	)
}

func onlineDeliveryModeLabel(mode onlinedelivery.Mode) string {
	switch mode {
	case onlinedelivery.ModeDurable:
		return "durable"
	case onlinedelivery.ModeTransient:
		return "transient"
	default:
		return "invalid"
	}
}

type onlineDeliveryOfflineObserver struct {
	next channelappend.OfflineRecipientsObserver
}

func (o onlineDeliveryOfflineObserver) ObserveOfflineRecipients(ctx context.Context, event runtimedelivery.OfflineRecipientsEvent) {
	if o.next == nil {
		return
	}
	o.next.ObserveOfflineRecipients(ctx, channelappend.OfflineRecipientsEvent{Event: event.Event, UIDs: event.UIDs})
}
