package capacity

import (
	"context"
	"fmt"
	"time"
)

// Status is the terminal capacity search status.
type Status string

const (
	// StatusPassed means at least one stable QPS was found.
	StatusPassed Status = "passed"
	// StatusFailed means no stable QPS was found.
	StatusFailed Status = "failed"
)

// AttemptRunner runs one offered-QPS attempt.
type AttemptRunner interface {
	RunAttempt(ctx context.Context, attempt Attempt) (AttemptResult, error)
}

type attemptRunnerFunc func(context.Context, Attempt) (AttemptResult, error)

func (f attemptRunnerFunc) RunAttempt(ctx context.Context, a Attempt) (AttemptResult, error) {
	return f(ctx, a)
}

// AttemptResult summarizes one capacity attempt.
type AttemptResult struct {
	// Attempt is the offered-QPS attempt metadata.
	Attempt Attempt `json:"attempt"`
	// Passed indicates whether this offered QPS met the stability criteria.
	Passed bool `json:"passed"`
	// ActualQPS is the observed successful sendack QPS during measured run.
	ActualQPS float64 `json:"actual_qps"`
	// SendackP50 is the observed run-phase sendack p50 latency.
	SendackP50 time.Duration `json:"sendack_p50"`
	// SendackP95 is the observed run-phase sendack p95 latency.
	SendackP95 time.Duration `json:"sendack_p95"`
	// SendackP99 is the observed run-phase sendack p99 latency.
	SendackP99 time.Duration `json:"sendack_p99"`
	// SendackErrorRate is the failed sendack ratio for this attempt.
	SendackErrorRate float64 `json:"sendack_error_rate"`
	// ConnectErrorRate is the failed connect ratio for this attempt.
	ConnectErrorRate float64 `json:"connect_error_rate"`
	// WorkerFailed is the number of failed workers for this attempt.
	WorkerFailed int `json:"worker_failed"`
	// FailureReason is the primary stability failure reason.
	FailureReason string `json:"failure_reason,omitempty"`
	// ReportDir is the wkbench report directory for this attempt.
	ReportDir string `json:"report_dir,omitempty"`
}

// Result summarizes a capacity search.
type Result struct {
	// Status is passed when at least one stable QPS was found.
	Status Status `json:"status"`
	// Profile is the traffic profile used for the search.
	Profile string `json:"profile"`
	// MaxStableQPS is the highest passing offered QPS discovered.
	MaxStableQPS float64 `json:"max_stable_qps"`
	// FirstFailedQPS is the lowest failed offered QPS that bounded the search.
	FirstFailedQPS float64 `json:"first_failed_qps,omitempty"`
	// StableAttempt is the final highest passing attempt.
	StableAttempt *AttemptResult `json:"stable_attempt,omitempty"`
	// FailedAttempt is the first or final failed attempt bounding capacity.
	FailedAttempt *AttemptResult `json:"failed_attempt,omitempty"`
	// Attempts contains every completed attempt in execution order.
	Attempts []AttemptResult `json:"attempts"`
	// ReportDir is the capacity result directory.
	ReportDir string `json:"report_dir,omitempty"`
}

// Search runs ramp and optional binary search to find maximum stable QPS.
func Search(ctx context.Context, cfg Config, runner AttemptRunner) (Result, error) {
	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}
	if runner == nil {
		return Result{}, fmt.Errorf("attempt runner is required")
	}
	result := Result{Status: StatusFailed, Profile: cfg.Profile, ReportDir: cfg.ReportDir}
	nextIndex := 0
	lastPassQPS := 0.0
	firstFailQPS := 0.0

	for offered := cfg.StartQPS; offered <= cfg.MaxQPS; offered *= cfg.StepFactor {
		attemptResult, err := runner.RunAttempt(ctx, Attempt{Index: nextIndex, OfferedQPS: offered})
		if err != nil {
			return result, err
		}
		nextIndex++
		result.Attempts = append(result.Attempts, attemptResult)
		if attemptResult.Passed {
			lastPassQPS = offered
			result.Status = StatusPassed
			result.MaxStableQPS = offered
			result.StableAttempt = cloneAttemptResult(attemptResult)
			if offered == cfg.MaxQPS {
				break
			}
			next := offered * cfg.StepFactor
			if next > cfg.MaxQPS && offered < cfg.MaxQPS {
				offered = cfg.MaxQPS / cfg.StepFactor
			}
			continue
		}
		firstFailQPS = offered
		result.FirstFailedQPS = offered
		result.FailedAttempt = cloneAttemptResult(attemptResult)
		break
	}

	if cfg.BinarySearch && lastPassQPS > 0 && firstFailQPS > 0 {
		for (firstFailQPS-lastPassQPS)/lastPassQPS > cfg.BinarySearchMinDeltaRatio {
			offered := (lastPassQPS + firstFailQPS) / 2
			attemptResult, err := runner.RunAttempt(ctx, Attempt{Index: nextIndex, OfferedQPS: offered})
			if err != nil {
				return result, err
			}
			nextIndex++
			result.Attempts = append(result.Attempts, attemptResult)
			if attemptResult.Passed {
				lastPassQPS = offered
				result.MaxStableQPS = offered
				result.StableAttempt = cloneAttemptResult(attemptResult)
				result.Status = StatusPassed
			} else {
				firstFailQPS = offered
				result.FirstFailedQPS = offered
				result.FailedAttempt = cloneAttemptResult(attemptResult)
			}
		}
	}
	return result, nil
}

func cloneAttemptResult(value AttemptResult) *AttemptResult {
	copy := value
	return &copy
}
