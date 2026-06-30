package nodeops

import (
	"context"
	"errors"
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
			"state_revision":88,
			"safe_to_proceed":false,
			"safe_to_remove":false,
			"blocked_by_health":true,
			"blocked_by_runtime_drain":true,
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
	if !got.BlockedByHealth || !got.BlockedByRuntimeDrain {
		t.Fatalf("ScaleInStatus() blockers = health:%t runtime_drain:%t, want both true", got.BlockedByHealth, got.BlockedByRuntimeDrain)
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
