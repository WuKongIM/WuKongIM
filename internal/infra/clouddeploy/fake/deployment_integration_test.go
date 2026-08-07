//go:build integration

package fake

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

func TestDeploymentControllerThroughFakeFleet(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	manifest := integrationManifest()
	plan, err := clouddeploy.BuildPlan(integrationLease(now, manifest), manifest, now)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := integrationSnapshot(plan, now)

	success := New(Options{Snapshot: snapshot})
	outcome := clouddeploy.Deploy(context.Background(), success, plan, manifest, now)
	if !outcome.Passed || outcome.Receipt == nil || len(success.Operations()) != 17 {
		t.Fatalf("successful fake deployment = %#v, operations=%v", outcome, success.Operations())
	}

	for _, fixture := range []struct {
		operation string
		code      clouddeploy.FailureCode
		gate      clouddeploy.Gate
		role      string
	}{
		{"stage:load", clouddeploy.FailureBundleTransfer, clouddeploy.GatePlanValidated, "load"},
		{"relay:load:service-2", clouddeploy.FailureBundleTransfer, clouddeploy.GatePlanValidated, "service-2"},
		{"verify:service-3", clouddeploy.FailureBundleDigest, clouddeploy.GateBundleTransferred, "service-3"},
		{"prepare:load", clouddeploy.FailureDiskMount, clouddeploy.GateBundleVerified, "load"},
		{"activate:service-1", clouddeploy.FailureActivation, clouddeploy.GateHostsPrepared, "service-1"},
		{"snapshot", clouddeploy.FailureEvidence, clouddeploy.GateServicesActive, ""},
	} {
		t.Run(fixture.operation, func(t *testing.T) {
			fleet := New(Options{Snapshot: snapshot, FailOperation: fixture.operation})
			got := clouddeploy.Deploy(context.Background(), fleet, plan, manifest, now)
			if got.Passed || got.Failure == nil || got.Failure.Code != fixture.code ||
				got.Failure.LastCompletedGate != fixture.gate || got.Failure.HostRole != fixture.role {
				t.Fatalf("outcome = %#v, operations=%v", got, fleet.Operations())
			}
		})
	}
}

func integrationManifest() clouddeploy.Manifest {
	return clouddeploy.Manifest{
		Schema: clouddeploy.ManifestSchemaV1, SourceSHA: strings.Repeat("1", 40), ControlSHA: strings.Repeat("2", 40),
		IntentSHA256: "sha256:" + strings.Repeat("3", 64), BundleDigest: "sha256:" + strings.Repeat("4", 64),
	}
}

func integrationLease(now time.Time, manifest clouddeploy.Manifest) clouddeploy.LeaseInventory {
	resources := []clouddeploy.LeaseResource{
		{ID: "i-service-1", Kind: "instance", Role: "service", PrivateAddress: "10.42.0.11"},
		{ID: "i-service-2", Kind: "instance", Role: "service", PrivateAddress: "10.42.0.12"},
		{ID: "i-service-3", Kind: "instance", Role: "service", PrivateAddress: "10.42.0.13"},
		{ID: "i-load", Kind: "instance", Role: "load", PrivateAddress: "10.42.0.20"},
		{ID: "d-service-1", Kind: "data_disk", Role: "service", ParentID: "i-service-1", SizeBytes: 500_000_000_000},
		{ID: "d-service-2", Kind: "data_disk", Role: "service", ParentID: "i-service-2", SizeBytes: 500_000_000_000},
		{ID: "d-service-3", Kind: "data_disk", Role: "service", ParentID: "i-service-3", SizeBytes: 500_000_000_000},
		{ID: "d-load", Kind: "data_disk", Role: "load", ParentID: "i-load", SizeBytes: 200_000_000_000},
		{ID: "eip-load", Kind: "public_address", Role: "load", PublicAddress: "203.0.113.10"},
	}
	return clouddeploy.LeaseInventory{
		LeaseID: "lease-integration", RequestID: "request-integration", Repository: "WuKongIM/WuKongIM",
		Provider: "fake", Region: "local", Zone: "zone-a", PlanDigest: "sha256:" + strings.Repeat("5", 64),
		SourceSHA: manifest.SourceSHA, BundleDigest: manifest.BundleDigest, State: "active", ExpiresAt: now.Add(time.Hour), Resources: resources,
	}
}

func integrationSnapshot(plan clouddeploy.DeploymentPlan, now time.Time) clouddeploy.ReadinessSnapshot {
	hosts := make([]clouddeploy.HostSnapshot, 0, len(plan.Hosts))
	for _, planned := range plan.Hosts {
		units := []string{"node-exporter.service", "wukongim-process-metrics.service", "wukongim-evidence.timer"}
		if planned.Role == "load" {
			units = append(units, "wkbench-worker@1.service", "wkbench-worker@2.service", "wkbench-worker@3.service", "prometheus.service", "wkanalysis.service", "caddy.service")
		} else {
			units = append(units, "wukongim.service", "wkbench-host-metrics.service")
		}
		hosts = append(hosts, clouddeploy.HostSnapshot{
			Role: planned.Role, OperatingSystem: "ubuntu", OSVersion: "24.04", Architecture: "x86_64", BaseToolsAvailable: true,
			BundleDigest: plan.BundleDigest, DataDiskID: planned.DataDiskID, DataMount: "/var/lib/wukongim-cloud",
			DataFilesystemBytes: planned.MinimumDataFilesystemBytes, DataFreeBytes: planned.MinimumDataFilesystemBytes / 2,
			SystemFilesystemBytes: 40_000_000_000, SystemFreeBytes: 20_000_000_000, ActiveUnits: units,
		})
	}
	return clouddeploy.ReadinessSnapshot{
		Schema: clouddeploy.SnapshotSchemaV1, DeploymentPlanDigest: plan.PlanDigest, ObservedAt: now, Hosts: hosts,
		Cluster: clouddeploy.ClusterSnapshot{ReadyNodes: 3, Members: 3, PhysicalHashSlots: 256, HealthySlotLeaders: 256,
			HealthySlotReplicaSets: 256, LogicalSlotGroups: 12, RuntimeConfigNodes: 3, SlotReplicas: 3, ChannelReplicas: 3},
		Load: clouddeploy.LoadSnapshot{ReadyWorkers: 3, PrometheusTargetsUp: 7, PrometheusTargetsWant: 7,
			WorkloadConfigValid: true, ProxyReady: true, ManagerReady: true, DemoReady: true, AnalysisReady: true},
	}
}
