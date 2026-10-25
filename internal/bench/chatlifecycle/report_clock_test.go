package chatlifecycle

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpointLatencyClockRoundTripKeepsMonotonicProofAcrossAuditDrift(t *testing.T) {
	for _, drift := range []time.Duration{10 * time.Nanosecond, -10 * time.Nanosecond, 200 * time.Microsecond, -200 * time.Microsecond} {
		t.Run(drift.String(), func(t *testing.T) {
			cfg := FormalConfig()
			start := time.Unix(1_970_000_000, 0)
			fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "clock-roundtrip", Generation: 1}
			recorder, err := NewCheckpointRecorder(cfg, fence, start)
			if err != nil {
				t.Fatal(err)
			}
			at := start.Add(24 * time.Hour)
			evidence := checkpointEvidenceFixture(false)
			evidence.LatencyWindow = qualifiedLatencyWindowFixture(start, at)
			evidence.LatencyWindow.WindowStart = evidence.LatencyWindow.WindowStart.Add(drift)
			directory := t.TempDir()
			paths := CheckpointOutputPaths{JSON: filepath.Join(directory, "checkpoint.json"), Markdown: filepath.Join(directory, "checkpoint.md")}
			_, err = recorder.CaptureAndWrite(at, qualifiedCheckpointSnapshotFixture(fence, 1, 24*time.Hour, 1000), evidence, paths)
			if err != nil {
				t.Fatalf("audit clock drift changed native qualification: %v", err)
			}
			loaded, err := ReadReport(paths.JSON)
			if err != nil {
				t.Fatalf("saved report lost monotonic proof: %v", err)
			}
			if loaded.Latency.QualificationWindow.Duration != 10*time.Minute || loaded.Latency.QualificationWindow.Status != ReportLatencySufficient {
				t.Fatalf("roundtrip proof changed: %+v", loaded.Latency.QualificationWindow)
			}
		})
	}
}

func TestReportLatencyClockReadDoesNotInferDurationFromAuditTimestamps(t *testing.T) {
	report := qualifiedReportFixture(t)
	body, err := MarshalReport(report, ReportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Report
	if err := json.Unmarshal(body, &persisted); err != nil {
		t.Fatal(err)
	}
	// Preserve source elapsed evidence while the serialized wall audit differs by ten nanoseconds.
	persisted.Latency.QualificationWindow.WindowStart = persisted.Latency.QualificationWindow.WindowStart.Add(-10 * time.Nanosecond)
	modified, err := json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "wall-audit-drift.json")
	if err := os.WriteFile(path, modified, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReport(path); err != nil {
		t.Fatalf("wall-only delta overrode durable monotonic evidence: %v", err)
	}
}

func TestCheckpointLatencyClockShortNativeWindowSurvivesAuditDrift(t *testing.T) {
	const nativeDuration = time.Duration(89_930_033_567)
	const elapsed = 150 * time.Second
	for _, drift := range []time.Duration{10 * time.Nanosecond, -10 * time.Nanosecond, 200 * time.Microsecond, -200 * time.Microsecond} {
		t.Run(drift.String(), func(t *testing.T) {
			cfg := LocalConfig()
			cfg.Thresholds.Timeline = TimelineThresholds{LatencyWarmup: time.Minute, Warmup: time.Minute, Checkpoint: time.Minute + time.Millisecond, Final: 5 * time.Minute}
			start := time.Unix(1_970_100_000, 0)
			fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "native-short-clock", Generation: 2}
			recorder, err := NewCheckpointRecorder(cfg, fence, start)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := captureCheckpoint(t, recorder, start.Add(cfg.Thresholds.Timeline.Checkpoint), coordinatorSnapshotFixture(fence, 1, cfg.Thresholds.Timeline.Checkpoint, 1), checkpointEvidenceFixture(false)); err != nil {
				t.Fatal(err)
			}
			baselineElapsed := elapsed - nativeDuration
			evidence := checkpointEvidenceFixture(false)
			evidence.Verdict = VerdictSnapshot{Terminal: true, Outcome: VerdictOperatorStop, Cause: VerdictCauseOperatorRequested}
			evidence.LatencyWindow = VerdictLatencyWindowSnapshot{
				WarmupEnd: start.Add(time.Minute), BaselineAt: start.Add(baselineElapsed), LastAt: start.Add(elapsed), WindowStart: start.Add(baselineElapsed + drift), WindowEnd: start.Add(elapsed),
				BaselineElapsed: baselineElapsed, WindowStartElapsed: baselineElapsed, WindowEndElapsed: elapsed,
				Duration: nativeDuration, RequiredDuration: 10 * time.Minute, MaxAge: 5 * time.Second, MinimumSamples: 1000, Samples: [3]uint64{1000, 1000, 1000},
			}
			snapshots := qualifiedCheckpointSnapshotFixture(fence, 2, elapsed, 1000)
			for index := range snapshots {
				snapshots[index].Phase = WorkerPhaseFinal
			}
			directory := t.TempDir()
			paths := CheckpointOutputPaths{JSON: filepath.Join(directory, "final.json"), Markdown: filepath.Join(directory, "final.md")}
			if _, err := recorder.CaptureAndWrite(start.Add(elapsed), snapshots, evidence, paths); err != nil {
				t.Fatalf("native short clock proof rejected: %v", err)
			}
			report, err := ReadReport(paths.JSON)
			if err != nil {
				t.Fatalf("native short report lost stats on read: %v", err)
			}
			window := report.Latency.QualificationWindow
			if window.Duration != nativeDuration || window.WindowEndElapsed-window.WindowStartElapsed != nativeDuration || window.Status != ReportLatencyInsufficient || window.Qualified != ([3]bool{}) || report.Verdict.Outcome != VerdictOperatorStop {
				t.Fatalf("native verdict/window changed: %+v / %+v", report.Verdict, window)
			}
			t.Logf("duration=%d elapsed_difference=%d audit_difference=%d", window.Duration, window.WindowEndElapsed-window.WindowStartElapsed, window.WindowEnd.Sub(window.WindowStart))
		})
	}
}

func TestReportLatencyClockElapsedPositionsRemainAuthoritative(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Report)
	}{
		{"negative baseline", func(r *Report) { r.Latency.QualificationWindow.BaselineElapsed = -time.Nanosecond }},
		{"baseline before warmup", func(r *Report) {
			r.Latency.QualificationWindow.BaselineElapsed = r.Thresholds.Timeline.LatencyWarmup - time.Nanosecond
		}},
		{"window before baseline", func(r *Report) {
			r.Latency.QualificationWindow.WindowStartElapsed = r.Latency.QualificationWindow.BaselineElapsed - time.Nanosecond
		}},
		{"end before start", func(r *Report) {
			r.Latency.QualificationWindow.WindowEndElapsed = r.Latency.QualificationWindow.WindowStartElapsed - time.Nanosecond
		}},
		{"future end despite current audit", func(r *Report) {
			w := &r.Latency.QualificationWindow
			w.WindowEndElapsed = r.Window.Elapsed + time.Nanosecond
			w.WindowStartElapsed = w.WindowEndElapsed - 10*time.Minute
		}},
		{"duration disagrees with elapsed", func(r *Report) { r.Latency.QualificationWindow.WindowStartElapsed += time.Nanosecond }},
		{"truncated monotonic qualification", func(r *Report) {
			w := &r.Latency.QualificationWindow
			w.WindowStartElapsed += time.Nanosecond
			w.Duration -= time.Nanosecond
		}},
		{"stale despite fresh audit", func(r *Report) {
			w := &r.Latency.QualificationWindow
			w.WindowEndElapsed -= 5*time.Second + time.Nanosecond
			w.WindowStartElapsed = w.WindowEndElapsed - 10*time.Minute
		}},
		{"reheader without elapsed positions", func(r *Report) {
			w := &r.Latency.QualificationWindow
			w.BaselineElapsed = 0
			w.WindowStartElapsed = 0
			w.WindowEndElapsed = 0
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := qualifiedReportFixture(t)
			test.mutate(&r)
			if _, err := MarshalReport(r, ReportFormatJSON); !errors.Is(err, ErrReportInvalid) {
				t.Fatalf("monotonic contract accepted invalid evidence: %v", err)
			}
		})
	}
	report := qualifiedReportFixture(t)
	report.Window.Elapsed += 5 * time.Second
	report.MinimumWorkerUptime = report.Window.Elapsed
	if _, err := MarshalReport(report, ReportFormatJSON); err != nil {
		t.Fatalf("exact monotonic freshness boundary rejected: %v", err)
	}
	// Moving audit clocks cannot extend, shorten or refresh the monotonic qualification.
	report = qualifiedReportFixture(t)
	report.Latency.QualificationWindow.BaselineAt = report.Window.Start.Add(-200 * time.Microsecond)
	report.Latency.QualificationWindow.WindowStart = report.Latency.QualificationWindow.WindowStart.Add(200 * time.Microsecond)
	report.Latency.QualificationWindow.WindowEnd = report.Latency.QualificationWindow.WindowEnd.Add(200 * time.Microsecond)
	report.Latency.QualificationWindow.LastAt = report.Latency.QualificationWindow.WindowEnd
	if _, err := MarshalReport(report, ReportFormatJSON); err != nil {
		t.Fatalf("audit-only ordering changed valid elapsed evidence: %v", err)
	}
	report = reportFixture(t)
	report.Latency.QualificationWindow.BaselineElapsed = time.Nanosecond
	if _, err := MarshalReport(report, ReportFormatJSON); !errors.Is(err, ErrReportInvalid) {
		t.Fatalf("empty unmeasured window retained elapsed data: %v", err)
	}
}

func TestReportLatencyClockRejectsV6AndMissingSerializedPositions(t *testing.T) {
	report := qualifiedReportFixture(t)
	report.SchemaVersion = "wukongim/chat-lifecycle-report/v6"
	if _, err := MarshalReport(report, ReportFormatJSON); !errors.Is(err, ErrReportInvalid) {
		t.Fatalf("legacy v6 accepted: %v", err)
	}
	report = qualifiedReportFixture(t)
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{"baseline_elapsed", "window_start_elapsed", "window_end_elapsed"} {
		t.Run(missing, func(t *testing.T) {
			var raw map[string]json.RawMessage
			_ = json.Unmarshal(body, &raw)
			var latency map[string]json.RawMessage
			_ = json.Unmarshal(raw["latency"], &latency)
			var window map[string]json.RawMessage
			_ = json.Unmarshal(latency["qualification_window"], &window)
			delete(window, missing)
			latency["qualification_window"], _ = json.Marshal(window)
			raw["latency"], _ = json.Marshal(latency)
			modified, _ := json.Marshal(raw)
			path := filepath.Join(t.TempDir(), "missing-position.json")
			if err := os.WriteFile(path, modified, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadReport(path); !errors.Is(err, ErrReportInvalid) {
				t.Fatalf("missing %s was derived from wall time: %v", missing, err)
			}
		})
	}
}
