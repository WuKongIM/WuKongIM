package backup

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
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

func TestBackupFenceSourceBindsExactSuccessor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost ||
				r.URL.Path != "/manager/backups/source-fence" {
				t.Fatalf("request = %s %s", r.Method, r.URL.Path)
			}
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode(): %v", err)
			}
			want := map[string]string{
				"restore_plan_id": "plan-1",
				"restore_point_id": "checkpoint-1",
				"target_cluster_id": "target-cluster",
				"target_generation": "target-generation-1",
			}
			for key, value := range want {
				if request[key] != value {
					t.Fatalf("request = %#v", request)
				}
			}
			_, _ = w.Write([]byte(`{"id":"source-fence-1"}`))
		},
	))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{
		"fence-source", "--server", server.URL,
		"--restore-plan", "plan-1",
		"--restore-point", "checkpoint-1",
		"--target-cluster", "target-cluster",
		"--target-generation", "target-generation-1",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !strings.Contains(stdout.String(), `"id": "source-fence-1"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestBackupRestoreActivateUsesReceiptFileAndRejectsAmbiguousEvidence(
	t *testing.T,
) {
	receipt := backupartifact.SourceFenceReceipt{
		SourceFenceRecord: backupartifact.SourceFenceRecord{
			Format: backupartifact.SourceFenceReceiptFormat,
			Version: backupartifact.SourceFenceReceiptVersion,
			ID: "source-fence-1", SourceClusterID: "source",
			SourceGeneration: "source-generation-1",
			RestorePlanID: "plan-1", RestorePointID: "checkpoint-1",
			ManifestSHA256: strings.Repeat("a", 64),
			TargetClusterID: "target",
			TargetGeneration: "target-generation-1",
			FenceControllerRevision: 9,
			RequestedAtUnixMillis: 1_800_000_000_000,
			ConvergedAtUnixMillis: 1_800_000_001_000,
		},
		Signature: &backupartifact.ManifestSignature{
			Algorithm: "ed25519", KeyID: "source-key", Value: []byte("signature"),
		},
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source-fence.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost ||
				r.URL.Path != "/manager/restore/plan-1/activate" {
				t.Fatalf("request = %s %s", r.Method, r.URL.Path)
			}
			var request struct {
				Receipt backupartifact.SourceFenceReceipt `json:"source_fence_receipt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode(): %v", err)
			}
			if request.Receipt.ID != receipt.ID ||
				request.Receipt.RestorePlanID != receipt.RestorePlanID {
				t.Fatalf("request receipt = %#v", request.Receipt)
			}
			_, _ = w.Write([]byte(`{"id":"plan-1","status":"activated"}`))
		},
	))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{
		"restore", "activate", "plan-1",
		"--source-fence-receipt", path,
		"--server", server.URL,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}

	cmd = NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{
		"restore", "activate", "plan-1",
		"--source-fence-receipt", path,
		"--break-glass-reason", "All source Controllers are unavailable.",
		"--server", server.URL,
	})
	if err := cmd.Execute(); err == nil ||
		!strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("ambiguous Execute() error = %v", err)
	}
}
