package app

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	runtimewebhook "github.com/WuKongIM/WuKongIM/internalv2/runtime/webhook"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
)

type webhookEventRuntime interface {
	Notify(context.Context, runtimewebhook.Message)
	Offline(context.Context, runtimewebhook.OfflineMessage)
	OnlineStatus(context.Context, runtimewebhook.OnlineStatus)
}

type webhookNotifyEnqueuer struct {
	runtime webhookEventRuntime
}

type webhookOfflineObserver struct {
	runtime      webhookEventRuntime
	uidBatchSize int
}

type webhookPresenceObserver struct {
	runtime webhookEventRuntime
}

type composedPersistAfterEnqueuer struct {
	left  channelappend.PersistAfterEnqueuer
	right channelappend.PersistAfterEnqueuer
}

type composedOfflineRecipientsObserver struct {
	pluginSingle channelappend.OfflineRecipientObserver
	webhookBatch channelappend.OfflineRecipientsObserver
}

func (a *App) wireWebhook() error {
	if !a.cfg.Webhook.Enabled || a.webhook != nil {
		return nil
	}
	sender := runtimewebhook.NewHTTPSender(runtimewebhook.HTTPSenderOptions{
		Addr:    a.cfg.Webhook.HTTPAddr,
		Timeout: a.cfg.Webhook.RequestTimeout,
	})
	runtime, err := runtimewebhook.New(runtimewebhook.RuntimeOptions{
		Sender:              sender,
		QueueSize:           a.cfg.Webhook.QueueSize,
		Workers:             a.cfg.Webhook.Workers,
		NotifyBatchMaxItems: a.cfg.Webhook.NotifyBatchMaxItems,
		NotifyBatchMaxWait:  a.cfg.Webhook.NotifyBatchMaxWait,
		OnlineBatchMaxItems: a.cfg.Webhook.OnlineBatchMaxItems,
		OnlineBatchMaxWait:  a.cfg.Webhook.OnlineBatchMaxWait,
		OfflineUIDBatchSize: a.cfg.Webhook.OfflineUIDBatchSize,
		RequestTimeout:      a.cfg.Webhook.RequestTimeout,
		RetryMaxAttempts:    a.cfg.Webhook.RetryMaxAttempts,
		FocusEvents:         a.cfg.Webhook.FocusEvents,
	})
	if err != nil {
		return fmt.Errorf("internalv2/app: create webhook runtime: %w", err)
	}
	a.webhook = runtime
	a.webhookNotify = webhookNotifyEnqueuer{runtime: runtime}
	a.webhookOffline = webhookOfflineObserver{runtime: runtime, uidBatchSize: a.cfg.Webhook.OfflineUIDBatchSize}
	a.webhookPresence = webhookPresenceObserver{runtime: runtime}
	return nil
}

func (e webhookNotifyEnqueuer) EnqueuePersistAfter(ctx context.Context, event channelappend.CommittedEnvelope) {
	if e.runtime == nil {
		return
	}
	e.runtime.Notify(ctx, webhookMessageFromCommitted(event))
}

func (o webhookOfflineObserver) ObserveOfflineRecipients(ctx context.Context, event channelappend.OfflineRecipientsEvent) {
	if o.runtime == nil || len(event.UIDs) == 0 {
		return
	}
	batchSize := o.uidBatchSize
	if batchSize <= 0 || batchSize > len(event.UIDs) {
		batchSize = len(event.UIDs)
	}
	message := webhookMessageFromCommitted(event.Event)
	for start := 0; start < len(event.UIDs); start += batchSize {
		end := start + batchSize
		if end > len(event.UIDs) {
			end = len(event.UIDs)
		}
		o.runtime.Offline(ctx, runtimewebhook.OfflineMessage{
			Message: message,
			ToUIDs:  append([]string(nil), event.UIDs[start:end]...),
		})
	}
}

func (o webhookOfflineObserver) ObserveOfflineRecipient(ctx context.Context, event channelappend.OfflineRecipientEvent) {
	if event.UID == "" {
		return
	}
	o.ObserveOfflineRecipients(ctx, channelappend.OfflineRecipientsEvent{
		Event: event.Event,
		UIDs:  []string{event.UID},
	})
}

func (o webhookPresenceObserver) ObserveOnlineStatus(ctx context.Context, event presence.OnlineStatusEvent) error {
	if o.runtime == nil {
		return nil
	}
	o.runtime.OnlineStatus(ctx, runtimewebhook.OnlineStatus{Value: event.Value})
	return nil
}

func composePersistAfterEnqueuers(left, right channelappend.PersistAfterEnqueuer) channelappend.PersistAfterEnqueuer {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return composedPersistAfterEnqueuer{left: left, right: right}
}

func (e composedPersistAfterEnqueuer) EnqueuePersistAfter(ctx context.Context, event channelappend.CommittedEnvelope) {
	e.left.EnqueuePersistAfter(ctx, event)
	e.right.EnqueuePersistAfter(ctx, event)
}

func composeOfflineRecipientObservers(
	pluginSingle channelappend.OfflineRecipientObserver,
	webhookBatch channelappend.OfflineRecipientsObserver,
) (channelappend.OfflineRecipientObserver, channelappend.OfflineRecipientsObserver) {
	if pluginSingle == nil || webhookBatch == nil {
		return pluginSingle, webhookBatch
	}
	return pluginSingle, composedOfflineRecipientsObserver{pluginSingle: pluginSingle, webhookBatch: webhookBatch}
}

func (o composedOfflineRecipientsObserver) ObserveOfflineRecipients(ctx context.Context, event channelappend.OfflineRecipientsEvent) {
	o.webhookBatch.ObserveOfflineRecipients(ctx, event)
	for _, uid := range event.UIDs {
		o.pluginSingle.ObserveOfflineRecipient(ctx, channelappend.OfflineRecipientEvent{
			Event: event.Event,
			UID:   uid,
		})
	}
}

func webhookMessageFromCommitted(event channelappend.CommittedEnvelope) runtimewebhook.Message {
	return runtimewebhook.Message{
		MessageID:         event.MessageID,
		MessageSeq:        event.MessageSeq,
		ChannelID:         event.ChannelID,
		ChannelType:       event.ChannelType,
		FromUID:           event.FromUID,
		ClientMsgNo:       event.ClientMsgNo,
		ServerTimestampMS: event.ServerTimestampMS,
		Payload:           append([]byte(nil), event.Payload...),
		RedDot:            event.RedDot,
		SyncOnce:          event.SyncOnce,
	}
}
