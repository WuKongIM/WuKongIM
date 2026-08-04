package chatlifecycle

func validateThresholds(t ThresholdsConfig) error {
	if t.MinimumDataFilesystemBytes <= 0 {
		return fieldError("thresholds.minimum_data_filesystem_bytes", "must be greater than zero")
	}
	if t.DiskSafeStopFreePercent < 1 || t.DiskSafeStopFreePercent > 100 {
		return fieldError("thresholds.disk_safe_stop_free_percent", "must be in 1..100")
	}
	correctness := []struct {
		path  string
		value int
	}{
		{"thresholds.correctness.terminal_sends", t.Correctness.TerminalSends},
		{"thresholds.correctness.activation_rejections", t.Correctness.ActivationRejections},
		{"thresholds.correctness.losses", t.Correctness.Losses},
		{"thresholds.correctness.duplicates", t.Correctness.Duplicates},
		{"thresholds.correctness.corruptions", t.Correctness.Corruptions},
		{"thresholds.correctness.sequence_regressions", t.Correctness.SequenceRegressions},
	}
	for _, entry := range correctness {
		if entry.value < 0 {
			return fieldError(entry.path, "must not be negative")
		}
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
	if t.Timeline.Warmup >= t.Timeline.Checkpoint {
		return fieldError("thresholds.timeline.warmup", "must be before checkpoint")
	}
	if t.Timeline.Checkpoint >= t.Timeline.Final {
		return fieldError("thresholds.timeline.checkpoint", "must be before final")
	}
	return nil
}
