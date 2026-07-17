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
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
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
	if r.StabilityVerdict != VerdictProductFailure {
		t.Fatalf("stability_verdict = %s, want %s", r.StabilityVerdict, VerdictProductFailure)
	}
}

func TestBuildClassifiesStandardStabilityEvidence(t *testing.T) {
	standard := model.Scenario{
		Run:        model.RunConfig{Duration: 48 * time.Hour},
		Objectives: model.ObjectivesConfig{Scale: "small", Standard: true},
	}
	tests := []struct {
		name           string
		scenario       model.Scenario
		classification *StabilityClassification
		want           StabilityVerdict
	}{
		{name: "passed", scenario: standard, want: VerdictPassed},
		{name: "non-standard scenario", scenario: model.Scenario{Run: model.RunConfig{Duration: 48 * time.Hour}}, want: VerdictInsufficientEvidence},
		{name: "diagnostic duration", scenario: func() model.Scenario { s := standard; s.Run.Duration = 2 * time.Hour; return s }(), want: VerdictInsufficientEvidence},
		{name: "harness invalid", scenario: standard, classification: &StabilityClassification{EvidenceComplete: true, HarnessInvalid: true}, want: VerdictHarnessInvalid},
		{name: "infrastructure failure", scenario: standard, classification: &StabilityClassification{EvidenceComplete: true, InfrastructureFailure: true}, want: VerdictInfrastructureFailure},
		{name: "operator modified", scenario: standard, classification: &StabilityClassification{EvidenceComplete: true, OperatorModified: true}, want: VerdictOperatorModified},
		{name: "missing evidence", scenario: standard, classification: &StabilityClassification{EvidenceComplete: false}, want: VerdictInsufficientEvidence},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := Build(Input{RunID: "run-1", Scenario: tt.scenario, Classification: tt.classification})
			if rep.StabilityVerdict != tt.want {
				t.Fatalf("stability_verdict = %s, want %s", rep.StabilityVerdict, tt.want)
			}
		})
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

func TestSummaryFromMetricsIncludesOnlyMeasuredRunSendSuccess(t *testing.T) {
	summary := SummaryFromMetrics(metrics.SnapshotData{Counters: map[string]uint64{
		"person_send_success_total{phase=warmup}": 100,
		"person_send_success_total{phase=run}":    7,
		"group_send_success_total{phase=run}":     3,
	}}, 0)

	if summary.SendSuccess != 10 {
		t.Fatalf("send_success = %d, want run-phase 10", summary.SendSuccess)
	}
}

func TestSummaryFromMetricsUsesOnlyRunPhaseForLatencyLimits(t *testing.T) {
	summary := SummaryFromMetrics(metrics.SnapshotData{
		Histograms: map[string]metrics.HistogramSummary{
			"group_send_latency_seconds{phase=warmup,traffic=send}": {P99Seconds: 0.900},
			"group_send_latency_seconds{phase=run,traffic=send}":    {P99Seconds: 0.050},
			"group_recv_latency_seconds{phase=warmup,traffic=recv}": {P99Seconds: 0.800},
			"group_recv_latency_seconds{phase=run,traffic=recv}":    {P99Seconds: 0.060},
		},
	}, 0)

	if summary.SendackMaxWorkerP99 != 50*time.Millisecond {
		t.Fatalf("sendack_max_worker_p99 = %s, want run-phase 50ms", summary.SendackMaxWorkerP99)
	}
	if summary.RecvMaxWorkerP99 != 60*time.Millisecond {
		t.Fatalf("recv_max_worker_p99 = %s, want run-phase 60ms", summary.RecvMaxWorkerP99)
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

func TestSendRunSummaryFromMetricsUsesOnlyRunPhase(t *testing.T) {
	summary := SendRunSummaryFromMetrics(metrics.SnapshotData{
		Counters: map[string]uint64{
			"person_send_success_total{channel_type=person,phase=warmup,profile=p,traffic=t}": 100,
			"person_send_success_total{channel_type=person,phase=run,profile=p,traffic=t}":    200,
			"group_send_success_total{channel_type=group,phase=run,profile=g,traffic=t}":      100,
			"person_send_error_total{channel_type=person,phase=run,profile=p,traffic=t}":      1,
		},
		Histograms: map[string]metrics.HistogramSummary{
			"person_send_latency_seconds{channel_type=person,phase=warmup,profile=p,traffic=t}": {Count: 100, P50Seconds: 1, P95Seconds: 1, P99Seconds: 1},
			"person_send_latency_seconds{channel_type=person,phase=run,profile=p,traffic=t}":    {Count: 200, P50Seconds: 0.010, P95Seconds: 0.030, P99Seconds: 0.050},
			"group_send_latency_seconds{channel_type=group,phase=run,profile=g,traffic=t}":      {Count: 100, P50Seconds: 0.020, P95Seconds: 0.040, P99Seconds: 0.060},
		},
	}, time.Minute)

	if summary.SendSuccess != 300 {
		t.Fatalf("send_success = %d, want 300", summary.SendSuccess)
	}
	if summary.SendErrors != 1 {
		t.Fatalf("send_errors = %d, want 1", summary.SendErrors)
	}
	if summary.IngressQPS != 5 {
		t.Fatalf("ingress_qps = %v, want 5", summary.IngressQPS)
	}
	if summary.SendackP50 != 20*time.Millisecond {
		t.Fatalf("sendack_p50 = %s, want 20ms", summary.SendackP50)
	}
	if summary.SendackP95 != 40*time.Millisecond {
		t.Fatalf("sendack_p95 = %s, want 40ms", summary.SendackP95)
	}
	if summary.SendackP99 != 60*time.Millisecond {
		t.Fatalf("sendack_p99 = %s, want 60ms", summary.SendackP99)
	}
}

func TestSummaryMarkdownLabelsMaxWorkerPercentiles(t *testing.T) {
	rep := Build(Input{RunID: "run-1", Summary: Summary{SendSuccess: 123, SendackMaxWorkerP99: 50 * time.Millisecond, RecvMaxWorkerP99: 70 * time.Millisecond}})

	md := summaryMarkdown(rep)
	if !strings.Contains(md, "send_success: 123") {
		t.Fatalf("summary markdown should expose measured-run successful sends, got:\n%s", md)
	}
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

func TestBuildCopiesPresenceSnapshots(t *testing.T) {
	r := Build(Input{
		RunID: "run-1",
		PresenceSnapshots: []model.PresenceSnapshot{
			{Version: "bench/v1", NodeID: 1, OwnerRoutesActive: 2},
			{Version: "bench/v1", NodeID: 2, AuthorityRoutesActive: 3},
		},
	})

	if len(r.PresenceSnapshots) != 2 {
		t.Fatalf("presence snapshots len = %d, want 2", len(r.PresenceSnapshots))
	}
	if r.PresenceSnapshots[0].NodeID != 1 || r.PresenceSnapshots[1].NodeID != 2 {
		t.Fatalf("unexpected presence snapshots: %+v", r.PresenceSnapshots)
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
		PresenceSnapshots: []model.PresenceSnapshot{
			{Version: "bench/v1", NodeID: 1, OwnerRoutesActive: 2},
		},
		ErrorSamples: []metrics.ErrorSample{{Name: "send", Message: "boom"}},
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
		"diagnostic-summary.json",
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
	if len(decoded.PresenceSnapshots) != 1 || decoded.PresenceSnapshots[0].OwnerRoutesActive != 2 {
		t.Fatalf("unexpected report presence snapshots: %+v", decoded.PresenceSnapshots)
	}
}

func TestWriterCreatesBoundedRedactedDiagnosticSummary(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, time.July, 17, 3, 23, 18, 0, time.UTC)
	endedAt := startedAt.Add(30 * time.Minute)
	rep := Build(Input{
		RunID:   "run-1",
		Summary: Summary{SendSuccess: 889785, WorkerFailed: 2},
		Limits:  model.LimitsConfig{Hard: model.HardLimitsConfig{MaxWorkerFailed: 2}},
		PhaseWindows: []PhaseWindow{{
			Phase: "run", StartedAt: startedAt, EndedAt: endedAt,
		}},
		WorkerFailures: []WorkerFailure{
			{
				WorkerID: "worker-3", Phase: "cooldown", ReasonCode: "phase_wait_failed",
				Detail:     "GET http://worker-3:19090/v1/status?token=secret failed with Authorization: Bearer secret",
				ObservedAt: endedAt,
			},
			{
				WorkerID: "worker-4", Phase: "collect", ReasonCode: "worker_report_unavailable",
				Detail:     `open /var/lib/wukongim/run.log via file:///tmp/run.log or ws://sim:9000 failed`,
				ObservedAt: endedAt,
			},
		},
	})
	rep.Status = StatusFailed
	rep.ExitCode = ExitWorkerFailed

	if err := WriteDir(dir, rep); err != nil {
		t.Fatalf("WriteDir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "diagnostic-summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got DiagnosticSummary
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("diagnostic-summary.json should be valid JSON: %v", err)
	}
	if got.Schema != DiagnosticSummarySchema || got.RunID != "run-1" || got.Summary.SendSuccess != 889785 {
		t.Fatalf("diagnostic summary identity = %+v", got)
	}
	if len(got.PhaseWindows) != 1 || !got.PhaseWindows[0].StartedAt.Equal(startedAt) || !got.PhaseWindows[0].EndedAt.Equal(endedAt) {
		t.Fatalf("phase windows = %+v", got.PhaseWindows)
	}
	if len(got.FailedWorkers) != 2 || got.FailedWorkers[0].WorkerID != "worker-3" || got.FailedWorkers[0].Phase != "cooldown" || got.FailedWorkers[0].ReasonCode != "phase_wait_failed" {
		t.Fatalf("failed workers = %+v", got.FailedWorkers)
	}
	encoded := string(data)
	for _, forbidden := range []string{"http://", "ws://", "file://", "/var/lib", "/tmp", "secret", "Authorization", "Bearer"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("diagnostic summary contains unredacted %q detail: %s", forbidden, encoded)
		}
	}
	if got.FailedWorkers[0].Detail != "[redacted]" || got.FailedWorkers[1].Detail != "[redacted]" {
		t.Fatalf("diagnostic summary contains unredacted detail: %s", encoded)
	}
	if !strings.Contains(encoded, `"violations": []`) || !strings.Contains(encoded, `"warnings": []`) {
		t.Fatalf("diagnostic summary must encode empty collections as arrays: %s", encoded)
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
