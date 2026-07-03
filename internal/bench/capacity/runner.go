package capacity

import (
	"context"
	"fmt"
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
func (r *Runner) Run(ctx context.Context) (Result, error) {
	if err := r.startWorker(); err != nil {
		return Result{}, err
	}
	defer r.close()
	result, err := Search(ctx, r.cfg, r)
	result.ReportDir = r.cfg.ReportDir
	return result, err
}

// RunAttempt executes one offered-QPS scenario through the normal coordinator.
func (r *Runner) RunAttempt(ctx context.Context, attempt Attempt) (AttemptResult, error) {
	scenario := BuildScenario(r.cfg, attempt)
	coord := coordinator.New(coordinator.CoordinatorConfig{Workers: r.workers, Target: r.discovered.Target})
	defer r.stopWorkers()
	result, err := coord.Run(ctx, scenario)
	attemptResult := EvaluateAttempt(r.cfg, attempt, result.Report)
	attemptResult.ReportDir = scenario.Run.ReportDir
	if err != nil && result.Status != coordinator.StatusHardLimitFailed {
		applyCoordinatorFailure(&attemptResult, result)
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

func (r *Runner) close() {
	r.stopWorkers()
	if r.server != nil {
		r.server.Close()
		r.server = nil
	}
}

func (r *Runner) stopWorkers() {
	for _, w := range r.workers {
		if strings.TrimSpace(w.Addr) == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = stopWorker(ctx, w)
		cancel()
	}
}

func stopWorker(ctx context.Context, w model.Worker) error {
	url := strings.TrimRight(strings.TrimSpace(w.Addr), "/") + "/v1/stop"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	if !w.InsecureControl && strings.TrimSpace(w.ControlToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(w.ControlToken))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
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
