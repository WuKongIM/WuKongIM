//go:build integration

package scripts_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cloudleasefake "github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/fake"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestRepairDiscoveryRecoversAnActiveLeaseFromItsAcquireReceipt(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	plan := cloudlease.Plan{
		Schema: cloudlease.PlanSchemaV1, LeaseID: "repair-recovery-repair-1", RequestID: "repair-recovery",
		Provider: cloudleasefake.ProviderName, Region: "local", Repository: "WuKongIM/WuKongIM", Operator: "tangtaoit",
		ExpiresAt: now.Add(6 * time.Hour),
		Budget:    cloudlease.Budget{Currency: "CNY", LimitMicros: 300_000_000, OperationalStopMicros: 250_000_000},
		Provenance: cloudlease.Provenance{
			SourceSHA: strings.Repeat("a", 40), BundleDigest: "sha256:" + strings.Repeat("b", 64),
		},
		Network: cloudlease.NetworkPlan{Isolated: true, SingleZone: true},
		HostGroups: []cloudlease.HostGroupPlan{{
			Role: "service", Count: 1,
			Compute:    cloudlease.ComputePlan{VCPUs: 4, MemoryBytes: 8 << 30, Architecture: "x86_64", BillingModel: "postpaid"},
			SystemDisk: cloudlease.DiskPlan{Role: "system", SizeBytes: 40 << 30, Class: "essd", PerformanceLevel: "PL0"},
		}},
		Tags: map[string]string{"stage": "repair"},
	}
	provider := cloudleasefake.New(cloudleasefake.Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := controller.Acquire(context.Background(), plan, quote)
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(directory, "receipt.json")
	writeRepairJSON(t, receiptPath, map[string]any{
		"schema": "wukongim.cloud_lease.receipt/v1", "receipt": receipt,
	})
	ownerPath := filepath.Join(directory, "repair-owner.json")
	writeRepairJSON(t, ownerPath, map[string]any{
		"schema":     "wukongim.chat_lifecycle.repair_acquire_owner/v1",
		"request_id": "repair-recovery", "parent_run_id": 987,
	})
	zipPath := filepath.Join(directory, "acquire.zip")
	archiveRepairFiles(t, zipPath, map[string]string{"receipt.json": receiptPath, "repair-owner.json": ownerPath})
	quoteOnlyPath := filepath.Join(directory, "quote-only.json")
	writeRepairJSON(t, quoteOnlyPath, map[string]any{"schema": "wukongim.cloud_lease.quote/v1"})
	quoteZipPath := filepath.Join(directory, "quote-only.zip")
	archiveRepairFiles(t, quoteZipPath, map[string]string{"quote.json": quoteOnlyPath})
	artifactsPath := filepath.Join(directory, "artifacts.json")
	writeRepairJSON(t, artifactsPath, map[string]any{"artifacts": []any{
		map[string]any{
			"id": 457, "name": "cloud-lease-provision-repair-recovery", "expired": false,
			"created_at": now.Add(time.Minute).Format(time.RFC3339), "workflow_run": map[string]any{"id": 124},
		},
		map[string]any{
			"id": 456, "name": "cloud-lease-provision-repair-recovery", "expired": false,
			"created_at": now.Format(time.RFC3339), "workflow_run": map[string]any{"id": 123},
		},
	}})
	runPath := filepath.Join(directory, "run.json")
	writeRepairJSON(t, runPath, map[string]any{
		"repository":      map[string]any{"full_name": "WuKongIM/WuKongIM"},
		"head_repository": map[string]any{"full_name": "WuKongIM/WuKongIM"},
		"head_branch":     "main", "event": "workflow_dispatch", "status": "completed", "conclusion": "success",
		"display_title": "Cloud Lease Provision repair-recovery", "path": ".github/workflows/cloud-lease-provision.yml",
	})
	fakeBin := filepath.Join(directory, "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRepairExecutable(t, filepath.Join(fakeBin, "gh"), `#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  *"/actions/artifacts?per_page=100&page=1"*) cat "$WK_TEST_ARTIFACTS" ;;
  *"/actions/runs/123"*|*"/actions/runs/124"*) cat "$WK_TEST_RUN" ;;
  *"/actions/artifacts/456/zip"*) cat "$WK_TEST_ZIP" ;;
  *"/actions/artifacts/457/zip"*) cat "$WK_TEST_QUOTE_ZIP" ;;
  *) printf 'unexpected gh call: %s\n' "$*" >&2; exit 91 ;;
esac
`)
	output := filepath.Join(directory, "matrix.json")
	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "discover-active-repair-handoffs.sh"), "", output)
	command.Dir = root
	command.Env = append(os.Environ(),
		"GOWORK=off", "GH_TOKEN=test", "GITHUB_REPOSITORY=WuKongIM/WuKongIM",
		"WK_TEST_ARTIFACTS="+artifactsPath, "WK_TEST_RUN="+runPath, "WK_TEST_ZIP="+zipPath,
		"WK_TEST_QUOTE_ZIP="+quoteZipPath,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if body, runErr := command.CombinedOutput(); runErr != nil {
		t.Fatalf("discover active repair acquire: %v\n%s", runErr, body)
	}
	var matrix struct {
		Include []struct {
			RequestID   string `json:"request_id"`
			HandoffKind string `json:"handoff_kind"`
			Artifact    string `json:"artifact_name"`
			HandoffRun  int    `json:"handoff_run_id"`
		} `json:"include"`
	}
	body, err := os.ReadFile(output)
	if err != nil || json.Unmarshal(body, &matrix) != nil {
		t.Fatalf("read recovery matrix: %v body=%s", err, body)
	}
	if len(matrix.Include) != 1 || matrix.Include[0].RequestID != "repair-recovery" ||
		matrix.Include[0].HandoffKind != "acquire" || matrix.Include[0].Artifact != "cloud-lease-provision-repair-recovery" ||
		matrix.Include[0].HandoffRun != 123 {
		t.Fatalf("recovery matrix = %+v", matrix.Include)
	}
}

func writeRepairJSON(t *testing.T, path string, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func archiveRepairFiles(t *testing.T, archivePath string, files map[string]string) {
	t.Helper()
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	for name, sourcePath := range files {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		body, readErr := os.ReadFile(sourcePath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, writeErr := entry.Write(body); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
}
