package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cloudsimalibaba "github.com/WuKongIM/WuKongIM/internal/infra/cloudsim/alibaba"
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
	activeUntil := now.Add(time.Hour).Format(time.RFC3339)
	code = execute([]string{"--state", statePath, "transition", "run-1", "running", "--active-until", activeUntil}, &stdout, &stderr, func() time.Time { return now })
	if code != 0 || !strings.Contains(stdout.String(), `"state": "running"`) {
		t.Fatalf("transition code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = execute([]string{"--state", statePath, "destroy", "run-1"}, &stdout, &stderr, func() time.Time { return now })
	if code != 0 || !strings.Contains(stdout.String(), `"state": "released"`) {
		t.Fatalf("destroy code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestCLIInventoryOutputsAuthorityBoundSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	statePath := filepath.Join(dir, "inventory.json")
	requestPath := filepath.Join(dir, "request.json")
	request := cloudsim.CreateRequest{
		RunID: "run-inventory", Provider: "fake", Region: "local", AccountIDHash: "sha256:account",
		Repository: "WuKongIM/WuKongIM", SourceSHA: "0123456789012345678901234567890123456789",
		ScenarioDigest: "sha256:scenario", DeploymentBundleDigest: "sha256:bundle",
		MCPCertificateFingerprint: "sha256:certificate", Preset: cloudsim.PresetSmall,
		ExpiresAt: now.Add(2 * time.Hour), MaxTotalCostMicros: 20_000_000, Currency: "CNY",
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"--state", statePath, "create", "--request", requestPath}, &stdout, &stderr, func() time.Time { return now }); code != 0 {
		t.Fatalf("create code=%d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"--state", statePath, "inventory"}, &stdout, &stderr, func() time.Time { return now }); code != 0 {
		t.Fatalf("inventory code=%d stderr=%s", code, stderr.String())
	}
	var snapshot cloudsim.InventorySnapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode inventory snapshot: %v\n%s", err, stdout.String())
	}
	if snapshot.Authority.Provider != "fake" || snapshot.Authority.Region != "local" ||
		snapshot.Authority.AccountIDHash != "sha256:account" {
		t.Fatalf("inventory authority = %#v", snapshot.Authority)
	}
	if len(snapshot.Runs) != 1 || snapshot.Runs[0].ID != "run-inventory" {
		t.Fatalf("inventory runs = %#v", snapshot.Runs)
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

func TestCLIOpenDeploymentRejectsNonHostPrefix(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := execute([]string{
		"--state", filepath.Join(t.TempDir(), "inventory.json"), "open-deployment", "run-1",
		"--source", "203.0.113.0/24", "--until", "2026-07-14T10:10:00Z",
	}, &stdout, &stderr, func() time.Time { return time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC) })
	if code == 0 {
		t.Fatalf("open-deployment code=0 stdout=%s", stdout.String())
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

func TestCLIDiscoverAlibabaConfig(t *testing.T) {
	provider := "alibaba"
	api := cliDiscoveryStub{}
	var stdout bytes.Buffer
	command := newDiscoverConfigCommand(&stdout, &provider, func(region string) (cloudsimalibaba.ConfigDiscoveryAPI, error) {
		if region != "cn-hangzhou" {
			t.Fatalf("factory region = %q", region)
		}
		return api, nil
	})
	command.SetArgs([]string{"--region", "cn-hangzhou"})
	if err := command.Execute(); err != nil {
		t.Fatalf("discover-config: %v", err)
	}
	var config cloudsimalibaba.Config
	if err := json.Unmarshal(stdout.Bytes(), &config); err != nil {
		t.Fatalf("decode config: %v\n%s", err, stdout.String())
	}
	if config.Region != "cn-hangzhou" || config.ZoneID != "cn-hangzhou-a" || config.AccountIDHash != "sha256:account" {
		t.Fatalf("config = %#v", config)
	}
	if got := config.Presets[cloudsim.PresetStress].InstanceTypes; len(got) != 1 || got[0] != "ecs.g8i.2xlarge" {
		t.Fatalf("stress preset = %v", got)
	}
}

type cliDiscoveryStub struct{}

func (cliDiscoveryStub) AccountIDHash(context.Context) (string, error) {
	return "sha256:account", nil
}

func (cliDiscoveryStub) EligibleSpotZones(context.Context, string) ([]string, error) {
	return []string{"cn-hangzhou-a"}, nil
}

func (cliDiscoveryStub) LatestLinuxImage(context.Context, string) (string, error) {
	return "aliyun-linux-3-x86_64", nil
}

func (cliDiscoveryStub) InstanceTypes(_ context.Context, _ string, cpu, _ int32) ([]cloudsimalibaba.InstanceTypeCandidate, error) {
	instanceType := map[int32]string{2: "ecs.g8i.large", 4: "ecs.g8i.xlarge", 8: "ecs.g8i.2xlarge"}[cpu]
	return []cloudsimalibaba.InstanceTypeCandidate{{
		ID: instanceType, CPUArchitecture: "X86", FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 8,
	}}, nil
}

func (cliDiscoveryStub) AvailableInstanceTypes(context.Context, string, string) (map[string]bool, error) {
	return map[string]bool{
		"ecs.g8i.large": true, "ecs.g8i.xlarge": true, "ecs.g8i.2xlarge": true,
	}, nil
}
