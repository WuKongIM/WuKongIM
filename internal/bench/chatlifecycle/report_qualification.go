package chatlifecycle

import "time"

// ReportQualificationScope states which protocol generated the evidence. It
// never asserts that this process ran on Linux or completed external acceptance.
type ReportQualificationScope string

const (
	ReportScopeLocalDiagnostic ReportQualificationScope = "local_diagnostic"
	ReportScopeRehearsal       ReportQualificationScope = "rehearsal"
	ReportScopeFormalProtocol  ReportQualificationScope = "formal_protocol"
)

// ReportQualificationEvidence separates protocol scope from release acceptance.
// Non-local protocols still require the independent fixed-host Linux evidence.
type ReportQualificationEvidence struct {
	Scope                         ReportQualificationScope `json:"scope"`
	ExternalLinuxEvidenceRequired bool                     `json:"external_linux_evidence_required"`
}

// ReportLatencyQualificationStatus is the closed positive-evidence vocabulary.
type ReportLatencyQualificationStatus string

const (
	ReportLatencyNotEvaluated ReportLatencyQualificationStatus = "not_evaluated"
	ReportLatencyInsufficient ReportLatencyQualificationStatus = "insufficient"
	ReportLatencySufficient   ReportLatencyQualificationStatus = "sufficient"
)

// ReportLatencyQualificationWindow persists the reducer's actual bounded window.
// Sample and qualified arrays are ordered hot SENDACK, cold SENDACK, full-sync.
// Samples preserve the existing histogram attempt semantics and are not a
// confidence interval. Elapsed positions share the process-start origin and remain authoritative after JSON serialization; wall timestamps are audit fields only.
type ReportLatencyQualificationWindow struct {
	WarmupEnd   time.Time `json:"warmup_end"`
	BaselineAt  time.Time `json:"baseline_at"`
	LastAt      time.Time `json:"last_at"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	// Elapsed positions retain the original process clock after time.Time loses its monotonic component.
	BaselineElapsed    time.Duration                    `json:"baseline_elapsed"`
	WindowStartElapsed time.Duration                    `json:"window_start_elapsed"`
	WindowEndElapsed   time.Duration                    `json:"window_end_elapsed"`
	Duration           time.Duration                    `json:"duration"`
	RequiredDuration   time.Duration                    `json:"required_duration"`
	MaxAge             time.Duration                    `json:"max_age"`
	MinimumSamples     uint64                           `json:"minimum_samples"`
	Samples            [3]uint64                        `json:"samples"`
	Qualified          [3]bool                          `json:"qualified"`
	Status             ReportLatencyQualificationStatus `json:"status"`
}

func reportQualification(profile Profile, stage Stage) ReportQualificationEvidence {
	switch {
	case profile == ProfileLocal && stage == StageShakeout:
		return ReportQualificationEvidence{Scope: ReportScopeLocalDiagnostic}
	case profile == ProfileFormal && stage == StageRehearsal:
		return ReportQualificationEvidence{Scope: ReportScopeRehearsal, ExternalLinuxEvidenceRequired: true}
	case profile == ProfileFormal && stage == StageFormal:
		return ReportQualificationEvidence{Scope: ReportScopeFormalProtocol, ExternalLinuxEvidenceRequired: true}
	default:
		return ReportQualificationEvidence{}
	}
}

func validReportQualification(report Report) bool {
	expected := reportQualification(report.Profile, report.Stage)
	return expected.Scope != "" && report.Qualification == expected
}

func validReportFormalProtocol(report Report) bool {
	if report.Profile != ProfileFormal {
		return true
	}
	return report.Thresholds == FormalConfig().Thresholds && report.Topology.LogicalSlotGroups == formalLogicalSlotGroups &&
		report.Topology.HashSlots == formalHashSlots && report.Topology.SlotReplicas == formalReplicas && report.Topology.ChannelReplicas == formalReplicas
}

// projectReportLatencyQualification fills absence only at the live producer seam;
// persisted report readers never normalize a legacy or incomplete contract.
func projectReportLatencyQualification(snapshot VerdictLatencyWindowSnapshot, start time.Time, processElapsed time.Duration, thresholds ThresholdsConfig) ReportLatencyQualificationWindow {
	if snapshot == (VerdictLatencyWindowSnapshot{}) {
		snapshot.WarmupEnd = start.Add(thresholds.Timeline.LatencyWarmup)
		snapshot.RequiredDuration = verdictLatencyQualificationWindow
		snapshot.MaxAge = verdictLatencyQualificationMaxAge
		snapshot.MinimumSamples = verdictLatencyQualificationMinimumSamples
	}
	window := ReportLatencyQualificationWindow{
		WarmupEnd: snapshot.WarmupEnd, BaselineAt: snapshot.BaselineAt, LastAt: snapshot.LastAt,
		WindowStart: snapshot.WindowStart, WindowEnd: snapshot.WindowEnd, Duration: snapshot.Duration,
		BaselineElapsed: snapshot.BaselineElapsed, WindowStartElapsed: snapshot.WindowStartElapsed, WindowEndElapsed: snapshot.WindowEndElapsed,
		RequiredDuration: snapshot.RequiredDuration, MaxAge: snapshot.MaxAge, MinimumSamples: snapshot.MinimumSamples,
		Samples: snapshot.Samples, Qualified: snapshot.Qualified,
	}
	// A later final cut may make earlier valid evidence stale. It cannot extend
	// the measured window or refresh its source timestamp.
	if window.LastAt.IsZero() || window.WindowEndElapsed > processElapsed || processElapsed-window.WindowEndElapsed > window.MaxAge {
		window.Qualified = [3]bool{}
	}
	window.Status = reportLatencyStatus(window)
	return window
}

func reportLatencyStatus(window ReportLatencyQualificationWindow) ReportLatencyQualificationStatus {
	if window.BaselineAt.IsZero() || window.Duration == 0 {
		return ReportLatencyNotEvaluated
	}
	if window.Qualified == ([3]bool{true, true, true}) {
		return ReportLatencySufficient
	}
	return ReportLatencyInsufficient
}

func validReportLatencyQualification(report Report) bool {
	window := report.Latency.QualificationWindow
	if !window.WarmupEnd.Equal(report.Window.LatencyWarmupEnd) || window.RequiredDuration != verdictLatencyQualificationWindow ||
		window.MaxAge != verdictLatencyQualificationMaxAge || window.MinimumSamples != verdictLatencyQualificationMinimumSamples {
		return false
	}
	if window.BaselineAt.IsZero() {
		return window.LastAt.IsZero() && window.WindowStart.IsZero() && window.WindowEnd.IsZero() && window.Duration == 0 &&
			window.BaselineElapsed == 0 && window.WindowStartElapsed == 0 && window.WindowEndElapsed == 0 &&
			window.Samples == ([3]uint64{}) && window.Qualified == ([3]bool{}) && window.Status == ReportLatencyNotEvaluated && !successfulReportVerdict(report)
	}
	// Serialized wall audit timestamps cannot reconstruct a process clock.
	// Require only their presence/correspondence; all numerical evidence is elapsed.
	if window.LastAt.IsZero() || window.WindowStart.IsZero() || window.WindowEnd.IsZero() || !window.WindowEnd.Equal(window.LastAt) ||
		window.BaselineElapsed < report.Thresholds.Timeline.LatencyWarmup || window.WindowStartElapsed < window.BaselineElapsed ||
		window.WindowEndElapsed < window.WindowStartElapsed || window.WindowEndElapsed > report.Window.Elapsed ||
		window.WindowStartElapsed < window.WindowEndElapsed-window.RequiredDuration ||
		window.Duration != window.WindowEndElapsed-window.WindowStartElapsed || window.Duration < 0 || window.Duration > window.RequiredDuration {
		return false
	}
	coldCount, ok := checkedUint64Add(report.Latency.ColdFirstCreateSendACK.Count, report.Lifecycle.ReheatLatency.Count)
	if !ok || window.Samples[0] > report.Latency.HotSendACK.Count || window.Samples[1] > coldCount || window.Samples[2] > report.Sync.Thresholds.Count {
		return false
	}
	fresh := report.Window.Elapsed-window.WindowEndElapsed <= window.MaxAge
	for index, qualified := range window.Qualified {
		if qualified && (!fresh || window.Duration < window.RequiredDuration || window.Samples[index] < window.MinimumSamples) {
			return false
		}
	}
	if window.Duration == 0 && (window.Samples != ([3]uint64{}) || window.Qualified != ([3]bool{})) {
		return false
	}
	if window.Status != reportLatencyStatus(window) {
		return false
	}
	return !successfulReportVerdict(report) || window.Status == ReportLatencySufficient
}

func successfulReportVerdict(report Report) bool {
	return report.Verdict.Terminal && validSuccessfulVerdictPair(report.Verdict.Outcome, report.Verdict.Cause)
}
