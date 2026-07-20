package capacity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/coordinator"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

// Runner executes capacity attempts against a discovered target.
type Runner struct {
	cfg        Config
	discovered DiscoveredTarget
	server     *httptest.Server
	workers    []model.Worker
}

// NewRunner creates a capacity runner using one temporary local worker.
func NewRunner(cfg Config, discovered DiscoveredTarget) *Runner {
	return &Runner{cfg: cfg, discovered: discovered}
}

// Run executes the configured capacity search.
func (r *Runner) Run(ctx context.Context) (result Result, err error) {
	if err := r.startWorker(); err != nil {
		return Result{}, err
	}
	defer func() {
		err = errors.Join(err, r.close())
	}()
	result, err = Search(ctx, r.cfg, r)
	result.ReportDir = r.cfg.ReportDir
	return result, err
}

// RunAttempt executes one offered-QPS scenario through the normal coordinator.
func (r *Runner) RunAttempt(ctx context.Context, attempt Attempt) (AttemptResult, error) {
	scenario := BuildScenario(r.cfg, attempt)
	coord := coordinator.New(coordinator.CoordinatorConfig{Workers: r.workers, Target: r.discovered.Target})
	result, err := coord.Run(ctx, scenario)
	var stopErr error
	if strings.TrimSpace(result.AssignmentID) != "" {
		stopErr = r.stopWorkers(worker.StopRequest{RunID: result.RunID, AssignmentID: result.AssignmentID})
	}
	err = errors.Join(err, stopErr)
	attemptResult := EvaluateAttempt(r.cfg, attempt, result.Report)
	attemptResult.ReportDir = scenario.Run.ReportDir
	if stopErr != nil {
		attemptResult.Passed = false
		attemptResult.WorkerFailed = max(attemptResult.WorkerFailed, 1)
		attemptResult.FailureReason = "worker_stop_failed"
	}
	if err != nil && result.Status != coordinator.StatusHardLimitFailed {
		applyCoordinatorFailure(&attemptResult, result)
		if stopErr != nil {
			attemptResult.FailureReason = "worker_stop_failed"
		}
	}
	if stopErr != nil {
		return attemptResult, err
	}
	if err != nil && !hasAttemptReport(result.Report) {
		return attemptResult, err
	}
	return attemptResult, nil
}

func hasAttemptReport(rep report.Report) bool {
	return rep.Scenario.Run.ID != "" || len(rep.Metrics.Counters) > 0 || len(rep.Metrics.Histograms) > 0 || len(rep.WorkerMetrics) > 0 || len(rep.WorkerReports) > 0
}

func applyCoordinatorFailure(attempt *AttemptResult, result coordinator.RunResult) {
	if attempt == nil {
		return
	}
	switch result.Status {
	case coordinator.StatusWorkerFailed:
		if attempt.WorkerFailed == 0 {
			attempt.WorkerFailed = 1
		}
		attempt.Passed = false
		attempt.FailureReason = "worker_failed"
	case coordinator.StatusTargetUnavailable:
		attempt.Passed = false
		attempt.FailureReason = "target_unavailable"
	case coordinator.StatusHardLimitFailed:
		attempt.Passed = false
	case coordinator.StatusCanceled, coordinator.StatusInternalFailed, coordinator.StatusConfigFailed, coordinator.StatusPreflightFailed:
		attempt.Passed = false
		attempt.FailureReason = string(result.Status)
	}
}

func (r *Runner) startWorker() error {
	if r.server != nil {
		return nil
	}
	server := httptest.NewServer(worker.NewServer(worker.Config{InsecureControl: true}))
	r.server = server
	r.workers = []model.Worker{{ID: "local-capacity-worker", Addr: server.URL, Weight: 1, InsecureControl: true}}
	return nil
}

func (r *Runner) close() error {
	stopErr := r.stopWorkersFromStatus()
	if r.server != nil {
		r.server.Close()
		r.server = nil
	}
	return stopErr
}

func (r *Runner) stopWorkers(request worker.StopRequest) error {
	return stopCapacityWorkers(r.workers, request)
}

func stopCapacityWorkers(workers []model.Worker, request worker.StopRequest) error {
	request.RunID = strings.TrimSpace(request.RunID)
	request.AssignmentID = strings.TrimSpace(request.AssignmentID)
	if request.RunID == "" && request.AssignmentID == "" {
		return nil
	}
	if request.RunID == "" || request.AssignmentID == "" {
		return fmt.Errorf("capacity worker stop requires exact run_id and assignment_id")
	}
	var errs []error
	for _, w := range workers {
		if strings.TrimSpace(w.Addr) == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		status, err := capacityWorkerStatus(ctx, w)
		if err == nil {
			statusRunID := strings.TrimSpace(status.Assignment.RunID)
			statusAssignmentID := strings.TrimSpace(status.Assignment.AssignmentID)
			if status.Phase == worker.PhaseIdle && statusRunID == "" && statusAssignmentID == "" {
				cancel()
				continue
			}
			if statusRunID != request.RunID || statusAssignmentID != request.AssignmentID || strings.TrimSpace(status.Assignment.WorkerID) != strings.TrimSpace(w.ID) {
				err = fmt.Errorf(
					"worker %s cleanup identity conflict: got %q/%q/%q want %q/%q/%q",
					strings.TrimSpace(w.ID),
					statusRunID,
					statusAssignmentID,
					strings.TrimSpace(status.Assignment.WorkerID),
					request.RunID,
					request.AssignmentID,
					strings.TrimSpace(w.ID),
				)
			} else if status.Phase == worker.PhaseStopped && status.ActivePhase == "" {
				cancel()
				continue
			} else {
				err = stopWorker(ctx, w, request)
			}
		}
		cancel()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Runner) stopWorkersFromStatus() error {
	var errs []error
	for _, w := range r.workers {
		if strings.TrimSpace(w.Addr) == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		status, err := capacityWorkerStatus(ctx, w)
		if err == nil && status.Phase == worker.PhaseIdle && strings.TrimSpace(status.Assignment.RunID) == "" && strings.TrimSpace(status.Assignment.AssignmentID) == "" {
			cancel()
			continue
		}
		if err == nil {
			err = stopWorker(ctx, w, worker.StopRequest{
				RunID:        status.Assignment.RunID,
				AssignmentID: status.Assignment.AssignmentID,
			})
		}
		cancel()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func stopWorker(ctx context.Context, w model.Worker, request worker.StopRequest) error {
	request.RunID = strings.TrimSpace(request.RunID)
	request.AssignmentID = strings.TrimSpace(request.AssignmentID)
	if request.RunID == "" || request.AssignmentID == "" {
		return fmt.Errorf("worker %s stop requires exact run_id and assignment_id", strings.TrimSpace(w.ID))
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode worker %s stop request: %w", strings.TrimSpace(w.ID), err)
	}
	url := strings.TrimRight(strings.TrimSpace(w.Addr), "/") + "/v1/stop"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if !w.InsecureControl && strings.TrimSpace(w.ControlToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(w.ControlToken))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("worker %s stop returned %s: %s", strings.TrimSpace(w.ID), resp.Status, strings.TrimSpace(string(detail)))
	}
	var status worker.Status
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status); err != nil {
		return fmt.Errorf("decode worker %s stop response: %w", strings.TrimSpace(w.ID), err)
	}
	if status.Assignment.RunID != request.RunID || status.Assignment.AssignmentID != request.AssignmentID || strings.TrimSpace(status.Assignment.WorkerID) != strings.TrimSpace(w.ID) {
		return fmt.Errorf(
			"worker %s stop response identity mismatch: got %q/%q/%q want %q/%q/%q",
			strings.TrimSpace(w.ID),
			status.Assignment.RunID,
			status.Assignment.AssignmentID,
			status.Assignment.WorkerID,
			request.RunID,
			request.AssignmentID,
			strings.TrimSpace(w.ID),
		)
	}
	if status.Phase != worker.PhaseStopped || status.ActivePhase != "" {
		return fmt.Errorf("worker %s stop response is not terminal: phase=%q active_phase=%q", strings.TrimSpace(w.ID), status.Phase, status.ActivePhase)
	}
	return nil
}

func capacityWorkerStatus(ctx context.Context, w model.Worker) (worker.Status, error) {
	url := strings.TrimRight(strings.TrimSpace(w.Addr), "/") + "/v1/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return worker.Status{}, err
	}
	if !w.InsecureControl && strings.TrimSpace(w.ControlToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(w.ControlToken))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return worker.Status{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return worker.Status{}, fmt.Errorf("worker %s status returned %s: %s", strings.TrimSpace(w.ID), resp.Status, strings.TrimSpace(string(detail)))
	}
	var status worker.Status
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status); err != nil {
		return worker.Status{}, fmt.Errorf("decode worker %s status response: %w", strings.TrimSpace(w.ID), err)
	}
	return status, nil
}

// EvaluateAttempt classifies one coordinator report against capacity criteria.
func EvaluateAttempt(cfg Config, attempt Attempt, rep report.Report) AttemptResult {
	send := report.SendRunSummaryFromMetrics(rep.Metrics, cfg.Duration)
	scheduled := scheduledMessagesForAttempt(attempt, cfg.Duration)
	out := AttemptResult{
		Attempt:           attempt,
		ActualQPS:         send.IngressQPS,
		ScheduledMessages: scheduled,
		SendSuccess:       send.SendSuccess,
		SendErrors:        send.SendErrors,
		BacklogMessages:   backlogMessages(scheduled, send.SendSuccess, send.SendErrors),
		SendackP50:        send.SendackP50,
		SendackP95:        send.SendackP95,
		SendackP99:        send.SendackP99,
		SendackErrorRate:  rep.Summary.SendackErrorRate,
		ConnectErrorRate:  rep.Summary.ConnectErrorRate,
		WorkerFailed:      rep.Summary.WorkerFailed,
		ReportDir:         rep.Scenario.Run.ReportDir,
	}
	out.Passed, out.FailureReason = attemptPassStatus(cfg, attempt, out)
	return out
}

func scheduledMessagesForAttempt(attempt Attempt, duration time.Duration) uint64 {
	if attempt.OfferedQPS <= 0 || duration <= 0 || math.IsNaN(attempt.OfferedQPS) || math.IsInf(attempt.OfferedQPS, 0) {
		return 0
	}
	scheduled := math.Round(attempt.OfferedQPS * duration.Seconds())
	if scheduled <= 0 {
		return 0
	}
	return uint64(scheduled)
}

func backlogMessages(scheduled, success, errors uint64) uint64 {
	observed := success + errors
	if observed >= scheduled {
		return 0
	}
	return scheduled - observed
}

func attemptPassStatus(cfg Config, attempt Attempt, result AttemptResult) (bool, string) {
	switch {
	case result.WorkerFailed > 0:
		return false, "worker_failed"
	case result.ConnectErrorRate > cfg.MaxConnectErrorRate:
		return false, "connect_error_rate_exceeded"
	case result.SendackErrorRate > cfg.MaxSendackErrorRate:
		return false, "sendack_error_rate_exceeded"
	case result.ActualQPS < attempt.OfferedQPS*cfg.MinActualRatio:
		return false, "actual_qps_below_min_ratio"
	case result.SendackP99 > cfg.StableP99:
		return false, "sendack_p99_exceeded"
	default:
		return true, ""
	}
}

func timestampedReportDir(root string, now time.Time) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, fmt.Sprintf("%s-send", now.Format("20060102-150405")))
}
