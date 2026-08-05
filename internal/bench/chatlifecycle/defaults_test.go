package chatlifecycle

import (
	"reflect"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Profile != ProfileFormal {
		t.Fatalf("profile = %q, want %q", cfg.Profile, ProfileFormal)
	}
	if cfg.RunID == "" || cfg.Seed == 0 {
		t.Fatalf("identity = run_id %q, seed %d; want non-empty run ID and nonzero seed", cfg.RunID, cfg.Seed)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLocalConfigValid(t *testing.T) {
	cfg := LocalConfig()

	if cfg.RunID != "local-chat-lifecycle" || cfg.Seed != 1 || cfg.Profile != ProfileLocal || cfg.Mode != ModeSoak {
		t.Fatalf("identity/profile/mode = %q/%d/%q/%q", cfg.RunID, cfg.Seed, cfg.Profile, cfg.Mode)
	}
	if cfg.Workload.Workers != 3 || cfg.Workload.OnlineUsers != 100 || cfg.Workload.NewUsersPerDay != 1_000 || cfg.Workload.SendRatePerSecond != 100 {
		t.Fatalf("core workload = %+v", cfg.Workload)
	}
	if cfg.Workload.HotSet != (HotSetConfig{PersonChannels: 80, GroupChannels: 20}) {
		t.Fatalf("hot set = %+v", cfg.Workload.HotSet)
	}
	if cfg.Workload.Topology != (TopologyConfig{LogicalSlotGroups: 12, HashSlots: 256, SlotReplicas: 3, ChannelReplicas: 3}) {
		t.Fatalf("topology = %+v", cfg.Workload.Topology)
	}
	if cfg.Workload.RuntimeSampling != (RuntimeSamplingConfig{Every: time.Minute, Size: 12}) {
		t.Fatalf("runtime sampling = %+v", cfg.Workload.RuntimeSampling)
	}
	if cfg.Workload.Sync != (SyncConfig{Version: 0, Limit: 500, MessageCount: 20}) {
		t.Fatalf("sync = %+v", cfg.Workload.Sync)
	}
	if cfg.Workload.BurstCredit != 2*time.Second || cfg.Workload.MaxGlobalBurst != 200 || cfg.Workload.MaxChannelsPerNode != 500 {
		t.Fatalf("burst/channel limits = %+v", cfg.Workload)
	}
	if cfg.Workload.Groups != (GroupCatalogConfig{Small: 16, Medium: 3, VeryLarge: 1, VeryLargeMembers: 1_000, FixedMembership: true, VeryLargeSendEvery: time.Minute}) {
		t.Fatalf("groups = %+v", cfg.Workload.Groups)
	}
	wantObservation := ObservationConfig{
		ServiceNodes: []EndpointDeclaration{{Name: "local-service-1", Address: "http://127.0.0.1:15001"}, {Name: "local-service-2", Address: "http://127.0.0.1:15002"}, {Name: "local-service-3", Address: "http://127.0.0.1:15003"}},
		Workers:      []EndpointDeclaration{{Name: "local-worker-1", Address: "http://127.0.0.1:19091"}, {Name: "local-worker-2", Address: "http://127.0.0.1:19092"}, {Name: "local-worker-3", Address: "http://127.0.0.1:19093"}},
		HostMetrics: []EndpointDeclaration{
			{Name: "local-host-metrics-1", Address: "http://127.0.0.1:19101", Mountpoint: "/var/lib/wukongim-1", Device: "/dev/local-data-1"},
			{Name: "local-host-metrics-2", Address: "http://127.0.0.1:19102", Mountpoint: "/var/lib/wukongim-2", Device: "/dev/local-data-2"},
			{Name: "local-host-metrics-3", Address: "http://127.0.0.1:19103", Mountpoint: "/var/lib/wukongim-3", Device: "/dev/local-data-3"},
		},
		APIAddrs:        []string{"http://127.0.0.1:15011", "http://127.0.0.1:15012", "http://127.0.0.1:15013"},
		GatewayTCPAddrs: []string{"127.0.0.1:15101", "127.0.0.1:15102", "127.0.0.1:15103"},
		Cadence:         5 * time.Second,
	}
	if !reflect.DeepEqual(cfg.Observation, wantObservation) {
		t.Fatalf("observation = %+v, want %+v", cfg.Observation, wantObservation)
	}
	if cfg.Thresholds.MinimumDataFilesystemBytes != 10_000_000_000 || cfg.Thresholds.Timeline != (TimelineThresholds{Warmup: 10 * time.Minute, Checkpoint: 20 * time.Minute, Final: 30 * time.Minute}) {
		t.Fatalf("local thresholds = %+v", cfg.Thresholds)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigUsesTopLevelIdentityAndMode(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.RunID == "" || cfg.Seed == 0 {
		t.Fatalf("top-level identity = run_id %q, seed %d", cfg.RunID, cfg.Seed)
	}
	if cfg.Profile != ProfileFormal || cfg.Mode != ModeSoak {
		t.Fatalf("top-level profile/mode = %q/%q", cfg.Profile, cfg.Mode)
	}
}

func TestLifecycleDistributionUsesNamedClasses(t *testing.T) {
	cfg := DefaultConfig()
	lifecycle := cfg.Workload.Lifecycle
	if lifecycle.OneShot.Percent != 60 || lifecycle.Revisit.Percent != 25 {
		t.Fatalf("one-shot/revisit = %+v/%+v", lifecycle.OneShot, lifecycle.Revisit)
	}
	if lifecycle.Rotating != (LifecycleBucket{Percent: 10, ActiveDuration: DurationRange{Min: 20 * time.Minute, Max: 40 * time.Minute}}) {
		t.Fatalf("rotating = %+v", lifecycle.Rotating)
	}
	if lifecycle.Long != (LifecycleBucket{Percent: 5, ActiveDuration: DurationRange{Min: 2 * time.Hour, Max: 4 * time.Hour}}) {
		t.Fatalf("long = %+v", lifecycle.Long)
	}
}

func TestFormalConfigDefaults(t *testing.T) {
	cfg := FormalConfig()

	if cfg.Workload.Workers != 3 || cfg.Workload.OnlineUsers != 10_000 || cfg.Workload.NewUsersPerDay != 250_000 || cfg.Workload.SendRatePerSecond != 2_000 {
		t.Fatalf("core workload = %+v", cfg.Workload)
	}
	if cfg.Workload.Traffic.PersonPercent != 90 || cfg.Workload.Traffic.GroupPercent != 10 || cfg.Workload.HotSet.PersonChannels != 8_000 || cfg.Workload.HotSet.GroupChannels != 2_000 {
		t.Fatalf("traffic/hot set = %+v / %+v", cfg.Workload.Traffic, cfg.Workload.HotSet)
	}
	if cfg.Workload.Topology != (TopologyConfig{LogicalSlotGroups: 12, HashSlots: 256, SlotReplicas: 3, ChannelReplicas: 3}) {
		t.Fatalf("topology = %+v", cfg.Workload.Topology)
	}
	if cfg.Workload.RuntimeSampling.Every != 10*time.Minute || cfg.Workload.RuntimeSampling.Size != 1_200 {
		t.Fatalf("runtime sampling = %+v", cfg.Workload.RuntimeSampling)
	}
	if cfg.Workload.Sync != (SyncConfig{Version: 0, Limit: 500, MessageCount: 20}) {
		t.Fatalf("sync = %+v", cfg.Workload.Sync)
	}
	if cfg.Workload.BurstCredit != 2*time.Second || cfg.Workload.MaxGlobalBurst != 4_000 || cfg.Workload.MaxChannelsPerNode != 50_000 {
		t.Fatalf("burst/channel limits = %+v", cfg.Workload)
	}

	assertPercentPair(t, "login", cfg.Workload.Login.NewPercent, cfg.Workload.Login.ReturningPercent, 80, 20)
	assertDurationBuckets(t, "session", cfg.Workload.Sessions, []DurationShare{
		{Percent: 25, Min: 5 * time.Minute, Max: 15 * time.Minute},
		{Percent: 50, Min: 15 * time.Minute, Max: 45 * time.Minute},
		{Percent: 20, Min: 45 * time.Minute, Max: 120 * time.Minute},
		{Percent: 5, Min: 2 * time.Hour, Max: 6 * time.Hour},
	})
	if cfg.Workload.Lifecycle != (LifecycleDistribution{OneShot: LifecycleBucket{Percent: 60}, Revisit: LifecycleBucket{Percent: 25}, Rotating: LifecycleBucket{Percent: 10, ActiveDuration: DurationRange{Min: 20 * time.Minute, Max: 40 * time.Minute}}, Long: LifecycleBucket{Percent: 5, ActiveDuration: DurationRange{Min: 2 * time.Hour, Max: 4 * time.Hour}}}) {
		t.Fatalf("lifecycle = %+v", cfg.Workload.Lifecycle)
	}
	if got, want := cfg.Workload.Payloads, []PayloadShare{{Percent: 70, Bytes: 256}, {Percent: 25, Bytes: 1_024}, {Percent: 4, Bytes: 4_096}, {Percent: 1, Bytes: 16_384}}; !samePayloads(got, want) {
		t.Fatalf("payloads = %+v, want %+v", got, want)
	}
	assertPercentPair(t, "person direction", cfg.Workload.PersonDirection.AlternatingPercent, cfg.Workload.PersonDirection.OneWayPercent, 70, 30)
	if cfg.Workload.Relationship.InitialMessages != (IntRange{Min: 2, Max: 8}) || cfg.Workload.Relationship.InitialMessageWindow != (DurationRange{Min: 5 * time.Second, Max: 30 * time.Second}) || cfg.Workload.Relationship.ReturningMessages != (IntRange{Min: 2, Max: 5}) {
		t.Fatalf("relationship = %+v", cfg.Workload.Relationship)
	}
	assertPercentPair(t, "returning age", cfg.Workload.Relationship.ReturningLast24hPercent, cfg.Workload.Relationship.ReturningOlderPercent, 80, 20)
	if cfg.Workload.Retry.MaxCount != 3 || len(cfg.Workload.Retry.Delays) != 3 || cfg.Workload.Retry.Delays[0] != 100*time.Millisecond || cfg.Workload.Retry.Delays[1] != 500*time.Millisecond || cfg.Workload.Retry.Delays[2] != 2*time.Second {
		t.Fatalf("retry = %+v", cfg.Workload.Retry)
	}
	if cfg.Workload.Groups != (GroupCatalogConfig{Small: 1_600, Medium: 300, Large: 99, VeryLarge: 1, VeryLargeMembers: 100_000, FixedMembership: true, VeryLargeSendEvery: time.Minute}) {
		t.Fatalf("groups = %+v", cfg.Workload.Groups)
	}

	if cfg.Thresholds.MinimumDataFilesystemBytes != 1_000_000_000_000 || cfg.Thresholds.DiskSafeStopFreePercent != 5 {
		t.Fatalf("disk thresholds = %+v", cfg.Thresholds)
	}
	if cfg.Thresholds.Cluster.HealthPollEvery != 5*time.Second || cfg.Thresholds.Cluster.UnhealthyFailAfter != 30*time.Second || cfg.Thresholds.Cluster.LeaderImbalancePercent != 20 || cfg.Thresholds.Cluster.LeaderImbalanceFor != 10*time.Minute {
		t.Fatalf("cluster thresholds = %+v", cfg.Thresholds.Cluster)
	}
	if cfg.Thresholds.Timeline.Warmup != 2*time.Hour || cfg.Thresholds.Timeline.Checkpoint != 24*time.Hour || cfg.Thresholds.Timeline.Final != 72*time.Hour {
		t.Fatalf("timeline = %+v", cfg.Thresholds.Timeline)
	}
	if cfg.Thresholds.Correctness.TerminalSends != 0 || cfg.Thresholds.Correctness.ActivationRejections != 0 || cfg.Thresholds.Correctness.Losses != 0 || cfg.Thresholds.Correctness.Duplicates != 0 || cfg.Thresholds.Correctness.Corruptions != 0 || cfg.Thresholds.Correctness.SequenceRegressions != 0 {
		t.Fatalf("correctness = %+v", cfg.Thresholds.Correctness)
	}
	if cfg.Thresholds.Correctness.OverallFirstAttemptFailure != (FailureRateLimit{MaxFailures: 1, PerAttempts: 10_000, Operator: ComparisonLessThan}) || cfg.Thresholds.Correctness.AnyMinuteFirstAttemptFailure != (FailureRateLimit{MaxFailures: 1, PerAttempts: 1_000, Operator: ComparisonLessOrEqual}) {
		t.Fatalf("failure limits = %+v", cfg.Thresholds.Correctness)
	}
	if cfg.Thresholds.Latency.HotSendACK != (LatencyLimit{P99: 200 * time.Millisecond, P999: time.Second}) || cfg.Thresholds.Latency.Cold != (LatencyLimit{P99: 2 * time.Second, P999: 5 * time.Second}) || cfg.Thresholds.Latency.Sync != (LatencyLimit{P99: time.Second, P999: 3 * time.Second}) || cfg.Thresholds.Latency.SingleAnomaly != 10*time.Second || cfg.Thresholds.Latency.SustainedBreachWindow != 5*time.Minute {
		t.Fatalf("latency = %+v", cfg.Thresholds.Latency)
	}
	if cfg.Thresholds.Resource.ForcedGCLiveHeapGrowthPercent != 5 || cfg.Thresholds.Resource.ForcedGCLiveHeapWindow != 6*time.Hour || cfg.Thresholds.Resource.GoroutineGrowthPercent != 5 || cfg.Thresholds.Resource.GoroutineGrowthWindow != 24*time.Hour {
		t.Fatalf("resource = %+v", cfg.Thresholds.Resource)
	}
	if cfg.Capacity.StartRatePerSecond != 2_000 || cfg.Capacity.RecoveryRatePerSecond != 2_000 || cfg.Capacity.StepPercent != 25 || cfg.Capacity.RefinePercent != 10 || cfg.Capacity.Step.Stabilize != 10*time.Minute || cfg.Capacity.Step.Measure != 20*time.Minute || cfg.Capacity.RecoveryDuration != 30*time.Minute {
		t.Fatalf("capacity = %+v", cfg.Capacity)
	}
}
