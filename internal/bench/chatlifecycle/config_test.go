package chatlifecycle

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Identity.Profile != ProfileFormal {
		t.Fatalf("profile = %q, want %q", cfg.Identity.Profile, ProfileFormal)
	}
	if cfg.Identity.RunID == "" || cfg.Identity.Seed == 0 {
		t.Fatalf("identity = %+v, want non-empty run ID and nonzero seed", cfg.Identity)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
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
	assertDurationBuckets(t, "lifecycle", cfg.Workload.Lifecycle, []DurationShare{
		{Percent: 60},
		{Percent: 25},
		{Percent: 10, Min: 20 * time.Minute, Max: 40 * time.Minute},
		{Percent: 5, Min: 2 * time.Hour, Max: 4 * time.Hour},
	})
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
	if cfg.Thresholds.Correctness.OverallFirstAttemptFailure != (FailureRateLimit{MaxFailures: 1, PerAttempts: 10_000, Inclusive: false}) || cfg.Thresholds.Correctness.AnyMinuteFirstAttemptFailure != (FailureRateLimit{MaxFailures: 1, PerAttempts: 1_000, Inclusive: true}) {
		t.Fatalf("failure limits = %+v", cfg.Thresholds.Correctness)
	}
	if cfg.Thresholds.Latency.HotSendACK != (LatencyLimit{P99: 200 * time.Millisecond, P999: time.Second}) || cfg.Thresholds.Latency.Cold != (LatencyLimit{P99: 2 * time.Second, P999: 5 * time.Second}) || cfg.Thresholds.Latency.Sync != (LatencyLimit{P99: time.Second, P999: 3 * time.Second}) || cfg.Thresholds.Latency.SingleAnomaly != 10*time.Second || cfg.Thresholds.Latency.SustainedBreachWindow != 5*time.Minute {
		t.Fatalf("latency = %+v", cfg.Thresholds.Latency)
	}
	if cfg.Thresholds.Resource.ForcedGCLiveHeapGrowthPercent != 5 || cfg.Thresholds.Resource.ForcedGCLiveHeapWindow != 6*time.Hour || cfg.Thresholds.Resource.GoroutineGrowthPercent != 5 || cfg.Thresholds.Resource.GoroutineGrowthWindow != 24*time.Hour {
		t.Fatalf("resource = %+v", cfg.Thresholds.Resource)
	}
	if cfg.Capacity.StartRatePerSecond != 2_000 || cfg.Capacity.StepPercent != 25 || cfg.Capacity.RefinePercent != 10 || cfg.Capacity.Step.Stabilize != 10*time.Minute || cfg.Capacity.Step.Measure != 20*time.Minute || cfg.Capacity.RecoveryDuration != 30*time.Minute {
		t.Fatalf("capacity = %+v", cfg.Capacity)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"blank run ID", func(c *Config) { c.Identity.RunID = " " }, "identity.run_id: is required"},
		{"zero seed", func(c *Config) { c.Identity.Seed = 0 }, "identity.seed: must be nonzero"},
		{"formal workers", func(c *Config) { c.Workload.Workers = 2 }, "workload.workers: must equal 3 for formal profile"},
		{"nonpositive users", func(c *Config) { c.Workload.OnlineUsers = 0 }, "workload.online_users: must be greater than zero"},
		{"formal topology", func(c *Config) { c.Workload.Topology.HashSlots = 255 }, "workload.topology.hash_slots: must equal 256 for formal profile"},
		{"sample size", func(c *Config) { c.Workload.RuntimeSampling.Size = 1_201 }, "workload.runtime_sampling.size: must not exceed 1200"},
		{"sample cadence", func(c *Config) { c.Workload.RuntimeSampling.Every = 0 }, "workload.runtime_sampling.every: must be greater than zero"},
		{"login share", func(c *Config) { c.Workload.Login.NewPercent = 79 }, "workload.login: percentages must total 100"},
		{"session range", func(c *Config) { c.Workload.Sessions[0].Min = 0 }, "workload.sessions[0].min: must be greater than zero"},
		{"lifecycle share", func(c *Config) { c.Workload.Lifecycle[0].Percent = 59 }, "workload.lifecycle: percentages must total 100"},
		{"payload share", func(c *Config) { c.Workload.Payloads[0].Percent = 69 }, "workload.payloads: percentages must total 100"},
		{"person direction", func(c *Config) { c.Workload.PersonDirection.OneWayPercent = 31 }, "workload.person_direction: percentages must total 100"},
		{"relationship messages", func(c *Config) { c.Workload.Relationship.InitialMessages.Min = 9 }, "workload.relationship.initial_messages: min must not exceed max"},
		{"retry count", func(c *Config) { c.Workload.Retry.MaxCount = 4 }, "workload.retry.max_count: must not exceed 3"},
		{"retry delays", func(c *Config) { c.Workload.Retry.Delays = c.Workload.Retry.Delays[:2] }, "workload.retry.delays: must contain exactly 3 delays"},
		{"formal sync", func(c *Config) { c.Workload.Sync.Limit = 499 }, "workload.sync.limit: must equal 500 for formal profile"},
		{"burst cap", func(c *Config) { c.Workload.MaxGlobalBurst = 3_999 }, "workload.max_global_burst: must equal burst_credit times send_rate_per_second"},
		{"filesystem", func(c *Config) { c.Thresholds.MinimumDataFilesystemBytes = 999_999_999_999 }, "thresholds.minimum_data_filesystem_bytes: must be at least 1000000000000 for formal profile"},
		{"disk percent", func(c *Config) { c.Thresholds.DiskSafeStopFreePercent = 0 }, "thresholds.disk_safe_stop_free_percent: must be in 1..100"},
		{"timeline", func(c *Config) { c.Thresholds.Timeline.Checkpoint = c.Thresholds.Timeline.Final }, "thresholds.timeline.checkpoint: must be before final"},
		{"formal timeline", func(c *Config) { c.Thresholds.Timeline.Warmup = time.Hour }, "thresholds.timeline.warmup: must equal 2h0m0s for formal profile"},
		{"groups", func(c *Config) { c.Workload.Groups.Small-- }, "workload.groups: catalog counts must total 2000"},
		{"very large group", func(c *Config) { c.Workload.Groups.VeryLarge = 0; c.Workload.Groups.Small++ }, "workload.groups.very_large: must equal 1"},
		{"fixed membership", func(c *Config) { c.Workload.Groups.FixedMembership = false }, "workload.groups.fixed_membership: must be true"},
		{"blank endpoint", func(c *Config) { c.Observation.Endpoints[0].Name = " " }, "observation.endpoints[0].name: is required"},
		{"duplicate endpoint", func(c *Config) { c.Observation.Endpoints[1].Name = c.Observation.Endpoints[0].Name }, "observation.endpoints[1].name: duplicates observation.endpoints[0].name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConfigValidateCapacityAgedCheckpoint(t *testing.T) {
	validCheckpoint := AgedCheckpoint{Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour}
	tests := []struct {
		name       string
		checkpoint AgedCheckpoint
		want       string
	}{
		{"missing", AgedCheckpoint{}, "capacity.aged_checkpoint.reference: is required in capacity profile"},
		{"incomplete", AgedCheckpoint{Reference: "checkpoint", Passed: true, Duration: 72 * time.Hour}, "capacity.aged_checkpoint.completed: must be true in capacity profile"},
		{"failed", AgedCheckpoint{Reference: "checkpoint", Completed: true, Duration: 72 * time.Hour}, "capacity.aged_checkpoint.passed: must be true in capacity profile"},
		{"too short", AgedCheckpoint{Reference: "checkpoint", Completed: true, Passed: true, Duration: 71*time.Hour + 59*time.Minute}, "capacity.aged_checkpoint.duration: must be at least 72h0m0s in capacity profile"},
		{"valid", validCheckpoint, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Identity.Profile = ProfileCapacity
			cfg.Capacity.AgedCheckpoint = tt.checkpoint
			err := cfg.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConfigValidateCapacityStaircase(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"zero step", func(c *Config) { c.Capacity.StepPercent = 0 }, "capacity.step_percent: must be in 1..100"},
		{"zero stabilize", func(c *Config) { c.Capacity.Step.Stabilize = 0 }, "capacity.step.stabilize: must be greater than zero"},
		{"wrong step total", func(c *Config) { c.Capacity.Step.Measure = 19 * time.Minute }, "capacity.step: stabilize plus measure must equal 30m0s"},
		{"zero recovery", func(c *Config) { c.Capacity.RecoveryDuration = 0 }, "capacity.recovery_duration: must be greater than zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Identity.Profile = ProfileCapacity
			cfg.Capacity.AgedCheckpoint = AgedCheckpoint{Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour}
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func assertPercentPair(t *testing.T, name string, first, second, wantFirst, wantSecond int) {
	t.Helper()
	if first != wantFirst || second != wantSecond {
		t.Fatalf("%s = %d/%d, want %d/%d", name, first, second, wantFirst, wantSecond)
	}
}

func assertDurationBuckets(t *testing.T, name string, got, want []DurationShare) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s buckets = %+v, want %+v", name, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s buckets[%d] = %+v, want %+v", name, index, got[index], want[index])
		}
	}
}

func samePayloads(got, want []PayloadShare) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestValidationErrorsAreFieldPaths(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Identity.Profile = Profile("unknown")
	err := cfg.Validate()
	if err == nil || !strings.HasPrefix(err.Error(), "identity.profile:") {
		t.Fatalf("Validate() error = %v, want identity.profile field path", err)
	}
}
