package chatlifecycle

import "time"

const (
	verdictLatencyGateSchema       = "wukongim/chat-lifecycle-latency-gate/v1"
	verdictLatencyGateNotEvaluated = "not_evaluated"
	verdictLatencyGateCurrent      = "current"
	verdictLatencyGateStale        = "stale"
	verdictLatencyGateInvalid      = "invalid"
)

// VerdictLatencyGateClass copies one native negative-window reducer. Its two
// sums remain separate because an invalid observation may have updated only one.
// BreachSinceElapsed is nil when no continuous breach is active.
type VerdictLatencyGateClass struct {
	Operation          LatencyOperation    `json:"operation"`
	P99Limit           time.Duration       `json:"p99_limit"`
	P999Limit          time.Duration       `json:"p999_limit"`
	P99Count           uint64              `json:"p99_count"`
	P99Above           uint64              `json:"p99_above"`
	P999Count          uint64              `json:"p999_count"`
	P999Above          uint64              `json:"p999_above"`
	P99Samples         int                 `json:"p99_samples"`
	P999Samples        int                 `json:"p999_samples"`
	Breached           bool                `json:"breached"`
	BreachSinceElapsed *time.Duration      `json:"breach_since_elapsed"`
	BreachAttribution  CapacityAttribution `json:"breach_attribution"`
}

// VerdictLatencyGateSnapshot is fixed-size evidence of the actual native gate,
// independent of positive ten-minute qualification and cumulative histograms.
// Status and LatencyAge describe EvaluatedElapsed, not report publication time;
// publication age is Report.Window.Elapsed minus EvaluatedElapsed. A terminal
// snapshot is frozen and never refreshed by later drain or cleanup observations.
type VerdictLatencyGateSnapshot struct {
	SchemaVersion           string                     `json:"schema_version"`
	Status                  string                     `json:"status"`
	EvaluatedElapsed        time.Duration              `json:"evaluated_elapsed"`
	LastLatencyElapsed      time.Duration              `json:"last_latency_elapsed"`
	LatencyAge              time.Duration              `json:"latency_age"`
	TerminalFrozen          bool                       `json:"terminal_frozen"`
	BaselineElapsed         time.Duration              `json:"baseline_elapsed"`
	WindowStartElapsed      time.Duration              `json:"window_start_elapsed"`
	WindowEndElapsed        time.Duration              `json:"window_end_elapsed"`
	WarmupDuration          time.Duration              `json:"warmup_duration"`
	WindowDuration          time.Duration              `json:"window_duration"`
	MaxAge                  time.Duration              `json:"max_age"`
	P99FractionDenominator  uint64                     `json:"p99_fraction_denominator"`
	P999FractionDenominator uint64                     `json:"p999_fraction_denominator"`
	WindowCapacity          int                        `json:"window_capacity"`
	Classes                 [3]VerdictLatencyGateClass `json:"classes"`
}

// latencyGateEvidence reads only owner state and existing native timestamps.
// It neither advances a window nor samples a clock or changes the verdict.
func (v *VerdictEvaluator) latencyGateEvidence() *VerdictLatencyGateSnapshot {
	if v.frozenLatencyGate != nil {
		return cloneVerdictLatencyGate(v.frozenLatencyGate)
	}
	return v.buildLatencyGateEvidence(false)
}

func (v *VerdictEvaluator) buildLatencyGateEvidence(terminal bool) *VerdictLatencyGateSnapshot {
	evidence := &VerdictLatencyGateSnapshot{
		SchemaVersion: verdictLatencyGateSchema, Status: verdictLatencyGateNotEvaluated,
		TerminalFrozen: terminal, WarmupDuration: v.thresholds.Timeline.LatencyWarmup,
		WindowDuration: v.thresholds.Latency.SustainedBreachWindow,
		MaxAge:         verdictLatencyQualificationMaxAge, WindowCapacity: verdictLatencyWindowSamples,
		P99FractionDenominator: 100, P999FractionDenominator: 1_000,
	}
	if !v.last.IsZero() {
		evidence.EvaluatedElapsed = v.last.Sub(v.start)
	}
	if !v.latencyLastAt.IsZero() {
		evidence.BaselineElapsed = v.latencyBaselineAt.Sub(v.start)
		evidence.LastLatencyElapsed = v.latencyLastAt.Sub(v.start)
		evidence.LatencyAge = evidence.EvaluatedElapsed - evidence.LastLatencyElapsed
		evidence.WindowEndElapsed = evidence.LastLatencyElapsed
		evidence.WindowStartElapsed = max(evidence.BaselineElapsed, evidence.WindowEndElapsed-evidence.WindowDuration)
	}
	operations := [...]LatencyOperation{LatencyHotSendACK, LatencyColdSendACK, LatencyFullSync}
	limits := [...]LatencyLimit{v.thresholds.Latency.HotSendACK, v.thresholds.Latency.Cold, v.thresholds.Latency.Sync}
	measured := false
	for index, state := range v.latency {
		row := &evidence.Classes[index]
		row.Operation, row.P99Limit, row.P999Limit = operations[index], limits[index].P99, limits[index].P999
		row.P99Above, row.P99Count = state.p99.Sum()
		row.P999Above, row.P999Count = state.p999.Sum()
		row.P99Samples, row.P999Samples = state.p99.Len(), state.p999.Len()
		measured = measured || row.P99Samples > 0 || row.P999Samples > 0
		row.Breached = state.breachSince != nil
		row.BreachAttribution = state.breachAttribution
		if state.breachSince != nil {
			elapsed := state.breachSince.Sub(v.start)
			row.BreachSinceElapsed = &elapsed
		}
	}
	switch {
	case v.latencyWindowInvalid || v.snapshot.Outcome == VerdictHarnessInvalid:
		evidence.Status = verdictLatencyGateInvalid
	case measured && evidence.LatencyAge > evidence.MaxAge:
		evidence.Status = verdictLatencyGateStale
	case measured:
		evidence.Status = verdictLatencyGateCurrent
	}
	return evidence
}

// cloneVerdictLatencyGate detaches every optional pointer from earlier cuts.
func cloneVerdictLatencyGate(source *VerdictLatencyGateSnapshot) *VerdictLatencyGateSnapshot {
	if source == nil {
		return nil
	}
	clone := *source
	for index := range clone.Classes {
		if since := clone.Classes[index].BreachSinceElapsed; since != nil {
			elapsed := *since
			clone.Classes[index].BreachSinceElapsed = &elapsed
		}
	}
	return &clone
}
