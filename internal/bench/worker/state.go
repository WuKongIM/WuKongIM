package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

var (
	// ErrActiveRunConflict reports that a different run or payload is already active.
	ErrActiveRunConflict = errors.New("active run conflict")
	// ErrInvalidPhaseTransition reports a non-monotonic worker phase transition.
	ErrInvalidPhaseTransition = errors.New("invalid phase transition")
	// ErrAssignmentPersistence reports a failure while saving accepted assignment state.
	ErrAssignmentPersistence = errors.New("assignment persistence failed")
)

// Phase is the coarse lifecycle phase of a worker assignment.
type Phase string

const (
	// PhaseIdle means the worker has not accepted an assignment yet.
	PhaseIdle Phase = "idle"
	// PhaseAssigned means the worker accepted an assignment but has not prepared yet.
	PhaseAssigned Phase = "assigned"
	// PhasePrepare means the worker is preparing benchmark data or local state.
	PhasePrepare Phase = "prepare"
	// PhaseConnect means the worker is establishing client connections.
	PhaseConnect Phase = "connect"
	// PhaseWarmup means the worker is running warmup traffic.
	PhaseWarmup Phase = "warmup"
	// PhaseRun means the worker is running measured traffic.
	PhaseRun Phase = "run"
	// PhaseCooldown means the worker is draining after measured traffic.
	PhaseCooldown Phase = "cooldown"
	// PhaseStopped means the assignment has stopped and a new run may be accepted.
	PhaseStopped Phase = "stopped"
)

// Assignment is the control-plane run shard assigned to this worker.
type Assignment struct {
	// RunID identifies the benchmark run that owns this assignment.
	RunID string `json:"run_id"`
	// WorkerID identifies this worker within the benchmark worker set.
	WorkerID string `json:"worker_id,omitempty"`
	// Client contains only this worker's optional per-session client capacity profile.
	Client *model.WorkerClientConfig `json:"client,omitempty"`
	// ChannelOwners records deterministic group channel owners by profile and channel index.
	ChannelOwners map[string]map[int]string `json:"channel_owners,omitempty"`
	// Plan is the deterministic worker-local shard assigned by the coordinator.
	Plan model.WorkerPlan `json:"plan,omitempty"`
	// Target carries the black-box WuKongIM deployment configuration for future workloads.
	Target model.Target `json:"target,omitempty"`
	// Scenario carries the validated scenario configuration for future fake or real workloads.
	Scenario model.Scenario `json:"scenario,omitempty"`
}

// Status is a JSON-friendly snapshot of the worker control state.
type Status struct {
	// Phase is the worker's current lifecycle phase.
	Phase Phase `json:"phase"`
	// ActivePhase is the phase hook currently running asynchronously.
	ActivePhase Phase `json:"active_phase,omitempty"`
	// CompletedPhase is the latest phase whose hook finished successfully.
	CompletedPhase Phase `json:"completed_phase,omitempty"`
	// LastError records the latest asynchronous phase hook failure.
	LastError string `json:"last_error,omitempty"`
	// Assignment is the active or most recently stopped assignment.
	Assignment Assignment `json:"assignment"`
}

// State stores the active worker assignment and monotonic phase.
type State struct {
	mu         sync.Mutex
	workDir    string
	phase      Phase
	active     Phase
	lastError  string
	assignment Assignment
}

// NewState creates empty worker assignment state. When workDir is non-empty,
// accepted assignments are persisted to current-run.json.
func NewState(workDir string) *State {
	return &State{workDir: workDir, phase: PhaseIdle}
}

// Assign stores a run assignment unless another non-equivalent assignment is active.
func (s *State) Assign(a Assignment) error {
	a.RunID = strings.TrimSpace(a.RunID)
	a.WorkerID = strings.TrimSpace(a.WorkerID)
	if a.RunID == "" {
		return fmt.Errorf("run_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assignment.RunID != "" && s.phase != PhaseStopped {
		if assignmentsEqual(s.assignment, a) {
			return nil
		}
		return ErrActiveRunConflict
	}
	if err := s.persistAssignment(a); err != nil {
		return fmt.Errorf("%w: %v", ErrAssignmentPersistence, err)
	}
	s.assignment = a
	s.phase = PhaseAssigned
	s.active = ""
	s.lastError = ""
	return nil
}

func assignmentsEqual(a, b Assignment) bool {
	aJSON, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bJSON, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(aJSON) == string(bJSON)
}

// Transition advances the worker to the next expected phase or accepts an idempotent retry.
func (s *State) Transition(next Phase) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transitionLocked(next)
}

// TransitionForAssignment advances the phase only if the active run still matches runID.
func (s *State) TransitionForAssignment(runID string, next Phase) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assignment.RunID != strings.TrimSpace(runID) {
		return fmt.Errorf("%w: active run %q does not match %q", ErrActiveRunConflict, s.assignment.RunID, strings.TrimSpace(runID))
	}
	return s.transitionLocked(next)
}

// BeginPhaseForAssignment starts a phase hook if it is not already running or complete.
func (s *State) BeginPhaseForAssignment(runID string, next Phase) (Status, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assignment.RunID != strings.TrimSpace(runID) {
		return s.statusLocked(), false, fmt.Errorf("%w: active run %q does not match %q", ErrActiveRunConflict, s.assignment.RunID, strings.TrimSpace(runID))
	}
	if s.phase == next && s.active == "" {
		return s.statusLocked(), false, nil
	}
	if s.active == next {
		return s.statusLocked(), false, nil
	}
	if s.active != "" {
		return s.statusLocked(), false, fmt.Errorf("%w: phase %s already running", ErrInvalidPhaseTransition, s.active)
	}
	if !canTransition(s.phase, next) {
		return s.statusLocked(), false, fmt.Errorf("%w: %s to %s", ErrInvalidPhaseTransition, s.phase, next)
	}
	s.active = next
	s.lastError = ""
	return s.statusLocked(), true, nil
}

// CompletePhaseForAssignment records the terminal result of an asynchronous phase hook.
func (s *State) CompletePhaseForAssignment(runID string, phase Phase, phaseErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assignment.RunID != strings.TrimSpace(runID) {
		return fmt.Errorf("%w: active run %q does not match %q", ErrActiveRunConflict, s.assignment.RunID, strings.TrimSpace(runID))
	}
	if s.active != phase {
		return fmt.Errorf("%w: active phase %q does not match %q", ErrInvalidPhaseTransition, s.active, phase)
	}
	s.active = ""
	if phaseErr != nil {
		s.lastError = phaseErr.Error()
		return nil
	}
	s.lastError = ""
	return s.transitionLocked(phase)
}

func (s *State) transitionLocked(next Phase) error {
	if s.phase == next {
		return nil
	}
	if !canTransition(s.phase, next) {
		return fmt.Errorf("%w: %s to %s", ErrInvalidPhaseTransition, s.phase, next)
	}
	s.phase = next
	return nil
}

// Stop marks the current assignment as stopped. Idle workers cannot be stopped.
func (s *State) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assignment.RunID == "" || s.phase == PhaseIdle {
		return fmt.Errorf("%w: %s to %s", ErrInvalidPhaseTransition, s.phase, PhaseStopped)
	}
	s.phase = PhaseStopped
	s.active = ""
	return nil
}

// Status returns a copy of the current worker control state.
func (s *State) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *State) statusLocked() Status {
	return Status{
		Phase:          s.phase,
		ActivePhase:    s.active,
		CompletedPhase: s.phase,
		LastError:      s.lastError,
		Assignment:     s.assignment,
	}
}

func (s *State) persistAssignment(a Assignment) error {
	if s.workDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.workDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(s.workDir, "current-run.json"), data, 0o644)
}

func canTransition(current, next Phase) bool {
	if current == PhaseStopped {
		return next == PhaseStopped
	}
	order := map[Phase]int{
		PhaseIdle:     0,
		PhaseAssigned: 1,
		PhasePrepare:  2,
		PhaseConnect:  3,
		PhaseWarmup:   4,
		PhaseRun:      5,
		PhaseCooldown: 6,
		PhaseStopped:  7,
	}
	currentIndex, ok := order[current]
	if !ok {
		return false
	}
	nextIndex, ok := order[next]
	return ok && nextIndex == currentIndex+1
}
