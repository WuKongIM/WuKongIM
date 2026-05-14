package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/model"
)

func TestBuildHardLimitFailureSetsFailedStatusAndExitCode3(t *testing.T) {
	r := Build(Input{
		RunID:   "run-1",
		Limits:  model.LimitsConfig{Hard: model.HardLimitsConfig{MaxSendackErrorRate: 0.001}},
		Summary: Summary{SendackErrorRate: 0.02},
	})

	if r.Status != StatusFailed {
		t.Fatalf("status = %s, want %s", r.Status, StatusFailed)
	}
	if r.ExitCode != ExitHardLimitFailed {
		t.Fatalf("exit_code = %d, want %d", r.ExitCode, ExitHardLimitFailed)
	}
	if len(r.Violations) != 1 || r.Violations[0].Name != "max_sendack_error_rate" {
		t.Fatalf("unexpected violations: %+v", r.Violations)
	}
}

func TestBuildSoftLimitWarnsWithoutFailOnSoft(t *testing.T) {
	r := Build(Input{
		RunID:   "run-1",
		Limits:  model.LimitsConfig{Soft: model.SoftLimitsConfig{MaxSendackP99: 10}},
		Summary: Summary{SendackP99: 20},
	})

	if r.Status != StatusPassed || r.ExitCode != ExitSuccess {
		t.Fatalf("status/exit = %s/%d, want passed/0", r.Status, r.ExitCode)
	}
	if len(r.Warnings) != 1 || r.Warnings[0].Name != "max_sendack_p99" {
		t.Fatalf("unexpected warnings: %+v", r.Warnings)
	}
}

func TestWriterCreatesDeterministicRunDirectoryFiles(t *testing.T) {
	dir := t.TempDir()
	scenario := model.Scenario{Version: "wkbench/v1", Run: model.RunConfig{ID: "run-1", ReportDir: dir}}
	target := model.Target{Name: "target"}
	workers := model.WorkerSet{Workers: []model.Worker{{ID: "w1", Addr: "http://worker"}}}
	plan := model.Plan{RunID: "run-1", WorkerOrder: []string{"w1"}, Workers: map[string]model.WorkerPlan{"w1": {WorkerID: "w1"}}}
	rep := Build(Input{
		RunID:           "run-1",
		Scenario:        scenario,
		Target:          target,
		Workers:         workers,
		Plan:            plan,
		Summary:         Summary{SendackErrorRate: 0},
		WorkerReports:   []WorkerReport{{WorkerID: "w1", Report: json.RawMessage(`{"ok":true}`)}},
		WorkerMetrics:   []metrics.WorkerSnapshot{{WorkerID: "w1", Metrics: metrics.SnapshotData{Counters: map[string]uint64{"send_total": 1}}}},
		TargetSnapshots: []json.RawMessage{json.RawMessage(`{"status":"ok"}`)},
		ErrorSamples:    []metrics.ErrorSample{{Name: "send", Message: "boom"}},
	})

	if err := WriteDir(dir, rep); err != nil {
		t.Fatalf("WriteDir: %v", err)
	}

	for _, rel := range []string{
		"scenario.yaml",
		"target.yaml",
		"workers.yaml",
		"plan.json",
		"summary.md",
		"report.json",
		"coordinator.log",
		"workers/w1.report.json",
		"metrics/worker-1s.jsonl",
		"metrics/target-snapshots.jsonl",
		"errors/samples.jsonl",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected %s: %v", rel, err)
		}
	}
	var decoded Report
	data, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("report.json should be valid JSON: %v", err)
	}
	if decoded.RunID != "run-1" {
		t.Fatalf("run_id = %q, want run-1", decoded.RunID)
	}
}
