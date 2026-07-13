package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cloudsim "github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

func TestCLICloudSimulationLifecycleAndRunLocator(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	statePath := filepath.Join(dir, "inventory.json")
	requestPath := filepath.Join(dir, "request.json")
	locatorPath := filepath.Join(dir, "locator.json")
	request := cloudsim.CreateRequest{
		RunID: "run-1", Provider: "fake", Region: "local", AccountIDHash: "sha256:account",
		Repository: "WuKongIM/WuKongIM", SourceSHA: "0123456789012345678901234567890123456789",
		ScenarioDigest: "sha256:scenario", DeploymentBundleDigest: "sha256:bundle",
		MCPCertificateFingerprint: "sha256:certificate", Preset: cloudsim.PresetSmall,
		ExpiresAt: now.Add(2 * time.Hour), MaxTotalCostMicros: 20_000_000, Currency: "CNY",
	}
	data, _ := json.Marshal(request)
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := execute([]string{
		"--state", statePath, "create", "--request", requestPath,
		"--locator", locatorPath, "--workflow-run-id", "42",
	}, &stdout, &stderr, func() time.Time { return now })
	if code != 0 {
		t.Fatalf("create code=%d stderr=%s", code, stderr.String())
	}
	locatorFile, err := os.Open(locatorPath)
	if err != nil {
		t.Fatal(err)
	}
	locator, err := cloudsim.DecodeRunLocator(locatorFile)
	locatorFile.Close()
	if err != nil {
		t.Fatalf("DecodeRunLocator() error = %v", err)
	}
	if locator.RunID != "run-1" || locator.ProvisionWorkflowRunID != 42 {
		t.Fatalf("locator = %#v", locator)
	}

	stdout.Reset()
	stderr.Reset()
	code = execute([]string{"--state", statePath, "status", "run-1"}, &stdout, &stderr, func() time.Time { return now })
	if code != 0 || !strings.Contains(stdout.String(), `"state": "ready"`) {
		t.Fatalf("status code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = execute([]string{"--state", statePath, "destroy", "run-1"}, &stdout, &stderr, func() time.Time { return now })
	if code != 0 || !strings.Contains(stdout.String(), `"state": "released"`) {
		t.Fatalf("destroy code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestCLIOpenAnalysisRejectsNonHostPrefix(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := execute([]string{
		"--state", filepath.Join(t.TempDir(), "inventory.json"), "open-analysis", "run-1",
		"--source", "203.0.113.0/24", "--until", "2026-07-14T10:30:00Z",
	}, &stdout, &stderr, func() time.Time { return time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC) })
	if code == 0 {
		t.Fatalf("open-analysis code=0 stdout=%s", stdout.String())
	}
}

func TestCLIScenarioDigest(t *testing.T) {
	scenarioPath := filepath.Join(t.TempDir(), "scenario.yaml")
	if err := os.WriteFile(scenarioPath, []byte("version: wkbench/v1\nrun:\n  id: test\n  random_seed: 42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := execute([]string{"scenario-digest", "--scenario", scenarioPath}, &stdout, &stderr, time.Now)
	if code != 0 || !strings.Contains(stdout.String(), `"digest": "sha256:`) {
		t.Fatalf("scenario-digest code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}
