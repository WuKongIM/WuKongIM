package backup

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
)

func TestBackupStatusUsesManagerAndBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/manager/backups/status" || r.Header.Get("Authorization") != "Bearer operator-token" {
			t.Fatalf("request = %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"enabled":true,"health":"healthy","recovery_point_age_seconds":12}`))
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"status", "--server", server.URL, "--token", "operator-token"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !strings.Contains(stdout.String(), `"health": "healthy"`) || !strings.Contains(stdout.String(), `"recovery_point_age_seconds": 12`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestBackupTriggerRejectsUnknownKindBeforeManagerCall(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"trigger", "--server", "http://127.0.0.1:1", "--kind", "weekly"})
	err := cmd.Execute()
	var exit command.Exit
	if err == nil || !strings.Contains(err.Error(), "--kind") || !strings.Contains(err.Error(), "incremental") {
		t.Fatalf("Execute() error = %v", err)
	}
	_ = exit
}

func TestBackupListFollowsManagerCursors(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			if r.URL.Query().Get("cursor") != "" || r.URL.Query().Get("limit") != "200" {
				t.Fatalf("first query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"rp-2"}],"next_cursor":"next","total":2}`))
		case 2:
			if r.URL.Query().Get("cursor") != "next" || r.URL.Query().Get("limit") != "200" {
				t.Fatalf("second query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"rp-1"}],"total":2}`))
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"list", "--server", server.URL, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if requests != 2 || !strings.Contains(stdout.String(), `"id":"rp-2"`) || !strings.Contains(stdout.String(), `"id":"rp-1"`) {
		t.Fatalf("requests=%d stdout=%q", requests, stdout.String())
	}
}

func TestBackupRestorePlanUsesExplicitRecoveryEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/manager/restore/plan" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode(): %v", err)
		}
		if request["latest_verified"] != true || request["repository"] != "secondary" || request["invalidate_tokens"] != true {
			t.Fatalf("request = %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"plan-1","status":"planned"}`))
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"restore", "plan", "--server", server.URL, "--latest-verified", "--repository", "secondary", "--invalidate-tokens"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !strings.Contains(stdout.String(), `"status": "planned"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
