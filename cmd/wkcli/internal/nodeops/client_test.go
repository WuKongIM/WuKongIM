package nodeops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientActivateNodePostsManagerRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/manager/nodes/4/activate" {
			t.Fatalf("path = %s, want /manager/nodes/4/activate", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("Authorization = %q, want Bearer secret", got)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"changed":true,"node_id":4,"addr":"127.0.0.1:11114","join_state":"active","revision":20}`))
	}))
	defer server.Close()

	client := NewClient(Config{Server: server.URL, Token: " secret "})
	got, err := client.ActivateNode(context.Background(), 4)
	if err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if !got.Changed || got.JoinState != "active" || got.Revision != 20 {
		t.Fatalf("ActivateNode() = %+v, want changed active revision 20", got)
	}
}

func TestClientTrimsServerWhitespaceAndTrailingSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manager/nodes" {
			t.Fatalf("path = %s, want /manager/nodes", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient(Config{Server: " " + server.URL + "/ "})
	var got struct {
		OK bool `json:"ok"`
	}
	if err := client.ListNodes(context.Background(), &got); err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if !got.OK {
		t.Fatalf("ListNodes() ok = false, want true")
	}
}

func TestClientRejectsOversizedSuccessResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		run  func(*Client) error
	}{
		{
			name: "json decode",
			body: `{"data":"` + strings.Repeat("x", 1<<20) + `"}`,
			run: func(client *Client) error {
				var out map[string]string
				return client.ListNodes(context.Background(), &out)
			},
		},
		{
			name: "discard",
			body: strings.Repeat("x", 1<<20+1),
			run: func(client *Client) error {
				return client.ListNodes(context.Background(), nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := NewClient(Config{Server: server.URL})
			err := tt.run(client)
			if err == nil {
				t.Fatal("request error = nil, want oversized body error")
			}
			if !strings.Contains(err.Error(), "manager response body exceeds 1MiB") {
				t.Fatalf("request error = %v, want oversized body error", err)
			}
		})
	}
}

func TestClientJSONBodyRoutesUseManagerBasePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		body     string
		callFunc func(context.Context, *Client, any) error
	}{
		{
			name: "onboarding plan",
			path: "/admin/manager/nodes/4/onboarding/plan",
			body: `{"max_slot_moves":7}`,
			callFunc: func(ctx context.Context, client *Client, out any) error {
				return client.OnboardingPlan(ctx, 4, 7, out)
			},
		},
		{
			name: "onboarding start",
			path: "/admin/manager/nodes/4/onboarding/start",
			body: `{"max_slot_moves":7}`,
			callFunc: func(ctx context.Context, client *Client, out any) error {
				return client.OnboardingStart(ctx, 4, 7, out)
			},
		},
		{
			name: "onboarding advance",
			path: "/admin/manager/nodes/4/onboarding/advance",
			body: `{"max_slot_moves":7}`,
			callFunc: func(ctx context.Context, client *Client, out any) error {
				return client.OnboardingAdvance(ctx, 4, 7, out)
			},
		},
		{
			name: "scale-in plan",
			path: "/admin/manager/nodes/4/scale-in/plan",
			body: `{"max_slot_moves":7}`,
			callFunc: func(ctx context.Context, client *Client, out any) error {
				return client.ScaleInPlan(ctx, 4, 7, out)
			},
		},
		{
			name: "scale-in advance",
			path: "/admin/manager/nodes/4/scale-in/advance",
			body: `{"max_slot_moves":7}`,
			callFunc: func(ctx context.Context, client *Client, out any) error {
				return client.ScaleInAdvance(ctx, 4, 7, out)
			},
		},
		{
			name: "scale-in drain",
			path: "/admin/manager/nodes/4/scale-in/drain",
			body: `{"draining":true}`,
			callFunc: func(ctx context.Context, client *Client, out any) error {
				return client.SetScaleInDrain(ctx, 4, true, out)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Fatalf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != tt.path {
					t.Fatalf("path = %s, want %s", r.URL.Path, tt.path)
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Fatalf("Content-Type = %q, want application/json", got)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer secret" {
					t.Fatalf("Authorization = %q, want Bearer secret", got)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read request body: %v", err)
				}
				if got := strings.TrimSpace(string(body)); got != tt.body {
					t.Fatalf("body = %s, want %s", got, tt.body)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()

			client := NewClient(Config{Server: server.URL + "/admin/", Token: "secret"})
			var out struct {
				OK bool `json:"ok"`
			}
			if err := tt.callFunc(context.Background(), client, &out); err != nil {
				t.Fatalf("%s error = %v", tt.name, err)
			}
			if !out.OK {
				t.Fatalf("%s ok = false, want true", tt.name)
			}
		})
	}
}

func TestClientScaleInStatusPreservesBlockers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/manager/nodes/4/scale-in/status" {
			t.Fatalf("path = %s, want /manager/nodes/4/scale-in/status", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"node_id":4,
			"join_state":"leaving",
			"generated_at":"2026-06-30T10:11:12Z",
			"state_revision":88,
			"safe_to_proceed":false,
			"safe_to_remove":false,
			"blocked_by_missing_node":true,
			"blocked_by_join_state":true,
			"blocked_by_health":true,
			"blocked_by_controller_role":true,
			"blocked_by_data_role":true,
			"blocked_by_runtime_drain":true,
			"health_report_age_ms":1234,
			"health_report_ttl_ms":5000,
			"blocked_reasons":["target_health_stale","gateway_sessions_present"],
			"observed_control_revision":80,
			"required_control_revision":88,
			"gateway_sessions":2
		}`))
	}))
	defer server.Close()

	client := NewClient(Config{Server: server.URL})
	got, err := client.ScaleInStatus(context.Background(), 4)
	if err != nil {
		t.Fatalf("ScaleInStatus() error = %v", err)
	}
	if got.GeneratedAt != "2026-06-30T10:11:12Z" {
		t.Fatalf("ScaleInStatus() generated_at = %q, want 2026-06-30T10:11:12Z", got.GeneratedAt)
	}
	if !got.BlockedByMissingNode || !got.BlockedByJoinState || !got.BlockedByControllerRole || !got.BlockedByDataRole {
		t.Fatalf("ScaleInStatus() manager blockers = missing:%t join:%t controller:%t data:%t, want all true",
			got.BlockedByMissingNode, got.BlockedByJoinState, got.BlockedByControllerRole, got.BlockedByDataRole)
	}
	if !got.BlockedByHealth || !got.BlockedByRuntimeDrain {
		t.Fatalf("ScaleInStatus() blockers = health:%t runtime_drain:%t, want both true", got.BlockedByHealth, got.BlockedByRuntimeDrain)
	}
	if got.HealthReportAgeMS != 1234 || got.HealthReportTTLMS != 5000 {
		t.Fatalf("ScaleInStatus() health report timing = age:%d ttl:%d, want 1234/5000", got.HealthReportAgeMS, got.HealthReportTTLMS)
	}
	if got.ObservedControlRevision != 80 || got.RequiredControlRevision != 88 {
		t.Fatalf("ScaleInStatus() revisions = observed:%d required:%d, want 80/88", got.ObservedControlRevision, got.RequiredControlRevision)
	}
	if got.GatewaySessions != 2 {
		t.Fatalf("ScaleInStatus() gateway_sessions = %d, want 2", got.GatewaySessions)
	}
	if len(got.BlockedReasons) != 2 || got.BlockedReasons[0] != "target_health_stale" || got.BlockedReasons[1] != "gateway_sessions_present" {
		t.Fatalf("ScaleInStatus() blocked_reasons = %#v, want target_health_stale/gateway_sessions_present", got.BlockedReasons)
	}
}

func TestClientDynamicNodeDiagnosticsCallsManagerRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/manager/nodes/4/diagnostics" {
			t.Fatalf("path = %s, want /manager/nodes/4/diagnostics", r.URL.Path)
		}
		if r.URL.RawQuery != "audit_limit=10&slot_limit=256&task_limit=20" {
			t.Fatalf("query = %q, want audit_limit=10&slot_limit=256&task_limit=20", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"node_id":4}`))
	}))
	defer server.Close()

	client := NewClient(Config{Server: server.URL})
	var out map[string]any
	err := client.DynamicNodeDiagnostics(context.Background(), 4, DiagnosticRequest{
		TaskLimit:  20,
		AuditLimit: 10,
		SlotLimit:  256,
	}, &out)
	if err != nil {
		t.Fatalf("DynamicNodeDiagnostics() error = %v", err)
	}
	if got := out["node_id"]; fmt.Sprint(got) != "4" {
		t.Fatalf("DynamicNodeDiagnostics() node_id = %#v, want 4", got)
	}
}

func TestClientErrorIncludesHTTPStatusAndBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/manager/nodes/4/scale-in/remove" {
			t.Fatalf("path = %s, want /manager/nodes/4/scale-in/remove", r.URL.Path)
		}
		http.Error(w, `{"error":"blocked_by_channels"}`, http.StatusConflict)
	}))
	defer server.Close()

	client := NewClient(Config{Server: server.URL})
	_, err := client.RemoveScaleInNode(context.Background(), 4)
	if err == nil {
		t.Fatal("RemoveScaleInNode() error = nil, want APIError")
	}
	var apiErr *APIError
	if !AsAPIError(err, &apiErr) {
		t.Fatalf("RemoveScaleInNode() error = %T %[1]v, want APIError", err)
	}
	if apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("APIError.StatusCode = %d, want 409", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Body, "blocked_by_channels") {
		t.Fatalf("APIError.Body = %q, want blocked_by_channels", apiErr.Body)
	}
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(%T) = false, want true", err)
	}
}
