package report

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestBuildExplicitZeroHardLimitFailsWhenActualIsPositive(t *testing.T) {
	r := Build(Input{
		RunID:   "run-1",
		Limits:  model.LimitsConfig{Hard: model.HardLimitsConfig{MaxSendackErrorRate: 0}},
		Summary: Summary{SendackErrorRate: 0.001},
	})

	if r.Status != StatusFailed {
		t.Fatalf("status = %s, want %s", r.Status, StatusFailed)
	}
	if len(r.Violations) != 1 || r.Violations[0].Name != "max_sendack_error_rate" || r.Violations[0].Limit != 0 {
		t.Fatalf("unexpected violations: %+v", r.Violations)
	}
}

func TestSummaryFromMetricsComputesRatesAndMaxWorkerP99(t *testing.T) {
	summary := SummaryFromMetrics(metrics.SnapshotData{
		Counters: map[string]uint64{
			"connect_success_total":     8,
			"connect_error_total":       2,
			"person_send_success_total": 9,
			"group_send_error_total":    1,
			"person_recv_success_total": 3,
			"group_recv_error_total":    1,
		},
		Histograms: map[string]metrics.HistogramSummary{
			"person_send_latency_seconds": {P99Seconds: 0.020},
			"group_send_latency_seconds":  {P99Seconds: 0.050},
			"person_recv_latency_seconds": {P99Seconds: 0.030},
		},
	}, 2)

	if summary.ConnectErrorRate != 0.2 {
		t.Fatalf("connect_error_rate = %v, want 0.2", summary.ConnectErrorRate)
	}
	if summary.SendackErrorRate != 0.1 {
		t.Fatalf("sendack_error_rate = %v, want 0.1", summary.SendackErrorRate)
	}
	if summary.RecvVerifyErrorRate != 0.25 {
		t.Fatalf("recv_verify_error_rate = %v, want 0.25", summary.RecvVerifyErrorRate)
	}
	if summary.WorkerFailed != 2 {
		t.Fatalf("worker_failed = %d, want 2", summary.WorkerFailed)
	}
	if summary.SendackMaxWorkerP99 != 50*time.Millisecond {
		t.Fatalf("sendack_max_worker_p99 = %s, want 50ms", summary.SendackMaxWorkerP99)
	}
}

func TestSummaryFromMetricsUsesConnectAttemptsAsRateDenominator(t *testing.T) {
	summary := SummaryFromMetrics(metrics.SnapshotData{
		Counters: map[string]uint64{
			"connect_attempt_total": 10,
			"connect_success_total": 6,
			"connect_error_total":   1,
		},
	}, 0)

	if summary.ConnectErrorRate != 0.1 {
		t.Fatalf("connect_error_rate = %v, want 0.1", summary.ConnectErrorRate)
	}
}

func TestSummaryMarkdownLabelsMaxWorkerPercentiles(t *testing.T) {
	rep := Build(Input{RunID: "run-1", Summary: Summary{SendackMaxWorkerP99: 50 * time.Millisecond, RecvMaxWorkerP99: 70 * time.Millisecond}})

	md := summaryMarkdown(rep)
	if !strings.Contains(md, "sendack_max_worker_p99") || !strings.Contains(md, "recv_max_worker_p99") {
		t.Fatalf("summary markdown should label max-worker percentiles, got:\n%s", md)
	}
	if strings.Contains(md, "sendack_p99") || strings.Contains(md, "recv_p99") {
		t.Fatalf("summary markdown should not label max-worker values as aggregate p99, got:\n%s", md)
	}
}

func TestBuildSoftLimitWarnsWithoutFailOnSoft(t *testing.T) {
	r := Build(Input{
		RunID:   "run-1",
		Limits:  model.LimitsConfig{Soft: model.SoftLimitsConfig{MaxSendackP99: 10}},
		Summary: Summary{SendackMaxWorkerP99: 20},
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

func TestFinishBufferedWriteReturnsFlushAndCloseErrors(t *testing.T) {
	flushErr := errors.New("flush failed")
	closeErr := errors.New("close failed")
	writer := bufio.NewWriter(errorWriter{err: flushErr})
	if _, err := writer.WriteString("payload"); err != nil {
		t.Fatalf("buffer write should not hit underlying writer before flush: %v", err)
	}

	err := finishBufferedWrite(writer, closeFunc(func() error { return closeErr }))

	if !errors.Is(err, flushErr) {
		t.Fatalf("expected flush error in joined error, got %v", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("expected close error in joined error, got %v", err)
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type closeFunc func() error

func (f closeFunc) Close() error { return f() }
