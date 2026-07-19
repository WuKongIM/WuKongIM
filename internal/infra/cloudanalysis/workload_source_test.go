package cloudanalysis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

func TestWorkloadSummarySourceParsesBoundedFinalSummary(t *testing.T) {
	reportDir := t.TempDir()
	summary := `{
  "schema":"wukongim/wkbench-diagnostic-summary/v1",
  "run_id":"run-1",
  "status":"failed",
  "exit_code":4,
  "stability_verdict":"harness_invalid",
  "summary":{"send_success":123456,"connect_attempts":10000,"connect_success":9999,"connect_errors":1,"connect_error_rate":0.000002,"sendack_error_rate":0.000001,"recv_verify_error_rate":0.000003,"worker_failed":1,"sendack_max_worker_p99":125000000,"recv_max_worker_p99":250000000},
  "violations":[],
  "warnings":[],
  "phase_windows":[{"phase":"run","started_at":"2026-07-17T03:23:18Z","ended_at":"2026-07-17T03:53:18Z"}],
  "failed_workers":[{"worker_id":"worker-3","phase":"cooldown","reason_code":"phase_hook_failed","operation":"group_sendack","detail":"worker phase hook failed","observed_at":"2026-07-17T03:53:18Z"}],
  "failed_workers_truncated":false
}`
	if err := os.WriteFile(filepath.Join(reportDir, "diagnostic-summary.json"), []byte(summary), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	result, err := newWorkloadSummarySource(reportDir).inspect(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("inspect() error = %v", err)
	}
	if result.Completeness != analysis.CompletenessComplete {
		t.Fatalf("completeness = %q, want complete", result.Completeness)
	}
	inspection, ok := result.Data.(analysis.WorkloadInspection)
	if !ok {
		t.Fatalf("data type = %T, want WorkloadInspection", result.Data)
	}
	if inspection.RunID != "run-1" || inspection.State != "completed" || inspection.Status != "failed" || inspection.ExitCode != 4 || inspection.StabilityVerdict != "harness_invalid" {
		t.Fatalf("inspection = %+v", inspection)
	}
	if inspection.Summary.SendSuccess != 123456 || inspection.Summary.ConnectAttempts != 10000 || inspection.Summary.ConnectSuccess != 9999 || inspection.Summary.ConnectErrors != 1 || inspection.Summary.ConnectErrorRate != 0.000002 || inspection.Summary.SendackMaxWorkerP99 != "125ms" || inspection.Summary.ReceiveMaxWorkerP99 != "250ms" {
		t.Fatalf("summary = %+v", inspection.Summary)
	}
	if len(inspection.PhaseWindows) != 1 || inspection.PhaseWindows[0].Phase != "run" || len(inspection.FailedWorkers) != 1 || inspection.FailedWorkers[0].WorkerID != "worker-3" || inspection.FailedWorkers[0].ReasonCode != "phase_hook_failed" || inspection.FailedWorkers[0].Operation != "group_sendack" {
		t.Fatalf("diagnostic evidence = %+v", inspection)
	}
}

func TestWorkloadSummarySourceReportsMissingSummaryAsInProgress(t *testing.T) {
	result, err := newWorkloadSummarySource(t.TempDir()).inspect(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("inspect() error = %v", err)
	}
	if result.Completeness != analysis.CompletenessPartial {
		t.Fatalf("completeness = %q, want partial", result.Completeness)
	}
	inspection := result.Data.(analysis.WorkloadInspection)
	if inspection.RunID != "run-1" || inspection.State != "in_progress" {
		t.Fatalf("inspection = %+v", inspection)
	}
}

func TestWorkloadSummarySourceRejectsIdentityMismatchAndOversize(t *testing.T) {
	valid := `{"schema":"wukongim/wkbench-diagnostic-summary/v1","run_id":"other-run","status":"failed","exit_code":2,"stability_verdict":"harness_invalid","summary":{"send_success":42,"connect_error_rate":0,"sendack_error_rate":0,"recv_verify_error_rate":0,"worker_failed":1,"sendack_max_worker_p99":1000000000,"recv_max_worker_p99":1000000000},"violations":[],"warnings":[],"phase_windows":[],"failed_workers":[],"failed_workers_truncated":false}`
	if _, err := decodeWorkloadSummary(strings.NewReader(valid), "run-1"); !errors.Is(err, errInvalidWorkloadSummary) {
		t.Fatalf("identity mismatch error = %v, want %v", err, errInvalidWorkloadSummary)
	}
	if _, err := decodeWorkloadSummary(strings.NewReader(strings.Repeat("x", maxWorkloadSummaryBytes+1)), "run-1"); !errors.Is(err, errInvalidWorkloadSummary) {
		t.Fatalf("oversize error = %v, want %v", err, errInvalidWorkloadSummary)
	}
}

func TestWorkloadSummarySourceRejectsIncompleteFailureDetails(t *testing.T) {
	summary := `{
  "schema":"wukongim/wkbench-diagnostic-summary/v1",
  "run_id":"run-1",
  "status":"failed",
  "exit_code":4,
  "stability_verdict":"harness_invalid",
  "summary":{"send_success":42,"connect_error_rate":0,"sendack_error_rate":0,"recv_verify_error_rate":0,"worker_failed":1,"sendack_max_worker_p99":0,"recv_max_worker_p99":0},
  "violations":[],
  "warnings":[],
  "phase_windows":[],
  "failed_workers":[],
  "failed_workers_truncated":false
}`

	if _, err := decodeWorkloadSummary(strings.NewReader(summary), "run-1"); !errors.Is(err, errInvalidWorkloadSummary) {
		t.Fatalf("incomplete failure details error = %v, want %v", err, errInvalidWorkloadSummary)
	}
}

func TestWorkloadSummarySourceRejectsUntrustedFailureDetail(t *testing.T) {
	summary := `{
  "schema":"wukongim/wkbench-diagnostic-summary/v1",
  "run_id":"run-1",
  "status":"failed",
  "exit_code":4,
  "stability_verdict":"harness_invalid",
  "summary":{"send_success":42,"connect_error_rate":0,"sendack_error_rate":0,"recv_verify_error_rate":0,"worker_failed":1,"sendack_max_worker_p99":0,"recv_max_worker_p99":0},
  "violations":[],
  "warnings":[],
  "phase_windows":[],
  "failed_workers":[{"worker_id":"worker-1","phase":"collect","reason_code":"worker_report_unavailable","detail":"open current-run.json: permission denied","observed_at":"2026-07-17T03:53:18Z"}],
  "failed_workers_truncated":false
}`

	if _, err := decodeWorkloadSummary(strings.NewReader(summary), "run-1"); !errors.Is(err, errInvalidWorkloadSummary) {
		t.Fatalf("untrusted failure detail error = %v, want %v", err, errInvalidWorkloadSummary)
	}
}

func TestWorkloadSummarySourceRejectsUntrustedFailureOperation(t *testing.T) {
	summary := `{
  "schema":"wukongim/wkbench-diagnostic-summary/v1",
  "run_id":"run-1",
  "status":"failed",
  "exit_code":4,
  "stability_verdict":"harness_invalid",
  "summary":{"send_success":42,"connect_error_rate":0,"sendack_error_rate":0,"recv_verify_error_rate":0,"worker_failed":1,"sendack_max_worker_p99":0,"recv_max_worker_p99":0},
  "violations":[],
  "warnings":[],
  "phase_windows":[],
  "failed_workers":[{"worker_id":"worker-1","phase":"warmup","reason_code":"phase_hook_failed","operation":"file:///tmp/forged-operation","detail":"worker phase hook failed","observed_at":"2026-07-17T03:53:18Z"}],
  "failed_workers_truncated":false
}`

	if _, err := decodeWorkloadSummary(strings.NewReader(summary), "run-1"); !errors.Is(err, errInvalidWorkloadSummary) {
		t.Fatalf("untrusted failure operation error = %v, want %v", err, errInvalidWorkloadSummary)
	}
	if validWorkloadFailureOperation("worker_report_unavailable", "group_sendack") {
		t.Fatal("session operation must not attach to a non-session failure reason")
	}
}

func TestWorkloadSummarySourceRejectsIncompleteFailureTuple(t *testing.T) {
	valid := `{
  "schema":"wukongim/wkbench-diagnostic-summary/v1",
  "run_id":"run-1",
  "status":"failed",
  "exit_code":4,
  "stability_verdict":"harness_invalid",
  "summary":{"send_success":0,"connect_error_rate":0,"sendack_error_rate":0,"recv_verify_error_rate":0,"worker_failed":1,"sendack_max_worker_p99":0,"recv_max_worker_p99":0},
  "violations":[],
  "warnings":[],
  "phase_windows":[],
  "failed_workers":[{"worker_id":"worker-1","phase":"collect","reason_code":"worker_report_unavailable","detail":"worker report unavailable","observed_at":"2026-07-17T03:53:18Z"}],
  "failed_workers_truncated":false
}`
	tests := map[string]string{
		"missing phase":  strings.Replace(valid, `"phase":"collect",`, "", 1),
		"missing detail": strings.Replace(valid, `"detail":"worker report unavailable",`, "", 1),
	}
	for name, summary := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeWorkloadSummary(strings.NewReader(summary), "run-1"); !errors.Is(err, errInvalidWorkloadSummary) {
				t.Fatalf("incomplete failure tuple error = %v, want %v", err, errInvalidWorkloadSummary)
			}
		})
	}
}

func TestWorkloadSummarySourceCanBeExplicitlyUnavailable(t *testing.T) {
	result, err := newWorkloadSummarySource("").inspect(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("inspect() error = %v", err)
	}
	if result.Completeness != analysis.CompletenessUnavailable {
		t.Fatalf("completeness = %q, want unavailable", result.Completeness)
	}
}
