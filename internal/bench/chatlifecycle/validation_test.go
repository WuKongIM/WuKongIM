package chatlifecycle

import (
	"strings"
	"testing"
	"time"
)

func TestLifecycleClassesRejectMissingAndWrongClassShapes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"missing class share", func(c *Config) { c.Profile = ProfileLocal; c.Workload.Lifecycle.Revisit.Percent = 0 }, "workload.lifecycle: percentages must total 100"},
		{"one-shot duration", func(c *Config) {
			c.Profile = ProfileLocal
			c.Workload.Lifecycle.OneShot.ActiveDuration = DurationRange{Min: time.Minute, Max: 2 * time.Minute}
		}, "workload.lifecycle.one_shot.active_duration: must be empty"},
		{"rotating missing duration", func(c *Config) {
			c.Profile = ProfileLocal
			c.Workload.Lifecycle.Rotating.ActiveDuration = DurationRange{}
		}, "workload.lifecycle.rotating.active_duration.min: must be greater than zero"},
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

func TestSessionBucketsRequirePositiveBoundedShares(t *testing.T) {
	zeroShare := LocalConfig()
	zeroShare.Workload.Sessions = append(zeroShare.Workload.Sessions, DurationShare{
		Percent: 0,
		Min:     time.Minute,
		Max:     2 * time.Minute,
	})
	wantZero := "workload.sessions[4].percent: must be in 1..100"
	if err := zeroShare.Validate(); err == nil || err.Error() != wantZero {
		t.Fatalf("Validate(zero share) error = %v, want %q", err, wantZero)
	}
	identity, err := NewIdentitySpace(zeroShare.RunID, zeroShare.Seed, uint64(zeroShare.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	if _, err := NewScheduleModel(identity, zeroShare.Workload); err == nil || err.Error() != wantZero {
		t.Fatalf("NewScheduleModel(zero share) error = %v, want %q", err, wantZero)
	}

	tooMany := LocalConfig()
	tooMany.Workload.Sessions = make([]DurationShare, 101)
	for index := range tooMany.Workload.Sessions {
		tooMany.Workload.Sessions[index] = DurationShare{Percent: 1, Min: time.Minute, Max: 2 * time.Minute}
	}
	wantTooMany := "workload.sessions: must contain at most 100 buckets"
	if err := tooMany.Validate(); err == nil || err.Error() != wantTooMany {
		t.Fatalf("Validate(101 buckets) error = %v, want %q", err, wantTooMany)
	}
	if _, err := NewScheduleModel(identity, tooMany.Workload); err == nil || err.Error() != wantTooMany {
		t.Fatalf("NewScheduleModel(101 buckets) error = %v, want %q", err, wantTooMany)
	}

	bounded := LocalConfig()
	bounded.Workload.Sessions = make([]DurationShare, 100)
	for index := range bounded.Workload.Sessions {
		bounded.Workload.Sessions[index] = DurationShare{Percent: 1, Min: time.Minute, Max: 2 * time.Minute}
	}
	if err := bounded.Validate(); err != nil {
		t.Fatalf("Validate(100 positive buckets) error = %v", err)
	}
	if _, err := NewScheduleModel(identity, bounded.Workload); err != nil {
		t.Fatalf("NewScheduleModel(100 positive buckets) error = %v", err)
	}
}

func TestFormalConfigRejectsApprovedDefaultMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		path   string
	}{
		{"workers", func(c *Config) { c.Workload.Workers = 4 }, "workload.workers"},
		{"online users", func(c *Config) { c.Workload.OnlineUsers = 10_001 }, "workload.online_users"},
		{"new users", func(c *Config) { c.Workload.NewUsersPerDay = 250_001 }, "workload.new_users_per_day"},
		{"send rate", func(c *Config) { c.Workload.SendRatePerSecond = 2_001; c.Workload.MaxGlobalBurst = 4_002 }, "workload.send_rate_per_second"},
		{"traffic person", func(c *Config) { c.Workload.Traffic.PersonPercent = 89; c.Workload.Traffic.GroupPercent = 11 }, "workload.traffic.person_percent"},
		{"hot person", func(c *Config) { c.Workload.HotSet.PersonChannels = 8_001 }, "workload.hot_set.person_channels"},
		{"sample cadence", func(c *Config) { c.Workload.RuntimeSampling.Every = 11 * time.Minute }, "workload.runtime_sampling.every"},
		{"sample size", func(c *Config) { c.Workload.RuntimeSampling.Size = 1_199 }, "workload.runtime_sampling.size"},
		{"max channels", func(c *Config) { c.Workload.MaxChannelsPerNode = 50_001 }, "workload.max_channels_per_node"},
		{"login", func(c *Config) { c.Workload.Login.NewPercent = 81; c.Workload.Login.ReturningPercent = 19 }, "workload.login.new_percent"},
		{"session", func(c *Config) { c.Workload.Sessions[0].Min = 6 * time.Minute }, "workload.sessions[0].min"},
		{"one-shot", func(c *Config) { c.Workload.Lifecycle.OneShot.Percent = 59; c.Workload.Lifecycle.Revisit.Percent = 26 }, "workload.lifecycle.one_shot.percent"},
		{"rotating", func(c *Config) { c.Workload.Lifecycle.Rotating.ActiveDuration.Min = 21 * time.Minute }, "workload.lifecycle.rotating.active_duration.min"},
		{"payload", func(c *Config) { c.Workload.Payloads[0].Bytes = 257 }, "workload.payloads[0].bytes"},
		{"direction", func(c *Config) {
			c.Workload.PersonDirection.AlternatingPercent = 69
			c.Workload.PersonDirection.OneWayPercent = 31
		}, "workload.person_direction.alternating_percent"},
		{"relationship", func(c *Config) { c.Workload.Relationship.ReturningMessages.Max = 4 }, "workload.relationship.returning_messages.max"},
		{"retry count", func(c *Config) { c.Workload.Retry.MaxCount = 2 }, "workload.retry.max_count"},
		{"retry bases", func(c *Config) {
			c.Workload.Retry.Delays[0], c.Workload.Retry.Delays[1] = c.Workload.Retry.Delays[1], c.Workload.Retry.Delays[0]
		}, "workload.retry.delays[0]"},
		{"groups", func(c *Config) { c.Workload.Groups.Small++; c.Workload.Groups.Medium-- }, "workload.groups.small"},
		{"group cadence", func(c *Config) { c.Workload.Groups.VeryLargeSendEvery = 2 * time.Minute }, "workload.groups.very_large_send_every"},
		{"filesystem", func(c *Config) { c.Thresholds.MinimumDataFilesystemBytes++ }, "thresholds.minimum_data_filesystem_bytes"},
		{"disk free", func(c *Config) { c.Thresholds.DiskSafeStopFreePercent = 6 }, "thresholds.disk_safe_stop_free_percent"},
		{"terminal sends", func(c *Config) { c.Thresholds.Correctness.TerminalSends = 1 }, "thresholds.correctness.terminal_sends"},
		{"overall rational", func(c *Config) { c.Thresholds.Correctness.OverallFirstAttemptFailure.MaxFailures = 2 }, "thresholds.correctness.overall_first_attempt_failure.max_failures"},
		{"hot latency", func(c *Config) { c.Thresholds.Latency.HotSendACK.P99 = 201 * time.Millisecond }, "thresholds.latency.hot_sendack.p99"},
		{"resource", func(c *Config) { c.Thresholds.Resource.GoroutineGrowthPercent = 6 }, "thresholds.resource.goroutine_growth_percent"},
		{"health", func(c *Config) { c.Thresholds.Cluster.HealthPollEvery = 6 * time.Second }, "thresholds.cluster.health_poll_every"},
		{"warmup", func(c *Config) { c.Thresholds.Timeline.Warmup = 3 * time.Hour }, "thresholds.timeline.warmup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FormalConfig()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.HasPrefix(err.Error(), tt.path+": must equal formal default") {
				t.Fatalf("Validate() error = %v, want formal default error at %s", err, tt.path)
			}
		})
	}
}

func TestGenericThresholdValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"negative correctness", func(c *Config) { c.Thresholds.Correctness.Losses = -1 }, "thresholds.correctness.losses: must not be negative"},
		{"zero rational denominator", func(c *Config) { c.Thresholds.Correctness.OverallFirstAttemptFailure.PerAttempts = 0 }, "thresholds.correctness.overall_first_attempt_failure.per_attempts: must be greater than zero"},
		{"rational numerator exceeds denominator", func(c *Config) { c.Thresholds.Correctness.OverallFirstAttemptFailure.MaxFailures = 10_001 }, "thresholds.correctness.overall_first_attempt_failure.max_failures: must not exceed per_attempts"},
		{"invalid rational operator", func(c *Config) { c.Thresholds.Correctness.OverallFirstAttemptFailure.Operator = Comparison("=") }, "thresholds.correctness.overall_first_attempt_failure.operator: must be < or <="},
		{"warmup ordering", func(c *Config) { c.Thresholds.Timeline.Warmup = c.Thresholds.Timeline.Checkpoint }, "thresholds.timeline.warmup: must be before checkpoint"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Profile = ProfileLocal
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"blank run ID", func(c *Config) { c.RunID = " " }, "run_id: is required"},
		{"zero seed", func(c *Config) { c.Seed = 0 }, "seed: must be nonzero"},
		{"formal workers", func(c *Config) { c.Workload.Workers = 2 }, "workload.workers: must equal formal default"},
		{"nonpositive users", func(c *Config) { c.Workload.OnlineUsers = 0 }, "workload.online_users: must be greater than zero"},
		{"formal topology", func(c *Config) { c.Workload.Topology.HashSlots = 255 }, "workload.topology: must preserve 12 logical slot groups, 256 hash slots, and 3 replicas"},
		{"sample size", func(c *Config) { c.Workload.RuntimeSampling.Size = 1_201 }, "workload.runtime_sampling.size: must not exceed 1200"},
		{"sample cadence", func(c *Config) { c.Workload.RuntimeSampling.Every = 0 }, "workload.runtime_sampling.every: must be greater than zero"},
		{"login share", func(c *Config) { c.Workload.Login.NewPercent = 79 }, "workload.login: percentages must total 100"},
		{"session range", func(c *Config) { c.Workload.Sessions[0].Min = 0 }, "workload.sessions[0].min: must be greater than zero"},
		{"lifecycle share", func(c *Config) { c.Workload.Lifecycle.OneShot.Percent = 59 }, "workload.lifecycle: percentages must total 100"},
		{"payload share", func(c *Config) { c.Workload.Payloads[0].Percent = 69 }, "workload.payloads: percentages must total 100"},
		{"person direction", func(c *Config) { c.Workload.PersonDirection.OneWayPercent = 31 }, "workload.person_direction: percentages must total 100"},
		{"relationship messages", func(c *Config) { c.Workload.Relationship.InitialMessages.Min = 9 }, "workload.relationship.initial_messages: min must not exceed max"},
		{"retry count", func(c *Config) { c.Workload.Retry.MaxCount = 4 }, "workload.retry.max_count: must not exceed 3"},
		{"retry delays", func(c *Config) { c.Workload.Retry.Delays = c.Workload.Retry.Delays[:2] }, "workload.retry.delays: must contain exactly 3 delays"},
		{"formal sync", func(c *Config) { c.Workload.Sync.Limit = 499 }, "workload.sync.limit: must equal 500 for real sync"},
		{"burst cap", func(c *Config) { c.Workload.MaxGlobalBurst = 3_999 }, "workload.max_global_burst: must equal burst_credit times send_rate_per_second"},
		{"filesystem", func(c *Config) { c.Thresholds.MinimumDataFilesystemBytes = 999_999_999_999 }, "thresholds.minimum_data_filesystem_bytes: must equal formal default"},
		{"disk percent", func(c *Config) { c.Thresholds.DiskSafeStopFreePercent = 0 }, "thresholds.disk_safe_stop_free_percent: must be in 1..100"},
		{"timeline", func(c *Config) { c.Thresholds.Timeline.Checkpoint = c.Thresholds.Timeline.Final }, "thresholds.timeline.checkpoint: must be before final"},
		{"formal timeline", func(c *Config) { c.Thresholds.Timeline.Warmup = time.Hour }, "thresholds.timeline.warmup: must equal formal default"},
		{"groups", func(c *Config) { c.Workload.Groups.Small-- }, "workload.groups: catalog counts must total 2000"},
		{"very large group", func(c *Config) { c.Workload.Groups.VeryLarge = 0; c.Workload.Groups.Small++ }, "workload.groups.very_large: must equal 1"},
		{"fixed membership", func(c *Config) { c.Workload.Groups.FixedMembership = false }, "workload.groups.fixed_membership: must be true"},
		{"blank endpoint", func(c *Config) { c.Observation.ServiceNodes[0].Name = " " }, "observation.service_nodes[0].name: is required"},
		{"duplicate endpoint", func(c *Config) { c.Observation.ServiceNodes[1].Name = c.Observation.ServiceNodes[0].Name }, "observation.service_nodes[1].name: duplicates observation.service_nodes[0].name"},
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

func TestValidationErrorsAreFieldPaths(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Profile = Profile("unknown")
	err := cfg.Validate()
	if err == nil || !strings.HasPrefix(err.Error(), "profile:") {
		t.Fatalf("Validate() error = %v, want profile field path", err)
	}
}
