package chatlifecycle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReportV7RejectsLegacyV5ThresholdV1(t *testing.T) {
	report := reportFixture(t)
	report.SchemaVersion = "wukongim/chat-lifecycle-report/v5"
	report.ThresholdVersion = "wukongim/chat-lifecycle-thresholds/v1"
	if _, err := MarshalReport(report, ReportFormatJSON); !errors.Is(err, ErrReportInvalid) {
		t.Fatalf("legacy report accepted under new qualification contract: %v", err)
	}
}

func TestReportV8CarriesScopeAndSeparateLatencyWarmup(t *testing.T) {
	report := reportFixture(t)
	body, err := MarshalReport(report, ReportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["schema_version"]) != `"wukongim/chat-lifecycle-report/v8"` || string(raw["threshold_version"]) != `"wukongim/chat-lifecycle-thresholds/v2"` {
		t.Fatalf("versions = %s / %s", raw["schema_version"], raw["threshold_version"])
	}
	if len(raw["qualification"]) == 0 {
		t.Fatal("report omitted evaluation scope")
	}
	var window map[string]json.RawMessage
	_ = json.Unmarshal(raw["window"], &window)
	if len(window["latency_warmup_end"]) == 0 {
		t.Fatal("report conflates latency and resource warmup")
	}
	var latency map[string]json.RawMessage
	_ = json.Unmarshal(raw["latency"], &latency)
	if len(latency["qualification_window"]) == 0 {
		t.Fatal("report omitted the actual positive latency window")
	}
}

func TestReportV7RejectsUnqualifiedSuccessfulFinal(t *testing.T) {
	report := reportFixture(t)
	report.Kind, report.Final, report.Continue = CheckpointFinal, true, false
	report.Window.End = report.Window.FinalAt
	report.Window.Elapsed = report.Window.End.Sub(report.Window.Start)
	report.MinimumWorkerUptime = report.Window.Elapsed
	report.Verdict = ReportVerdictEvidence{Terminal: true, Outcome: VerdictPass, Cause: VerdictCauseCompleted}
	for index := range report.Workers {
		report.Workers[index].Phase = WorkerPhaseFinal
	}
	if _, err := MarshalReport(report, ReportFormatJSON); !errors.Is(err, ErrReportInvalid) {
		t.Fatalf("report promoted missing latency evidence to PASS: %v", err)
	}
}

func TestReportV7QualificationScopeFollowsProfileAndStage(t *testing.T) {
	for _, test := range []struct {
		cfg      Config
		scope    ReportQualificationScope
		external bool
	}{
		{LocalConfig(), ReportScopeLocalDiagnostic, false}, {RehearsalConfig(), ReportScopeRehearsal, true}, {FormalConfig(), ReportScopeFormalProtocol, true},
	} {
		cfg := test.cfg
		t.Run(string(test.scope), func(t *testing.T) {
			start := time.Unix(1_802_100_000, 0)
			fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "scope", Generation: 1}
			recorder, err := NewCheckpointRecorder(cfg, fence, start)
			if err != nil {
				t.Fatal(err)
			}
			evidence := checkpointEvidenceFixture(false)
			evidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictHarnessInvalid, Cause: VerdictCauseInvalidObservation}
			report, err := captureCheckpoint(t, recorder, start.Add(5*time.Minute), coordinatorSnapshotFixture(fence, 1, 5*time.Minute, 1), evidence)
			if err != nil {
				t.Fatal(err)
			}
			if report.Qualification.Scope != test.scope || report.Qualification.ExternalLinuxEvidenceRequired != test.external {
				t.Fatalf("scope = %+v", report.Qualification)
			}
			if report.Latency.QualificationWindow.Status != ReportLatencyNotEvaluated || !report.Window.LatencyWarmupEnd.Equal(start.Add(cfg.Thresholds.Timeline.LatencyWarmup)) || !report.Window.WarmupEnd.Equal(start.Add(cfg.Thresholds.Timeline.Warmup)) {
				t.Fatalf("unmeasured window = %+v / %+v", report.Window, report.Latency.QualificationWindow)
			}
			report.Qualification.Scope = ReportScopeFormalProtocol
			report.Qualification.ExternalLinuxEvidenceRequired = !test.external
			if _, err := MarshalReport(report, ReportFormatJSON); !errors.Is(err, ErrReportInvalid) {
				t.Fatalf("mismatched scope accepted: %v", err)
			}
		})
	}
}

func TestReportV7RejectsEditedFormalThresholdsAndTopology(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Report)
	}{
		{"hot latency", func(r *Report) { r.Thresholds.Latency.HotSendACK.P99 = time.Second }},
		{"correctness loss", func(r *Report) { r.Thresholds.Correctness.Losses = 1 }},
		{"resource threshold", func(r *Report) { r.Thresholds.Resource.HostCPUPercent-- }},
		{"logical slots", func(r *Report) { r.Topology.LogicalSlotGroups = 1 }},
		{"hash slots", func(r *Report) { r.Topology.HashSlots = 1 }},
		{"slot replicas", func(r *Report) { r.Topology.SlotReplicas = 1 }},
		{"channel replicas", func(r *Report) { r.Topology.ChannelReplicas = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := reportFixture(t)
			test.mutate(&report)
			if _, err := MarshalReport(report, ReportFormatJSON); !errors.Is(err, ErrReportInvalid) {
				t.Fatalf("edited formal gate accepted: %v", err)
			}
		})
	}
}

func TestReportV7RejectsContradictoryOrStalePositiveWindows(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Report)
	}{
		{"wrong scope", func(r *Report) { r.Qualification.Scope = ReportScopeLocalDiagnostic }},
		{"claims external evidence unnecessary", func(r *Report) { r.Qualification.ExternalLinuxEvidenceRequired = false }},
		{"missing window", func(r *Report) { r.Latency.QualificationWindow = ReportLatencyQualificationWindow{} }},
		{"warmup mismatch", func(r *Report) { r.Window.LatencyWarmupEnd = r.Window.LatencyWarmupEnd.Add(time.Second) }},
		{"qualification warmup mismatch", func(r *Report) {
			r.Latency.QualificationWindow.WarmupEnd = r.Latency.QualificationWindow.WarmupEnd.Add(time.Second)
		}},
		{"too few samples", func(r *Report) { r.Latency.QualificationWindow.Samples[2] = 999 }},
		{"hot sample exceeds total", func(r *Report) { r.Latency.QualificationWindow.Samples[0] = r.Latency.HotSendACK.Count + 1 }},
		{"cold sample exceeds total", func(r *Report) {
			r.Latency.QualificationWindow.Samples[1] = r.Latency.ColdFirstCreateSendACK.Count + r.Lifecycle.ReheatLatency.Count + 1
		}},
		{"sync sample exceeds total", func(r *Report) { r.Latency.QualificationWindow.Samples[2] = r.Sync.Thresholds.Count + 1 }},
		{"truncated interval", func(r *Report) { r.Latency.QualificationWindow.Duration -= time.Nanosecond }},
		{"late baseline", func(r *Report) {
			r.Latency.QualificationWindow.BaselineElapsed = r.Latency.QualificationWindow.WindowStartElapsed + time.Second
		}},
		{"future cut", func(r *Report) {
			r.Latency.QualificationWindow.WindowEndElapsed = r.Window.Elapsed + time.Second
			r.Latency.QualificationWindow.WindowStartElapsed = r.Latency.QualificationWindow.WindowEndElapsed - 10*time.Minute
		}},
		{"stale claim", func(r *Report) {
			r.Window.End = r.Window.End.Add(5*time.Second + time.Nanosecond)
			r.Window.Elapsed += 5*time.Second + time.Nanosecond
			r.MinimumWorkerUptime = r.Window.Elapsed
		}},
		{"wrong status", func(r *Report) { r.Latency.QualificationWindow.Status = ReportLatencyInsufficient }},
		{"loosened sample floor", func(r *Report) { r.Latency.QualificationWindow.MinimumSamples = 1 }},
		{"loosened duration", func(r *Report) { r.Latency.QualificationWindow.RequiredDuration = time.Minute }},
		{"loosened freshness", func(r *Report) { r.Latency.QualificationWindow.MaxAge = time.Hour }},
		{"message invariant", func(r *Report) { r.Messages.FirstAttemptTimeouts = r.Messages.FirstAttempts + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := qualifiedReportFixture(t)
			test.mutate(&report)
			if _, err := MarshalReport(report, ReportFormatJSON); !errors.Is(err, ErrReportInvalid) {
				t.Fatalf("contradictory positive window accepted: %v", err)
			}
		})
	}
	report := qualifiedReportFixture(t)
	if _, err := MarshalReport(report, ReportFormatJSON); err != nil {
		t.Fatalf("qualified final rejected: %v", err)
	}
	report.Window.End = report.Window.End.Add(5 * time.Second)
	report.Window.Elapsed += 5 * time.Second
	report.MinimumWorkerUptime = report.Window.Elapsed
	if _, err := MarshalReport(report, ReportFormatJSON); err != nil {
		t.Fatalf("exact five-second freshness boundary rejected: %v", err)
	}
}

func TestReportV7RejectsReheaderWithoutNewEvidence(t *testing.T) {
	report := qualifiedReportFixture(t)
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{"qualification", "latency.qualification_window", "window.latency_warmup_end"} {
		t.Run(missing, func(t *testing.T) {
			var copy map[string]json.RawMessage
			if err := json.Unmarshal(body, &copy); err != nil {
				t.Fatal(err)
			}
			switch missing {
			case "qualification":
				delete(copy, "qualification")
			case "latency.qualification_window":
				var latency map[string]json.RawMessage
				_ = json.Unmarshal(copy["latency"], &latency)
				delete(latency, "qualification_window")
				copy["latency"], _ = json.Marshal(latency)
			default:
				var window map[string]json.RawMessage
				_ = json.Unmarshal(copy["window"], &window)
				delete(window, "latency_warmup_end")
				copy["window"], _ = json.Marshal(window)
			}
			modified, _ := json.Marshal(copy)
			path := filepath.Join(t.TempDir(), "reheader.json")
			if err := os.WriteFile(path, modified, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadReport(path); !errors.Is(err, ErrReportInvalid) {
				t.Fatalf("reheader without %s accepted: %v", missing, err)
			}
		})
	}
}

func TestCheckpointProjectsExactLatencyQualificationEvidence(t *testing.T) {
	cfg := FormalConfig()
	start := time.Unix(1_802_200_000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "latency-window", Generation: 1}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	at := start.Add(24 * time.Hour)
	evidence := checkpointEvidenceFixture(false)
	evidence.LatencyWindow = qualifiedLatencyWindowFixture(start, at)
	report, err := captureCheckpoint(t, recorder, at, qualifiedCheckpointSnapshotFixture(fence, 1, 24*time.Hour, 100), evidence)
	if err != nil {
		t.Fatal(err)
	}
	got := report.Latency.QualificationWindow
	if got.Status != ReportLatencySufficient || got.Samples != evidence.LatencyWindow.Samples || got.Qualified != evidence.LatencyWindow.Qualified || got.Duration != 10*time.Minute || !got.BaselineAt.Equal(evidence.LatencyWindow.BaselineAt) || !got.WindowStart.Equal(at.Add(-10*time.Minute)) {
		t.Fatalf("checkpoint projection = %+v", got)
	}
}

func qualifiedReportFixture(t *testing.T) Report {
	t.Helper()
	report := reportFixture(t)
	report.Kind, report.Final, report.Continue = CheckpointFinal, true, false
	report.Window.End = report.Window.FinalAt
	report.Window.Elapsed = report.Window.End.Sub(report.Window.Start)
	report.MinimumWorkerUptime = report.Window.Elapsed
	report.Verdict = ReportVerdictEvidence{Terminal: true, Outcome: VerdictPass, Cause: VerdictCauseCompleted}
	for index := range report.Workers {
		report.Workers[index].Phase = WorkerPhaseFinal
	}
	addQualifiedReportEvidence(&report)
	return report
}

func qualifiedLatencyWindowFixture(start, end time.Time) VerdictLatencyWindowSnapshot {
	return VerdictLatencyWindowSnapshot{BaselineElapsed: 10 * time.Minute, WindowStartElapsed: end.Sub(start) - 10*time.Minute, WindowEndElapsed: end.Sub(start), WarmupEnd: start.Add(10 * time.Minute), BaselineAt: start.Add(10 * time.Minute), LastAt: end, WindowStart: end.Add(-10 * time.Minute), WindowEnd: end, Duration: 10 * time.Minute, RequiredDuration: 10 * time.Minute, MaxAge: 5 * time.Second, MinimumSamples: 1_000, Samples: [3]uint64{1_000, 1_000, 1_000}, Qualified: [3]bool{true, true, true}}
}

func qualifiedReportLatencyWindowFixture(start, end time.Time) ReportLatencyQualificationWindow {
	return ReportLatencyQualificationWindow{BaselineElapsed: 10 * time.Minute, WindowStartElapsed: end.Sub(start) - 10*time.Minute, WindowEndElapsed: end.Sub(start), WarmupEnd: start.Add(10 * time.Minute), BaselineAt: start.Add(10 * time.Minute), LastAt: end, WindowStart: end.Add(-10 * time.Minute), WindowEnd: end, Duration: 10 * time.Minute, RequiredDuration: 10 * time.Minute, MaxAge: 5 * time.Second, MinimumSamples: 1_000, Samples: [3]uint64{1_000, 1_000, 1_000}, Qualified: [3]bool{true, true, true}, Status: ReportLatencySufficient}
}

func reportLatencyHistogramFixture(count uint64) WorkerHistogramSnapshot {
	histogram := newWorkerHistogramSnapshot()
	histogram.Count = count
	histogram.SumNanos = count * uint64(time.Millisecond)
	histogram.MaxNanos = uint64(time.Millisecond)
	histogram.Buckets[1] = count
	return histogram
}

func addQualifiedReportEvidence(report *Report) {
	report.Latency.QualificationWindow = qualifiedReportLatencyWindowFixture(report.Window.Start, report.Window.End)
	report.Latency.HotSendACK = reportLatencyHistogramFixture(6_000)
	report.Latency.ColdFirstCreateSendACK = reportLatencyHistogramFixture(6_000)
	report.Latency.FullSync = reportLatencyHistogramFixture(6_000)
	report.Sync.Latency = report.Latency.FullSync
	report.Sync.Thresholds = LatencyThresholdCounters{P99Limit: report.Thresholds.Latency.Sync.P99, P999Limit: report.Thresholds.Latency.Sync.P999, Count: 6_000}
}

func qualifiedCheckpointSnapshotFixture(fence WorkerFence, sequence uint64, uptime time.Duration, base uint64) []WorkerSnapshot {
	snapshots := coordinatorSnapshotFixture(fence, sequence, uptime, base)
	for index := range snapshots {
		count := base + uint64(index)
		if count < 1_000 {
			count = 1_000 + uint64(index)
		}
		histogram := reportLatencyHistogramFixture(count)
		snapshots[index].HotSendackLatency = histogram
		snapshots[index].ColdFirstCreateSendackLatency = histogram
		snapshots[index].Sync.Latency = histogram
		snapshots[index].Sync.Thresholds = LatencyThresholdCounters{P99Limit: time.Second, P999Limit: 3 * time.Second, Count: count}
	}
	return snapshots
}
