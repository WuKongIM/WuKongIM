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
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/planner"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const (
	defaultPollInterval = 25 * time.Millisecond
	defaultPollTimeout  = 10 * time.Second
	defaultStopTimeout  = 2 * time.Second
	maxBodySnippetBytes = 512
)

var errWorkerReportCollection = errors.New("worker report collection failed")

// Phase is the workload lifecycle phase orchestrated by the coordinator.
type Phase = worker.Phase

const (
	// PhasePrepare asks workers to prepare benchmark data or local state.
	PhasePrepare Phase = worker.PhasePrepare
	// PhaseConnect asks workers to establish benchmark client connections.
	PhaseConnect Phase = worker.PhaseConnect
	// PhaseWarmup asks workers to run warmup traffic.
	PhaseWarmup Phase = worker.PhaseWarmup
	// PhaseRun asks workers to run measured traffic.
	PhaseRun Phase = worker.PhaseRun
	// PhaseCooldown asks workers to drain benchmark traffic.
	PhaseCooldown Phase = worker.PhaseCooldown
)

// RunStatus is the terminal coordinator result status.
type RunStatus string

const (
	// StatusCompleted means all workers completed every workload phase.
	StatusCompleted RunStatus = "completed"
	// StatusConfigFailed means static scenario or worker planning failed.
	StatusConfigFailed RunStatus = "config_failed"
	// StatusPreflightFailed means target or worker preflight failed before assignment.
	StatusPreflightFailed RunStatus = "preflight_failed"
	// StatusHardLimitFailed means one or more hard report limits failed.
	StatusHardLimitFailed RunStatus = "hard_limit_failed"
	// StatusWorkerFailed means at least one worker failed or was unreachable.
	StatusWorkerFailed RunStatus = "worker_failed"
	// StatusTargetUnavailable means the benchmark target became unavailable during the run.
	StatusTargetUnavailable RunStatus = "target_unavailable"
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
	case StatusHardLimitFailed:
		return 3
	case StatusWorkerFailed:
		return 4
	case StatusTargetUnavailable:
		return 5
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
	// Report is the optional run report written when scenario.run.report_dir is configured.
	Report report.Report
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

// Coordinator assigns workers and drives workload phases.
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

// Run builds the worker plan, runs preflight, assigns workers, and executes workload phases.
func (c *Coordinator) Run(ctx context.Context, scenario model.Scenario) (RunResult, error) {
	result := RunResult{RunID: scenario.Run.ID, Report: report.Report{RunID: scenario.Run.ID}}
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

	if err := c.assignWorkers(ctx, scenario, plan); err != nil {
		result.Status = statusForError(ctx, err)
		if result.Status == StatusCanceled {
			return result, err
		}
		failedWorkers := make(map[string]struct{})
		var failureDetails []report.WorkerFailure
		addWorkerFailures(failedWorkers, err)
		addWorkerFailureDetails(&failureDetails, err)
		if _, reportErr := c.writeReport(ctx, scenario, &result, failedWorkers, nil, failureDetails); reportErr != nil {
			if !errors.Is(reportErr, errWorkerReportCollection) {
				result.Status = StatusInternalFailed
			}
			return result, errors.Join(err, reportErr)
		}
		return result, err
	}

	phaseFailures := make(map[string]struct{})
	var workerFailures []report.WorkerFailure
	var phaseWindows []report.PhaseWindow
	var phaseErrs []error
	for _, phase := range runPhases() {
		startedAt := time.Now().UTC()
		err := c.runPhase(ctx, scenario, phase, phaseFailures, plan)
		phaseWindows = append(phaseWindows, report.PhaseWindow{Phase: string(phase), StartedAt: startedAt, EndedAt: time.Now().UTC()})
		if err != nil {
			result.Status = statusForError(ctx, err)
			if result.Status == StatusCanceled {
				c.stopAll(c.cfg.Workers)
				return result, err
			}
			phaseErrs = append(phaseErrs, err)
			addWorkerFailures(phaseFailures, err)
			addWorkerFailureDetails(&workerFailures, err)
			if scenario.Run.FailFast {
				c.stopAll(c.cfg.Workers)
				break
			}
		}
	}
	if len(phaseErrs) > 0 {
		result.Status = StatusWorkerFailed
		if isTargetUnavailable(errors.Join(phaseErrs...)) {
			result.Status = StatusTargetUnavailable
		}
		if _, err := c.writeReport(ctx, scenario, &result, phaseFailures, phaseWindows, workerFailures); err != nil {
			if errorsIsContext(err) {
				result.Status = StatusCanceled
			} else if !errors.Is(err, errWorkerReportCollection) {
				result.Status = StatusInternalFailed
			}
			return result, errors.Join(errors.Join(phaseErrs...), err)
		}
		return result, errors.Join(phaseErrs...)
	}
	rep, err := c.writeReport(ctx, scenario, &result, nil, phaseWindows, nil)
	if err != nil {
		if errorsIsContext(err) {
			result.Status = StatusCanceled
		} else if result.Status == "" {
			result.Status = StatusInternalFailed
		}
		return result, err
	}
	if rep.ExitCode == report.ExitHardLimitFailed {
		result.Status = StatusHardLimitFailed
		result.Report = rep
		return result, fmt.Errorf("hard limit failed")
	}
	result.Status = StatusCompleted
	return result, nil
}

func (c *Coordinator) writeReport(
	ctx context.Context,
	scenario model.Scenario,
	result *RunResult,
	workerFailureSet map[string]struct{},
	phaseWindows []report.PhaseWindow,
	workerFailures []report.WorkerFailure,
) (report.Report, error) {
	workerMetrics, workerReports, collectionFailures, collectionFailureDetails, err := c.collectWorkerReports(ctx)
	if err != nil {
		return report.Report{}, err
	}
	agg, err := metrics.Aggregate(workerMetrics)
	if err != nil {
		return report.Report{}, err
	}
	targetSnapshots, presenceSnapshots, targetErrors, err := c.collectTargetSnapshots(ctx)
	if err != nil {
		return report.Report{}, err
	}
	for _, sample := range targetErrors {
		agg.Errors = appendBoundedReportError(agg.Errors, sample)
	}
	failedWorkers := mergeWorkerFailureSets(workerFailureSet, collectionFailures)
	workerFailures = append(workerFailures, collectionFailureDetails...)
	summary := report.SummaryFromMetrics(agg, len(failedWorkers))
	input := report.Input{
		RunID:             scenario.Run.ID,
		Scenario:          scenario,
		Target:            c.cfg.Target,
		Workers:           model.WorkerSet{Workers: c.cfg.Workers},
		Plan:              result.Plan,
		Summary:           summary,
		Metrics:           agg,
		WorkerReports:     workerReports,
		PhaseWindows:      phaseWindows,
		WorkerFailures:    workerFailures,
		WorkerMetrics:     workerMetrics,
		TargetSnapshots:   targetSnapshots,
		PresenceSnapshots: presenceSnapshots,
		ErrorSamples:      agg.Errors,
		Classification: &report.StabilityClassification{
			EvidenceComplete: result.Status != StatusTargetUnavailable,
			HarnessInvalid:   len(failedWorkers) > 0 && result.Status != StatusTargetUnavailable,
		},
	}
	rep := report.Build(input)
	if rep.ExitCode == report.ExitHardLimitFailed {
		result.Status = StatusHardLimitFailed
	} else if result.Status == StatusTargetUnavailable {
		rep.Status = report.StatusFailed
		rep.ExitCode = report.ExitTargetUnavailable
	} else if len(failedWorkers) > 0 {
		rep.Status = report.StatusFailed
		rep.ExitCode = report.ExitWorkerFailed
		if result.Status == "" {
			result.Status = StatusWorkerFailed
		}
	}
	result.Report = rep
	if strings.TrimSpace(scenario.Run.ReportDir) != "" {
		if err := report.WriteDir(scenario.Run.ReportDir, rep); err != nil {
			result.Status = StatusInternalFailed
			result.Report = rep
			return rep, err
		}
	}
	if len(collectionFailures) > 0 {
		if rep.ExitCode == report.ExitHardLimitFailed {
			result.Status = StatusHardLimitFailed
		} else if result.Status == "" {
			result.Status = StatusWorkerFailed
		}
		return rep, fmt.Errorf("%w for %d worker(s)", errWorkerReportCollection, len(collectionFailures))
	}
	return rep, nil
}

func (c *Coordinator) collectWorkerReports(ctx context.Context) ([]metrics.WorkerSnapshot, []report.WorkerReport, map[string]struct{}, []report.WorkerFailure, error) {
	workerMetrics := make([]metrics.WorkerSnapshot, 0, len(c.cfg.Workers))
	workerReports := make([]report.WorkerReport, 0, len(c.cfg.Workers))
	collectionFailures := make(map[string]struct{})
	var failureDetails []report.WorkerFailure
	for _, w := range c.cfg.Workers {
		workerID := strings.TrimSpace(w.ID)
		var snap metrics.SnapshotData
		if err := c.getJSON(ctx, w, "/v1/metrics", &snap); err == nil {
			workerMetrics = append(workerMetrics, metrics.WorkerSnapshot{WorkerID: workerID, Metrics: snap})
		} else {
			if contextCanceled(ctx, err) {
				return nil, nil, nil, nil, contextError(ctx, err)
			}
			collectionFailures[workerID] = struct{}{}
			failureDetails = append(failureDetails, report.WorkerFailure{
				WorkerID: workerID, Phase: "collect", ReasonCode: "worker_metrics_unavailable", Detail: err.Error(), ObservedAt: time.Now().UTC(),
			})
			workerMetrics = append(workerMetrics, metrics.WorkerSnapshot{WorkerID: workerID, Metrics: emptyWorkerMetricsWithError("worker_metrics_error", err)})
		}

		var raw json.RawMessage
		if err := c.getJSON(ctx, w, "/v1/report", &raw); err == nil && len(raw) > 0 {
			workerReports = append(workerReports, normalizeWorkerReport(workerID, raw))
		} else {
			if contextCanceled(ctx, err) {
				return nil, nil, nil, nil, contextError(ctx, err)
			}
			collectionFailures[workerID] = struct{}{}
			if err != nil {
				failureDetails = append(failureDetails, report.WorkerFailure{
					WorkerID: workerID, Phase: "collect", ReasonCode: "worker_report_unavailable", Detail: err.Error(), ObservedAt: time.Now().UTC(),
				})
				addReportErrorToLastMetric(workerMetrics, workerID, "worker_report_error", err)
			}
			payload, _ := json.Marshal(map[string]any{"worker_id": workerID, "error": "worker report unavailable"})
			workerReports = append(workerReports, report.WorkerReport{WorkerID: workerID, Report: payload})
		}
	}
	return workerMetrics, workerReports, collectionFailures, failureDetails, nil
}

// normalizeWorkerReport unwraps the current worker envelope while preserving legacy raw payloads.
func normalizeWorkerReport(workerID string, raw json.RawMessage) report.WorkerReport {
	var envelope report.WorkerReport
	if err := json.Unmarshal(raw, &envelope); err == nil && strings.TrimSpace(envelope.WorkerID) != "" && len(envelope.Report) > 0 {
		if strings.TrimSpace(workerID) == "" {
			workerID = strings.TrimSpace(envelope.WorkerID)
		}
		return report.WorkerReport{WorkerID: workerID, Report: envelope.Report}
	}
	return report.WorkerReport{WorkerID: workerID, Report: raw}
}

func mergeWorkerFailureSets(sets ...map[string]struct{}) map[string]struct{} {
	merged := make(map[string]struct{})
	for _, set := range sets {
		for workerID := range set {
			if workerID != "" {
				merged[workerID] = struct{}{}
			}
		}
	}
	return merged
}

func (c *Coordinator) collectTargetSnapshots(ctx context.Context) ([]json.RawMessage, []model.PresenceSnapshot, []metrics.ErrorSample, error) {
	apiAddrs := c.cfg.Target.BenchAPI.Addrs
	if len(apiAddrs) == 0 {
		apiAddrs = c.cfg.Target.API.Addrs
	}
	client := target.NewClient(target.Config{APIAddrs: apiAddrs, Token: c.cfg.Target.BenchAPI.Token, HTTPClient: c.http})
	var samples []metrics.ErrorSample
	var targetSnapshots []json.RawMessage
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		if contextCanceled(ctx, err) {
			return nil, nil, nil, contextError(ctx, err)
		}
		samples = append(samples, metrics.ErrorSample{Name: "target_metrics_error", Message: err.Error(), At: time.Now()})
	} else {
		data, err := json.Marshal(snapshot)
		if err != nil {
			samples = append(samples, metrics.ErrorSample{Name: "target_metrics_error", Message: err.Error(), At: time.Now()})
		} else {
			targetSnapshots = append(targetSnapshots, json.RawMessage(data))
		}
	}

	presenceSnapshots, presenceErrors, err := c.collectTargetPresenceSnapshots(ctx, client)
	if err != nil {
		return nil, nil, nil, err
	}
	samples = append(samples, presenceErrors...)
	return targetSnapshots, presenceSnapshots, samples, nil
}

func (c *Coordinator) collectTargetPresenceSnapshots(ctx context.Context, client *target.Client) ([]model.PresenceSnapshot, []metrics.ErrorSample, error) {
	snapshots, err := client.PresenceSnapshots(ctx)
	if err != nil {
		if contextCanceled(ctx, err) {
			return nil, nil, contextError(ctx, err)
		}
		return snapshots, []metrics.ErrorSample{{Name: "target_presence_snapshot_error", Message: err.Error(), At: time.Now()}}, nil
	}
	return snapshots, nil, nil
}

func emptyWorkerMetricsWithError(name string, err error) metrics.SnapshotData {
	return metrics.SnapshotData{
		Counters:   map[string]uint64{},
		Gauges:     map[string]float64{},
		Histograms: map[string]metrics.HistogramSummary{},
		Errors:     []metrics.ErrorSample{{Name: name, Message: err.Error(), At: time.Now()}},
	}
}

func addReportErrorToLastMetric(snapshots []metrics.WorkerSnapshot, workerID, name string, err error) {
	for i := len(snapshots) - 1; i >= 0; i-- {
		if snapshots[i].WorkerID != workerID {
			continue
		}
		snapshots[i].Metrics.Errors = appendBoundedReportError(snapshots[i].Metrics.Errors, metrics.ErrorSample{Name: name, Message: err.Error(), At: time.Now()})
		return
	}
}

func appendBoundedReportError(samples []metrics.ErrorSample, sample metrics.ErrorSample) []metrics.ErrorSample {
	const max = 32
	if len(samples) >= max {
		copy(samples, samples[1:])
		samples[len(samples)-1] = sample
		return samples
	}
	return append(samples, sample)
}

func (c *Coordinator) assignWorkers(ctx context.Context, scenario model.Scenario, plan model.Plan) error {
	assigned := make([]model.Worker, 0, len(c.cfg.Workers))
	for _, w := range c.cfg.Workers {
		workerID := strings.TrimSpace(w.ID)
		assignment := worker.Assignment{
			RunID:         scenario.Run.ID,
			WorkerID:      workerID,
			Client:        cloneWorkerClientConfig(w.Client),
			TCPSource:     cloneWorkerTCPSourceConfig(w.TCPSource),
			ChannelOwners: plan.ChannelOwners,
			Plan:          plan.Workers[workerID],
			Target:        c.cfg.Target,
			Scenario:      scenario,
		}
		if err := c.postJSON(ctx, w, "/v1/assign", assignment, nil); err != nil {
			c.stopAll(assigned)
			assignErr := fmt.Errorf("worker %s assign failed: %w", workerName(w), err)
			return newWorkerFailureError(workerID, Phase("assign"), "worker_assignment_failed", assignErr)
		}
		assigned = append(assigned, w)
	}
	return nil
}

func cloneWorkerClientConfig(cfg *model.WorkerClientConfig) *model.WorkerClientConfig {
	if cfg == nil {
		return nil
	}
	cloned := *cfg
	return &cloned
}

func cloneWorkerTCPSourceConfig(cfg *model.TCPSourceConfig) *model.TCPSourceConfig {
	if cfg == nil {
		return nil
	}
	cloned := *cfg
	cloned.IPv4Addrs = append([]string(nil), cfg.IPv4Addrs...)
	return &cloned
}

func (c *Coordinator) runPhase(ctx context.Context, scenario model.Scenario, phase Phase, failed map[string]struct{}, plan model.Plan) error {
	if phase == PhasePrepare {
		return c.runPreparePhase(ctx, scenario.Run.ID, failed, plan)
	}
	return c.runWorkerPhase(ctx, scenario.Run.ID, phase, c.phasePollTimeout(scenario, phase), failed, c.cfg.Workers)
}

func (c *Coordinator) runPreparePhase(ctx context.Context, runID string, failed map[string]struct{}, plan model.Plan) error {
	var errs []error
	for _, w := range c.prepareOwners(plan) {
		if _, skip := failed[strings.TrimSpace(w.ID)]; skip {
			continue
		}
		if err := c.postJSON(ctx, w, "/v1/prepare/channels", nil, nil); err != nil {
			ownerErr := fmt.Errorf("worker %s prepare channels failed: %w", workerName(w), err)
			errs = append(errs, newWorkerFailureError(strings.TrimSpace(w.ID), PhasePrepare, workerFailureReason("phase_start_failed", ownerErr), ownerErr))
			if errorsIsParentContext(ctx, err) {
				return errors.Join(errs...)
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return c.runWorkerPhase(ctx, runID, PhasePrepare, c.cfg.PollTimeout, failed, c.cfg.Workers)
}

func (c *Coordinator) prepareOwners(plan model.Plan) []model.Worker {
	seen := make(map[string]struct{})
	owners := make([]model.Worker, 0, len(c.cfg.Workers))
	for _, w := range c.cfg.Workers {
		workerID := strings.TrimSpace(w.ID)
		workerPlan := plan.Workers[workerID]
		for profileName, shard := range workerPlan.Profiles {
			if shard.ChannelType != model.ChannelTypeGroup || shard.TrafficPartitionCount <= 0 {
				continue
			}
			for channelIndex := shard.ChannelRange.Start; channelIndex < shard.ChannelRange.End; channelIndex++ {
				if plan.ChannelOwners[profileName][channelIndex] != workerID {
					continue
				}
				if _, ok := seen[workerID]; !ok {
					seen[workerID] = struct{}{}
					owners = append(owners, w)
				}
				break
			}
		}
	}
	return owners
}

func (c *Coordinator) phasePollTimeout(scenario model.Scenario, phase Phase) time.Duration {
	timeout := c.cfg.PollTimeout
	switch phase {
	case PhaseConnect:
		timeout += connectScheduleDuration(scenario)
	case PhaseWarmup:
		timeout += positiveDuration(scenario.Run.Warmup)
	case PhaseRun:
		timeout += positiveDuration(scenario.Run.Duration)
	case PhaseCooldown:
		timeout += positiveDuration(scenario.Run.Cooldown)
	}
	return timeout
}

func connectScheduleDuration(scenario model.Scenario) time.Duration {
	totalUsers := scenario.Online.TotalUsers
	rate := scenario.Online.ConnectRate.PerSecond
	if totalUsers <= 0 || rate <= 0 {
		return 0
	}
	return time.Duration(float64(totalUsers) / rate * float64(time.Second))
}

func positiveDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func (c *Coordinator) runWorkerPhase(ctx context.Context, runID string, phase Phase, pollTimeout time.Duration, failed map[string]struct{}, workers []model.Worker) error {
	accepted := make([]model.Worker, 0, len(c.cfg.Workers))
	var errs []error
	for _, w := range workers {
		if _, skip := failed[strings.TrimSpace(w.ID)]; skip {
			continue
		}
		if err := c.postJSON(ctx, w, "/v1/phase/"+string(phase), nil, nil); err != nil {
			phaseErr := fmt.Errorf("worker %s phase %s failed: %w", workerName(w), phase, err)
			errs = append(errs, newWorkerFailureError(strings.TrimSpace(w.ID), phase, workerFailureReason("phase_start_failed", phaseErr), phaseErr))
			if errorsIsParentContext(ctx, err) {
				return errors.Join(errs...)
			}
			continue
		}
		accepted = append(accepted, w)
	}
	for _, w := range accepted {
		if _, skip := failed[strings.TrimSpace(w.ID)]; skip {
			continue
		}
		if err := c.waitForPhase(ctx, w, runID, phase, pollTimeout); err != nil {
			waitErr := fmt.Errorf("worker %s wait for phase %s failed: %w", workerName(w), phase, err)
			reasonCode := workerFailureReason("phase_wait_failed", err)
			errs = append(errs, newWorkerFailureError(strings.TrimSpace(w.ID), phase, reasonCode, waitErr))
			if errorsIsParentContext(ctx, err) {
				return errors.Join(errs...)
			}
		}
	}
	return errors.Join(errs...)
}

type workerFailureError struct {
	workerID   string
	phase      Phase
	reasonCode string
	observedAt time.Time
	err        error
}

func newWorkerFailureError(workerID string, phase Phase, reasonCode string, err error) *workerFailureError {
	return &workerFailureError{
		workerID: strings.TrimSpace(workerID), phase: phase, reasonCode: reasonCode,
		observedAt: time.Now().UTC(), err: err,
	}
}

func workerFailureReason(fallback string, err error) string {
	var reasonErr *workerReasonError
	if errors.As(err, &reasonErr) && reasonErr.code.Valid() {
		return string(reasonErr.code)
	}
	if errors.Is(err, errPollTimeout) {
		return "phase_timeout"
	}
	switch {
	case errors.Is(err, errWorkerStatusMismatch):
		return "worker_status_mismatch"
	case isTargetUnavailable(err):
		return "target_unavailable"
	default:
		return fallback
	}
}

func (e *workerFailureError) Error() string {
	if e == nil || e.err == nil {
		return "worker failure"
	}
	return e.err.Error()
}

func (e *workerFailureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func addWorkerFailures(failed map[string]struct{}, err error) {
	collectWorkerFailures(failed, err)
}

func addWorkerFailureDetails(failures *[]report.WorkerFailure, err error) {
	collectWorkerFailureDetails(failures, err)
}

func collectWorkerFailureDetails(failures *[]report.WorkerFailure, err error) {
	if err == nil {
		return
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			collectWorkerFailureDetails(failures, child)
		}
		return
	}
	if workerErr, ok := err.(*workerFailureError); ok {
		failure := report.WorkerFailure{
			WorkerID: workerErr.workerID, Phase: string(workerErr.phase), ReasonCode: workerErr.reasonCode,
			ObservedAt: workerErr.observedAt,
		}
		if workerErr.err != nil {
			failure.Detail = workerErr.err.Error()
		}
		*failures = append(*failures, failure)
		return
	}
	if wrapped := errors.Unwrap(err); wrapped != nil {
		collectWorkerFailureDetails(failures, wrapped)
	}
}

func collectWorkerFailures(failed map[string]struct{}, err error) {
	if err == nil {
		return
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			collectWorkerFailures(failed, child)
		}
		return
	}
	if workerErr, ok := err.(*workerFailureError); ok {
		if workerErr.workerID != "" {
			failed[workerErr.workerID] = struct{}{}
		}
		return
	}
	if wrapped := errors.Unwrap(err); wrapped != nil {
		collectWorkerFailures(failed, wrapped)
	}
}

func (c *Coordinator) waitForPhase(parent context.Context, w model.Worker, runID string, want Phase, pollTimeout time.Duration) error {
	if pollTimeout <= 0 {
		pollTimeout = c.cfg.PollTimeout
	}
	ctx, cancel := context.WithTimeout(parent, pollTimeout)
	defer cancel()
	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()
	for {
		status, err := c.workerStatus(ctx, w)
		if err != nil {
			if errorsIsParentContext(parent, err) {
				return parent.Err()
			}
			return err
		}
		if err := validateStatusAssignment(status, w, runID); err != nil {
			return err
		}
		if status.LastError != "" {
			return workerStatusPhaseError(status.LastError, status.LastErrorCode)
		}
		if status.CompletedPhase == want || (status.CompletedPhase == "" && status.Phase == want) {
			return nil
		}
		if status.Phase == worker.PhaseStopped {
			return fmt.Errorf("worker stopped before phase %s", want)
		}
		select {
		case <-parent.Done():
			return parent.Err()
		case <-ctx.Done():
			if parentErr := parent.Err(); parentErr != nil {
				return parentErr
			}
			return errPollTimeout
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

func (c *Coordinator) stopAll(workers []model.Worker) {
	var wg sync.WaitGroup
	for _, w := range workers {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), c.cfg.StopTimeout)
			defer cancel()
			_ = c.postJSON(ctx, w, "/v1/stop", nil, nil)
		}()
	}
	wg.Wait()
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

func statusForError(parent context.Context, err error) RunStatus {
	if parentErr := parent.Err(); parentErr != nil && errors.Is(err, parentErr) {
		return StatusCanceled
	}
	if isTargetUnavailable(err) {
		return StatusTargetUnavailable
	}
	return StatusWorkerFailed
}

var errPollTimeout = errors.New("worker phase poll timeout")
var errWorkerStatusMismatch = errors.New("worker status assignment mismatch")

type workerReasonError struct {
	code worker.FailureReasonCode
	err  error
}

func (e *workerReasonError) Error() string {
	if e == nil || e.err == nil {
		return "worker phase failed"
	}
	return e.err.Error()
}

func (e *workerReasonError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func workerStatusPhaseError(message string, code worker.FailureReasonCode) error {
	var err error = errors.New(message)
	if code == worker.FailureReasonTargetUnavailable {
		err = fmt.Errorf("%s: %w", message, errTargetUnavailable)
	}
	if code.Valid() {
		return &workerReasonError{code: code, err: err}
	}
	return err
}

func validateStatusAssignment(status worker.Status, w model.Worker, runID string) error {
	workerID := strings.TrimSpace(w.ID)
	if status.Assignment.RunID != runID || status.Assignment.WorkerID != workerID {
		return fmt.Errorf("%w: got run_id=%q worker_id=%q want run_id=%q worker_id=%q", errWorkerStatusMismatch, status.Assignment.RunID, status.Assignment.WorkerID, runID, workerID)
	}
	return nil
}

func errorsIsParentContext(parent context.Context, err error) bool {
	parentErr := parent.Err()
	return parentErr != nil && errors.Is(err, parentErr)
}

func errorsIsContext(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func contextCanceled(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errorsIsContext(err)
}

func contextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func coordinatorStatusError(method, url string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodySnippetBytes))
	snippet := strings.TrimSpace(string(body))
	var payload struct {
		Error      string                   `json:"error"`
		ReasonCode worker.FailureReasonCode `json:"reason_code"`
	}
	_ = json.Unmarshal(body, &payload)
	var responseErr error
	if message := strings.TrimSpace(payload.Error); message != "" {
		responseErr = fmt.Errorf("worker request returned status %d: %s", resp.StatusCode, message)
		if resp.StatusCode == http.StatusServiceUnavailable {
			responseErr = fmt.Errorf("%s: %w", responseErr, errTargetUnavailable)
		}
	} else if snippet == "" {
		if resp.StatusCode == http.StatusServiceUnavailable {
			responseErr = fmt.Errorf("%s %s returned status %d: %w", method, url, resp.StatusCode, errTargetUnavailable)
		} else {
			responseErr = fmt.Errorf("%s %s returned status %d", method, url, resp.StatusCode)
		}
	} else if resp.StatusCode == http.StatusServiceUnavailable {
		responseErr = fmt.Errorf("%s %s returned status %d: %s: %w", method, url, resp.StatusCode, snippet, errTargetUnavailable)
	} else {
		responseErr = fmt.Errorf("%s %s returned status %d: %s", method, url, resp.StatusCode, snippet)
	}
	if payload.ReasonCode.Valid() {
		if payload.ReasonCode == worker.FailureReasonTargetUnavailable && !errors.Is(responseErr, errTargetUnavailable) {
			responseErr = fmt.Errorf("%s: %w", responseErr, errTargetUnavailable)
		}
		return &workerReasonError{code: payload.ReasonCode, err: responseErr}
	}
	return responseErr
}

var errTargetUnavailable = errors.New("target unavailable")

func isTargetUnavailable(err error) bool {
	return errors.Is(err, errTargetUnavailable)
}
