package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
)

var errTerminalCutDisabled = errors.New("external terminal cut is not enabled")
var errTerminalReceiveSealFailed = errors.New("terminal receive seal failed")

// assignmentLifecycle owns one worker's assignment generations, including task
// admission, cancellation/join, teardown and terminal evidence. Only this module
// operates assignmentState; callers never coordinate state with runner hooks.
type assignmentLifecycle struct {
	// state owns persisted assignment metadata and legal phase transitions.
	state *assignmentState
	// runner executes workload effects; task lifetime remains owned here.
	runner WorkloadRunner
	// lifecycleMu serializes assignment admission, phase task publication, and
	// terminal stop commit without covering the phase hook's execution time.
	lifecycleMu sync.Mutex
	taskMu      sync.Mutex
	taskSeq     uint64
	activeTask  lifecycleTask
	stopMu      sync.Mutex
	stopTask    *terminalStopTask
	// stoppingAssignment fences assignment work as soon as an exact-generation
	// stop is admitted. It remains set until a stopped assignment is replaced.
	stoppingAssignment assignmentIdentity
	terminalMu         sync.Mutex
	// terminalLifecycle is an exact-generation post-drain cut captured before
	// EndAssignment closes sessions. It remains readable after PhaseStopped.
	terminalLifecycle *terminalLifecycleSnapshot
}

type terminalLifecycleSnapshot struct {
	identity  assignmentIdentity
	lifecycle LifecycleStatus
}

// lifecycleTaskKind identifies assignment work that terminal stop must cancel and join.
type lifecycleTaskKind string

const (
	lifecycleTaskPhase           lifecycleTaskKind = "phase"
	lifecycleTaskPrepareChannels lifecycleTaskKind = "prepare_channels"
)

// lifecycleTask is the single published phase or owner-channel preparation hook.
type lifecycleTask struct {
	id           uint64
	runID        string
	assignmentID string
	kind         lifecycleTaskKind
	phase        Phase
	cancel       context.CancelFunc
	done         <-chan struct{}
}

// terminalStopTask is the shared exact-run finalization outcome joined by stop retries.
type terminalStopTask struct {
	runID        string
	assignmentID string
	done         chan struct{}
	status       Status
	err          error
}

// newAssignmentLifecycle binds the existing workload adapter to its lifetime owner.
func newAssignmentLifecycle(workDir string, runner WorkloadRunner) *assignmentLifecycle {
	return &assignmentLifecycle{state: newAssignmentState(workDir), runner: runner}
}

// Assign persists a fresh generation before resetting its runner and evidence.
// An exact retry does not restart hooks or erase the current phase.
func (s *assignmentLifecycle) Assign(a Assignment) (Status, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	before := s.state.Status()
	if !s.stoppingAssignment.isZero() && before.Phase != PhaseStopped {
		return Status{}, fmt.Errorf("%w: assignment %q/%q is stopping", ErrInvalidPhaseTransition, s.stoppingAssignment.runID, s.stoppingAssignment.assignmentID)
	}
	if err := s.state.Assign(a); err != nil {
		return Status{}, err
	}
	status := s.state.Status()
	if assignmentStarted(before, status) {
		s.stoppingAssignment = assignmentIdentity{}
		s.clearTerminalLifecycle()
		s.stopMu.Lock()
		s.stopTask = nil
		s.stopMu.Unlock()
		if starter, ok := s.runner.(AssignmentStarter); ok {
			starter.BeginAssignment(status.Assignment)
		}
	}
	return status, nil
}

func assignmentStarted(before, after Status) bool {
	if after.Phase != PhaseAssigned || strings.TrimSpace(after.Assignment.RunID) == "" || strings.TrimSpace(after.Assignment.AssignmentID) == "" {
		return false
	}
	if strings.TrimSpace(before.Assignment.RunID) == "" {
		return true
	}
	if before.Phase == PhaseStopped {
		return !assignmentIdentityMatches(before.Assignment, after.Assignment.RunID, after.Assignment.AssignmentID)
	}
	return !assignmentIdentityMatches(before.Assignment, after.Assignment.RunID, after.Assignment.AssignmentID)
}

// StartPhase admits and publishes a phase task before returning. A nil result
// channel denotes an idempotent retry; a non-nil channel receives one outcome.
// Execution belongs to the assignment, independent of a caller's request lifetime.
func (s *assignmentLifecycle) StartPhase(identity assignmentIdentity, phase Phase) (Status, <-chan error, error) {
	// Stopped is a lifetime transition, never a runnable phase: it must join
	// tasks and teardown through Stop even for callers without HTTP routing.
	switch phase {
	case PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown:
	default:
		return Status{}, nil, fmt.Errorf("%w: unsupported workload phase %q", ErrInvalidPhaseTransition, phase)
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	status := s.state.Status()
	if !identity.matches(status.Assignment) {
		return Status{}, nil, assignmentIdentityConflict(status.Assignment, identity.runID, identity.assignmentID)
	}
	if !s.stoppingAssignment.isZero() {
		stopping := s.stoppingAssignment
		return Status{}, nil, fmt.Errorf("%w: assignment %q/%q is stopping", ErrInvalidPhaseTransition, stopping.runID, stopping.assignmentID)
	}
	activeTask := s.currentLifecycleTask()
	if activeTask.id != 0 && !(activeTask.kind == lifecycleTaskPhase && activeTask.runID == identity.runID && activeTask.assignmentID == identity.assignmentID && activeTask.phase == phase) {
		return Status{}, nil, fmt.Errorf("%w: lifecycle task %q already running", ErrInvalidPhaseTransition, activeTask.kind)
	}
	nextStatus, started, err := s.state.BeginPhaseForAssignment(identity.runID, identity.assignmentID, phase)
	if err != nil {
		return Status{}, nil, err
	}
	if !started {
		return nextStatus, nil, nil
	}
	assignment := nextStatus.Assignment
	phaseCtx, phaseCancel := context.WithCancel(context.Background())
	phaseDone := make(chan struct{})
	taskID := s.storeLifecycleTask(assignment.RunID, assignment.AssignmentID, lifecycleTaskPhase, phase, phaseCancel, phaseDone)
	result := make(chan error, 1)
	go func() {
		err := s.completePhase(phaseCtx, phase, assignment)
		close(phaseDone)
		s.clearLifecycleTask(taskID)
		result <- err
	}()
	return nextStatus, result, nil
}

// PrepareChannels shares phase task admission and stop ownership, but follows
// the caller's context and does not advance the workload phase.
func (s *assignmentLifecycle) PrepareChannels(ctx context.Context, identity assignmentIdentity) (Status, error) {
	s.lifecycleMu.Lock()
	status := s.state.Status()
	if !identity.matches(status.Assignment) {
		s.lifecycleMu.Unlock()
		return Status{}, assignmentIdentityConflict(status.Assignment, identity.runID, identity.assignmentID)
	}
	if !s.stoppingAssignment.isZero() {
		stopping := s.stoppingAssignment
		s.lifecycleMu.Unlock()
		return Status{}, fmt.Errorf("%w: assignment %q/%q is stopping", ErrInvalidPhaseTransition, stopping.runID, stopping.assignmentID)
	}
	if status.Assignment.RunID == "" || status.Phase == PhaseIdle || status.Phase == PhaseStopped {
		s.lifecycleMu.Unlock()
		return Status{}, errWorkerNotAssigned
	}
	if activeTask := s.currentLifecycleTask(); activeTask.id != 0 {
		s.lifecycleMu.Unlock()
		return Status{}, fmt.Errorf("%w: lifecycle task %q already running", ErrInvalidPhaseTransition, activeTask.kind)
	}
	runner, ok := s.runner.(PrepareChannelsRunner)
	if !ok {
		s.lifecycleMu.Unlock()
		return status, nil
	}
	prepareCtx, prepareCancel := context.WithCancel(ctx)
	prepareDone := make(chan struct{})
	taskID := s.storeLifecycleTask(status.Assignment.RunID, status.Assignment.AssignmentID, lifecycleTaskPrepareChannels, "", prepareCancel, prepareDone)
	assignment := status.Assignment
	s.lifecycleMu.Unlock()
	err := runner.PrepareChannels(prepareCtx, assignment)
	prepareCancel()
	close(prepareDone)
	s.clearLifecycleTask(taskID)
	if err != nil {
		return Status{}, &assignmentExecutionError{err}
	}
	return s.state.Status(), nil
}

var errWorkerNotAssigned = errors.New("worker is not assigned")

// assignmentExecutionError distinguishes an admitted hook failure from admission
// rejection, preserving the adapter's existing response classification.
type assignmentExecutionError struct{ error }

func (e *assignmentExecutionError) Unwrap() error { return e.error }

// AcknowledgeTerminalCut binds one exact generation while assignment replacement
// is fenced. Identical retries retain their original immutable binding.
func (s *assignmentLifecycle) AcknowledgeTerminalCut(request TerminalCutRequest) (TerminalCutBinding, error) {
	identity, err := requiredAssignmentIdentity(request.RunID, request.AssignmentID)
	if err != nil {
		return TerminalCutBinding{}, err
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	status := s.state.Status()
	if !identity.matches(status.Assignment) {
		return TerminalCutBinding{}, assignmentIdentityConflict(status.Assignment, identity.runID, identity.assignmentID)
	}
	coordinator, ok := s.runner.(TerminalCutCoordinator)
	if !ok {
		return TerminalCutBinding{}, errTerminalCutDisabled
	}
	cutStatus := coordinator.TerminalCutStatus()
	if cutStatus.Binding != nil {
		if terminalCutRequestMatchesBinding(request, *cutStatus.Binding) {
			return *cutStatus.Binding, nil
		}
		return TerminalCutBinding{}, ErrTerminalCutAlreadyAcknowledged
	}
	if status.Phase != PhaseRun || status.ActivePhase != PhaseCooldown || !cutStatus.Required || !cutStatus.Ready {
		return TerminalCutBinding{}, ErrTerminalCutNotReady
	}
	if err := validateTerminalCutRequest(request, cutStatus.ReadyAt, cutStatus.DeadlineAt, time.Now().UTC()); err != nil {
		return TerminalCutBinding{}, err
	}
	return coordinator.AcknowledgeTerminalCut(request)
}

func (s *assignmentLifecycle) completePhase(ctx context.Context, phase Phase, assignment Assignment) error {
	err := s.runPhaseHook(ctx, phase, assignment)
	completeErr := s.state.CompletePhaseForAssignment(assignment.RunID, assignment.AssignmentID, phase, err)
	if errors.Is(err, context.Canceled) && errors.Is(completeErr, ErrInvalidPhaseTransition) {
		return nil
	}
	if err != nil {
		return err
	}
	return completeErr
}

func (s *assignmentLifecycle) storeLifecycleTask(runID, assignmentID string, kind lifecycleTaskKind, phase Phase, cancel context.CancelFunc, done <-chan struct{}) uint64 {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	s.taskSeq++
	s.activeTask = lifecycleTask{id: s.taskSeq, runID: runID, assignmentID: assignmentID, kind: kind, phase: phase, cancel: cancel, done: done}
	return s.taskSeq
}

// clearLifecycleTask removes only the exact task generation that completed.
func (s *assignmentLifecycle) clearLifecycleTask(taskID uint64) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.activeTask.id != taskID {
		return
	}
	s.activeTask = lifecycleTask{}
}

// currentLifecycleTask reaps completed tasks without clearing a newer generation.
func (s *assignmentLifecycle) currentLifecycleTask() lifecycleTask {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.activeTask.id != 0 && lifecycleTaskDone(s.activeTask.done) {
		s.activeTask = lifecycleTask{}
	}
	return s.activeTask
}

func (s *assignmentLifecycle) cancelActiveLifecycleTask(expected assignmentIdentity) <-chan struct{} {
	task := s.currentLifecycleTask()
	if task.id != 0 && (task.runID != expected.runID || task.assignmentID != expected.assignmentID) {
		return nil
	}
	cancel := task.cancel
	if cancel != nil {
		cancel()
	}
	return task.done
}

func lifecycleTaskDone(done <-chan struct{}) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func (s *assignmentLifecycle) runPhaseHook(ctx context.Context, phase Phase, assignment Assignment) error {
	if s.runner == nil {
		return nil
	}
	switch phase {
	case PhasePrepare:
		return s.runner.Prepare(ctx, assignment)
	case PhaseConnect:
		return s.runner.Connect(ctx, assignment)
	case PhaseWarmup:
		return s.runner.Warmup(ctx, assignment)
	case PhaseRun:
		return s.runner.Run(ctx, assignment)
	case PhaseCooldown:
		return s.runner.Cooldown(ctx, assignment)
	default:
		return nil
	}
}

// Stop fences new work immediately and returns the shared terminal outcome.
// Request cancellation only abandons waiting; task join and teardown stay owned.
func (s *assignmentLifecycle) Stop(expected assignmentIdentity) *terminalStopTask {
	// Stop admission is synchronous with assignment and phase admission. Once
	// this lock is released, no new assignment work for the run may start.
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	current := s.state.Status()
	if !expected.matches(current.Assignment) {
		task := &terminalStopTask{
			runID:        expected.runID,
			assignmentID: expected.assignmentID,
			done:         make(chan struct{}),
			status:       current,
			err:          assignmentIdentityConflict(current.Assignment, expected.runID, expected.assignmentID),
		}
		close(task.done)
		return task
	}
	if current.Phase == PhaseIdle {
		task := &terminalStopTask{
			runID:        expected.runID,
			assignmentID: expected.assignmentID,
			done:         make(chan struct{}),
			status:       current,
			err:          fmt.Errorf("%w: %s to %s", ErrInvalidPhaseTransition, current.Phase, PhaseStopped),
		}
		close(task.done)
		return task
	}
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	if existing := s.stopTask; existing != nil && existing.runID == expected.runID && existing.assignmentID == expected.assignmentID {
		select {
		case <-existing.done:
			if existing.err == nil || errors.Is(existing.err, errTerminalReceiveSealFailed) {
				return existing
			}
		default:
			return existing
		}
	}
	task := &terminalStopTask{runID: expected.runID, assignmentID: expected.assignmentID, done: make(chan struct{})}
	s.stopTask = task
	s.stoppingAssignment = expected
	activeDone := s.cancelActiveLifecycleTask(expected)
	// Terminal cleanup must outlive the caller's HTTP deadline. Concurrent
	// retries for the same run join this one background finalizer.
	go func() {
		task.status, task.err = s.finalizeStop(expected, activeDone)
		close(task.done)
	}()
	return task
}

func (s *assignmentLifecycle) finalizeStop(expected assignmentIdentity, activeDone <-chan struct{}) (Status, error) {
	if activeDone != nil {
		<-activeDone
	}

	s.lifecycleMu.Lock()
	before := s.state.Status()
	if !expected.matches(before.Assignment) {
		s.lifecycleMu.Unlock()
		return before, assignmentIdentityConflict(before.Assignment, expected.runID, expected.assignmentID)
	}
	if before.Phase == PhaseStopped {
		s.lifecycleMu.Unlock()
		return before, nil
	}
	assignment := before.Assignment
	s.lifecycleMu.Unlock()

	// Freeze the exact post-drain lifecycle before teardown. The stopped status
	// exposes this cut with explicit provenance instead of pretending the
	// sessions remain live after EndAssignment closes them.
	var terminalReceiveSealErr error
	if before.Phase == PhaseCooldown && before.ActivePhase == "" && before.LastError == "" {
		sealComplete := true
		if coordinator, ok := s.runner.(TerminalCutCoordinator); ok && coordinator.TerminalCutStatus().Required {
			sealComplete = false
			sealer, canSeal := s.runner.(TerminalReceiveSealer)
			if canSeal {
				cut := coordinator.TerminalCutStatus()
				if cut.Binding == nil || cut.DeadlineAt.IsZero() {
					sealComplete = false
				} else {
					sealCtx, cancel := context.WithDeadline(context.Background(), cut.DeadlineAt)
					if err := sealer.SealTerminalReceive(sealCtx, assignment); err != nil {
						terminalReceiveSealErr = errTerminalReceiveSealFailed
						sealComplete = false
					} else {
						sealComplete = true
					}
					cancel()
				}
			}
		}
		if sealComplete {
			s.freezeTerminalLifecycle(expected)
		}
	}

	// Admission remains fenced while teardown runs, so a slow runner cannot
	// make exact-run phase or prepare requests wait behind this cleanup.
	if stopper, ok := s.runner.(AssignmentStopper); ok {
		if err := stopper.EndAssignment(assignment); err != nil {
			return s.state.Status(), fmt.Errorf("end assignment %q/%q: %w", assignment.RunID, assignment.AssignmentID, err)
		}
	}

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	current := s.state.Status()
	if !expected.matches(current.Assignment) {
		return current, assignmentIdentityConflict(current.Assignment, expected.runID, expected.assignmentID)
	}
	if err := s.state.StopForAssignment(expected.runID, expected.assignmentID); err != nil {
		return s.state.Status(), err
	}
	return s.statusWithTerminalLifecycle(s.state.Status()), terminalReceiveSealErr
}

// Status projects live progress or retained pre-close proof. It does not collect
// report histograms or promise an atomic live cut across assignment replacement.
func (s *assignmentLifecycle) Status() Status {
	status := s.state.Status()
	status.ObservedAt = time.Now().UTC()
	if status.Phase == PhaseStopped {
		return s.statusWithTerminalLifecycle(status)
	}
	if reporter, ok := s.runner.(LifecycleStatusReporter); ok {
		lifecycle := reporter.LifecycleStatus()
		lifecycle.TerminalPreClose = false
		decorateLifecycleTerminalCut(s.runner, &lifecycle)
		status.Lifecycle = &lifecycle
		return status
	}
	if reporter, ok := s.runner.(ConnectionStatusReporter); ok {
		active, reconnected := reporter.ConnectionStatus()
		lifecycle := LifecycleStatus{ActiveConnections: active, ReconnectedUsers: reconnected}
		decorateLifecycleTerminalCut(s.runner, &lifecycle)
		status.Lifecycle = &lifecycle
	}
	return status
}

func (s *assignmentLifecycle) freezeTerminalLifecycle(identity assignmentIdentity) {
	var lifecycle *LifecycleStatus
	if reporter, ok := s.runner.(LifecycleStatusReporter); ok {
		value := reporter.LifecycleStatus()
		decorateLifecycleTerminalCut(s.runner, &value)
		if value.Traffic.Remaining != 0 || !value.ReceiveDrain.TerminalProofComplete() {
			return
		}
		if value.TerminalCutRequired && (!value.ReceiveDrain.Required || !value.TerminalCutReady || value.TerminalCut == nil || !validTerminalCutBinding(*value.TerminalCut, identity)) {
			return
		}
		value.TerminalPreClose = true
		lifecycle = &value
	}
	if lifecycle == nil {
		return
	}
	s.terminalMu.Lock()
	s.terminalLifecycle = &terminalLifecycleSnapshot{identity: identity, lifecycle: *lifecycle}
	s.terminalMu.Unlock()
}

func decorateLifecycleTerminalCut(runner WorkloadRunner, lifecycle *LifecycleStatus) {
	coordinator, ok := runner.(TerminalCutCoordinator)
	if !ok || lifecycle == nil {
		return
	}
	status := coordinator.TerminalCutStatus()
	lifecycle.TerminalCutRequired = status.Required
	lifecycle.TerminalCutReady = status.Ready
	lifecycle.TerminalCutReadyAt = status.ReadyAt
	lifecycle.TerminalCutDeadlineAt = status.DeadlineAt
	if status.Binding != nil {
		binding := *status.Binding
		lifecycle.TerminalCut = &binding
	} else {
		lifecycle.TerminalCut = nil
	}
}

func (s *assignmentLifecycle) statusWithTerminalLifecycle(status Status) Status {
	if status.ObservedAt.IsZero() {
		status.ObservedAt = time.Now().UTC()
	}
	identity, err := requiredAssignmentIdentity(status.Assignment.RunID, status.Assignment.AssignmentID)
	if err != nil {
		return status
	}
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	if s.terminalLifecycle == nil || s.terminalLifecycle.identity != identity {
		return status
	}
	lifecycle := s.terminalLifecycle.lifecycle
	status.Lifecycle = &lifecycle
	return status
}

func (s *assignmentLifecycle) clearTerminalLifecycle() {
	s.terminalMu.Lock()
	s.terminalLifecycle = nil
	s.terminalMu.Unlock()
}

func (s *assignmentLifecycle) metricsSnapshot() metrics.SnapshotData {
	if reporter, ok := s.runner.(MetricsReporter); ok {
		return normalizeMetricsSnapshot(reporter.MetricsSnapshot())
	}
	return metrics.SnapshotData{Counters: map[string]uint64{}, Gauges: map[string]float64{}, Histograms: map[string]metrics.HistogramSummary{}}
}

func normalizeMetricsSnapshot(snapshot metrics.SnapshotData) metrics.SnapshotData {
	snapshot = metrics.SanitizeSnapshot(snapshot)
	if snapshot.Counters == nil {
		snapshot.Counters = map[string]uint64{}
	}
	if snapshot.Gauges == nil {
		snapshot.Gauges = map[string]float64{}
	}
	if snapshot.Histograms == nil {
		snapshot.Histograms = map[string]metrics.HistogramSummary{}
	}
	return snapshot
}

// assignmentEvidence is captured under one assignment fence. Serialization may
// happen after releasing that fence because metric sanitization detaches its maps.
type assignmentEvidence struct {
	status  Status
	metrics metrics.SnapshotData
}

// Evidence validates the exact stopped generation and captures its metrics
// before assignment replacement can reset the runner. It never returns live data.
func (s *assignmentLifecycle) Evidence(expected assignmentIdentity) (assignmentEvidence, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	status := s.state.Status()
	if !expected.matches(status.Assignment) {
		return assignmentEvidence{}, assignmentIdentityConflict(status.Assignment, expected.runID, expected.assignmentID)
	}
	if status.Phase != PhaseStopped || status.ActivePhase != "" {
		return assignmentEvidence{}, fmt.Errorf("evidence assignment %q/%q is not terminal: phase=%q active_phase=%q", expected.runID, expected.assignmentID, status.Phase, status.ActivePhase)
	}
	return assignmentEvidence{status: status, metrics: s.metricsSnapshot()}, nil
}

// controlStatus returns phase response metadata without collecting live telemetry.
func (s *assignmentLifecycle) controlStatus() Status { return s.state.Status() }
