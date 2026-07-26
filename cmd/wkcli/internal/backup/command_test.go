package backup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
		_, _ = w.Write([]byte(`{"enabled":true,"health":"healthy","checkpoint_age_seconds":12}`))
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"status", "--server", server.URL, "--token", "operator-token"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !strings.Contains(stdout.String(), `"health": "healthy"`) || !strings.Contains(stdout.String(), `"checkpoint_age_seconds": 12`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestBackupOldJobCommandsAreAbsent(t *testing.T) {
	for _, name := range []string{"list", "trigger", "cancel", "hold", "release", "verify"} {
		var stdout, stderr bytes.Buffer
		cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
		if child, _, err := cmd.Find([]string{name}); err == nil && child != cmd {
			t.Fatalf("legacy command %q is still registered", name)
		}
	}
}

func TestBackupCheckpointListUsesBoundedCatalogQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/manager/backups/checkpoints" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("cursor") != "next" ||
			r.URL.Query().Get("limit") != "25" ||
			r.URL.Query().Get("id") != "checkpoint-1" {
			t.Fatalf("query = %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"checkpoint-1"}],"total":1}`))
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{
		"checkpoint", "list", "--server", server.URL, "--json",
		"--limit", "25", "--cursor", "next", "--id", "checkpoint-1",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !strings.Contains(stdout.String(), `"id":"checkpoint-1"`) {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestBackupCheckpointShowAndPublishUseContinuousEndpoints(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"checkpoint":{"id":"checkpoint-1"}}`))
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"checkpoint", "show", "checkpoint-1", "--server", server.URL},
		{"checkpoint", "publish", "--server", server.URL},
	} {
		var stdout, stderr bytes.Buffer
		cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute(%v): %v", args, err)
		}
	}
	want := []string{
		http.MethodGet + " /manager/backups/checkpoints/checkpoint-1",
		http.MethodPost + " /manager/backups/checkpoints",
	}
	if len(requests) != len(want) {
		t.Fatalf("requests = %#v", requests)
	}
	for index := range want {
		if requests[index] != want[index] {
			t.Fatalf("requests = %#v", requests)
		}
	}
}

func TestBackupRestorePlanUsesExplicitRecoveryEndpoint(t *testing.T) {
	const catalogHeadToken = "opaque-catalog-head-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/manager/restore/plan" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode(): %v", err)
		}
		if request["checkpoint_id"] != "checkpoint-1" ||
			request["invalidate_tokens"] != true ||
			request["latest_verified"] != nil || request["repository"] != nil ||
			request["catalog_head"] != nil ||
			request["catalog_head_token"] != catalogHeadToken {
			t.Fatalf("request = %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"plan-1","status":"planned"}`))
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{
		"restore", "plan", "--server", server.URL,
		"--checkpoint", "checkpoint-1",
		"--catalog-head", catalogHeadToken,
		"--invalidate-tokens",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !strings.Contains(stdout.String(), `"status": "planned"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestBackupCheckpointHoldAndReleaseUseOneBoundedEndpoint(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var request map[string]bool
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode(): %v", err)
			}
			requests = append(requests, fmt.Sprintf(
				"%s %s held=%t",
				r.Method, r.URL.Path, request["held"],
			))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"checkpoint-7"}`))
		},
	))
	defer server.Close()

	for _, action := range []string{"hold", "release"} {
		var stdout, stderr bytes.Buffer
		cmd := NewCommand(command.Deps{
			Stdout: &stdout, Stderr: &stderr,
		})
		cmd.SetArgs([]string{
			"checkpoint", action, "checkpoint-7",
			"--server", server.URL,
		})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s Execute(): %v", action, err)
		}
	}
	want := []string{
		"POST /manager/backups/checkpoints/checkpoint-7/hold held=true",
		"POST /manager/backups/checkpoints/checkpoint-7/hold held=false",
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %#v, want %#v", requests, want)
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
				"restore_plan_id":   "plan-1",
				"checkpoint_id":     "checkpoint-1",
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
		"--checkpoint", "checkpoint-1",
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
			Format:  backupartifact.SourceFenceReceiptFormat,
			Version: backupartifact.SourceFenceReceiptVersion,
			ID:      "source-fence-1", SourceClusterID: "source",
			SourceGeneration: "source-generation-1",
			RestorePlanID:    "plan-1", CheckpointID: "checkpoint-1",
			CheckpointSHA256:        strings.Repeat("a", 64),
			TargetClusterID:         "target",
			TargetGeneration:        "target-generation-1",
			FenceControllerRevision: 9,
			RequestedAtUnixMillis:   1_800_000_000_000,
			ConvergedAtUnixMillis:   1_800_000_001_000,
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
