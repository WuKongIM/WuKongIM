package manager

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// WebhookConfigProvider returns the current process webhook startup configuration.
type WebhookConfigProvider interface {
	// WebhookConfigSnapshot returns a read-only snapshot of the effective webhook configuration.
	WebhookConfigSnapshot(context.Context) (WebhookConfigSnapshot, error)
}

// WebhookConfigSnapshot is the manager JSON body for read-only webhook configuration.
type WebhookConfigSnapshot struct {
	// Enabled reports whether the node-local webhook runtime is enabled.
	Enabled bool `json:"enabled"`
	// HTTPAddr is the configured webhook callback endpoint.
	HTTPAddr string `json:"http_addr"`
	// FocusEvents lists the configured event filter; an empty list means all supported events.
	FocusEvents []string `json:"focus_events"`
	// SupportedEvents lists every webhook event supported by this process.
	SupportedEvents []string `json:"supported_events"`
	// QueueSize bounds accepted webhook events waiting in memory per event queue.
	QueueSize int `json:"queue_size"`
	// Workers bounds concurrent webhook sender calls per event queue.
	Workers int `json:"workers"`
	// MsgNotifyBatchMaxItems limits msg.notify messages sent in one webhook request.
	MsgNotifyBatchMaxItems int `json:"msg_notify_batch_max_items"`
	// MsgNotifyBatchMaxWait is the formatted max wait before a partial msg.notify batch is sent.
	MsgNotifyBatchMaxWait string `json:"msg_notify_batch_max_wait"`
	// OnlineStatusBatchMaxItems limits user.onlinestatus records sent in one webhook request.
	OnlineStatusBatchMaxItems int `json:"online_status_batch_max_items"`
	// OnlineStatusBatchMaxWait is the formatted max wait before a partial user.onlinestatus batch is sent.
	OnlineStatusBatchMaxWait string `json:"online_status_batch_max_wait"`
	// OfflineUIDBatchSize limits offline recipient UIDs sent in one msg.offline request.
	OfflineUIDBatchSize int `json:"offline_uid_batch_size"`
	// RequestTimeout is the formatted timeout for one outbound webhook request attempt.
	RequestTimeout string `json:"request_timeout"`
	// RetryMaxAttempts bounds attempts for one admitted webhook batch before it is dropped.
	RetryMaxAttempts int `json:"retry_max_attempts"`
	// Source identifies where this snapshot was derived from.
	Source string `json:"source"`
	// RequiresRestart reports whether changing this configuration requires a process restart.
	RequiresRestart bool `json:"requires_restart"`
}

func (s *Server) handleWebhookConfig(c *gin.Context) {
	if s == nil || s.webhookConfig == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "webhook config provider is not configured")
		return
	}
	snapshot, err := s.webhookConfig.WebhookConfigSnapshot(c.Request.Context())
	if err != nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "webhook config unavailable")
		return
	}
	c.JSON(http.StatusOK, snapshot)
}
