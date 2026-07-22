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
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const defaultDeliveryRetryMaxAttempts = 3
const defaultDeliveryRetryBackoff = 10 * time.Millisecond

type deliveryRuntimeAdapter struct {
	// manager handles committed-message fanout and ack mutations.
	manager *runtimedelivery.Manager
}

type deliveryWorkerGroup []WorkerRuntime

func appendDeliveryWorker(current WorkerRuntime, worker WorkerRuntime) WorkerRuntime {
	if worker == nil {
		return current
	}
	group, ok := current.(deliveryWorkerGroup)
	if !ok {
		if current == nil {
			return deliveryWorkerGroup{worker}
		}
		group = deliveryWorkerGroup{current}
	}
	for _, existing := range group {
		if existing == worker {
			return group
		}
	}
	return append(group, worker)
}

func (g deliveryWorkerGroup) Start(ctx context.Context) error {
	for idx, worker := range g {
		if worker == nil {
			continue
		}
		if err := worker.Start(ctx); err != nil {
			for i := idx - 1; i >= 0; i-- {
				if g[i] != nil {
					_ = g[i].Stop(ctx)
				}
			}
			return err
		}
	}
	return nil
}

// Stop preserves reverse dependency order; earlier workers stay running if a later worker cannot drain.
func (g deliveryWorkerGroup) Stop(ctx context.Context) error {
	for i := len(g) - 1; i >= 0; i-- {
		if g[i] == nil {
			continue
		}
		if stopErr := g[i].Stop(ctx); stopErr != nil {
			return stopErr
		}
	}
	return nil
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

func (a deliveryRuntimeAdapter) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if a.manager == nil {
		return nil
	}
	event = scopePersonDeliveryEvent(event)
	return a.manager.SubmitCommitted(ctx, event)
}

// scopePersonDeliveryEvent narrows person-channel fanout to the two channel participants.
func scopePersonDeliveryEvent(event messageevents.MessageCommitted) messageevents.MessageCommitted {
	if event.ChannelType != frame.ChannelTypePerson || len(event.MessageScopedUIDs) > 0 {
		return event
	}
	left, right, err := runtimechannelid.DecodePersonChannel(event.ChannelID)
	if err != nil {
		return event
	}
	if left != "" {
		event.MessageScopedUIDs = append(event.MessageScopedUIDs, left)
	}
	if right != "" && right != left {
		event.MessageScopedUIDs = append(event.MessageScopedUIDs, right)
	}
	return event
}

func (a deliveryRuntimeAdapter) Recvack(ctx context.Context, cmd deliveryusecase.RecvackCommand) error {
	if a.manager == nil {
		return nil
	}
	return a.manager.Recvack(ctx, runtimedelivery.Recvack{
		UID:        cmd.UID,
		SessionID:  cmd.SessionID,
		MessageID:  cmd.MessageID,
		MessageSeq: cmd.MessageSeq,
	})
}

func (a deliveryRuntimeAdapter) SessionClosed(ctx context.Context, cmd deliveryusecase.SessionClosedCommand) error {
	if a.manager == nil {
		return nil
	}
	return a.manager.SessionClosed(ctx, runtimedelivery.SessionClosed{UID: cmd.UID, SessionID: cmd.SessionID})
}

type appSubscriberPlanner struct {
	// channel scans durable subscribers for non-person channel fanout.
	channel runtimedelivery.SubscriberPlanner
}

func (p appSubscriberPlanner) NextPartitionPage(ctx context.Context, task runtimedelivery.FanoutTask, cursor string, limit int) (runtimedelivery.UIDPage, error) {
	if task.Envelope.ChannelType != frame.ChannelTypePerson {
		if p.channel == nil {
			return runtimedelivery.UIDPage{Done: true}, nil
		}
		return p.channel.NextPartitionPage(ctx, task, cursor, limit)
	}
	if cursor != "" {
		return runtimedelivery.UIDPage{Done: true}, nil
	}
	left, right, err := runtimechannelid.DecodePersonChannel(task.Envelope.ChannelID)
	if err != nil {
		return runtimedelivery.UIDPage{Done: true}, nil
	}
	uids := make([]string, 0, 2)
	if left != "" {
		uids = append(uids, left)
	}
	if right != "" && right != left {
		uids = append(uids, right)
	}
	return runtimedelivery.UIDPage{UIDs: uids, Done: true}, nil
}

type noopSubscriberPlanner struct{}

func (noopSubscriberPlanner) NextPartitionPage(context.Context, runtimedelivery.FanoutTask, string, int) (runtimedelivery.UIDPage, error) {
	return runtimedelivery.UIDPage{Done: true}, nil
}

type presenceResolverAdapter struct {
	// presence resolves authoritative routes for selected UIDs.
	presence *presence.App
}

func (r presenceResolverAdapter) EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]runtimedelivery.Route, error) {
	out := make(map[string][]runtimedelivery.Route, len(uids))
	if r.presence == nil {
		return out, nil
	}
	routesByUID, err := r.presence.EndpointsByUIDs(ctx, uids)
	if err != nil {
		return nil, err
	}
	for uid, routes := range routesByUID {
		for _, route := range routes {
			out[uid] = append(out[uid], runtimedelivery.Route{
				UID:         route.UID,
				OwnerNodeID: route.OwnerNodeID,
				OwnerBootID: route.OwnerBootID,
				OwnerSeq:    route.OwnerSeq,
				SessionID:   route.SessionID,
				DeviceID:    route.DeviceID,
				DeviceFlag:  route.DeviceFlag,
				DeviceLevel: route.DeviceLevel,
			})
		}
	}
	return out, nil
}
