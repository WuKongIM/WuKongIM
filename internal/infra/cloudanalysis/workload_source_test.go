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
	summary := `# wkbench report
- run_id: run-1
- status: passed
- exit_code: 0
- sendack_error_rate: 0.000001
- connect_error_rate: 0.000002
- recv_verify_error_rate: 0.000003
- worker_failed: 0
- sendack_max_worker_p99: 125ms
- recv_max_worker_p99: 250ms
`
	if err := os.WriteFile(filepath.Join(reportDir, "summary.md"), []byte(summary), 0o600); err != nil {
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
	if inspection.RunID != "run-1" || inspection.State != "completed" || inspection.Status != "passed" || inspection.ExitCode != 0 {
		t.Fatalf("inspection = %+v", inspection)
	}
	if inspection.Summary.ConnectErrorRate != 0.000002 || inspection.Summary.SendackMaxWorkerP99 != "125ms" || inspection.Summary.ReceiveMaxWorkerP99 != "250ms" {
		t.Fatalf("summary = %+v", inspection.Summary)
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
	valid := `# wkbench report
- run_id: other-run
- status: failed
- exit_code: 2
- sendack_error_rate: 0
- connect_error_rate: 0
- recv_verify_error_rate: 0
- worker_failed: 1
- sendack_max_worker_p99: 1s
- recv_max_worker_p99: 1s
`
	if _, err := decodeWorkloadSummary(strings.NewReader(valid), "run-1"); !errors.Is(err, errInvalidWorkloadSummary) {
		t.Fatalf("identity mismatch error = %v, want %v", err, errInvalidWorkloadSummary)
	}
	if _, err := decodeWorkloadSummary(strings.NewReader(strings.Repeat("x", maxWorkloadSummaryBytes+1)), "run-1"); !errors.Is(err, errInvalidWorkloadSummary) {
		t.Fatalf("oversize error = %v, want %v", err, errInvalidWorkloadSummary)
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
