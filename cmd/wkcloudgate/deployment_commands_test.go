package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	cloudleasefake "github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/fake"
	"github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

func TestDeploymentPlanAndGateCommandsUseValidatedReceipts(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	manifest := commandManifest()
	receipt := commandLeaseReceipt(t, now, manifest)
	directory := t.TempDir()
	receiptPath := writeCommandJSON(t, directory, "lease-receipt.json", receipt)
	manifestPath := writeCommandJSON(t, directory, "bundle-manifest.json", manifest)

	var stdout bytes.Buffer
	command := newRootCommand(&stdout, &bytes.Buffer{})
	command.SetArgs([]string{"deployment-plan", "--lease-receipt", receiptPath, "--bundle-manifest", manifestPath, "--now", now.Format(time.RFC3339Nano)})
	if err := command.Execute(); err != nil {
		t.Fatalf("deployment-plan error = %v", err)
	}
	var plan clouddeploy.DeploymentPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Topology.PhysicalHashSlots != 256 || plan.Topology.LogicalSlotGroups != 12 || len(plan.Hosts) != 4 {
		t.Fatalf("plan = %#v", plan)
	}
	planPath := writeCommandJSON(t, directory, "deployment-plan.json", plan)
	snapshot := commandReadySnapshot(plan, now)
	snapshotPath := writeCommandJSON(t, directory, "readiness.json", snapshot)
	stdout.Reset()
	command = newRootCommand(&stdout, &bytes.Buffer{})
	command.SetArgs([]string{"deployment-gate", "--plan", planPath, "--bundle-manifest", manifestPath, "--snapshot", snapshotPath, "--now", now.Format(time.RFC3339Nano)})
	if err := command.Execute(); err != nil {
		t.Fatalf("deployment-gate error = %v; output=%s", err, stdout.String())
	}
	var outcome clouddeploy.Outcome
	if err := json.Unmarshal(stdout.Bytes(), &outcome); err != nil || !outcome.Passed || outcome.Receipt == nil {
		t.Fatalf("outcome = %#v, %v", outcome, err)
	}
}

func TestDeploymentGateEmitsStructuredFailure(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	manifest := commandManifest()
	receipt := commandLeaseReceipt(t, now, manifest)
	plan, err := clouddeploy.BuildPlan(normalizeLeaseReceipt(receipt), manifest, now)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := commandReadySnapshot(plan, now)
	snapshot.Cluster.PhysicalHashSlots = 12
	directory := t.TempDir()
	planPath := writeCommandJSON(t, directory, "plan.json", plan)
	manifestPath := writeCommandJSON(t, directory, "manifest.json", manifest)
	snapshotPath := writeCommandJSON(t, directory, "snapshot.json", snapshot)
	var stdout bytes.Buffer
	command := newRootCommand(&stdout, &bytes.Buffer{})
	command.SetArgs([]string{"deployment-gate", "--plan", planPath, "--bundle-manifest", manifestPath, "--snapshot", snapshotPath, "--now", now.Format(time.RFC3339Nano)})
	if err := command.Execute(); err == nil {
		t.Fatal("deployment-gate succeeded")
	}
	var outcome clouddeploy.Outcome
	if err := json.Unmarshal(stdout.Bytes(), &outcome); err != nil || outcome.Failure == nil || outcome.Failure.Code != clouddeploy.FailureSlotTopology {
		t.Fatalf("outcome = %#v, %v", outcome, err)
	}
}

func TestDeploymentJSONReaderRejectsOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(path, bytes.Repeat([]byte{' '}, maxDeploymentJSONBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	var plan clouddeploy.DeploymentPlan
	if err := readStrictDeploymentJSON(path, &plan); err == nil {
		t.Fatal("readStrictDeploymentJSON(oversized) succeeded")
	}
}

func commandLeaseReceipt(t *testing.T, now time.Time, manifest clouddeploy.Manifest) cloudlease.Receipt {
	t.Helper()
	provider := cloudleasefake.New(cloudleasefake.Options{Now: func() time.Time { return now }})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := cloudlease.Plan{
		Schema: cloudlease.PlanSchemaV1, LeaseID: "lease-deploy", RequestID: "request-deploy",
		Provider: cloudleasefake.ProviderName, Region: "local", Repository: "WuKongIM/WuKongIM", Operator: "tangtaoit",
		ExpiresAt: now.Add(6 * time.Hour), Budget: cloudlease.Budget{Currency: "CNY", LimitMicros: 10_000_000},
		Provenance: cloudlease.Provenance{SourceSHA: manifest.SourceSHA, BundleDigest: manifest.BundleDigest},
		Network:    cloudlease.NetworkPlan{Isolated: true, SingleZone: true},
		HostGroups: []cloudlease.HostGroupPlan{
			{Role: "service", Count: 3, Compute: commandCompute(), SystemDisk: commandDisk("system", 40_000_000_000), DataDisks: []cloudlease.DiskPlan{commandDisk("data", 500_000_000_000)}},
			{Role: "load", Count: 1, Compute: commandCompute(), SystemDisk: commandDisk("system", 40_000_000_000), DataDisks: []cloudlease.DiskPlan{commandDisk("data", 200_000_000_000)}, PublicIPv4: true, InternetEgress: true, PeakBandwidthMbps: 20},
		},
	}
	quote, err := controller.Quote(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := controller.Acquire(context.Background(), plan, quote)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func commandCompute() cloudlease.ComputePlan {
	return cloudlease.ComputePlan{VCPUs: 4, MemoryBytes: 8 << 30, Architecture: "x86_64", BillingModel: "postpaid"}
}

func commandDisk(role string, size int64) cloudlease.DiskPlan {
	return cloudlease.DiskPlan{Role: role, SizeBytes: size, Class: "ssd", PerformanceLevel: "pl0"}
}

func commandManifest() clouddeploy.Manifest {
	return clouddeploy.Manifest{Schema: clouddeploy.ManifestSchemaV1,
		SourceSHA: "0123456789012345678901234567890123456789", ControlSHA: "abcdefabcdefabcdefabcdefabcdefabcdefabcd",
		IntentSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		BundleDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
}

func commandReadySnapshot(plan clouddeploy.DeploymentPlan, now time.Time) clouddeploy.ReadinessSnapshot {
	hosts := make([]clouddeploy.HostSnapshot, 0, len(plan.Hosts))
	for _, planned := range plan.Hosts {
		units := []string{"node-exporter.service", "wukongim-process-metrics.service", "wukongim-evidence.timer"}
		if planned.Role == "load" {
			units = append(units, "wkbench-worker@1.service", "wkbench-worker@2.service", "wkbench-worker@3.service", "prometheus.service", "wkanalysis.service", "caddy.service")
		} else {
			units = append(units, "wukongim.service", "wkbench-host-metrics.service")
		}
		hosts = append(hosts, clouddeploy.HostSnapshot{Role: planned.Role, OperatingSystem: "ubuntu", OSVersion: "24.04", Architecture: "x86_64",
			BaseToolsAvailable: true, BundleDigest: plan.BundleDigest, DataDiskID: planned.DataDiskID, DataMount: "/var/lib/wukongim-cloud",
			DataFilesystemBytes: planned.MinimumDataFilesystemBytes, DataFreeBytes: planned.MinimumDataFilesystemBytes / 2,
			SystemFilesystemBytes: 40_000_000_000, SystemFreeBytes: 20_000_000_000, ActiveUnits: units})
	}
	return clouddeploy.ReadinessSnapshot{Schema: clouddeploy.SnapshotSchemaV1, DeploymentPlanDigest: plan.PlanDigest, ObservedAt: now,
		Hosts: hosts, Cluster: clouddeploy.ClusterSnapshot{ReadyNodes: 3, Members: 3, PhysicalHashSlots: 256, HealthySlotLeaders: 256,
			HealthySlotReplicaSets: 256, LogicalSlotGroups: 12, RuntimeConfigNodes: 3, SlotReplicas: 3, ChannelReplicas: 3},
		Load: clouddeploy.LoadSnapshot{ReadyWorkers: 3, PrometheusTargetsUp: 7, PrometheusTargetsWant: 7, WorkloadConfigValid: true,
			ProxyReady: true, ManagerReady: true, DemoReady: true, AnalysisReady: true}}
}

func writeCommandJSON(t *testing.T, directory, name string, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
