package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const defaultDeliveryRetryMaxAttempts = 3
const defaultDeliveryRetryBackoff = 10 * time.Millisecond

var errOnlineDeliveryCommittedSubmitUnsupported = errors.New("internal/app: committed delivery submission requires a canonical recipient plan")

type onlineDeliveryUsecaseAdapter struct {
	// runtime owns recipient feedback after channelappend produces canonical plans.
	runtime *runtimedelivery.Runtime
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

func (o deliveryMessageObserver) SetChannelAppendRecipientDeliveryQueue(event channelappend.RecipientDeliveryQueueObservation) {
	if o.app == nil {
		return
	}
	if o.app.metrics != nil {
		o.app.metrics.Delivery.SetRecipientWorkerQueue(event.QueueDepth, event.QueueCapacity)
	}
	if collector, ok := o.app.topProvider.(*topCollector); ok {
		collector.SetDeliveryRecipientQueue(int64(event.QueueDepth), int64(event.QueueCapacity))
	}
}

func (o deliveryMessageObserver) SetChannelAppendRecipientDeliveryWorkerPressure(event channelappend.RecipientDeliveryWorkerPressureObservation) {
	if o.app == nil || o.app.metrics == nil {
		return
	}
	o.app.metrics.Delivery.SetRecipientWorkerPressure(event.Inflight, event.Capacity)
}

func (o deliveryMessageObserver) ObserveChannelAppendRecipientDeliveryAdmission(event channelappend.RecipientDeliveryAdmissionObservation) {
	if o.app == nil {
		return
	}
	if o.app.metrics != nil {
		o.app.metrics.Delivery.ObserveRecipientWorkerAdmission(event.Result, event.Duration)
	}
	if collector, ok := o.app.topProvider.(*topCollector); ok {
		if event.Result != "accepted" && event.Result != "ok" {
			collector.addCounter(topCounterDeliveryPushErr, 1)
		}
	}
}

func (o deliveryMessageObserver) ObserveChannelAppendRecipientDeliveryProcess(event channelappend.RecipientDeliveryProcessObservation) {
	if o.app == nil {
		return
	}
	if o.app.metrics != nil {
		o.app.metrics.Delivery.ObserveRecipientWorkerProcess(event.Result, event.Recipients, event.Duration)
	}
	if collector, ok := o.app.topProvider.(*topCollector); ok && event.Result != "ok" {
		collector.addCounter(topCounterDeliveryPushErr, 1)
	}
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

// SubmitCommitted is retained only for the temporary delivery-usecase facade.
// Channelappend is the sole production producer of canonical delivery plans.
func (a onlineDeliveryUsecaseAdapter) SubmitCommitted(context.Context, messageevents.MessageCommitted) error {
	return errOnlineDeliveryCommittedSubmitUnsupported
}

func (a onlineDeliveryUsecaseAdapter) Recvack(ctx context.Context, cmd deliveryusecase.RecvackCommand) error {
	if a.runtime == nil {
		return nil
	}
	return a.runtime.Recvack(ctx, runtimedelivery.Recvack{
		UID: cmd.UID, SessionID: cmd.SessionID, MessageID: cmd.MessageID, MessageSeq: cmd.MessageSeq,
	})
}

func (a onlineDeliveryUsecaseAdapter) SessionClosed(ctx context.Context, cmd deliveryusecase.SessionClosedCommand) error {
	if a.runtime == nil {
		return nil
	}
	return a.runtime.SessionClosed(ctx, runtimedelivery.SessionClosed{UID: cmd.UID, SessionID: cmd.SessionID})
}
