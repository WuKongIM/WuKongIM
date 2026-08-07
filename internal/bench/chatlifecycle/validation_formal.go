package chatlifecycle

import (
	"fmt"
)

func validateFormalDefaults(c Config) error {
	expected := FormalConfig()
	w, wantW := c.Workload, expected.Workload
	if w.Workers != wantW.Workers {
		return formalError("workload.workers")
	}
	if w.OnlineUsers != wantW.OnlineUsers {
		return formalError("workload.online_users")
	}
	if w.NewUsersPerDay != wantW.NewUsersPerDay {
		return formalError("workload.new_users_per_day")
	}
	if w.SendRatePerSecond != wantW.SendRatePerSecond {
		return formalError("workload.send_rate_per_second")
	}
	if w.Traffic.PersonPercent != wantW.Traffic.PersonPercent {
		return formalError("workload.traffic.person_percent")
	}
	if w.Traffic.GroupPercent != wantW.Traffic.GroupPercent {
		return formalError("workload.traffic.group_percent")
	}
	if w.HotSet.PersonChannels != wantW.HotSet.PersonChannels {
		return formalError("workload.hot_set.person_channels")
	}
	if w.HotSet.GroupChannels != wantW.HotSet.GroupChannels {
		return formalError("workload.hot_set.group_channels")
	}
	if w.Topology.LogicalSlotGroups != wantW.Topology.LogicalSlotGroups {
		return formalError("workload.topology.logical_slot_groups")
	}
	if w.Topology.HashSlots != wantW.Topology.HashSlots {
		return formalError("workload.topology.hash_slots")
	}
	if w.Topology.SlotReplicas != wantW.Topology.SlotReplicas {
		return formalError("workload.topology.slot_replicas")
	}
	if w.Topology.ChannelReplicas != wantW.Topology.ChannelReplicas {
		return formalError("workload.topology.channel_replicas")
	}
	if w.RuntimeSampling.Every != wantW.RuntimeSampling.Every {
		return formalError("workload.runtime_sampling.every")
	}
	if w.RuntimeSampling.Size != wantW.RuntimeSampling.Size {
		return formalError("workload.runtime_sampling.size")
	}
	if w.Sync.Version != wantW.Sync.Version {
		return formalError("workload.sync.version")
	}
	if w.Sync.Limit != wantW.Sync.Limit {
		return formalError("workload.sync.limit")
	}
	if w.Sync.MessageCount != wantW.Sync.MessageCount {
		return formalError("workload.sync.message_count")
	}
	if w.BurstCredit != wantW.BurstCredit {
		return formalError("workload.burst_credit")
	}
	if w.MaxGlobalBurst != wantW.MaxGlobalBurst {
		return formalError("workload.max_global_burst")
	}
	if w.MaxChannelsPerNode != wantW.MaxChannelsPerNode {
		return formalError("workload.max_channels_per_node")
	}
	if w.Login.NewPercent != wantW.Login.NewPercent {
		return formalError("workload.login.new_percent")
	}
	if w.Login.ReturningPercent != wantW.Login.ReturningPercent {
		return formalError("workload.login.returning_percent")
	}
	if err := validateExactDurationShares("workload.sessions", w.Sessions, wantW.Sessions); err != nil {
		return err
	}
	if err := validateExactLifecycle(w.Lifecycle, wantW.Lifecycle); err != nil {
		return err
	}
	if err := validateExactPayloads(w.Payloads, wantW.Payloads); err != nil {
		return err
	}
	if w.PersonDirection.AlternatingPercent != wantW.PersonDirection.AlternatingPercent {
		return formalError("workload.person_direction.alternating_percent")
	}
	if w.PersonDirection.OneWayPercent != wantW.PersonDirection.OneWayPercent {
		return formalError("workload.person_direction.one_way_percent")
	}
	if err := validateExactIntRange("workload.relationship.initial_messages", w.Relationship.InitialMessages, wantW.Relationship.InitialMessages); err != nil {
		return err
	}
	if err := validateExactDurationRange("workload.relationship.initial_message_window", w.Relationship.InitialMessageWindow, wantW.Relationship.InitialMessageWindow); err != nil {
		return err
	}
	if err := validateExactIntRange("workload.relationship.returning_messages", w.Relationship.ReturningMessages, wantW.Relationship.ReturningMessages); err != nil {
		return err
	}
	if w.Relationship.ReturningLast24hPercent != wantW.Relationship.ReturningLast24hPercent {
		return formalError("workload.relationship.returning_last_24h_percent")
	}
	if w.Relationship.ReturningOlderPercent != wantW.Relationship.ReturningOlderPercent {
		return formalError("workload.relationship.returning_older_percent")
	}
	if w.Retry.MaxCount != wantW.Retry.MaxCount {
		return formalError("workload.retry.max_count")
	}
	for i := range wantW.Retry.Delays {
		if len(w.Retry.Delays) <= i || w.Retry.Delays[i] != wantW.Retry.Delays[i] {
			return formalError(fmt.Sprintf("workload.retry.delays[%d]", i))
		}
	}
	if w.Groups.Small != wantW.Groups.Small {
		return formalError("workload.groups.small")
	}
	if w.Groups.Medium != wantW.Groups.Medium {
		return formalError("workload.groups.medium")
	}
	if w.Groups.Large != wantW.Groups.Large {
		return formalError("workload.groups.large")
	}
	if w.Groups.VeryLarge != wantW.Groups.VeryLarge {
		return formalError("workload.groups.very_large")
	}
	if w.Groups.VeryLargeMembers != wantW.Groups.VeryLargeMembers {
		return formalError("workload.groups.very_large_members")
	}
	if w.Groups.FixedMembership != wantW.Groups.FixedMembership {
		return formalError("workload.groups.fixed_membership")
	}
	if w.Groups.VeryLargeSendEvery != wantW.Groups.VeryLargeSendEvery {
		return formalError("workload.groups.very_large_send_every")
	}
	if err := validateExactObservation(c.Observation, expected.Observation); err != nil {
		return err
	}
	if err := validateExactThresholds(c.Thresholds, expected.Thresholds); err != nil {
		return err
	}
	return nil
}

func validateExactDurationShares(path string, got, want []DurationShare) error {
	if len(got) != len(want) {
		return formalError(path)
	}
	for i := range want {
		if got[i].Percent != want[i].Percent {
			return formalError(fmt.Sprintf("%s[%d].percent", path, i))
		}
		if got[i].Min != want[i].Min {
			return formalError(fmt.Sprintf("%s[%d].min", path, i))
		}
		if got[i].Max != want[i].Max {
			return formalError(fmt.Sprintf("%s[%d].max", path, i))
		}
	}
	return nil
}

func validateExactLifecycle(got, want LifecycleDistribution) error {
	entries := []struct {
		path      string
		got, want LifecycleBucket
	}{
		{"workload.lifecycle.one_shot", got.OneShot, want.OneShot}, {"workload.lifecycle.revisit", got.Revisit, want.Revisit},
		{"workload.lifecycle.rotating", got.Rotating, want.Rotating}, {"workload.lifecycle.long", got.Long, want.Long},
	}
	for _, entry := range entries {
		if entry.got.Percent != entry.want.Percent {
			return formalError(entry.path + ".percent")
		}
		if err := validateExactDurationRange(entry.path+".active_duration", entry.got.ActiveDuration, entry.want.ActiveDuration); err != nil {
			return err
		}
	}
	return nil
}

func validateExactPayloads(got, want []PayloadShare) error {
	if len(got) != len(want) {
		return formalError("workload.payloads")
	}
	for i := range want {
		if got[i].Percent != want[i].Percent {
			return formalError(fmt.Sprintf("workload.payloads[%d].percent", i))
		}
		if got[i].Bytes != want[i].Bytes {
			return formalError(fmt.Sprintf("workload.payloads[%d].bytes", i))
		}
	}
	return nil
}

func validateExactIntRange(path string, got, want IntRange) error {
	if got.Min != want.Min {
		return formalError(path + ".min")
	}
	if got.Max != want.Max {
		return formalError(path + ".max")
	}
	return nil
}

func validateExactDurationRange(path string, got, want DurationRange) error {
	if got.Min != want.Min {
		return formalError(path + ".min")
	}
	if got.Max != want.Max {
		return formalError(path + ".max")
	}
	return nil
}

func validateExactObservation(got, want ObservationConfig) error {
	if got.Cadence != want.Cadence {
		return formalError("observation.cadence")
	}
	if len(got.ServiceNodes) != len(want.ServiceNodes) {
		return formalError("observation.service_nodes")
	}
	if len(got.Workers) != len(want.Workers) {
		return formalError("observation.workers")
	}
	if len(got.HostMetrics) != len(want.HostMetrics) {
		return formalError("observation.host_metrics")
	}
	if got.LoadHostMetrics.Name == "" || got.LoadHostMetrics.Address == "" {
		return formalError("observation.load_host_metrics")
	}
	if len(got.APIAddrs) != len(want.APIAddrs) {
		return formalError("observation.api_addrs")
	}
	if len(got.GatewayTCPAddrs) != len(want.GatewayTCPAddrs) {
		return formalError("observation.gateway_tcp_addrs")
	}
	return nil
}

func validateExactThresholds(got, want ThresholdsConfig) error {
	if got.MinimumDataFilesystemBytes != want.MinimumDataFilesystemBytes {
		return formalError("thresholds.minimum_data_filesystem_bytes")
	}
	if got.DiskSafeStopFreePercent != want.DiskSafeStopFreePercent {
		return formalError("thresholds.disk_safe_stop_free_percent")
	}
	correctness := []struct {
		path      string
		got, want int
	}{
		{"thresholds.correctness.terminal_sends", got.Correctness.TerminalSends, want.Correctness.TerminalSends}, {"thresholds.correctness.activation_rejections", got.Correctness.ActivationRejections, want.Correctness.ActivationRejections},
		{"thresholds.correctness.losses", got.Correctness.Losses, want.Correctness.Losses}, {"thresholds.correctness.duplicates", got.Correctness.Duplicates, want.Correctness.Duplicates},
		{"thresholds.correctness.corruptions", got.Correctness.Corruptions, want.Correctness.Corruptions}, {"thresholds.correctness.sequence_regressions", got.Correctness.SequenceRegressions, want.Correctness.SequenceRegressions},
	}
	for _, entry := range correctness {
		if entry.got != entry.want {
			return formalError(entry.path)
		}
	}
	if err := validateExactFailureRate("thresholds.correctness.overall_first_attempt_failure", got.Correctness.OverallFirstAttemptFailure, want.Correctness.OverallFirstAttemptFailure); err != nil {
		return err
	}
	if err := validateExactFailureRate("thresholds.correctness.any_minute_first_attempt_failure", got.Correctness.AnyMinuteFirstAttemptFailure, want.Correctness.AnyMinuteFirstAttemptFailure); err != nil {
		return err
	}
	if err := validateExactLatency("thresholds.latency.hot_sendack", got.Latency.HotSendACK, want.Latency.HotSendACK); err != nil {
		return err
	}
	if err := validateExactLatency("thresholds.latency.cold", got.Latency.Cold, want.Latency.Cold); err != nil {
		return err
	}
	if err := validateExactLatency("thresholds.latency.sync", got.Latency.Sync, want.Latency.Sync); err != nil {
		return err
	}
	if got.Latency.SingleAnomaly != want.Latency.SingleAnomaly {
		return formalError("thresholds.latency.single_anomaly")
	}
	if got.Latency.SustainedBreachWindow != want.Latency.SustainedBreachWindow {
		return formalError("thresholds.latency.sustained_breach_window")
	}
	if got.Resource.ForcedGCLiveHeapGrowthPercent != want.Resource.ForcedGCLiveHeapGrowthPercent {
		return formalError("thresholds.resource.forced_gc_live_heap_growth_percent")
	}
	if got.Resource.ForcedGCLiveHeapWindow != want.Resource.ForcedGCLiveHeapWindow {
		return formalError("thresholds.resource.forced_gc_live_heap_window")
	}
	if got.Resource.GoroutineGrowthPercent != want.Resource.GoroutineGrowthPercent {
		return formalError("thresholds.resource.goroutine_growth_percent")
	}
	if got.Resource.GoroutineGrowthWindow != want.Resource.GoroutineGrowthWindow {
		return formalError("thresholds.resource.goroutine_growth_window")
	}
	if got.Resource.HostCPUPercent != want.Resource.HostCPUPercent {
		return formalError("thresholds.resource.host_cpu_percent")
	}
	if got.Resource.HostMemoryPercent != want.Resource.HostMemoryPercent {
		return formalError("thresholds.resource.host_memory_percent")
	}
	if got.Resource.BoundedQueuePercent != want.Resource.BoundedQueuePercent {
		return formalError("thresholds.resource.bounded_queue_percent")
	}
	if got.Resource.SustainedSaturationWindow != want.Resource.SustainedSaturationWindow {
		return formalError("thresholds.resource.sustained_saturation_window")
	}
	if got.Resource.MinimumLoadFilesystemBytes != want.Resource.MinimumLoadFilesystemBytes {
		return formalError("thresholds.resource.minimum_load_filesystem_bytes")
	}
	if got.Resource.PrometheusSafeStopBytes != want.Resource.PrometheusSafeStopBytes {
		return formalError("thresholds.resource.prometheus_safe_stop_bytes")
	}
	if got.Cluster.HealthPollEvery != want.Cluster.HealthPollEvery {
		return formalError("thresholds.cluster.health_poll_every")
	}
	if got.Cluster.UnhealthyFailAfter != want.Cluster.UnhealthyFailAfter {
		return formalError("thresholds.cluster.unhealthy_fail_after")
	}
	if got.Cluster.MaxHotReplicaLagEntries != want.Cluster.MaxHotReplicaLagEntries {
		return formalError("thresholds.cluster.max_hot_replica_lag_entries")
	}
	if got.Cluster.LeaderImbalancePercent != want.Cluster.LeaderImbalancePercent {
		return formalError("thresholds.cluster.leader_imbalance_percent")
	}
	if got.Cluster.LeaderImbalanceFor != want.Cluster.LeaderImbalanceFor {
		return formalError("thresholds.cluster.leader_imbalance_for")
	}
	if got.Timeline.Warmup != want.Timeline.Warmup {
		return formalError("thresholds.timeline.warmup")
	}
	if got.Timeline.Checkpoint != want.Timeline.Checkpoint {
		return formalError("thresholds.timeline.checkpoint")
	}
	if got.Timeline.Final != want.Timeline.Final {
		return formalError("thresholds.timeline.final")
	}
	return nil
}

func validateExactFailureRate(path string, got, want FailureRateLimit) error {
	if got.MaxFailures != want.MaxFailures {
		return formalError(path + ".max_failures")
	}
	if got.PerAttempts != want.PerAttempts {
		return formalError(path + ".per_attempts")
	}
	if got.Operator != want.Operator {
		return formalError(path + ".operator")
	}
	return nil
}

func validateExactLatency(path string, got, want LatencyLimit) error {
	if got.P99 != want.P99 {
		return formalError(path + ".p99")
	}
	if got.P999 != want.P999 {
		return formalError(path + ".p999")
	}
	return nil
}

func formalError(path string) error { return fieldError(path, "must equal formal default") }
