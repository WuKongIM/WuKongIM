package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
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

// FailureReasonCode is the stable machine-readable cause of a worker phase failure.
type FailureReasonCode string

const (
	// FailureReasonPhaseHookFailed means a phase hook failed without a narrower classification.
	FailureReasonPhaseHookFailed FailureReasonCode = "phase_hook_failed"
	// FailureReasonTCPSourceUnavailable means the configured local TCP source could not be used.
	FailureReasonTCPSourceUnavailable FailureReasonCode = "tcp_source_unavailable"
	// FailureReasonTCPSourcePoolExhausted means every configured local TCP source candidate was consumed.
	FailureReasonTCPSourcePoolExhausted FailureReasonCode = "tcp_source_pool_exhausted"
	// FailureReasonTargetUnavailable means the remote benchmark target was unavailable.
	FailureReasonTargetUnavailable FailureReasonCode = "target_unavailable"
)

// Valid reports whether the code belongs to the worker control API contract.
func (c FailureReasonCode) Valid() bool {
	switch c {
	case FailureReasonPhaseHookFailed, FailureReasonTCPSourceUnavailable, FailureReasonTCPSourcePoolExhausted, FailureReasonTargetUnavailable:
		return true
	default:
		return false
	}
}

// FailureOperationCode identifies a bounded workload operation or control
// stage that failed.
type FailureOperationCode string

const (
	// FailureOperationPersonSendackLock means a person sender could not acquire its sendack lock.
	FailureOperationPersonSendackLock FailureOperationCode = "person_sendack_lock"
	// FailureOperationPersonSend means a person message send failed.
	FailureOperationPersonSend FailureOperationCode = "person_send"
	// FailureOperationPersonSendack means a person send acknowledgement failed.
	FailureOperationPersonSendack FailureOperationCode = "person_sendack"
	// FailureOperationPersonRecv means a person receive verification failed.
	FailureOperationPersonRecv FailureOperationCode = "person_recv"
	// FailureOperationPersonRecvack means a person receive acknowledgement failed.
	FailureOperationPersonRecvack FailureOperationCode = "person_recvack"
	// FailureOperationGroupSendackLock means a group sender could not acquire its sendack lock.
	FailureOperationGroupSendackLock FailureOperationCode = "group_sendack_lock"
	// FailureOperationGroupSend means a group message send failed.
	FailureOperationGroupSend FailureOperationCode = "group_send"
	// FailureOperationGroupSendack means a group send acknowledgement failed.
	FailureOperationGroupSendack FailureOperationCode = "group_sendack"
	// FailureOperationGroupRecv means a group receive verification failed.
	FailureOperationGroupRecv FailureOperationCode = "group_recv"
	// FailureOperationGroupRecvack means a group receive acknowledgement failed.
	FailureOperationGroupRecvack FailureOperationCode = "group_recvack"
	// FailureOperationWorkerStatus means the coordinator could not read worker status before the phase deadline.
	FailureOperationWorkerStatus FailureOperationCode = "worker_status"
	// FailureOperationPhaseCompletion means an exact-run worker status was observed but the phase did not complete before its deadline.
	FailureOperationPhaseCompletion FailureOperationCode = "phase_completion"
)

// Valid reports whether the operation belongs to the worker control API contract.
func (c FailureOperationCode) Valid() bool {
	switch c {
	case FailureOperationPersonSendackLock, FailureOperationPersonSend, FailureOperationPersonSendack,
		FailureOperationPersonRecv, FailureOperationPersonRecvack, FailureOperationGroupSendackLock,
		FailureOperationGroupSend, FailureOperationGroupSendack, FailureOperationGroupRecv, FailureOperationGroupRecvack,
		FailureOperationWorkerStatus, FailureOperationPhaseCompletion:
		return true
	default:
		return false
	}
}

// Assignment is the control-plane run shard assigned to this worker.
type Assignment struct {
	// RunID identifies the benchmark run that owns this assignment.
	RunID string `json:"run_id"`
	// AssignmentID identifies one immutable worker-assignment generation within RunID.
	AssignmentID string `json:"assignment_id"`
	// WorkerID identifies this worker within the benchmark worker set.
	WorkerID string `json:"worker_id,omitempty"`
	// Client contains only this worker's optional per-session client capacity profile.
	Client *model.WorkerClientConfig `json:"client,omitempty"`
	// TCPSource contains only this worker's optional local TCP source pool.
	TCPSource *model.TCPSourceConfig `json:"tcp_source,omitempty"`
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
	// LastErrorCode classifies LastError without requiring callers to parse its text.
	LastErrorCode FailureReasonCode `json:"last_error_code,omitempty"`
	// LastErrorOperation identifies a safe low-cardinality workload operation when known.
	LastErrorOperation FailureOperationCode `json:"last_error_operation,omitempty"`
	// Assignment is the active or most recently stopped assignment.
	Assignment Assignment `json:"assignment"`
}

// State stores the active worker assignment and monotonic phase.
type State struct {
	mu                 sync.Mutex
	workDir            string
	phase              Phase
	active             Phase
	lastError          string
	lastErrorCode      FailureReasonCode
	lastErrorOperation FailureOperationCode
	assignment         Assignment
}

// NewState creates empty worker assignment state. When workDir is non-empty,
// accepted assignments are persisted to current-run.json.
func NewState(workDir string) *State {
	return &State{workDir: workDir, phase: PhaseIdle}
}

// Assign stores a run assignment unless another non-equivalent assignment is active.
func (s *State) Assign(a Assignment) error {
	a.RunID = strings.TrimSpace(a.RunID)
	a.AssignmentID = strings.TrimSpace(a.AssignmentID)
	a.WorkerID = strings.TrimSpace(a.WorkerID)
	if a.RunID == "" {
		return fmt.Errorf("run_id is required")
	}
	if a.AssignmentID == "" {
		return fmt.Errorf("assignment_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assignment.RunID != "" {
		if assignmentsEqual(s.assignment, a) && s.phase != PhaseStopped {
			return nil
		}
		if s.phase != PhaseStopped || assignmentIdentityMatches(s.assignment, a.RunID, a.AssignmentID) {
			return fmt.Errorf(
				"%w: assignment %q/%q cannot replace phase %s",
				ErrActiveRunConflict,
				s.assignment.RunID,
				s.assignment.AssignmentID,
				s.phase,
			)
		}
	}
	if err := s.persistAssignment(a); err != nil {
		return fmt.Errorf("%w: %v", ErrAssignmentPersistence, err)
	}
	s.assignment = a
	s.phase = PhaseAssigned
	s.active = ""
	s.lastError = ""
	s.lastErrorCode = ""
	s.lastErrorOperation = ""
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

// TransitionForAssignment advances the phase only if the exact assignment generation matches.
func (s *State) TransitionForAssignment(runID, assignmentID string, next Phase) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !assignmentIdentityMatches(s.assignment, runID, assignmentID) {
		return assignmentIdentityConflict(s.assignment, runID, assignmentID)
	}
	return s.transitionLocked(next)
}

// BeginPhaseForAssignment starts a phase hook if it is not already running or complete.
func (s *State) BeginPhaseForAssignment(runID, assignmentID string, next Phase) (Status, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !assignmentIdentityMatches(s.assignment, runID, assignmentID) {
		return s.statusLocked(), false, assignmentIdentityConflict(s.assignment, runID, assignmentID)
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
	s.lastErrorCode = ""
	s.lastErrorOperation = ""
	return s.statusLocked(), true, nil
}

// CompletePhaseForAssignment records the terminal result of an asynchronous phase hook.
func (s *State) CompletePhaseForAssignment(runID, assignmentID string, phase Phase, phaseErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !assignmentIdentityMatches(s.assignment, runID, assignmentID) {
		return assignmentIdentityConflict(s.assignment, runID, assignmentID)
	}
	if s.active != phase {
		return fmt.Errorf("%w: active phase %q does not match %q", ErrInvalidPhaseTransition, s.active, phase)
	}
	s.active = ""
	if phaseErr != nil {
		s.lastError = phaseErr.Error()
		s.lastErrorCode = failureReasonForError(phaseErr)
		s.lastErrorOperation = failureOperationForError(phaseErr)
		return nil
	}
	s.lastError = ""
	s.lastErrorCode = ""
	s.lastErrorOperation = ""
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
	s.lastError = ""
	s.lastErrorCode = ""
	s.lastErrorOperation = ""
	return nil
}

// StopForAssignment marks only the exact active assignment generation stopped.
func (s *State) StopForAssignment(runID, assignmentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !assignmentIdentityMatches(s.assignment, runID, assignmentID) {
		return assignmentIdentityConflict(s.assignment, runID, assignmentID)
	}
	if s.assignment.RunID == "" || s.phase == PhaseIdle {
		return fmt.Errorf("%w: %s to %s", ErrInvalidPhaseTransition, s.phase, PhaseStopped)
	}
	s.phase = PhaseStopped
	s.active = ""
	s.lastError = ""
	s.lastErrorCode = ""
	s.lastErrorOperation = ""
	return nil
}

func assignmentIdentityMatches(assignment Assignment, runID, assignmentID string) bool {
	return assignment.RunID == strings.TrimSpace(runID) && assignment.AssignmentID == strings.TrimSpace(assignmentID)
}

func assignmentIdentityConflict(active Assignment, runID, assignmentID string) error {
	return fmt.Errorf(
		"%w: active assignment %q/%q does not match %q/%q",
		ErrActiveRunConflict,
		active.RunID,
		active.AssignmentID,
		strings.TrimSpace(runID),
		strings.TrimSpace(assignmentID),
	)
}

// Status returns a copy of the current worker control state.
func (s *State) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *State) statusLocked() Status {
	return Status{
		Phase:              s.phase,
		ActivePhase:        s.active,
		CompletedPhase:     s.phase,
		LastError:          s.lastError,
		LastErrorCode:      s.lastErrorCode,
		LastErrorOperation: s.lastErrorOperation,
		Assignment:         s.assignment,
	}
}

// failureReasonForError maps typed worker errors to the stable control API code.
func failureReasonForError(err error) FailureReasonCode {
	var sourceErr *benchworkload.TCPSourceError
	if errors.As(err, &sourceErr) {
		if sourceErr.Kind == benchworkload.TCPSourceErrorExhausted {
			return FailureReasonTCPSourcePoolExhausted
		}
		return FailureReasonTCPSourceUnavailable
	}
	if errors.Is(err, errTargetUnavailable) {
		return FailureReasonTargetUnavailable
	}
	return FailureReasonPhaseHookFailed
}

// failureOperationForError maps typed session failures to a fixed operation code.
func failureOperationForError(err error) FailureOperationCode {
	var sessionErr *benchworkload.SessionError
	if !errors.As(err, &sessionErr) || sessionErr == nil {
		return ""
	}
	switch strings.TrimSpace(sessionErr.Operation) {
	case "person sendack lock":
		return FailureOperationPersonSendackLock
	case "person send":
		return FailureOperationPersonSend
	case "person sendack":
		return FailureOperationPersonSendack
	case "person recv":
		return FailureOperationPersonRecv
	case "person recvack":
		return FailureOperationPersonRecvack
	case "group sendack lock":
		return FailureOperationGroupSendackLock
	case "group send":
		return FailureOperationGroupSend
	case "group sendack":
		return FailureOperationGroupSendack
	case "group recv":
		return FailureOperationGroupRecv
	case "group recvack":
		return FailureOperationGroupRecvack
	default:
		return ""
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
