package cloudanalysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

const maxWorkloadSummaryBytes = 16 << 10

const workloadDiagnosticSummarySchema = "wukongim/wkbench-diagnostic-summary/v1"

var errInvalidWorkloadSummary = errors.New("internal/infra/cloudanalysis: invalid workload summary")

var (
	workloadWorkerIDPattern   = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	workloadReasonCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type workloadSummarySource struct {
	summaryPath string
}

func newWorkloadSummarySource(reportDir string) *workloadSummarySource {
	if strings.TrimSpace(reportDir) == "" {
		return &workloadSummarySource{}
	}
	return &workloadSummarySource{
		summaryPath: filepath.Join(filepath.Clean(reportDir), "diagnostic-summary.json"),
	}
}

func (s *workloadSummarySource) inspect(ctx context.Context, runID string) (analysis.SourceResult, error) {
	if err := ctx.Err(); err != nil {
		return analysis.SourceResult{}, err
	}
	if strings.TrimSpace(runID) == "" {
		return analysis.SourceResult{}, errInvalidWorkloadSummary
	}
	if s.summaryPath == "" {
		return analysis.SourceResult{
			Node: "sim", Source: "wkbench_diagnostic_summary", Completeness: analysis.CompletenessUnavailable,
			Warnings: []string{"workload summary source is not configured"},
			Data:     analysis.WorkloadInspection{RunID: runID, State: "in_progress"},
		}, nil
	}
	file, err := os.Open(s.summaryPath)
	if errors.Is(err, os.ErrNotExist) {
		return analysis.SourceResult{
			Node: "sim", Source: "wkbench_diagnostic_summary", Completeness: analysis.CompletenessPartial,
			Warnings: []string{"final wkbench diagnostic summary is not available; the workload may still be running or may have failed before reporting"},
			Data:     analysis.WorkloadInspection{RunID: runID, State: "in_progress"},
		}, nil
	}
	if err != nil {
		return analysis.SourceResult{}, fmt.Errorf("read workload summary: %w", err)
	}
	defer file.Close()
	inspection, err := decodeWorkloadSummary(io.LimitReader(file, maxWorkloadSummaryBytes+1), runID)
	if err != nil {
		return analysis.SourceResult{}, err
	}
	return analysis.SourceResult{
		Node: "sim", Source: "wkbench_diagnostic_summary", Completeness: analysis.CompletenessComplete, Data: inspection,
	}, nil
}

func decodeWorkloadSummary(reader io.Reader, expectedRunID string) (analysis.WorkloadInspection, error) {
	data, err := io.ReadAll(reader)
	if err != nil || len(data) > maxWorkloadSummaryBytes {
		return analysis.WorkloadInspection{}, errInvalidWorkloadSummary
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var document workloadDiagnosticSummary
	if err := decoder.Decode(&document); err != nil {
		return analysis.WorkloadInspection{}, errInvalidWorkloadSummary
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return analysis.WorkloadInspection{}, errInvalidWorkloadSummary
	}
	if document.Schema != workloadDiagnosticSummarySchema || document.RunID != expectedRunID {
		return analysis.WorkloadInspection{}, errInvalidWorkloadSummary
	}
	if !validWorkloadTerminal(document.Status, document.ExitCode, document.StabilityVerdict) {
		return analysis.WorkloadInspection{}, errInvalidWorkloadSummary
	}
	if !validWorkloadSummary(document.Summary) || !validWorkloadLimits(document.Violations) || !validWorkloadLimits(document.Warnings) ||
		!validWorkloadPhaseWindows(document.PhaseWindows) || !validWorkloadFailures(document.FailedWorkers, document.Summary.WorkerFailed, document.FailedWorkersTruncated) {
		return analysis.WorkloadInspection{}, errInvalidWorkloadSummary
	}
	phaseWindows := make([]analysis.WorkloadPhaseWindow, 0, len(document.PhaseWindows))
	for _, window := range document.PhaseWindows {
		phaseWindows = append(phaseWindows, analysis.WorkloadPhaseWindow{
			Phase: window.Phase, StartedAt: window.StartedAt, EndedAt: window.EndedAt,
		})
	}
	failedWorkers := make([]analysis.WorkloadWorkerFailure, 0, len(document.FailedWorkers))
	for _, failure := range document.FailedWorkers {
		failedWorkers = append(failedWorkers, analysis.WorkloadWorkerFailure{
			WorkerID: failure.WorkerID, Phase: failure.Phase, ReasonCode: failure.ReasonCode,
			Detail: failure.Detail, ObservedAt: failure.ObservedAt,
		})
	}
	return analysis.WorkloadInspection{
		RunID: expectedRunID, State: "completed", Status: document.Status, ExitCode: document.ExitCode,
		StabilityVerdict: document.StabilityVerdict,
		Summary: analysis.WorkloadSummary{
			SendSuccess:      document.Summary.SendSuccess,
			ConnectErrorRate: document.Summary.ConnectErrorRate, SendackErrorRate: document.Summary.SendackErrorRate,
			RecvVerifyErrorRate: document.Summary.RecvVerifyErrorRate, WorkerFailed: document.Summary.WorkerFailed,
			SendackMaxWorkerP99: document.Summary.SendackMaxWorkerP99.String(), ReceiveMaxWorkerP99: document.Summary.ReceiveMaxWorkerP99.String(),
		},
		Violations: mapWorkloadLimits(document.Violations), LimitWarnings: mapWorkloadLimits(document.Warnings),
		PhaseWindows: phaseWindows, FailedWorkers: failedWorkers, FailedWorkersTruncated: document.FailedWorkersTruncated,
	}, nil
}

type workloadDiagnosticSummary struct {
	Schema                 string                      `json:"schema"`
	RunID                  string                      `json:"run_id"`
	Status                 string                      `json:"status"`
	ExitCode               int                         `json:"exit_code"`
	StabilityVerdict       string                      `json:"stability_verdict"`
	Summary                workloadDiagnosticMetrics   `json:"summary"`
	Violations             []workloadDiagnosticLimit   `json:"violations"`
	Warnings               []workloadDiagnosticLimit   `json:"warnings"`
	PhaseWindows           []workloadDiagnosticWindow  `json:"phase_windows"`
	FailedWorkers          []workloadDiagnosticFailure `json:"failed_workers"`
	FailedWorkersTruncated bool                        `json:"failed_workers_truncated"`
}

type workloadDiagnosticMetrics struct {
	SendSuccess         uint64        `json:"send_success"`
	ConnectErrorRate    float64       `json:"connect_error_rate"`
	SendackErrorRate    float64       `json:"sendack_error_rate"`
	RecvVerifyErrorRate float64       `json:"recv_verify_error_rate"`
	WorkerFailed        int           `json:"worker_failed"`
	SendackMaxWorkerP99 time.Duration `json:"sendack_max_worker_p99"`
	ReceiveMaxWorkerP99 time.Duration `json:"recv_max_worker_p99"`
}

type workloadDiagnosticLimit struct {
	Name   string  `json:"name"`
	Actual float64 `json:"actual"`
	Limit  float64 `json:"limit"`
	Hard   bool    `json:"hard"`
}

type workloadDiagnosticWindow struct {
	Phase     string    `json:"phase"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

type workloadDiagnosticFailure struct {
	WorkerID   string    `json:"worker_id"`
	Phase      string    `json:"phase,omitempty"`
	ReasonCode string    `json:"reason_code"`
	Detail     string    `json:"detail,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

func validWorkloadTerminal(status string, exitCode int, verdict string) bool {
	if exitCode < 0 || exitCode > 6 || status != "passed" && status != "failed" || status == "passed" && exitCode != 0 || status == "failed" && exitCode == 0 {
		return false
	}
	switch verdict {
	case "passed", "product_failure", "infrastructure_failure", "harness_invalid", "operator_modified", "insufficient_evidence":
		return true
	default:
		return false
	}
}

func validWorkloadSummary(summary workloadDiagnosticMetrics) bool {
	return validWorkloadRate(summary.ConnectErrorRate) && validWorkloadRate(summary.SendackErrorRate) &&
		validWorkloadRate(summary.RecvVerifyErrorRate) && summary.WorkerFailed >= 0 &&
		summary.SendackMaxWorkerP99 >= 0 && summary.ReceiveMaxWorkerP99 >= 0
}

func validWorkloadRate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func validWorkloadLimits(limits []workloadDiagnosticLimit) bool {
	if len(limits) > 32 {
		return false
	}
	for _, limit := range limits {
		if strings.TrimSpace(limit.Name) == "" || len(limit.Name) > 128 || math.IsNaN(limit.Actual) || math.IsInf(limit.Actual, 0) || math.IsNaN(limit.Limit) || math.IsInf(limit.Limit, 0) {
			return false
		}
	}
	return true
}

func validWorkloadPhaseWindows(windows []workloadDiagnosticWindow) bool {
	if len(windows) > 5 {
		return false
	}
	seen := make(map[string]struct{}, len(windows))
	for _, window := range windows {
		if !validWorkloadPhase(window.Phase) || window.StartedAt.IsZero() || window.EndedAt.Before(window.StartedAt) {
			return false
		}
		if _, exists := seen[window.Phase]; exists {
			return false
		}
		seen[window.Phase] = struct{}{}
	}
	return true
}

func validWorkloadFailures(failures []workloadDiagnosticFailure, failedCount int, truncated bool) bool {
	if len(failures) > 16 {
		return false
	}
	workers := make(map[string]struct{}, len(failures))
	for _, failure := range failures {
		if !workloadWorkerIDPattern.MatchString(failure.WorkerID) || failure.Phase != "" && !validWorkloadFailurePhase(failure.Phase) ||
			!workloadReasonCodePattern.MatchString(failure.ReasonCode) || len(failure.Detail) > 256 || failure.ObservedAt.IsZero() {
			return false
		}
		workers[failure.WorkerID] = struct{}{}
	}
	if failedCount == 0 {
		return len(failures) == 0 && !truncated
	}
	if truncated {
		return len(failures) == 16 && len(workers) <= failedCount
	}
	return len(workers) == failedCount
}

func validWorkloadPhase(phase string) bool {
	switch phase {
	case "prepare", "connect", "warmup", "run", "cooldown":
		return true
	default:
		return false
	}
}

func validWorkloadFailurePhase(phase string) bool {
	return phase == "assign" || phase == "collect" || validWorkloadPhase(phase)
}

func mapWorkloadLimits(input []workloadDiagnosticLimit) []analysis.WorkloadLimit {
	output := make([]analysis.WorkloadLimit, 0, len(input))
	for _, limit := range input {
		output = append(output, analysis.WorkloadLimit{Name: limit.Name, Actual: limit.Actual, Limit: limit.Limit, Hard: limit.Hard})
	}
	return output
}
