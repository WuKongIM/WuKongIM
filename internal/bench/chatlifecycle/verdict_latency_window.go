package chatlifecycle

import "time"

const (
	// Positive qualification needs enough counts to resolve the p99.9 rank;
	// it is not a statistical confidence claim and does not change breach limits.
	verdictLatencyQualificationMinimumSamples uint64 = 1_000
	verdictLatencyQualificationWindow                = 10 * time.Minute
	verdictLatencyQualificationSamples               = 128
	verdictLatencyQualificationMaxAge                = 5 * time.Second
)

// VerdictLatencyWindowSnapshot exposes bounded evidence for positive latency qualification.
// Arrays are ordered hot SENDACK, cold SENDACK, and full-sync attempts. Samples
// count valid cumulative histogram deltas ending within the statistics window.
// The independent numerical sustained-breach window remains five minutes.
type VerdictLatencyWindowSnapshot struct {
	WarmupEnd time.Time
	// BaselineAt is the first cumulative cut at or after latency warmup; it is never measured traffic.
	BaselineAt       time.Time
	LastAt           time.Time
	WindowStart      time.Time
	WindowEnd        time.Time
	Duration         time.Duration
	RequiredDuration time.Duration
	MaxAge           time.Duration
	MinimumSamples   uint64
	Samples          [3]uint64
	Qualified        [3]bool
}

// LatencyWindowEvidence returns the last statistics window without advancing its clock.
// Non-latency observations may make it stale but cannot refresh it or add samples.
func (v *VerdictEvaluator) LatencyWindowEvidence() VerdictLatencyWindowSnapshot {
	if v == nil {
		return VerdictLatencyWindowSnapshot{}
	}
	return v.latencyWindowEvidenceAt(v.last)
}

func (v *VerdictEvaluator) latencyWindowEvidenceAt(at time.Time) VerdictLatencyWindowSnapshot {
	evidence := VerdictLatencyWindowSnapshot{
		WarmupEnd:  v.start.Add(v.thresholds.Timeline.LatencyWarmup),
		BaselineAt: v.latencyBaselineAt, LastAt: v.latencyLastAt,
		WindowEnd: v.latencyLastAt, RequiredDuration: verdictLatencyQualificationWindow,
		MaxAge: verdictLatencyQualificationMaxAge, MinimumSamples: verdictLatencyQualificationMinimumSamples,
	}
	if v.latencyLastAt.IsZero() || v.latencyQualificationStart.IsZero() {
		return evidence
	}
	evidence.WindowStart = v.latencyQualificationStart
	if cutoff := v.latencyLastAt.Add(-verdictLatencyQualificationWindow); cutoff.After(evidence.WindowStart) {
		evidence.WindowStart = cutoff
	}
	evidence.Duration = evidence.WindowEnd.Sub(evidence.WindowStart)
	fresh := !at.IsZero() && !v.latencyLastAt.After(at) && at.Sub(v.latencyLastAt) <= verdictLatencyQualificationMaxAge
	valid := fresh && !v.latencyWindowInvalid && v.snapshot.Outcome != VerdictHarnessInvalid
	for index, window := range v.latencyQualification {
		_, evidence.Samples[index] = window.Sum()
		evidence.Qualified[index] = valid && evidence.Duration >= evidence.RequiredDuration && evidence.Samples[index] >= evidence.MinimumSamples
	}
	return evidence
}

// observeLatencyQualification retains only valid measured deltas. A cumulative
// interval longer than the statistics window cannot be split exactly, so its
// ending cut restarts positive qualification without suppressing negative breaches.
func (v *VerdictEvaluator) observeLatencyQualification(at time.Time, delta LatencyCounters) bool {
	if at.Sub(v.latencyLastAt) > verdictLatencyQualificationWindow {
		v.latencyQualificationStart = at
		for _, window := range v.latencyQualification {
			*window = CounterWindow{span: window.span, entries: window.entries}
		}
		return true
	}
	for index, counts := range [...]LatencyThresholdCounters{delta.Hot, delta.Cold, delta.Sync} {
		if err := v.latencyQualification[index].Add(at, 0, counts.Count); err != nil {
			return false
		}
	}
	return true
}
