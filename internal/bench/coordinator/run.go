package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/WuKongIM/WuKongIM/internal/bench/planner"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
)

const (
	defaultPollInterval = 25 * time.Millisecond
	defaultPollTimeout  = 10 * time.Second
	defaultStopTimeout  = 2 * time.Second
	maxBodySnippetBytes = 512
)

// Phase is the fake workload lifecycle phase orchestrated by the coordinator.
type Phase = worker.Phase

const (
	// PhasePrepare asks workers to prepare benchmark data or local state.
	PhasePrepare Phase = worker.PhasePrepare
	// PhaseConnect asks workers to establish fake/no-op client connections.
	PhaseConnect Phase = worker.PhaseConnect
	// PhaseWarmup asks workers to run fake/no-op warmup traffic.
	PhaseWarmup Phase = worker.PhaseWarmup
	// PhaseRun asks workers to run fake/no-op measured traffic.
	PhaseRun Phase = worker.PhaseRun
	// PhaseCooldown asks workers to drain fake/no-op traffic.
	PhaseCooldown Phase = worker.PhaseCooldown
)

// RunStatus is the terminal coordinator result status.
type RunStatus string

const (
	// StatusCompleted means all workers completed every fake workload phase.
	StatusCompleted RunStatus = "completed"
	// StatusConfigFailed means static scenario or worker planning failed.
	StatusConfigFailed RunStatus = "config_failed"
	// StatusPreflightFailed means target or worker preflight failed before assignment.
	StatusPreflightFailed RunStatus = "preflight_failed"
	// StatusWorkerFailed means at least one worker failed or was unreachable.
	StatusWorkerFailed RunStatus = "worker_failed"
	// StatusCanceled means the coordinator context was canceled before completion.
	StatusCanceled RunStatus = "canceled"
	// StatusInternalFailed means the coordinator hit an unexpected internal error.
	StatusInternalFailed RunStatus = "internal_failed"
)

// ExitCode maps a terminal run status to the wkbench CLI exit code contract.
func (s RunStatus) ExitCode() int {
	switch s {
	case StatusCompleted:
		return 0
	case StatusConfigFailed:
		return 1
	case StatusPreflightFailed:
		return 2
	case StatusWorkerFailed:
		return 4
	default:
		return 6
	}
}

// RunResult summarizes a coordinator run.
type RunResult struct {
	// RunID is the scenario run identifier.
	RunID string
	// Status is the terminal coordinator status.
	Status RunStatus
	// Plan is the deterministic worker assignment used for the run.
	Plan model.Plan
}

// PreflightChecker verifies target and worker readiness before assignment.
type PreflightChecker interface {
	Check(ctx context.Context, target model.Target, workers model.WorkerSet) error
}

// CoordinatorConfig wires coordinator dependencies and polling behavior.
type CoordinatorConfig struct {
	// Workers are worker control clients available for this run.
	Workers []model.Worker
	// Target describes the black-box WuKongIM deployment under test.
	Target model.Target
	// HTTPClient overrides worker HTTP calls for tests.
	HTTPClient *http.Client
	// Preflight overrides the default black-box preflight checker for tests.
	Preflight PreflightChecker
	// PollInterval is the delay between worker status polls.
	PollInterval time.Duration
	// PollTimeout is the maximum wait for a worker to report a requested phase.
	PollTimeout time.Duration
	// StopTimeout bounds best-effort stop requests after failure or cancellation.
	StopTimeout time.Duration
}

// Coordinator assigns workers and drives fake/no-op workload phases.
type Coordinator struct {
	cfg       CoordinatorConfig
	http      *http.Client
	preflight PreflightChecker
}

// New creates a coordinator for one wkbench run.
func New(cfg CoordinatorConfig) *Coordinator {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: workerInfoTimeout}
	}
	preflight := cfg.Preflight
	if preflight == nil {
		preflight = NewPreflight(PreflightConfig{HTTPClient: hc})
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = defaultPollTimeout
	}
	if cfg.StopTimeout <= 0 {
		cfg.StopTimeout = defaultStopTimeout
	}
	return &Coordinator{cfg: cfg, http: hc, preflight: preflight}
}

// Run builds the worker plan, runs preflight, assigns workers, and executes fake phases.
func (c *Coordinator) Run(ctx context.Context, scenario model.Scenario) (RunResult, error) {
	result := RunResult{RunID: scenario.Run.ID}
	plan, err := planner.Build(scenario, c.cfg.Workers)
	if err != nil {
		result.Status = StatusConfigFailed
		return result, err
	}
	result.Plan = plan

	workers := model.WorkerSet{Workers: c.cfg.Workers}
	if err := c.preflight.Check(ctx, c.cfg.Target, workers); err != nil {
		if errorsIsContext(err) {
			result.Status = StatusCanceled
		} else {
			result.Status = StatusPreflightFailed
		}
		return result, err
	}
	if err := c.checkContext(ctx, &result); err != nil {
		return result, err
	}

	if err := c.assignWorkers(ctx, scenario.Run.ID); err != nil {
		result.Status = statusForError(err)
		if result.Status == StatusCanceled {
			c.stopAll()
		}
		return result, err
	}

	for _, phase := range runPhases() {
		if err := c.runPhase(ctx, phase); err != nil {
			result.Status = statusForError(err)
			if result.Status == StatusCanceled || scenario.Run.FailFast {
				c.stopAll()
			}
			return result, err
		}
	}
	result.Status = StatusCompleted
	return result, nil
}

func (c *Coordinator) assignWorkers(ctx context.Context, runID string) error {
	for _, w := range c.cfg.Workers {
		assignment := worker.Assignment{RunID: runID, WorkerID: strings.TrimSpace(w.ID)}
		if err := c.postJSON(ctx, w, "/v1/assign", assignment, nil); err != nil {
			return fmt.Errorf("worker %s assign failed: %w", workerName(w), err)
		}
	}
	return nil
}

func (c *Coordinator) runPhase(ctx context.Context, phase Phase) error {
	for _, w := range c.cfg.Workers {
		if err := c.postJSON(ctx, w, "/v1/phase/"+string(phase), nil, nil); err != nil {
			return fmt.Errorf("worker %s phase %s failed: %w", workerName(w), phase, err)
		}
	}
	for _, w := range c.cfg.Workers {
		if err := c.waitForPhase(ctx, w, phase); err != nil {
			return fmt.Errorf("worker %s wait for phase %s failed: %w", workerName(w), phase, err)
		}
	}
	return nil
}

func (c *Coordinator) waitForPhase(ctx context.Context, w model.Worker, want Phase) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.PollTimeout)
	defer cancel()
	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()
	for {
		status, err := c.workerStatus(ctx, w)
		if err != nil {
			return err
		}
		if status.Phase == want {
			return nil
		}
		if status.Phase == worker.PhaseStopped {
			return fmt.Errorf("worker stopped before phase %s", want)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Coordinator) workerStatus(ctx context.Context, w model.Worker) (worker.Status, error) {
	var status worker.Status
	if err := c.getJSON(ctx, w, "/v1/status", &status); err != nil {
		return worker.Status{}, err
	}
	return status, nil
}

func (c *Coordinator) stopAll() {
	ctx, cancel := context.WithTimeout(context.Background(), c.cfg.StopTimeout)
	defer cancel()
	for _, w := range c.cfg.Workers {
		_ = c.postJSON(ctx, w, "/v1/stop", nil, nil)
	}
}

func (c *Coordinator) postJSON(ctx context.Context, w model.Worker, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s: %w", path, err)
		}
		reader = bytes.NewReader(data)
	}
	return c.doJSON(ctx, http.MethodPost, w, path, reader, body != nil, out)
}

func (c *Coordinator) getJSON(ctx context.Context, w model.Worker, path string, out any) error {
	return c.doJSON(ctx, http.MethodGet, w, path, nil, false, out)
}

func (c *Coordinator) doJSON(ctx context.Context, method string, w model.Worker, path string, body io.Reader, hasBody bool, out any) error {
	url := strings.TrimRight(strings.TrimSpace(w.Addr), "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("build %s %s: %w", method, url, err)
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if !w.InsecureControl && strings.TrimSpace(w.ControlToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(w.ControlToken))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return coordinatorStatusError(method, url, resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, url, err)
	}
	return nil
}

func (c *Coordinator) checkContext(ctx context.Context, result *RunResult) error {
	select {
	case <-ctx.Done():
		result.Status = StatusCanceled
		return ctx.Err()
	default:
		return nil
	}
}

func runPhases() []Phase {
	return []Phase{PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown}
}

func statusForError(err error) RunStatus {
	if errors.Is(err, context.Canceled) {
		return StatusCanceled
	}
	return StatusWorkerFailed
}

func errorsIsContext(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func coordinatorStatusError(method, url string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodySnippetBytes))
	snippet := strings.TrimSpace(string(body))
	if snippet == "" {
		return fmt.Errorf("%s %s returned status %d", method, url, resp.StatusCode)
	}
	return fmt.Errorf("%s %s returned status %d: %s", method, url, resp.StatusCode, snippet)
}
