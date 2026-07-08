package app

import (
	"context"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	runtimewebhook "github.com/WuKongIM/WuKongIM/internal/runtime/webhook"
)

const webhookConfigSnapshotSource = "startup_config"

// WebhookConfigSnapshot returns a read-only view of the normalized startup webhook configuration.
func (a *App) WebhookConfigSnapshot(context.Context) (accessmanager.WebhookConfigSnapshot, error) {
	cfg := a.cfg.Webhook
	return accessmanager.WebhookConfigSnapshot{
		Enabled:                   cfg.Enabled,
		HTTPAddr:                  cfg.HTTPAddr,
		FocusEvents:               cloneWebhookConfigStrings(cfg.FocusEvents),
		SupportedEvents:           supportedWebhookEvents(),
		QueueSize:                 cfg.QueueSize,
		Workers:                   cfg.Workers,
		MsgNotifyBatchMaxItems:    cfg.NotifyBatchMaxItems,
		MsgNotifyBatchMaxWait:     cfg.NotifyBatchMaxWait.String(),
		OnlineStatusBatchMaxItems: cfg.OnlineBatchMaxItems,
		OnlineStatusBatchMaxWait:  cfg.OnlineBatchMaxWait.String(),
		OfflineUIDBatchSize:       cfg.OfflineUIDBatchSize,
		RequestTimeout:            cfg.RequestTimeout.String(),
		RetryMaxAttempts:          cfg.RetryMaxAttempts,
		Source:                    webhookConfigSnapshotSource,
		RequiresRestart:           true,
	}, nil
}

func supportedWebhookEvents() []string {
	return []string{
		runtimewebhook.EventMsgNotify,
		runtimewebhook.EventMsgOffline,
		runtimewebhook.EventUserOnlineStatus,
	}
}

func cloneWebhookConfigStrings(values []string) []string {
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}
