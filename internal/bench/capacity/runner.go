package capacity

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/coordinator"
	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
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
	result, err := coord.Run(ctx, scenario)
	attemptResult := EvaluateAttempt(r.cfg, attempt, result.Report)
	attemptResult.ReportDir = scenario.Run.ReportDir
	if err != nil && result.Report.RunID == "" {
		return attemptResult, err
	}
	return attemptResult, nil
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
	if r.server != nil {
		r.server.Close()
		r.server = nil
	}
}

// EvaluateAttempt classifies one coordinator report against capacity criteria.
func EvaluateAttempt(cfg Config, attempt Attempt, rep report.Report) AttemptResult {
	send := report.SendRunSummaryFromMetrics(rep.Metrics, cfg.Duration)
	out := AttemptResult{
		Attempt:          attempt,
		ActualQPS:        send.IngressQPS,
		SendackP50:       send.SendackP50,
		SendackP95:       send.SendackP95,
		SendackP99:       send.SendackP99,
		SendackErrorRate: rep.Summary.SendackErrorRate,
		ConnectErrorRate: rep.Summary.ConnectErrorRate,
		WorkerFailed:     rep.Summary.WorkerFailed,
		ReportDir:        rep.Scenario.Run.ReportDir,
	}
	out.Passed, out.FailureReason = attemptPassStatus(cfg, attempt, out)
	return out
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
