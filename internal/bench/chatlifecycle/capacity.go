package chatlifecycle

import (
	"errors"
	"math"
	"math/bits"
	"sync"
	"time"
)

const (
	maxCapacitySteps       = 192
	maxCapacityRecentSteps = 32
)

var (
	// ErrCapacityConfig rejects an unusable capacity profile or start instant.
	ErrCapacityConfig = errors.New("chat lifecycle capacity: invalid configuration")
	// ErrCapacityAdmission rejects anything other than the same live aged dataset proven by a passing 72-hour report.
	ErrCapacityAdmission = errors.New("chat lifecycle capacity: invalid aged-dataset admission")
	// ErrCapacityObservation rejects missing, regressing, or structurally invalid phase evidence.
	ErrCapacityObservation = errors.New("chat lifecycle capacity: invalid observation")
	// ErrCapacityTerminal rejects evidence after the capacity result is frozen.
	ErrCapacityTerminal = errors.New("chat lifecycle capacity: already terminal")
)

// CapacityAdmission binds the frozen Soak report to the currently live service dataset.
type CapacityAdmission struct {
	Reference             string
	Checkpoint            Report
	CheckpointDatasetHash string
	LiveDatasetHash       string
	Live                  bool
	Aged                  bool
	Clean                 bool
}

// CapacityPhase is the closed staircase lifecycle.
type CapacityPhase string

const (
	CapacityPhaseStabilize CapacityPhase = "stabilize"
	CapacityPhaseMeasure   CapacityPhase = "measure"
	CapacityPhaseRecovery  CapacityPhase = "recovery"
	CapacityPhaseComplete  CapacityPhase = "complete"
	CapacityPhaseTerminal  CapacityPhase = "terminal"
)

// CapacityOutcome is the closed capacity-stage result.
type CapacityOutcome string

const (
	CapacityRunning        CapacityOutcome = "running"
	CapacityPassed         CapacityOutcome = "pass"
	CapacityProductFailure CapacityOutcome = "product_failure"
	CapacityHarnessInvalid CapacityOutcome = "harness_invalid"
)

// CapacityCause is the identity-free first terminal cause.
type CapacityCause string

const (
	CapacityCauseNone        CapacityCause = ""
	CapacityCauseCompleted   CapacityCause = "completed"
	CapacityCauseCorrectness CapacityCause = "correctness"
	CapacityCauseRecovery    CapacityCause = "recovery"
	CapacityCauseObservation CapacityCause = "invalid_observation"
	CapacityCauseNoBoundary  CapacityCause = "no_bounded_breakpoint"
)

// CapacityGateFailure is a fixed bit set for one measured step.
type CapacityGateFailure uint8

const (
	CapacityGateErrorRate CapacityGateFailure = 1 << iota
	CapacityGateLatency
	CapacityGateQueueInflight
	CapacityGateClusterLag
	CapacityGateResource
)

// CapacityObservation is one bounded phase-boundary evidence projection.
type CapacityObservation struct {
	Complete              bool
	CorrectnessFailure    bool
	HarnessInvalid        bool
	ErrorRateAccepted     bool
	LatencyAccepted       bool
	QueueInflightAccepted bool
	ClusterLagAccepted    bool
	ResourceAccepted      bool
}

// CapacityStepResult is one bounded measured rate result.
type CapacityStepResult struct {
	RatePerSecond uint64              `json:"rate_per_second"`
	StartedAt     time.Time           `json:"started_at"`
	EndedAt       time.Time           `json:"ended_at"`
	Passed        bool                `json:"passed"`
	Failures      CapacityGateFailure `json:"failures"`
	Refined       bool                `json:"refined"`
}

// CapacityTransition tells the coordinator whether the next grant tick needs a rate change.
type CapacityTransition struct {
	Phase         CapacityPhase
	Outcome       CapacityOutcome
	ScheduleRate  bool
	RatePerSecond uint64
	FirstFailing  uint64
	LastPassing   uint64
}

// CapacitySnapshot is the bounded report-safe staircase projection.
type CapacitySnapshot struct {
	Phase            CapacityPhase
	Outcome          CapacityOutcome
	Cause            CapacityCause
	Terminal         bool
	CurrentRate      uint64
	PhaseStart       time.Time
	PhaseEnd         time.Time
	LastPassingRate  uint64
	FirstFailingRate uint64
	StepCount        uint64
	CoarseSteps      uint64
	RefineSteps      uint64
	RecoveryPassed   bool
	RecentSteps      []CapacityStepResult
}

// ReportEvidence projects the terminal staircase summary into the report seam.
func (s CapacitySnapshot) ReportEvidence() ReportCapacityEvidence {
	if s.Phase == "" || s.Outcome == "" {
		return ReportCapacityEvidence{}
	}
	completed := s.Terminal && (s.Cause == CapacityCauseCompleted || s.Cause == CapacityCauseRecovery)
	return ReportCapacityEvidence{
		Attempted: true, Completed: completed, MaximumPassingRate: s.LastPassingRate,
		FirstFailingRate: s.FirstFailingRate, RecoveryPassed: s.RecoveryPassed,
	}
}

type capacitySearchMode uint8

const (
	capacitySearchCoarse capacitySearchMode = iota
	capacitySearchRefine
)

// CapacityStaircase owns one non-resumable, fixed-memory aged-data search.
type CapacityStaircase struct {
	mu sync.Mutex

	cfg        CapacityConfig
	start      time.Time
	last       time.Time
	phase      CapacityPhase
	outcome    CapacityOutcome
	cause      CapacityCause
	terminal   bool
	mode       capacitySearchMode
	current    uint64
	phaseStart time.Time
	phaseEnd   time.Time
	stepStart  time.Time

	lastPassing  uint64
	firstFailing uint64
	stepCount    uint64
	coarseSteps  uint64
	refineSteps  uint64
	recoveryPass bool
	recent       [maxCapacityRecentSteps]CapacityStepResult
	recentHead   int
	recentSize   int
}

// NewCapacityStaircase admits only a passing formal Soak report bound to the
// exact same currently live, non-clean aged service dataset.
func NewCapacityStaircase(cfg Config, admission CapacityAdmission, start time.Time) (*CapacityStaircase, error) {
	if start.IsZero() || cfg.Profile != ProfileFormal || cfg.Mode != ModeCapacity || cfg.Validate() != nil {
		return nil, ErrCapacityConfig
	}
	checkpoint := admission.Checkpoint
	if !admission.Live || !admission.Aged || admission.Clean || admission.Reference != cfg.Capacity.AgedCheckpoint.Reference ||
		!validReportHash(admission.CheckpointDatasetHash) || admission.CheckpointDatasetHash != admission.LiveDatasetHash ||
		validateReport(checkpoint) != nil || checkpoint.Profile != ProfileFormal || checkpoint.Mode != ModeSoak ||
		checkpoint.Kind != CheckpointFinal || !checkpoint.Final || checkpoint.Continue ||
		!checkpoint.Verdict.Terminal || checkpoint.Verdict.Outcome != VerdictPass || checkpoint.Verdict.Cause != VerdictCauseCompleted ||
		checkpoint.Window.Elapsed < formalCheckpointDuration || checkpoint.Capacity.Attempted ||
		!start.After(checkpoint.Window.End) || cfg.Capacity.AgedCheckpoint.Duration > checkpoint.Window.Elapsed {
		return nil, ErrCapacityAdmission
	}
	rate := uint64(cfg.Capacity.StartRatePerSecond)
	return &CapacityStaircase{
		cfg: cfg.Capacity, start: start, last: start, phase: CapacityPhaseStabilize,
		outcome: CapacityRunning, mode: capacitySearchCoarse, current: rate,
		phaseStart: start, phaseEnd: start.Add(cfg.Capacity.Step.Stabilize), stepStart: start,
	}, nil
}

// Advance consumes a phase boundary or immediate correctness/harness failure.
func (s *CapacityStaircase) Advance(at time.Time, observation CapacityObservation) (CapacityTransition, error) {
	if s == nil {
		return CapacityTransition{}, ErrCapacityConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return s.transition(false), ErrCapacityTerminal
	}
	if at.IsZero() || at.Before(s.start) || at.Before(s.last) {
		s.freeze(CapacityHarnessInvalid, CapacityCauseObservation, CapacityPhaseTerminal)
		return s.transition(false), ErrCapacityObservation
	}
	if observation.CorrectnessFailure {
		s.last = at
		s.freeze(CapacityProductFailure, CapacityCauseCorrectness, CapacityPhaseTerminal)
		return s.transition(false), nil
	}
	if observation.HarnessInvalid {
		s.last = at
		s.freeze(CapacityHarnessInvalid, CapacityCauseObservation, CapacityPhaseTerminal)
		return s.transition(false), ErrCapacityObservation
	}
	if at.Before(s.phaseEnd) {
		return s.transition(false), ErrCapacityObservation
	}
	s.last = at

	switch s.phase {
	case CapacityPhaseStabilize:
		s.phase = CapacityPhaseMeasure
		s.phaseStart = at
		s.phaseEnd = at.Add(s.cfg.Step.Measure)
		return s.transition(false), nil
	case CapacityPhaseMeasure:
		if !observation.Complete {
			s.freeze(CapacityHarnessInvalid, CapacityCauseObservation, CapacityPhaseTerminal)
			return s.transition(false), ErrCapacityObservation
		}
		return s.finishMeasurement(at, observation)
	case CapacityPhaseRecovery:
		if !observation.Complete {
			s.freeze(CapacityHarnessInvalid, CapacityCauseObservation, CapacityPhaseTerminal)
			return s.transition(false), ErrCapacityObservation
		}
		if capacityObservationFailures(observation) == 0 {
			s.recoveryPass = true
			s.freeze(CapacityPassed, CapacityCauseCompleted, CapacityPhaseComplete)
		} else {
			s.freeze(CapacityProductFailure, CapacityCauseRecovery, CapacityPhaseTerminal)
		}
		return s.transition(false), nil
	default:
		s.freeze(CapacityHarnessInvalid, CapacityCauseObservation, CapacityPhaseTerminal)
		return s.transition(false), ErrCapacityObservation
	}
}

// Snapshot returns a deep copy of the fixed recent-step ring.
func (s *CapacityStaircase) Snapshot() CapacitySnapshot {
	if s == nil {
		return CapacitySnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recent := make([]CapacityStepResult, s.recentSize)
	for index := range recent {
		recent[index] = s.recent[(s.recentHead+index)%len(s.recent)]
	}
	return CapacitySnapshot{
		Phase: s.phase, Outcome: s.outcome, Cause: s.cause, Terminal: s.terminal,
		CurrentRate: s.current, PhaseStart: s.phaseStart, PhaseEnd: s.phaseEnd,
		LastPassingRate: s.lastPassing, FirstFailingRate: s.firstFailing,
		StepCount: s.stepCount, CoarseSteps: s.coarseSteps, RefineSteps: s.refineSteps,
		RecoveryPassed: s.recoveryPass, RecentSteps: recent,
	}
}

func (s *CapacityStaircase) finishMeasurement(at time.Time, observation CapacityObservation) (CapacityTransition, error) {
	if s.stepCount >= maxCapacitySteps {
		s.freeze(CapacityHarnessInvalid, CapacityCauseNoBoundary, CapacityPhaseTerminal)
		return s.transition(false), ErrCapacityObservation
	}
	failures := capacityObservationFailures(observation)
	passed := failures == 0
	s.recordStep(CapacityStepResult{
		RatePerSecond: s.current, StartedAt: s.stepStart, EndedAt: at,
		Passed: passed, Failures: failures, Refined: s.mode == capacitySearchRefine,
	})
	if s.mode == capacitySearchCoarse {
		s.coarseSteps++
	} else {
		s.refineSteps++
	}
	if passed {
		s.lastPassing = s.current
	} else {
		s.firstFailing = s.current
	}

	if s.mode == capacitySearchCoarse && passed {
		next, ok := nextCapacityRate(s.current, s.cfg.StepPercent)
		if !ok {
			s.freeze(CapacityHarnessInvalid, CapacityCauseNoBoundary, CapacityPhaseTerminal)
			return s.transition(false), ErrCapacityObservation
		}
		return s.beginStabilization(at, next), nil
	}
	if s.mode == capacitySearchCoarse {
		s.mode = capacitySearchRefine
	}
	next, ok := refineCapacityRate(s.lastPassing, s.firstFailing, s.cfg.RefinePercent)
	if !ok {
		return s.beginRecovery(at), nil
	}
	return s.beginStabilization(at, next), nil
}

func (s *CapacityStaircase) beginStabilization(at time.Time, rate uint64) CapacityTransition {
	changed := s.current != rate
	s.current = rate
	s.phase = CapacityPhaseStabilize
	s.phaseStart = at
	s.phaseEnd = at.Add(s.cfg.Step.Stabilize)
	s.stepStart = at
	return s.transition(changed)
}

func (s *CapacityStaircase) beginRecovery(at time.Time) CapacityTransition {
	rate := uint64(s.cfg.RecoveryRatePerSecond)
	changed := s.current != rate
	s.current = rate
	s.phase = CapacityPhaseRecovery
	s.phaseStart = at
	s.phaseEnd = at.Add(s.cfg.RecoveryDuration)
	return s.transition(changed)
}

func (s *CapacityStaircase) transition(schedule bool) CapacityTransition {
	return CapacityTransition{
		Phase: s.phase, Outcome: s.outcome, ScheduleRate: schedule, RatePerSecond: s.current,
		FirstFailing: s.firstFailing, LastPassing: s.lastPassing,
	}
}

func (s *CapacityStaircase) freeze(outcome CapacityOutcome, cause CapacityCause, phase CapacityPhase) {
	if s.terminal {
		return
	}
	s.outcome, s.cause, s.phase, s.terminal = outcome, cause, phase, true
}

func (s *CapacityStaircase) recordStep(result CapacityStepResult) {
	s.stepCount++
	if s.recentSize < len(s.recent) {
		index := (s.recentHead + s.recentSize) % len(s.recent)
		s.recent[index] = result
		s.recentSize++
		return
	}
	s.recent[s.recentHead] = result
	s.recentHead = (s.recentHead + 1) % len(s.recent)
}

func capacityObservationFailures(observation CapacityObservation) CapacityGateFailure {
	var failures CapacityGateFailure
	if !observation.ErrorRateAccepted {
		failures |= CapacityGateErrorRate
	}
	if !observation.LatencyAccepted {
		failures |= CapacityGateLatency
	}
	if !observation.QueueInflightAccepted {
		failures |= CapacityGateQueueInflight
	}
	if !observation.ClusterLagAccepted {
		failures |= CapacityGateClusterLag
	}
	if !observation.ResourceAccepted {
		failures |= CapacityGateResource
	}
	return failures
}

func refineCapacityRate(lastPassing, firstFailing uint64, percent int) (uint64, bool) {
	if lastPassing == 0 || firstFailing <= lastPassing {
		return 0, false
	}
	next, ok := nextCapacityRate(lastPassing, percent)
	if !ok || next >= firstFailing {
		return 0, false
	}
	return next, true
}

func nextCapacityRate(rate uint64, percent int) (uint64, bool) {
	if rate == 0 || rate > math.MaxUint64/2 || percent <= 0 || percent > 100 {
		return 0, false
	}
	high, low := bits.Mul64(rate, uint64(percent))
	if high >= 100 {
		return 0, false
	}
	increment, remainder := bits.Div64(high, low, 100)
	if remainder != 0 {
		if increment == math.MaxUint64 {
			return 0, false
		}
		increment++
	}
	if math.MaxUint64-rate < increment {
		return 0, false
	}
	next := rate + increment
	if next <= rate || next > math.MaxUint64/2 {
		return 0, false
	}
	return next, true
}
