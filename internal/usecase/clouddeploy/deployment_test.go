package clouddeploy_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	clouddeploy "github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

func TestBuildPlanBindsExactFourHostLeaseInventory(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	manifest := deploymentManifest()
	plan, err := clouddeploy.BuildPlan(deploymentLease(now), manifest, now)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if plan.Schema != clouddeploy.PlanSchemaV2 || plan.Topology.PhysicalHashSlots != 256 ||
		plan.Topology.LogicalSlotGroups != 12 || plan.Topology.SlotReplicas != 3 || plan.Topology.ChannelReplicas != 3 {
		t.Fatalf("plan topology = %#v", plan)
	}
	roles := make([]string, 0, len(plan.Hosts))
	for _, host := range plan.Hosts {
		roles = append(roles, host.Role)
	}
	if !slices.Equal(roles, []string{"service-1", "service-2", "service-3", "load"}) {
		t.Fatalf("roles = %v", roles)
	}
	if plan.Hosts[0].MinimumDataFilesystemBytes != 500_000_000_000 || plan.Hosts[3].MinimumDataFilesystemBytes != 200_000_000_000 {
		t.Fatalf("storage minimums = %#v", plan.Hosts)
	}
	if plan.Hosts[3].PublicAddress != "203.0.113.10" || plan.PlanDigest == "" {
		t.Fatalf("load/identity = %#v", plan)
	}
	if err := clouddeploy.ValidatePlan(plan, manifest, now); err != nil {
		t.Fatalf("ValidatePlan() error = %v", err)
	}
}

func TestBuildRepairPlanReusesOnlyRepairLeaseForNewBundleGeneration(t *testing.T) {
	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)
	lease := deploymentLease(now)
	lease.Tags = map[string]string{"stage": "repair"}
	lease.Budget.LimitMicros = clouddeploy.RepairBudgetHardMicros
	lease.Budget.OperationalStopMicros = clouddeploy.RepairBudgetStopMicros
	candidate := deploymentManifest()
	candidate.SourceSHA = "1111111111111111111111111111111111111111"
	candidate.BundleDigest = digest('d')

	plan, err := clouddeploy.BuildRepairPlan(lease, candidate, 2, now)
	if err != nil {
		t.Fatalf("BuildRepairPlan() error = %v", err)
	}
	if plan.Purpose != clouddeploy.DeploymentPurposeRepair || plan.Generation != 2 ||
		plan.LeaseSourceSHA != lease.SourceSHA || plan.LeaseBundleDigest != lease.BundleDigest ||
		plan.SourceSHA != candidate.SourceSHA || plan.BundleDigest != candidate.BundleDigest {
		t.Fatalf("repair plan identity = %#v", plan)
	}

	third, err := clouddeploy.BuildRepairPlan(lease, candidate, 3, now)
	if err != nil {
		t.Fatalf("BuildRepairPlan(generation 3) error = %v", err)
	}
	if third.PlanDigest == plan.PlanDigest {
		t.Fatal("repair generations share one deployment plan digest")
	}

	ordinary := deploymentLease(now)
	if _, err := clouddeploy.BuildRepairPlan(ordinary, candidate, 2, now); !errors.Is(err, clouddeploy.ErrInvalidDeployment) {
		t.Fatalf("ordinary Lease repair error = %v", err)
	}
	if _, err := clouddeploy.BuildRepairPlan(lease, candidate, 0, now); !errors.Is(err, clouddeploy.ErrInvalidDeployment) {
		t.Fatalf("zero generation repair error = %v", err)
	}
}

func TestBuildPlanRejectsWrongInventoryAndArtifactIdentity(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*clouddeploy.LeaseInventory, *clouddeploy.Manifest){
		"inactive lease": func(lease *clouddeploy.LeaseInventory, _ *clouddeploy.Manifest) { lease.State = "released" },
		"expired lease":  func(lease *clouddeploy.LeaseInventory, _ *clouddeploy.Manifest) { lease.ExpiresAt = now },
		"wrong source": func(lease *clouddeploy.LeaseInventory, _ *clouddeploy.Manifest) {
			lease.SourceSHA = "1111111111111111111111111111111111111111"
		},
		"wrong bundle": func(lease *clouddeploy.LeaseInventory, _ *clouddeploy.Manifest) { lease.BundleDigest = digest('1') },
		"oversized identity": func(lease *clouddeploy.LeaseInventory, _ *clouddeploy.Manifest) {
			lease.RequestID = string(make([]byte, 129))
		},
		"small service disk": func(lease *clouddeploy.LeaseInventory, _ *clouddeploy.Manifest) {
			lease.Resources[4].SizeBytes = 499_999_999_999
		},
		"duplicate data disk": func(lease *clouddeploy.LeaseInventory, _ *clouddeploy.Manifest) {
			lease.Resources = append(lease.Resources, lease.Resources[4])
		},
		"service public address": func(lease *clouddeploy.LeaseInventory, _ *clouddeploy.Manifest) {
			lease.Resources[0].PublicAddress = "203.0.113.99"
		},
		"duplicate private address": func(lease *clouddeploy.LeaseInventory, _ *clouddeploy.Manifest) {
			lease.Resources[2].PrivateAddress = lease.Resources[0].PrivateAddress
		},
		"conflicting load addresses": func(lease *clouddeploy.LeaseInventory, _ *clouddeploy.Manifest) {
			lease.Resources[1].PublicAddress = "203.0.113.99"
		},
		"load without EIP": func(lease *clouddeploy.LeaseInventory, _ *clouddeploy.Manifest) {
			lease.Resources = lease.Resources[:8]
		},
	} {
		t.Run(name, func(t *testing.T) {
			lease := deploymentLease(now)
			manifest := deploymentManifest()
			mutate(&lease, &manifest)
			if _, err := clouddeploy.BuildPlan(lease, manifest, now); !errors.Is(err, clouddeploy.ErrInvalidDeployment) {
				t.Fatalf("BuildPlan() error = %v", err)
			}
		})
	}
}

func TestEvaluateReadinessReturnsTypedReceipt(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan, err := clouddeploy.BuildPlan(deploymentLease(now), deploymentManifest(), now)
	if err != nil {
		t.Fatal(err)
	}
	outcome := clouddeploy.EvaluateReadiness(plan, readySnapshot(plan, now), now)
	if !outcome.Passed || outcome.Receipt == nil || outcome.Failure != nil {
		t.Fatalf("outcome = %#v", outcome)
	}
	if outcome.Receipt.Schema != clouddeploy.ReceiptSchemaV2 || outcome.Receipt.BundleDigest != plan.BundleDigest ||
		outcome.Receipt.DeploymentPlanDigest != plan.PlanDigest || len(outcome.Receipt.Hosts) != 4 {
		t.Fatalf("receipt = %#v", outcome.Receipt)
	}
	if outcome.Receipt.PublicEndpoints.Manager != "http://203.0.113.10/" ||
		outcome.Receipt.PublicEndpoints.Demo != "http://203.0.113.10/demo/" {
		t.Fatalf("public endpoints = %#v", outcome.Receipt.PublicEndpoints)
	}
}

func TestEvaluateReadinessRetainsRepairGenerationIdentity(t *testing.T) {
	now := time.Date(2026, 8, 22, 17, 30, 0, 0, time.UTC)
	lease := deploymentLease(now)
	lease.Tags = map[string]string{"stage": "repair"}
	lease.Budget.LimitMicros = clouddeploy.RepairBudgetHardMicros
	lease.Budget.OperationalStopMicros = clouddeploy.RepairBudgetStopMicros
	candidate := deploymentManifest()
	candidate.SourceSHA = "1111111111111111111111111111111111111111"
	candidate.BundleDigest = digest('d')
	plan, err := clouddeploy.BuildRepairPlan(lease, candidate, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	outcome := clouddeploy.EvaluateReadiness(plan, readySnapshot(plan, now), now)
	if !outcome.Passed || outcome.Receipt == nil {
		t.Fatalf("outcome = %#v", outcome)
	}
	if outcome.Receipt.Purpose != clouddeploy.DeploymentPurposeRepair || outcome.Receipt.Generation != 7 ||
		outcome.Receipt.LeaseSourceSHA != lease.SourceSHA || outcome.Receipt.LeaseBundleDigest != lease.BundleDigest ||
		outcome.Receipt.SourceSHA != candidate.SourceSHA || outcome.Receipt.BundleDigest != candidate.BundleDigest {
		t.Fatalf("repair receipt identity = %#v", outcome.Receipt)
	}
}

func TestEvaluateReadinessMajorFailuresAreStableAndBounded(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan, err := clouddeploy.BuildPlan(deploymentLease(now), deploymentManifest(), now)
	if err != nil {
		t.Fatal(err)
	}
	for name, fixture := range map[string]struct {
		mutate func(*clouddeploy.ReadinessSnapshot)
		code   clouddeploy.FailureCode
		gate   clouddeploy.Gate
	}{
		"host identity":     {func(s *clouddeploy.ReadinessSnapshot) { s.Hosts[0].OSVersion = "22.04" }, clouddeploy.FailureHostIdentity, clouddeploy.GateServicesActive},
		"bundle":            {func(s *clouddeploy.ReadinessSnapshot) { s.Hosts[1].BundleDigest = digest('9') }, clouddeploy.FailureBundleDigest, clouddeploy.GateServicesActive},
		"base tools":        {func(s *clouddeploy.ReadinessSnapshot) { s.Hosts[2].BaseToolsAvailable = false }, clouddeploy.FailureBaseTools, clouddeploy.GateServicesActive},
		"disk mount":        {func(s *clouddeploy.ReadinessSnapshot) { s.Hosts[0].DataMount = "/tmp" }, clouddeploy.FailureDiskMount, clouddeploy.GateServicesActive},
		"500 GB disk":       {func(s *clouddeploy.ReadinessSnapshot) { s.Hosts[0].DataFilesystemBytes = 499_999_999_999 }, clouddeploy.FailureDiskCapacity, clouddeploy.GateServicesActive},
		"five percent free": {func(s *clouddeploy.ReadinessSnapshot) { s.Hosts[0].DataFreeBytes = 24_999_999_999 }, clouddeploy.FailureDiskFree, clouddeploy.GateServicesActive},
		"time":              {func(s *clouddeploy.ReadinessSnapshot) { s.Hosts[0].ClockOffsetMilliseconds = 1001 }, clouddeploy.FailureTimeDrift, clouddeploy.GateServicesActive},
		"systemd":           {func(s *clouddeploy.ReadinessSnapshot) { s.Hosts[0].ActiveUnits = s.Hosts[0].ActiveUnits[1:] }, clouddeploy.FailureServiceInactive, clouddeploy.GateHostsPrepared},
		"cluster":           {func(s *clouddeploy.ReadinessSnapshot) { s.Cluster.Members = 2 }, clouddeploy.FailureClusterMembership, clouddeploy.GateServicesActive},
		"physical slots":    {func(s *clouddeploy.ReadinessSnapshot) { s.Cluster.PhysicalHashSlots = 12 }, clouddeploy.FailureSlotTopology, clouddeploy.GateClusterConverged},
		"logical groups":    {func(s *clouddeploy.ReadinessSnapshot) { s.Cluster.LogicalSlotGroups = 3 }, clouddeploy.FailureSlotTopology, clouddeploy.GateClusterConverged},
		"runtime configs":   {func(s *clouddeploy.ReadinessSnapshot) { s.Cluster.RuntimeConfigNodes = 2 }, clouddeploy.FailureSlotTopology, clouddeploy.GateClusterConverged},
		"channel replicas":  {func(s *clouddeploy.ReadinessSnapshot) { s.Cluster.ChannelReplicas = 2 }, clouddeploy.FailureSlotTopology, clouddeploy.GateClusterConverged},
		"workers":           {func(s *clouddeploy.ReadinessSnapshot) { s.Load.ReadyWorkers = 2 }, clouddeploy.FailureWorkers, clouddeploy.GateClusterConverged},
		"prometheus":        {func(s *clouddeploy.ReadinessSnapshot) { s.Load.PrometheusTargetsUp = 6 }, clouddeploy.FailurePrometheus, clouddeploy.GateClusterConverged},
		"workload config":   {func(s *clouddeploy.ReadinessSnapshot) { s.Load.WorkloadConfigValid = false }, clouddeploy.FailureWorkloadConfig, clouddeploy.GateClusterConverged},
		"public endpoint":   {func(s *clouddeploy.ReadinessSnapshot) { s.Load.DemoReady = false }, clouddeploy.FailurePublicEndpoints, clouddeploy.GateClusterConverged},
		"analysis":          {func(s *clouddeploy.ReadinessSnapshot) { s.Load.AnalysisReady = false }, clouddeploy.FailureAnalysis, clouddeploy.GateClusterConverged},
		"stale evidence": {func(s *clouddeploy.ReadinessSnapshot) {
			s.ObservedAt = now.Add(-clouddeploy.MaximumReadinessAge - time.Second)
		}, clouddeploy.FailureEvidence, clouddeploy.GateServicesActive},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := readySnapshot(plan, now)
			fixture.mutate(&snapshot)
			outcome := clouddeploy.EvaluateReadiness(plan, snapshot, now)
			if outcome.Passed || outcome.Receipt != nil || outcome.Failure == nil || outcome.Failure.Code != fixture.code || outcome.Failure.LastCompletedGate != fixture.gate {
				t.Fatalf("outcome = %#v", outcome)
			}
			if len(outcome.Failure.Evidence) == 0 || len(outcome.Failure.Evidence[0]) > 256 {
				t.Fatalf("failure evidence = %#v", outcome.Failure)
			}
		})
	}
}

func TestEvaluateReadinessRejectsInvalidPublicEndpointWithoutPanicking(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	plan, err := clouddeploy.BuildPlan(deploymentLease(now), deploymentManifest(), now)
	if err != nil {
		t.Fatal(err)
	}
	plan.Hosts[3].PublicAddress = "not-an-ip"
	outcome := clouddeploy.EvaluateReadiness(plan, readySnapshot(plan, now), now)
	if outcome.Passed || outcome.Failure == nil || outcome.Failure.Code != clouddeploy.FailureEvidence || outcome.Failure.HostRole != "load" {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestDeployUsesLoadHopAndStopsAtExactGate(t *testing.T) {
	now := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	manifest := deploymentManifest()
	plan, err := clouddeploy.BuildPlan(deploymentLease(now), manifest, now)
	if err != nil {
		t.Fatal(err)
	}
	fleet := &recordingFleet{snapshot: readySnapshot(plan, now)}
	outcome := clouddeploy.Deploy(context.Background(), fleet, plan, manifest, now)
	if !outcome.Passed {
		t.Fatalf("Deploy() = %#v", outcome)
	}
	wantPrefix := []string{"stage:load", "relay:load:service-1", "relay:load:service-2", "relay:load:service-3"}
	if len(fleet.calls) < len(wantPrefix) || !slices.Equal(fleet.calls[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("calls = %v", fleet.calls)
	}

	fleet = &recordingFleet{snapshot: readySnapshot(plan, now), failCall: "prepare:service-2"}
	outcome = clouddeploy.Deploy(context.Background(), fleet, plan, manifest, now)
	if outcome.Passed || outcome.Failure == nil || outcome.Failure.Code != clouddeploy.FailureDiskMount ||
		outcome.Failure.LastCompletedGate != clouddeploy.GateBundleVerified || outcome.Failure.HostRole != "service-2" {
		t.Fatalf("failed outcome = %#v", outcome)
	}
}

type recordingFleet struct {
	calls    []string
	failCall string
	snapshot clouddeploy.ReadinessSnapshot
}

func (f *recordingFleet) call(value string) error {
	f.calls = append(f.calls, value)
	if value == f.failCall {
		return errors.New("injected secret-bearing detail must not escape")
	}
	return nil
}

func (f *recordingFleet) StageBundle(_ context.Context, host clouddeploy.HostPlan, _ string) error {
	return f.call("stage:" + host.Role)
}
func (f *recordingFleet) RelayBundle(_ context.Context, load, host clouddeploy.HostPlan, _ string) error {
	return f.call("relay:" + load.Role + ":" + host.Role)
}
func (f *recordingFleet) VerifyBundle(_ context.Context, host clouddeploy.HostPlan, _ string) error {
	return f.call("verify:" + host.Role)
}
func (f *recordingFleet) PrepareHost(_ context.Context, host clouddeploy.HostPlan) error {
	return f.call("prepare:" + host.Role)
}
func (f *recordingFleet) ActivateHost(_ context.Context, host clouddeploy.HostPlan) error {
	return f.call("activate:" + host.Role)
}
func (f *recordingFleet) Snapshot(_ context.Context, _ clouddeploy.DeploymentPlan) (clouddeploy.ReadinessSnapshot, error) {
	if err := f.call("snapshot"); err != nil {
		return clouddeploy.ReadinessSnapshot{}, err
	}
	return f.snapshot, nil
}

func deploymentManifest() clouddeploy.Manifest {
	return clouddeploy.Manifest{
		Schema:       clouddeploy.ManifestSchemaV1,
		SourceSHA:    "0123456789012345678901234567890123456789",
		ControlSHA:   "abcdefabcdefabcdefabcdefabcdefabcdefabcd",
		BundleDigest: digest('a'), IntentSHA256: digest('b'),
	}
}

func deploymentLease(now time.Time) clouddeploy.LeaseInventory {
	lease := clouddeploy.LeaseInventory{
		LeaseID: "lease-1", RequestID: "request-1", Repository: "WuKongIM/WuKongIM",
		Provider: "alibaba", Region: "cn-hangzhou", Zone: "cn-hangzhou-j",
		PlanDigest: digest('c'), SourceSHA: deploymentManifest().SourceSHA,
		BundleDigest: deploymentManifest().BundleDigest, State: "active", CreatedAt: now, ExpiresAt: now.Add(96 * time.Hour),
		Budget: clouddeploy.DeploymentBudget{Currency: "CNY", LimitMicros: clouddeploy.FormalBudgetHardMicros, OperationalStopMicros: clouddeploy.FormalBudgetStopMicros, EstimatedCostMicros: 100_000_000, LineItems: []clouddeploy.DeploymentBudgetLineItem{{Kind: "lease", Role: "all", Quantity: 1, CostMicros: 100_000_000}}},
		Resources: []clouddeploy.LeaseResource{
			{ID: "i-service-c", Kind: "instance", Role: "service", PrivateAddress: "10.42.0.13"},
			{ID: "i-load", Kind: "instance", Role: "load", PrivateAddress: "10.42.0.20"},
			{ID: "i-service-a", Kind: "instance", Role: "service", PrivateAddress: "10.42.0.11"},
			{ID: "i-service-b", Kind: "instance", Role: "service", PrivateAddress: "10.42.0.12"},
			{ID: "d-service-c", Kind: "data_disk", Role: "service", ParentID: "i-service-c", SizeBytes: 500_000_000_000},
			{ID: "d-load", Kind: "data_disk", Role: "load", ParentID: "i-load", SizeBytes: 200_000_000_000},
			{ID: "d-service-a", Kind: "data_disk", Role: "service", ParentID: "i-service-a", SizeBytes: 500_000_000_000},
			{ID: "d-service-b", Kind: "data_disk", Role: "service", ParentID: "i-service-b", SizeBytes: 500_000_000_000},
			{ID: "eip-load", Kind: "public_address", Role: "load", PublicAddress: "203.0.113.10"},
		},
	}
	return lease
}

func readySnapshot(plan clouddeploy.DeploymentPlan, now time.Time) clouddeploy.ReadinessSnapshot {
	hosts := make([]clouddeploy.HostSnapshot, 0, len(plan.Hosts))
	for _, planned := range plan.Hosts {
		units := []string{"node-exporter.service", "wukongim-process-metrics.service", "wukongim-evidence.timer"}
		if planned.Role == "load" {
			units = append(units, "wkbench-host-metrics.service", "wkbench-worker@1.service", "wkbench-worker@2.service", "wkbench-worker@3.service",
				"prometheus.service", "wkanalysis.service", "caddy.service")
		} else {
			units = append(units, "wukongim.service", "wkbench-host-metrics.service")
		}
		hosts = append(hosts, clouddeploy.HostSnapshot{
			Role: planned.Role, OperatingSystem: "ubuntu", OSVersion: "24.04", Architecture: "x86_64",
			BaseToolsAvailable: true, BundleDigest: plan.BundleDigest, DataDiskID: planned.DataDiskID,
			DataMount: "/var/lib/wukongim-cloud", DataFilesystemBytes: planned.MinimumDataFilesystemBytes,
			DataFreeBytes: planned.MinimumDataFilesystemBytes / 2, SystemFilesystemBytes: 40_000_000_000,
			SystemFreeBytes: 20_000_000_000, ClockOffsetMilliseconds: 0, ActiveUnits: units,
		})
	}
	return clouddeploy.ReadinessSnapshot{
		Schema: clouddeploy.SnapshotSchemaV1, DeploymentPlanDigest: plan.PlanDigest, ObservedAt: now,
		Hosts: hosts,
		Cluster: clouddeploy.ClusterSnapshot{ReadyNodes: 3, Members: 3, PhysicalHashSlots: 256,
			HealthySlotLeaders: 256, HealthySlotReplicaSets: 256, LogicalSlotGroups: 12,
			RuntimeConfigNodes: 3, SlotReplicas: 3, ChannelReplicas: 3},
		Load: clouddeploy.LoadSnapshot{ReadyWorkers: 3, PrometheusTargetsUp: 7, PrometheusTargetsWant: 7,
			WorkloadConfigValid: true, ProxyReady: true, ManagerReady: true, DemoReady: true, AnalysisReady: true},
	}
}

func digest(value byte) string {
	return "sha256:" + string(make([]byte, 0)) + repeatByte(value, 64)
}

func repeatByte(value byte, count int) string {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return string(result)
}
