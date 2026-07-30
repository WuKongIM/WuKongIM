package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const defaultDeliveryRetryMaxAttempts = 3
const defaultDeliveryRetryBackoff = 10 * time.Millisecond

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
			wklog.Uint64("targetHashSlot", uint64(event.Failure.Target.HashSlot)),
			wklog.Uint64("targetSlotID", uint64(event.Failure.Target.SlotID)),
			wklog.Uint64("targetLeaderNodeID", event.Failure.Target.LeaderNodeID),
			wklog.Uint64("targetLeaderTerm", event.Failure.Target.LeaderTerm),
			wklog.Uint64("targetConfigEpoch", event.Failure.Target.ConfigEpoch),
			wklog.Uint64("targetRouteRevision", event.Failure.Target.RouteRevision),
			wklog.Uint64("targetAuthorityEpoch", event.Failure.Target.AuthorityEpoch),
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
	if event.Result != runtimedelivery.ObservationResultOK {
		o.app.deliveryLogger().Warn("online delivery owner push incomplete",
			wklog.Event("internal.app.delivery.owner_push_incomplete"),
			wklog.Uint64("ownerNodeID", event.OwnerNodeID),
			wklog.Result(string(event.Result)),
			wklog.Int("routes", event.Routes),
			wklog.Int("retryable", event.Retryable),
			wklog.Int("dropped", event.Dropped),
			wklog.UID(event.Failure.Route.UID),
			wklog.SessionID(event.Failure.Route.SessionID),
			wklog.Uint64("ownerBootID", event.Failure.Route.OwnerBootID),
			wklog.Uint64("ownerSeq", event.Failure.Route.OwnerSeq),
			wklog.Error(event.Failure.Err),
		)
	}
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
		event.Phase,
		event.Outcome,
		event.Items,
		event.Shards,
		event.Rejected,
		event.Rollback,
		event.Duration,
	)
}

type deliveryMessageObserver struct {
	// app records non-fatal delivery sink failures for tests and diagnostics.
	app *App
}

func (o deliveryMessageObserver) CommittedSinkError(_ message.SendCommand, err error) {
	if o.app != nil {
		o.app.recordDeliveryError(err)
		if err != nil {
			o.app.deliveryLogger().Warn("delivery committed sink failed",
				wklog.Event("internal.app.delivery.committed_sink_failed"),
				wklog.String("errorClass", runtimedelivery.DeliveryErrorClass(err)),
				wklog.Error(err),
			)
		}
	}
}

func (o deliveryMessageObserver) AppendFinished(path string, err error, dur time.Duration) {
	if o.app == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
		label := messageAppendErrorLabel(err)
		if o.app.metrics != nil {
			o.app.metrics.Message.ObserveAppendError(path, label)
		}
		if shouldLogMessageAppendError(label) {
			o.app.deliveryLogger().Error("message append failed",
				wklog.Event("internal.app.delivery.message_append_failed"),
				wklog.String("path", path),
				wklog.String("errorClass", label),
				wklog.Duration("duration", dur),
				wklog.Error(err),
			)
		}
	}
	if o.app.metrics != nil {
		o.app.metrics.Message.ObserveAppend(path, result, dur)
	}
	if collector, ok := o.app.topProvider.(*topCollector); ok {
		collector.ObserveMessageAppend(path, result, dur)
	}
}

func (o deliveryMessageObserver) ObserveChannelAppendRouter(event channelappend.RouterObservation) {
	if o.app == nil || o.app.metrics == nil {
		return
	}
	o.app.metrics.ChannelAppend.ObserveRouter(event.Path, event.Result, event.Items, event.Duration)
}

func (o deliveryMessageObserver) ObserveChannelAppendLocalAdmission(event channelappend.LocalAdmissionObservation) {
	if o.app == nil || o.app.metrics == nil {
		return
	}
	o.app.metrics.ChannelAppend.ObserveLocalAdmission(event.Result, event.Items)
}

func (o deliveryMessageObserver) SetChannelAppendWriterPressure(event channelappend.WriterPressureObservation) {
	if o.app == nil || o.app.metrics == nil {
		return
	}
	o.app.metrics.ChannelAppend.SetWriterPressure(
		event.AdmissionDepth,
		event.AdmissionCapacity,
		event.WorkerRunning,
		event.WorkerCapacity,
		event.PendingAppendItems,
		event.AppendInflightItems,
		event.PostCommitBacklog,
		event.PostCommitHandoffDepth,
		event.PostCommitHandoffCapacity,
		event.PostCommitRetryQueueDepth,
		event.PostCommitRetryContended,
	)
}

func (o deliveryMessageObserver) ObserveChannelAppendEffectPool(event channelappend.EffectPoolObservation) {
	if o.app == nil || o.app.metrics == nil {
		return
	}
	o.app.metrics.ChannelAppend.ObserveEffectPool(
		event.Stage,
		event.Result,
		event.Inflight,
		event.Capacity,
		event.Saturated,
	)
}

func (o deliveryMessageObserver) ObserveChannelAppendAntsPool(event channelappend.AntsPoolObservation) {
	if o.app == nil || o.app.metrics == nil {
		return
	}
	o.app.metrics.AntsPool.SetUsage("channelappend", event.Pool, event.Running, event.Capacity, event.Waiting)
}

func (o deliveryMessageObserver) ObserveChannelAppendEffect(event channelappend.EffectObservation) {
	if o.app == nil || o.app.metrics == nil {
		return
	}
	o.app.metrics.ChannelAppend.ObserveEffect(event.Stage, event.Result, event.Items, event.Duration)
}

func (o deliveryMessageObserver) ObserveChannelAppendPostCommitFailure(event channelappend.PostCommitFailureObservation) {
	if o.app == nil {
		return
	}
	fields := channelAppendPostCommitFailureFields(event)
	if isExpectedPostCommitRouteFailure(event.Err) {
		o.app.deliveryLogger().Warn("channelappend post-commit route failure",
			fields...,
		)
		return
	}
	o.app.deliveryLogger().Error("channelappend post-commit failed",
		fields...,
	)
}

func channelAppendPostCommitFailureFields(event channelappend.PostCommitFailureObservation) []wklog.Field {
	return []wklog.Field{
		wklog.Event("internal.app.channelappend.post_commit_failed"),
		wklog.ChannelID(event.ChannelID),
		wklog.ChannelType(int64(event.ChannelType)),
		wklog.Uint64("messageID", event.MessageID),
		wklog.MessageSeq(event.MessageSeq),
		wklog.Int("attempt", event.Attempt),
		wklog.String("result", event.Result),
		wklog.String("phase", event.Phase),
		wklog.UID(event.UID),
		wklog.Int("uidCount", event.UIDCount),
		wklog.Int("recipientCount", event.RecipientCount),
		wklog.Uint64("targetHashSlot", uint64(event.TargetHashSlot)),
		wklog.Uint64("targetSlotID", uint64(event.TargetSlotID)),
		wklog.Uint64("targetLeaderNodeID", event.TargetLeaderNodeID),
		wklog.Uint64("targetRouteRevision", event.TargetRouteRevision),
		wklog.Uint64("targetAuthorityEpoch", event.TargetAuthorityEpoch),
		wklog.Int("dispatchTargetCount", event.DispatchTargetCount),
		wklog.Int("dispatchBatchSize", event.DispatchBatchSize),
		wklog.Uint64("dispatchOwnerNodeID", event.DispatchOwnerNodeID),
		wklog.Int("dispatchOwnerRouteNum", event.DispatchOwnerRouteNum),
		wklog.Error(event.Err),
	}
}

func isExpectedPostCommitRouteFailure(err error) bool {
	return errors.Is(err, conversationusecase.ErrStaleRoute) ||
		errors.Is(err, conversationusecase.ErrNotLeader) ||
		errors.Is(err, conversationusecase.ErrRouteNotReady) ||
		errors.Is(err, channelappend.ErrStaleRoute) ||
		errors.Is(err, channelappend.ErrNotLeader) ||
		errors.Is(err, channelappend.ErrRouteNotReady)
}

func appendFailureLogLine(path string, err error) string {
	return fmt.Sprintf("internal/app: message append failed path=%s err=%v", path, err)
}

func shouldLogMessageAppendError(label string) bool {
	return label == "append_failed" || label == "timeout"
}

func (a *App) deliveryLogger() wklog.Logger {
	if a == nil || a.logger == nil {
		return wklog.NewNop()
	}
	return a.logger.Named("delivery")
}

func (a *App) recordDeliveryError(err error) {
	if a == nil {
		return
	}
	a.deliveryErrors.Add(1)
	if a.metrics != nil {
		if class := runtimedelivery.DeliveryErrorClass(err); class != runtimedelivery.DeliveryErrorClassNone {
			a.metrics.Delivery.ObserveError(class)
		}
	}
}
