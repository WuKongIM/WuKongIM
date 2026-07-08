package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerWebhookConfigReturnsStartupSnapshot(t *testing.T) {
	expected := WebhookConfigSnapshot{
		Enabled:                   true,
		HTTPAddr:                  "http://127.0.0.1:19090/webhook",
		FocusEvents:               []string{"msg.notify", "msg.offline"},
		SupportedEvents:           []string{"msg.notify", "msg.offline", "user.onlinestatus"},
		QueueSize:                 1024,
		Workers:                   16,
		MsgNotifyBatchMaxItems:    100,
		MsgNotifyBatchMaxWait:     "500ms",
		OnlineStatusBatchMaxItems: 512,
		OnlineStatusBatchMaxWait:  "2s",
		OfflineUIDBatchSize:       512,
		RequestTimeout:            "5s",
		RetryMaxAttempts:          3,
		Source:                    "startup_config",
		RequiresRestart:           true,
	}
	var called bool
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.webhook",
				Actions:  []string{"r"},
			}},
		}}),
		WebhookConfig: webhookConfigProviderFunc(func(ctx context.Context) (WebhookConfigSnapshot, error) {
			called = true
			if ctx == nil {
				t.Fatalf("WebhookConfigSnapshot() ctx = nil")
			}
			return expected, nil
		}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/webhooks/config", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !called {
		t.Fatalf("WebhookConfigSnapshot() was not called")
	}
	if !jsonEqual(rec.Body.String(), `{
		"enabled": true,
		"http_addr": "http://127.0.0.1:19090/webhook",
		"focus_events": ["msg.notify", "msg.offline"],
		"supported_events": ["msg.notify", "msg.offline", "user.onlinestatus"],
		"queue_size": 1024,
		"workers": 16,
		"msg_notify_batch_max_items": 100,
		"msg_notify_batch_max_wait": "500ms",
		"online_status_batch_max_items": 512,
		"online_status_batch_max_wait": "2s",
		"offline_uid_batch_size": 512,
		"request_timeout": "5s",
		"retry_max_attempts": 3,
		"source": "startup_config",
		"requires_restart": true
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(body) != 15 {
		t.Fatalf("field count = %d, want 15: %s", len(body), rec.Body.String())
	}
}

func TestManagerWebhookConfigUnavailableWithoutProvider(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts Options
		body string
	}{
		{
			name: "nil provider",
			body: `{"error":"service_unavailable","message":"webhook config provider is not configured"}`,
		},
		{
			name: "provider error",
			opts: Options{
				WebhookConfig: webhookConfigProviderFunc(func(context.Context) (WebhookConfigSnapshot, error) {
					return WebhookConfigSnapshot{}, errors.New("snapshot failed")
				}),
			},
			body: `{"error":"service_unavailable","message":"webhook config unavailable"}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(tt.opts)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/manager/webhooks/config", nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.body) {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}
}

func TestManagerWebhookConfigRequiresWebhookReadPermission(t *testing.T) {
	var called bool
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		WebhookConfig: webhookConfigProviderFunc(func(context.Context) (WebhookConfigSnapshot, error) {
			called = true
			return WebhookConfigSnapshot{}, nil
		}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/webhooks/config", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if called {
		t.Fatalf("WebhookConfigSnapshot() called without cluster.webhook:r")
	}
}

type webhookConfigProviderFunc func(context.Context) (WebhookConfigSnapshot, error)

func (f webhookConfigProviderFunc) WebhookConfigSnapshot(ctx context.Context) (WebhookConfigSnapshot, error) {
	return f(ctx)
}
