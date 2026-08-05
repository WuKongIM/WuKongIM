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

// CapacityDatasetState is the closed result of the live target dataset probe.
type CapacityDatasetState string

const (
	CapacityDatasetUnavailable CapacityDatasetState = "unavailable"
	CapacityDatasetLiveAged    CapacityDatasetState = "live_aged"
	CapacityDatasetClean       CapacityDatasetState = "clean"
)

// CapacityLiveDatasetNodeEvidence is one direct service-node response from the
// current capacity admission probe. DatasetDigest includes the service process
// generation, so a restart cannot reuse the frozen checkpoint identity.
type CapacityLiveDatasetNodeEvidence struct {
	NodeID        uint64
	DatasetDigest string
	ObservedAt    time.Time
	State         CapacityDatasetState
}

// CapacityLiveDatasetEvidence contains exactly one direct response for each
// service node. The coordinator obtains it during Run; callers cannot attach a
// previously cached aggregate to CapacityAdmission.
type CapacityLiveDatasetEvidence struct {
	Nodes [coordinatorWorkerCount]CapacityLiveDatasetNodeEvidence
}

// CapacityAdmission identifies the frozen 72-hour Soak report. Live evidence
// is always collected independently by the coordinator during Run.
type CapacityAdmission struct {
	Reference  string
	Checkpoint Report
}

// capacityAdmissionToken is created only after the coordinator's current
// all-node probe has matched the frozen checkpoint.
type capacityAdmissionToken struct{}

// CapacityPhase is the closed staircase lifecycle.
type CapacityPhase string

const (
	CapacityPhaseStabilize   CapacityPhase = "stabilize"
	CapacityPhaseMeasure     CapacityPhase = "measure"
	CapacityPhaseRatePending CapacityPhase = "rate_pending"
	CapacityPhaseRecovery    CapacityPhase = "recovery"
	CapacityPhaseComplete    CapacityPhase = "complete"
	CapacityPhaseTerminal    CapacityPhase = "terminal"
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
	CapacityCauseRateChange  CapacityCause = "rate_change"
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
	CapacityGateReadiness
	CapacityGateLifecycle
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
	ReadinessAccepted     bool
	LifecycleAccepted     bool
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
	PendingRate      uint64
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

	cfg          CapacityConfig
	start        time.Time
	last         time.Time
	phase        CapacityPhase
	outcome      CapacityOutcome
	cause        CapacityCause
	terminal     bool
	mode         capacitySearchMode
	current      uint64
	pendingRate  uint64
	pendingPhase CapacityPhase
	phaseStart   time.Time
	phaseEnd     time.Time
	stepStart    time.Time

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

func validateCapacityCheckpoint(cfg Config, admission CapacityAdmission) error {
	checkpoint := admission.Checkpoint
	if cfg.Profile != ProfileFormal || cfg.Mode != ModeCapacity || cfg.Validate() != nil ||
		admission.Reference != cfg.Capacity.AgedCheckpoint.Reference ||
		!validReportHash(checkpoint.DatasetDigest) || validateReport(checkpoint) != nil ||
		checkpoint.Profile != ProfileFormal || checkpoint.Mode != ModeSoak ||
		checkpoint.Kind != CheckpointFinal || !checkpoint.Final || checkpoint.Continue ||
		!checkpoint.Verdict.Terminal || checkpoint.Verdict.Outcome != VerdictPass || checkpoint.Verdict.Cause != VerdictCauseCompleted ||
		checkpoint.Window.Elapsed < formalCheckpointDuration || checkpoint.Capacity.Attempted ||
		cfg.Capacity.AgedCheckpoint.Duration > checkpoint.Window.Elapsed {
		return ErrCapacityAdmission
	}
	return nil
}

func validateCapacityLiveDataset(
	cfg Config,
	admission CapacityAdmission,
	live CapacityLiveDatasetEvidence,
	probeStartedAt time.Time,
	probeCompletedAt time.Time,
) (capacityAdmissionToken, error) {
	if probeStartedAt.IsZero() || probeCompletedAt.Before(probeStartedAt) ||
		validateCapacityCheckpoint(cfg, admission) != nil || !probeStartedAt.After(admission.Checkpoint.Window.End) {
		return capacityAdmissionToken{}, ErrCapacityAdmission
	}
	seen := make(map[uint64]struct{}, coordinatorWorkerCount)
	for _, node := range live.Nodes {
		if node.NodeID == 0 || node.DatasetDigest != admission.Checkpoint.DatasetDigest ||
			node.State != CapacityDatasetLiveAged || node.ObservedAt.Before(probeStartedAt) ||
			node.ObservedAt.After(probeCompletedAt) {
			return capacityAdmissionToken{}, ErrCapacityAdmission
		}
		if _, duplicate := seen[node.NodeID]; duplicate {
			return capacityAdmissionToken{}, ErrCapacityAdmission
		}
		seen[node.NodeID] = struct{}{}
	}
	return capacityAdmissionToken{}, nil
}

// newCapacityStaircase starts only from a token produced by the coordinator's
// current all-node aged-dataset probe.
func newCapacityStaircase(cfg Config, _ capacityAdmissionToken, start time.Time) (*CapacityStaircase, error) {
	if start.IsZero() || cfg.Profile != ProfileFormal || cfg.Mode != ModeCapacity || cfg.Validate() != nil {
		return nil, ErrCapacityConfig
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
	if s.phase == CapacityPhaseRatePending {
		return s.transition(false), ErrCapacityObservation
	}
	if at.Before(s.phaseEnd) {
		return s.transition(false), ErrCapacityObservation
	}
	if at.After(s.phaseEnd) {
		s.freeze(CapacityHarnessInvalid, CapacityCauseObservation, CapacityPhaseTerminal)
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

// CommitRate starts stabilization or recovery only after the grant owner has
// applied the requested global rate on a successfully delivered grant Tick.
func (s *CapacityStaircase) CommitRate(at time.Time, rate uint64) (CapacityTransition, error) {
	if s == nil {
		return CapacityTransition{}, ErrCapacityConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return s.transition(false), ErrCapacityTerminal
	}
	if s.phase != CapacityPhaseRatePending || rate == 0 || rate != s.pendingRate ||
		at.IsZero() || at.Before(s.last) || !at.After(s.phaseStart) {
		return s.transition(false), ErrCapacityObservation
	}
	s.last, s.current, s.phase = at, rate, s.pendingPhase
	s.phaseStart = at
	s.pendingRate, s.pendingPhase = 0, ""
	s.stepStart = at
	switch s.phase {
	case CapacityPhaseStabilize:
		s.phaseEnd = at.Add(s.cfg.Step.Stabilize)
	case CapacityPhaseRecovery:
		s.phaseEnd = at.Add(s.cfg.RecoveryDuration)
	default:
		s.freeze(CapacityHarnessInvalid, CapacityCauseObservation, CapacityPhaseTerminal)
		return s.transition(false), ErrCapacityObservation
	}
	return s.transition(false), nil
}

// FailRateChange freezes a pending control round as harness-invalid without
// starting or shortening the requested stabilization/recovery window.
func (s *CapacityStaircase) FailRateChange(at time.Time) (CapacityTransition, error) {
	if s == nil {
		return CapacityTransition{}, ErrCapacityConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return s.transition(false), ErrCapacityTerminal
	}
	if s.phase != CapacityPhaseRatePending || at.IsZero() || at.Before(s.last) || at.Before(s.phaseStart) {
		return s.transition(false), ErrCapacityObservation
	}
	s.last = at
	s.freeze(CapacityHarnessInvalid, CapacityCauseRateChange, CapacityPhaseTerminal)
	return s.transition(false), ErrCapacityObservation
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
		CurrentRate: s.current, PendingRate: s.pendingRate, PhaseStart: s.phaseStart, PhaseEnd: s.phaseEnd,
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
		return s.beginRateChange(at, next, CapacityPhaseStabilize), nil
	}
	if s.mode == capacitySearchCoarse {
		s.mode = capacitySearchRefine
	}
	next, ok := refineCapacityRate(s.lastPassing, s.firstFailing, s.cfg.RefinePercent)
	if !ok {
		return s.beginRecovery(at), nil
	}
	return s.beginRateChange(at, next, CapacityPhaseStabilize), nil
}

func (s *CapacityStaircase) beginRateChange(at time.Time, rate uint64, target CapacityPhase) CapacityTransition {
	s.phase = CapacityPhaseRatePending
	s.phaseStart = at
	s.phaseEnd = time.Time{}
	s.pendingRate, s.pendingPhase = rate, target
	return s.transition(true)
}

func (s *CapacityStaircase) beginRecovery(at time.Time) CapacityTransition {
	rate := uint64(s.cfg.RecoveryRatePerSecond)
	if s.current != rate {
		return s.beginRateChange(at, rate, CapacityPhaseRecovery)
	}
	s.phase = CapacityPhaseRecovery
	s.phaseStart = at
	s.phaseEnd = at.Add(s.cfg.RecoveryDuration)
	return s.transition(false)
}

func (s *CapacityStaircase) transition(schedule bool) CapacityTransition {
	rate := s.current
	if s.phase == CapacityPhaseRatePending {
		rate = s.pendingRate
	}
	return CapacityTransition{
		Phase: s.phase, Outcome: s.outcome, ScheduleRate: schedule, RatePerSecond: rate,
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
	if !observation.ReadinessAccepted {
		failures |= CapacityGateReadiness
	}
	if !observation.LifecycleAccepted {
		failures |= CapacityGateLifecycle
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
