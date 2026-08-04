package chatlifecycle

import (
	"fmt"
	"strings"
	"time"
)

const (
	formalWorkers            = 3
	formalLogicalSlotGroups  = 12
	formalHashSlots          = 256
	formalReplicas           = 3
	formalRuntimeSampleSize  = 1_200
	formalSyncLimit          = 500
	formalSyncMessageCount   = 20
	formalFilesystemBytes    = int64(1_000_000_000_000)
	formalGroupCatalogTotal  = 2_000
	formalVeryLargeMembers   = 100_000
	capacityStepDuration     = 30 * time.Minute
	formalCheckpointDuration = 72 * time.Hour
)

// DefaultConfig returns the validated formal configuration for lifecycle planning.
func DefaultConfig() Config {
	return FormalConfig()
}

// FormalConfig returns all reviewed formal workload, threshold, and staircase defaults.
func FormalConfig() Config {
	return Config{
		Identity: IdentityConfig{RunID: "formal-chat-lifecycle", Seed: 1, Profile: ProfileFormal},
		Workload: WorkloadConfig{
			Workers:            formalWorkers,
			OnlineUsers:        10_000,
			NewUsersPerDay:     250_000,
			SendRatePerSecond:  2_000,
			Traffic:            TrafficShareConfig{PersonPercent: 90, GroupPercent: 10},
			HotSet:             HotSetConfig{PersonChannels: 8_000, GroupChannels: 2_000},
			Topology:           TopologyConfig{LogicalSlotGroups: formalLogicalSlotGroups, HashSlots: formalHashSlots, SlotReplicas: formalReplicas, ChannelReplicas: formalReplicas},
			RuntimeSampling:    RuntimeSamplingConfig{Every: 10 * time.Minute, Size: formalRuntimeSampleSize},
			Sync:               SyncConfig{Version: 0, Limit: formalSyncLimit, MessageCount: formalSyncMessageCount},
			BurstCredit:        2 * time.Second,
			MaxGlobalBurst:     4_000,
			MaxChannelsPerNode: 50_000,
			Login:              LoginDistribution{NewPercent: 80, ReturningPercent: 20},
			Sessions: []DurationShare{
				{Percent: 25, Min: 5 * time.Minute, Max: 15 * time.Minute},
				{Percent: 50, Min: 15 * time.Minute, Max: 45 * time.Minute},
				{Percent: 20, Min: 45 * time.Minute, Max: 120 * time.Minute},
				{Percent: 5, Min: 2 * time.Hour, Max: 6 * time.Hour},
			},
			Lifecycle: []DurationShare{
				{Percent: 60}, {Percent: 25},
				{Percent: 10, Min: 20 * time.Minute, Max: 40 * time.Minute},
				{Percent: 5, Min: 2 * time.Hour, Max: 4 * time.Hour},
			},
			Payloads:        []PayloadShare{{Percent: 70, Bytes: 256}, {Percent: 25, Bytes: 1_024}, {Percent: 4, Bytes: 4_096}, {Percent: 1, Bytes: 16_384}},
			PersonDirection: PersonDirectionConfig{AlternatingPercent: 70, OneWayPercent: 30},
			Relationship: RelationshipConfig{
				InitialMessages: IntRange{Min: 2, Max: 8}, InitialMessageWindow: DurationRange{Min: 5 * time.Second, Max: 30 * time.Second},
				ReturningMessages: IntRange{Min: 2, Max: 5}, ReturningLast24hPercent: 80, ReturningOlderPercent: 20,
			},
			Retry:  RetryConfig{MaxCount: 3, Delays: []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}},
			Groups: GroupCatalogConfig{Small: 1_600, Medium: 300, Large: 99, VeryLarge: 1, VeryLargeMembers: formalVeryLargeMembers, FixedMembership: true, VeryLargeSendEvery: time.Minute},
		},
		Observation: ObservationConfig{Endpoints: []EndpointDeclaration{{Name: "service"}, {Name: "api"}, {Name: "gateway"}, {Name: "worker"}, {Name: "host_metrics"}}, Cadence: 10 * time.Second},
		Thresholds: ThresholdsConfig{
			MinimumDataFilesystemBytes: formalFilesystemBytes, DiskSafeStopFreePercent: 5,
			Correctness: CorrectnessThresholds{OverallFirstAttemptFailure: FailureRateLimit{MaxFailures: 1, PerAttempts: 10_000}, AnyMinuteFirstAttemptFailure: FailureRateLimit{MaxFailures: 1, PerAttempts: 1_000, Inclusive: true}},
			Latency:     LatencyThresholds{HotSendACK: LatencyLimit{P99: 200 * time.Millisecond, P999: time.Second}, Cold: LatencyLimit{P99: 2 * time.Second, P999: 5 * time.Second}, Sync: LatencyLimit{P99: time.Second, P999: 3 * time.Second}, SingleAnomaly: 10 * time.Second, SustainedBreachWindow: 5 * time.Minute},
			Resource:    ResourceThresholds{ForcedGCLiveHeapGrowthPercent: 5, ForcedGCLiveHeapWindow: 6 * time.Hour, GoroutineGrowthPercent: 5, GoroutineGrowthWindow: 24 * time.Hour},
			Cluster:     ClusterThresholds{HealthPollEvery: 5 * time.Second, UnhealthyFailAfter: 30 * time.Second, LeaderImbalancePercent: 20, LeaderImbalanceFor: 10 * time.Minute},
			Timeline:    TimelineThresholds{Warmup: 2 * time.Hour, Checkpoint: 24 * time.Hour, Final: formalCheckpointDuration},
		},
		Capacity: CapacityConfig{StartRatePerSecond: 2_000, StepPercent: 25, RefinePercent: 10, Step: CapacityStep{Stabilize: 10 * time.Minute, Measure: 20 * time.Minute}, RecoveryDuration: 30 * time.Minute},
	}
}

// Validate checks static deterministic configuration before planning or I/O.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Identity.RunID) == "" {
		return fieldError("identity.run_id", "is required")
	}
	if c.Identity.Seed == 0 {
		return fieldError("identity.seed", "must be nonzero")
	}
	if c.Identity.Profile != ProfileFormal && c.Identity.Profile != ProfileLocal && c.Identity.Profile != ProfileCapacity {
		return fieldError("identity.profile", "must be formal, local, or capacity")
	}
	if err := validateWorkload(c.Workload, c.Identity.Profile); err != nil {
		return err
	}
	if err := validateObservation(c.Observation); err != nil {
		return err
	}
	if err := validateThresholds(c.Thresholds, c.Identity.Profile); err != nil {
		return err
	}
	return validateCapacity(c.Capacity, c.Identity.Profile)
}

func validateWorkload(w WorkloadConfig, profile Profile) error {
	if w.Workers <= 0 {
		return fieldError("workload.workers", "must be greater than zero")
	}
	if w.OnlineUsers <= 0 {
		return fieldError("workload.online_users", "must be greater than zero")
	}
	if w.NewUsersPerDay <= 0 {
		return fieldError("workload.new_users_per_day", "must be greater than zero")
	}
	if w.SendRatePerSecond <= 0 {
		return fieldError("workload.send_rate_per_second", "must be greater than zero")
	}
	if w.HotSet.PersonChannels <= 0 {
		return fieldError("workload.hot_set.person_channels", "must be greater than zero")
	}
	if w.HotSet.GroupChannels <= 0 {
		return fieldError("workload.hot_set.group_channels", "must be greater than zero")
	}
	if w.Topology.LogicalSlotGroups <= 0 {
		return fieldError("workload.topology.logical_slot_groups", "must be greater than zero")
	}
	if w.Topology.HashSlots <= 0 {
		return fieldError("workload.topology.hash_slots", "must be greater than zero")
	}
	if w.Topology.SlotReplicas <= 0 {
		return fieldError("workload.topology.slot_replicas", "must be greater than zero")
	}
	if w.Topology.ChannelReplicas <= 0 {
		return fieldError("workload.topology.channel_replicas", "must be greater than zero")
	}
	if w.RuntimeSampling.Every <= 0 {
		return fieldError("workload.runtime_sampling.every", "must be greater than zero")
	}
	if w.RuntimeSampling.Size <= 0 {
		return fieldError("workload.runtime_sampling.size", "must be greater than zero")
	}
	if w.RuntimeSampling.Size > formalRuntimeSampleSize {
		return fieldError("workload.runtime_sampling.size", "must not exceed 1200")
	}
	if w.Sync.Limit <= 0 {
		return fieldError("workload.sync.limit", "must be greater than zero")
	}
	if w.Sync.MessageCount <= 0 {
		return fieldError("workload.sync.message_count", "must be greater than zero")
	}
	if w.BurstCredit <= 0 {
		return fieldError("workload.burst_credit", "must be greater than zero")
	}
	if w.MaxGlobalBurst <= 0 {
		return fieldError("workload.max_global_burst", "must be greater than zero")
	}
	if int64(w.BurstCredit)*int64(w.SendRatePerSecond) != int64(w.MaxGlobalBurst)*int64(time.Second) {
		return fieldError("workload.max_global_burst", "must equal burst_credit times send_rate_per_second")
	}
	if w.MaxChannelsPerNode <= 0 {
		return fieldError("workload.max_channels_per_node", "must be greater than zero")
	}
	if err := validatePercentPair("workload.traffic", w.Traffic.PersonPercent, w.Traffic.GroupPercent); err != nil {
		return err
	}
	if err := validatePercentPair("workload.login", w.Login.NewPercent, w.Login.ReturningPercent); err != nil {
		return err
	}
	if err := validateDurationShares("workload.sessions", w.Sessions, true); err != nil {
		return err
	}
	if err := validateDurationShares("workload.lifecycle", w.Lifecycle, false); err != nil {
		return err
	}
	if err := validatePayloads(w.Payloads); err != nil {
		return err
	}
	if err := validatePercentPair("workload.person_direction", w.PersonDirection.AlternatingPercent, w.PersonDirection.OneWayPercent); err != nil {
		return err
	}
	if err := validateIntRange("workload.relationship.initial_messages", w.Relationship.InitialMessages); err != nil {
		return err
	}
	if err := validateDurationRange("workload.relationship.initial_message_window", w.Relationship.InitialMessageWindow); err != nil {
		return err
	}
	if err := validateIntRange("workload.relationship.returning_messages", w.Relationship.ReturningMessages); err != nil {
		return err
	}
	if err := validatePercentPair("workload.relationship.returning_age", w.Relationship.ReturningLast24hPercent, w.Relationship.ReturningOlderPercent); err != nil {
		return err
	}
	if w.Retry.MaxCount < 0 {
		return fieldError("workload.retry.max_count", "must not be negative")
	}
	if w.Retry.MaxCount > 3 {
		return fieldError("workload.retry.max_count", "must not exceed 3")
	}
	if len(w.Retry.Delays) != 3 {
		return fieldError("workload.retry.delays", "must contain exactly 3 delays")
	}
	for i, delay := range w.Retry.Delays {
		if delay <= 0 {
			return fieldError(fmt.Sprintf("workload.retry.delays[%d]", i), "must be greater than zero")
		}
	}
	if w.Groups.Small < 0 || w.Groups.Medium < 0 || w.Groups.Large < 0 || w.Groups.VeryLarge < 0 {
		return fieldError("workload.groups", "counts must not be negative")
	}
	if w.Groups.Small+w.Groups.Medium+w.Groups.Large+w.Groups.VeryLarge != formalGroupCatalogTotal {
		return fieldError("workload.groups", "catalog counts must total 2000")
	}
	if w.Groups.VeryLarge != 1 {
		return fieldError("workload.groups.very_large", "must equal 1")
	}
	if w.Groups.VeryLargeMembers != formalVeryLargeMembers {
		return fieldError("workload.groups.very_large_members", "must equal 100000")
	}
	if !w.Groups.FixedMembership {
		return fieldError("workload.groups.fixed_membership", "must be true")
	}
	if w.Groups.VeryLargeSendEvery <= 0 {
		return fieldError("workload.groups.very_large_send_every", "must be greater than zero")
	}
	if profile == ProfileFormal {
		if w.Workers != formalWorkers {
			return fieldError("workload.workers", "must equal 3 for formal profile")
		}
		if w.Topology.LogicalSlotGroups != formalLogicalSlotGroups {
			return fieldError("workload.topology.logical_slot_groups", "must equal 12 for formal profile")
		}
		if w.Topology.HashSlots != formalHashSlots {
			return fieldError("workload.topology.hash_slots", "must equal 256 for formal profile")
		}
		if w.Topology.SlotReplicas != formalReplicas {
			return fieldError("workload.topology.slot_replicas", "must equal 3 for formal profile")
		}
		if w.Topology.ChannelReplicas != formalReplicas {
			return fieldError("workload.topology.channel_replicas", "must equal 3 for formal profile")
		}
		if w.RuntimeSampling.Size != formalRuntimeSampleSize {
			return fieldError("workload.runtime_sampling.size", "must equal 1200 for formal profile")
		}
		if w.Sync.Version != 0 {
			return fieldError("workload.sync.version", "must equal 0 for formal profile")
		}
		if w.Sync.Limit != formalSyncLimit {
			return fieldError("workload.sync.limit", "must equal 500 for formal profile")
		}
		if w.Sync.MessageCount != formalSyncMessageCount {
			return fieldError("workload.sync.message_count", "must equal 20 for formal profile")
		}
		if w.BurstCredit != 2*time.Second {
			return fieldError("workload.burst_credit", "must equal 2s for formal profile")
		}
		if w.MaxGlobalBurst != 4_000 {
			return fieldError("workload.max_global_burst", "must equal 4000 for formal profile")
		}
	}
	if w.Topology.LogicalSlotGroups != formalLogicalSlotGroups || w.Topology.HashSlots != formalHashSlots || w.Topology.SlotReplicas != formalReplicas || w.Topology.ChannelReplicas != formalReplicas {
		return fieldError("workload.topology", "must preserve 12 logical slot groups, 256 hash slots, and 3 replicas")
	}
	return nil
}

func validateObservation(o ObservationConfig) error {
	if o.Cadence <= 0 {
		return fieldError("observation.cadence", "must be greater than zero")
	}
	seen := make(map[string]int, len(o.Endpoints))
	for i, endpoint := range o.Endpoints {
		name := strings.TrimSpace(endpoint.Name)
		if name == "" {
			return fieldError(fmt.Sprintf("observation.endpoints[%d].name", i), "is required")
		}
		if previous, ok := seen[name]; ok {
			return fieldError(fmt.Sprintf("observation.endpoints[%d].name", i), fmt.Sprintf("duplicates observation.endpoints[%d].name", previous))
		}
		seen[name] = i
	}
	return nil
}

func validateThresholds(t ThresholdsConfig, profile Profile) error {
	if t.MinimumDataFilesystemBytes <= 0 {
		return fieldError("thresholds.minimum_data_filesystem_bytes", "must be greater than zero")
	}
	if t.DiskSafeStopFreePercent < 1 || t.DiskSafeStopFreePercent > 100 {
		return fieldError("thresholds.disk_safe_stop_free_percent", "must be in 1..100")
	}
	if err := validateFailureRate("thresholds.correctness.overall_first_attempt_failure", t.Correctness.OverallFirstAttemptFailure); err != nil {
		return err
	}
	if err := validateFailureRate("thresholds.correctness.any_minute_first_attempt_failure", t.Correctness.AnyMinuteFirstAttemptFailure); err != nil {
		return err
	}
	if err := validateLatencyLimit("thresholds.latency.hot_sendack", t.Latency.HotSendACK); err != nil {
		return err
	}
	if err := validateLatencyLimit("thresholds.latency.cold", t.Latency.Cold); err != nil {
		return err
	}
	if err := validateLatencyLimit("thresholds.latency.sync", t.Latency.Sync); err != nil {
		return err
	}
	if t.Latency.SingleAnomaly <= 0 {
		return fieldError("thresholds.latency.single_anomaly", "must be greater than zero")
	}
	if t.Latency.SustainedBreachWindow <= 0 {
		return fieldError("thresholds.latency.sustained_breach_window", "must be greater than zero")
	}
	if t.Resource.ForcedGCLiveHeapGrowthPercent < 0 || t.Resource.ForcedGCLiveHeapGrowthPercent > 100 {
		return fieldError("thresholds.resource.forced_gc_live_heap_growth_percent", "must be in 0..100")
	}
	if t.Resource.ForcedGCLiveHeapWindow <= 0 {
		return fieldError("thresholds.resource.forced_gc_live_heap_window", "must be greater than zero")
	}
	if t.Resource.GoroutineGrowthPercent < 0 || t.Resource.GoroutineGrowthPercent > 100 {
		return fieldError("thresholds.resource.goroutine_growth_percent", "must be in 0..100")
	}
	if t.Resource.GoroutineGrowthWindow <= 0 {
		return fieldError("thresholds.resource.goroutine_growth_window", "must be greater than zero")
	}
	if t.Cluster.HealthPollEvery <= 0 {
		return fieldError("thresholds.cluster.health_poll_every", "must be greater than zero")
	}
	if t.Cluster.UnhealthyFailAfter <= 0 {
		return fieldError("thresholds.cluster.unhealthy_fail_after", "must be greater than zero")
	}
	if t.Cluster.LeaderImbalancePercent < 0 || t.Cluster.LeaderImbalancePercent > 100 {
		return fieldError("thresholds.cluster.leader_imbalance_percent", "must be in 0..100")
	}
	if t.Cluster.LeaderImbalanceFor <= 0 {
		return fieldError("thresholds.cluster.leader_imbalance_for", "must be greater than zero")
	}
	if t.Timeline.Warmup <= 0 {
		return fieldError("thresholds.timeline.warmup", "must be greater than zero")
	}
	if t.Timeline.Checkpoint <= 0 {
		return fieldError("thresholds.timeline.checkpoint", "must be greater than zero")
	}
	if t.Timeline.Final <= 0 {
		return fieldError("thresholds.timeline.final", "must be greater than zero")
	}
	if t.Timeline.Checkpoint >= t.Timeline.Final {
		return fieldError("thresholds.timeline.checkpoint", "must be before final")
	}
	if profile == ProfileFormal {
		if t.MinimumDataFilesystemBytes < formalFilesystemBytes {
			return fieldError("thresholds.minimum_data_filesystem_bytes", "must be at least 1000000000000 for formal profile")
		}
		if t.Timeline.Warmup != 2*time.Hour {
			return fieldError("thresholds.timeline.warmup", "must equal 2h0m0s for formal profile")
		}
		if t.Timeline.Checkpoint != 24*time.Hour {
			return fieldError("thresholds.timeline.checkpoint", "must equal 24h0m0s for formal profile")
		}
		if t.Timeline.Final != formalCheckpointDuration {
			return fieldError("thresholds.timeline.final", "must equal 72h0m0s for formal profile")
		}
	}
	return nil
}

func validateCapacity(c CapacityConfig, profile Profile) error {
	if c.StartRatePerSecond <= 0 {
		return fieldError("capacity.start_rate_per_second", "must be greater than zero")
	}
	if c.StepPercent < 1 || c.StepPercent > 100 {
		return fieldError("capacity.step_percent", "must be in 1..100")
	}
	if c.RefinePercent < 1 || c.RefinePercent > 100 {
		return fieldError("capacity.refine_percent", "must be in 1..100")
	}
	if c.Step.Stabilize <= 0 {
		return fieldError("capacity.step.stabilize", "must be greater than zero")
	}
	if c.Step.Measure <= 0 {
		return fieldError("capacity.step.measure", "must be greater than zero")
	}
	if c.Step.Stabilize+c.Step.Measure != capacityStepDuration {
		return fieldError("capacity.step", "stabilize plus measure must equal 30m0s")
	}
	if c.RecoveryDuration <= 0 {
		return fieldError("capacity.recovery_duration", "must be greater than zero")
	}
	if profile != ProfileCapacity {
		return nil
	}
	if strings.TrimSpace(c.AgedCheckpoint.Reference) == "" {
		return fieldError("capacity.aged_checkpoint.reference", "is required in capacity profile")
	}
	if !c.AgedCheckpoint.Completed {
		return fieldError("capacity.aged_checkpoint.completed", "must be true in capacity profile")
	}
	if !c.AgedCheckpoint.Passed {
		return fieldError("capacity.aged_checkpoint.passed", "must be true in capacity profile")
	}
	if c.AgedCheckpoint.Duration < formalCheckpointDuration {
		return fieldError("capacity.aged_checkpoint.duration", "must be at least 72h0m0s in capacity profile")
	}
	return nil
}

func validatePercentPair(path string, first, second int) error {
	if first < 0 || first > 100 || second < 0 || second > 100 {
		return fieldError(path, "percentages must be in 0..100")
	}
	if first+second != 100 {
		return fieldError(path, "percentages must total 100")
	}
	return nil
}

func validateDurationShares(path string, shares []DurationShare, requireRange bool) error {
	if len(shares) == 0 {
		return fieldError(path, "must not be empty")
	}
	total := 0
	for i, share := range shares {
		if share.Percent < 0 || share.Percent > 100 {
			return fieldError(fmt.Sprintf("%s[%d].percent", path, i), "must be in 0..100")
		}
		total += share.Percent
		if share.Min == 0 && share.Max == 0 && !requireRange {
			continue
		}
		if share.Min <= 0 {
			return fieldError(fmt.Sprintf("%s[%d].min", path, i), "must be greater than zero")
		}
		if share.Max <= 0 {
			return fieldError(fmt.Sprintf("%s[%d].max", path, i), "must be greater than zero")
		}
		if share.Min > share.Max {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), "min must not exceed max")
		}
	}
	if total != 100 {
		return fieldError(path, "percentages must total 100")
	}
	return nil
}

func validatePayloads(shares []PayloadShare) error {
	if len(shares) == 0 {
		return fieldError("workload.payloads", "must not be empty")
	}
	total := 0
	for i, share := range shares {
		if share.Percent < 0 || share.Percent > 100 {
			return fieldError(fmt.Sprintf("workload.payloads[%d].percent", i), "must be in 0..100")
		}
		if share.Bytes <= 0 {
			return fieldError(fmt.Sprintf("workload.payloads[%d].bytes", i), "must be greater than zero")
		}
		total += share.Percent
	}
	if total != 100 {
		return fieldError("workload.payloads", "percentages must total 100")
	}
	return nil
}

func validateIntRange(path string, r IntRange) error {
	if r.Min <= 0 {
		return fieldError(path+".min", "must be greater than zero")
	}
	if r.Max <= 0 {
		return fieldError(path+".max", "must be greater than zero")
	}
	if r.Min > r.Max {
		return fieldError(path, "min must not exceed max")
	}
	return nil
}

func validateDurationRange(path string, r DurationRange) error {
	if r.Min <= 0 {
		return fieldError(path+".min", "must be greater than zero")
	}
	if r.Max <= 0 {
		return fieldError(path+".max", "must be greater than zero")
	}
	if r.Min > r.Max {
		return fieldError(path, "min must not exceed max")
	}
	return nil
}

func validateFailureRate(path string, limit FailureRateLimit) error {
	if limit.PerAttempts == 0 {
		return fieldError(path+".per_attempts", "must be greater than zero")
	}
	return nil
}

func validateLatencyLimit(path string, limit LatencyLimit) error {
	if limit.P99 <= 0 {
		return fieldError(path+".p99", "must be greater than zero")
	}
	if limit.P999 <= 0 {
		return fieldError(path+".p999", "must be greater than zero")
	}
	if limit.P99 > limit.P999 {
		return fieldError(path, "p99 must not exceed p999")
	}
	return nil
}

func fieldError(path, reason string) error { return fmt.Errorf("%s: %s", path, reason) }
