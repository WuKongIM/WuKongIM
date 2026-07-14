package cloudanalysis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

const maxWorkloadSummaryBytes = 16 << 10

var errInvalidWorkloadSummary = errors.New("internal/infra/cloudanalysis: invalid workload summary")

type workloadSummarySource struct {
	summaryPath string
}

func newWorkloadSummarySource(reportDir string) *workloadSummarySource {
	if strings.TrimSpace(reportDir) == "" {
		return &workloadSummarySource{}
	}
	return &workloadSummarySource{
		summaryPath: filepath.Join(filepath.Clean(reportDir), "summary.md"),
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
			Node: "sim", Source: "wkbench_summary", Completeness: analysis.CompletenessUnavailable,
			Warnings: []string{"workload summary source is not configured"},
			Data:     analysis.WorkloadInspection{RunID: runID, State: "in_progress"},
		}, nil
	}
	file, err := os.Open(s.summaryPath)
	if errors.Is(err, os.ErrNotExist) {
		return analysis.SourceResult{
			Node: "sim", Source: "wkbench_summary", Completeness: analysis.CompletenessPartial,
			Warnings: []string{"final wkbench summary is not available; the workload may still be running or may have failed before reporting"},
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
		Node: "sim", Source: "wkbench_summary", Completeness: analysis.CompletenessComplete, Data: inspection,
	}, nil
}

func decodeWorkloadSummary(reader io.Reader, expectedRunID string) (analysis.WorkloadInspection, error) {
	data, err := io.ReadAll(reader)
	if err != nil || len(data) > maxWorkloadSummaryBytes {
		return analysis.WorkloadInspection{}, errInvalidWorkloadSummary
	}
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return analysis.WorkloadInspection{}, errInvalidWorkloadSummary
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if scanner.Err() != nil || values["run_id"] != expectedRunID {
		return analysis.WorkloadInspection{}, errInvalidWorkloadSummary
	}
	status := values["status"]
	exitCode, err := strconv.Atoi(values["exit_code"])
	if err != nil || exitCode < 0 || exitCode > 6 || status != "passed" && status != "failed" || status == "passed" && exitCode != 0 || status == "failed" && exitCode == 0 {
		return analysis.WorkloadInspection{}, errInvalidWorkloadSummary
	}
	connectRate, err := parseWorkloadRate(values["connect_error_rate"])
	if err != nil {
		return analysis.WorkloadInspection{}, err
	}
	sendackRate, err := parseWorkloadRate(values["sendack_error_rate"])
	if err != nil {
		return analysis.WorkloadInspection{}, err
	}
	recvRate, err := parseWorkloadRate(values["recv_verify_error_rate"])
	if err != nil {
		return analysis.WorkloadInspection{}, err
	}
	workerFailed, err := strconv.Atoi(values["worker_failed"])
	if err != nil || workerFailed < 0 {
		return analysis.WorkloadInspection{}, errInvalidWorkloadSummary
	}
	sendackP99, err := parseWorkloadDuration(values["sendack_max_worker_p99"])
	if err != nil {
		return analysis.WorkloadInspection{}, err
	}
	recvP99, err := parseWorkloadDuration(values["recv_max_worker_p99"])
	if err != nil {
		return analysis.WorkloadInspection{}, err
	}
	return analysis.WorkloadInspection{
		RunID: expectedRunID, State: "completed", Status: status, ExitCode: exitCode,
		Summary: analysis.WorkloadSummary{
			ConnectErrorRate: connectRate, SendackErrorRate: sendackRate, RecvVerifyErrorRate: recvRate,
			WorkerFailed: workerFailed, SendackMaxWorkerP99: sendackP99, ReceiveMaxWorkerP99: recvP99,
		},
	}, nil
}

func parseWorkloadRate(raw string) (float64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return 0, errInvalidWorkloadSummary
	}
	return value, nil
}

func parseWorkloadDuration(raw string) (string, error) {
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		return "", errInvalidWorkloadSummary
	}
	return value.String(), nil
}
