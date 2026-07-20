package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
)

const phaseStartGrace = 25 * time.Millisecond

// WorkloadRunner receives worker lifecycle hooks for assigned benchmark shards.
type WorkloadRunner interface {
	// Prepare prepares target-side benchmark data for the active assignment.
	Prepare(ctx context.Context, assignment Assignment) error
	// Connect establishes workload connections for the active assignment.
	Connect(ctx context.Context, assignment Assignment) error
	// Warmup runs warmup traffic for the active assignment.
	Warmup(ctx context.Context, assignment Assignment) error
	// Run runs measured traffic for the active assignment.
	Run(ctx context.Context, assignment Assignment) error
	// Cooldown drains workload state after measured traffic.
	Cooldown(ctx context.Context, assignment Assignment) error
}

// MetricsReporter exposes worker-local metrics collected by a workload runner.
type MetricsReporter interface {
	// MetricsSnapshot returns a JSON-friendly worker-local metrics snapshot.
	MetricsSnapshot() metrics.SnapshotData
}

// ConnectionStatusReporter exposes live online connection state for dev-sim diagnostics.
type ConnectionStatusReporter interface {
	// ConnectionStatus returns the latest active connection count and reconnect churn.
	ConnectionStatus() (activeUsers int, reconnectedUsers uint64)
}

// AssignmentStarter receives a hook when the control plane accepts a fresh run assignment.
type AssignmentStarter interface {
	// BeginAssignment resets per-run runner state before any phase hook executes.
	BeginAssignment(assignment Assignment)
}

// AssignmentStopper releases resources owned by a terminal worker assignment.
type AssignmentStopper interface {
	// EndAssignment closes assignment-scoped connections and background work.
	// Implementations must be idempotent because stop requests may be retried.
	EndAssignment(assignment Assignment) error
}

// StopRequest identifies the exact assignment that a coordinator intends to stop.
type StopRequest struct {
	// RunID prevents a delayed stop request from terminating a newer assignment.
	RunID string `json:"run_id"`
	// AssignmentID prevents a delayed stop from terminating a newer generation of RunID.
	AssignmentID string `json:"assignment_id"`
}

// RunRequest binds an assignment operation to one exact worker assignment generation.
type RunRequest struct {
	// RunID prevents a delayed control request from operating on a newer assignment.
	RunID string `json:"run_id"`
	// AssignmentID prevents a delayed control request from crossing run generations.
	AssignmentID string `json:"assignment_id"`
}

// assignmentIdentity identifies one immutable worker assignment generation.
type assignmentIdentity struct {
	// runID identifies the parent benchmark run.
	runID string
	// assignmentID identifies one generation within runID.
	assignmentID string
}

func requiredAssignmentIdentity(runID, assignmentID string) (assignmentIdentity, error) {
	identity := assignmentIdentity{
		runID:        strings.TrimSpace(runID),
		assignmentID: strings.TrimSpace(assignmentID),
	}
	if identity.runID == "" {
		return assignmentIdentity{}, fmt.Errorf("run_id is required")
	}
	if identity.assignmentID == "" {
		return assignmentIdentity{}, fmt.Errorf("assignment_id is required")
	}
	return identity, nil
}

func (i assignmentIdentity) matches(assignment Assignment) bool {
	return assignmentIdentityMatches(assignment, i.runID, i.assignmentID)
}

func (i assignmentIdentity) isZero() bool {
	return i.runID == "" && i.assignmentID == ""
}

// TrafficResetter rebuilds traffic executors for an assignment without reconnecting sessions.
type TrafficResetter interface {
	// ResetTraffic applies assignment traffic changes while preserving existing connections.
	ResetTraffic(assignment Assignment) error
}

// TrafficRecoverer repairs failed sessions and rebuilds traffic executors.
type TrafficRecoverer interface {
	// RecoverTraffic applies recovery for cause while preserving healthy connections.
	RecoverTraffic(ctx context.Context, assignment Assignment, cause error) error
}

// Config controls the worker HTTP control server.
type Config struct {
	// ControlToken is the bearer token required for /v1 control routes.
	ControlToken string
	// InsecureControl allows unauthenticated /v1 control routes when ControlToken is empty.
	InsecureControl bool
	// WorkDir stores the active assignment file current-run.json when configured.
	WorkDir string
	// WorkloadRunner receives prepare, connect, warmup, run, and cooldown phase hooks.
	WorkloadRunner WorkloadRunner
	// WorkloadClientFactory overrides default runner client creation for tests.
	WorkloadClientFactory WorkloadClientFactory
}

// Server exposes the wkbench worker control HTTP API.
type Server struct {
	cfg    Config
	state  *State
	runner WorkloadRunner
	mux    *http.ServeMux

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

// NewServer builds a worker control server with in-memory assignment state.
func NewServer(cfg Config) *Server {
	runner := cfg.WorkloadRunner
	if runner == nil {
		runner = newDefaultWorkloadRunner(cfg.WorkloadClientFactory)
	}
	s := &Server{cfg: cfg, state: NewState(cfg.WorkDir), runner: runner, mux: http.NewServeMux()}
	s.routes()
	return s
}

// ServeHTTP dispatches worker control HTTP requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.notFound)
	s.mux.HandleFunc("/healthz", s.healthz)
	s.mux.HandleFunc("/v1/info", s.withControl(s.info))
	s.mux.HandleFunc("/v1/assign", s.withControl(s.assign))
	s.mux.HandleFunc("/v1/phase/prepare", s.withControl(s.phase(PhasePrepare)))
	s.mux.HandleFunc("/v1/phase/connect", s.withControl(s.phase(PhaseConnect)))
	s.mux.HandleFunc("/v1/phase/warmup", s.withControl(s.phase(PhaseWarmup)))
	s.mux.HandleFunc("/v1/phase/run", s.withControl(s.phase(PhaseRun)))
	s.mux.HandleFunc("/v1/phase/cooldown", s.withControl(s.phase(PhaseCooldown)))
	s.mux.HandleFunc("/v1/prepare/channels", s.withControl(s.prepareChannels))
	s.mux.HandleFunc("/v1/stop", s.withControl(s.stop))
	s.mux.HandleFunc("/v1/status", s.withControl(s.status))
	s.mux.HandleFunc("/v1/metrics", s.withControl(s.metrics))
	s.mux.HandleFunc("/v1/report", s.withControl(s.report))
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"worker": "wkbench", "insecure_control": s.cfg.InsecureControl})
}

func (s *Server) assign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var a Assignment
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeError(w, http.StatusBadRequest, "invalid assignment json")
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	before := s.state.Status()
	if !s.stoppingAssignment.isZero() && before.Phase != PhaseStopped {
		writeError(w, http.StatusConflict, fmt.Sprintf("%v: assignment %q/%q is stopping", ErrInvalidPhaseTransition, s.stoppingAssignment.runID, s.stoppingAssignment.assignmentID))
		return
	}
	if err := s.state.Assign(a); err != nil {
		switch {
		case errors.Is(err, ErrActiveRunConflict):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrAssignmentPersistence):
			writeError(w, http.StatusInternalServerError, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	status := s.state.Status()
	if assignmentStarted(before, status) {
		s.stoppingAssignment = assignmentIdentity{}
		s.stopMu.Lock()
		s.stopTask = nil
		s.stopMu.Unlock()
		if starter, ok := s.runner.(AssignmentStarter); ok {
			starter.BeginAssignment(status.Assignment)
		}
	}
	writeJSON(w, http.StatusOK, status)
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

func (s *Server) phase(phase Phase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		var request RunRequest
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeError(w, http.StatusBadRequest, "invalid phase json")
				return
			}
		}
		identity, err := requiredAssignmentIdentity(request.RunID, request.AssignmentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.lifecycleMu.Lock()
		status := s.state.Status()
		if !identity.matches(status.Assignment) {
			s.lifecycleMu.Unlock()
			writeError(w, http.StatusConflict, assignmentIdentityConflict(status.Assignment, identity.runID, identity.assignmentID).Error())
			return
		}
		if !s.stoppingAssignment.isZero() {
			stopping := s.stoppingAssignment
			s.lifecycleMu.Unlock()
			writeError(w, http.StatusConflict, fmt.Sprintf("%v: assignment %q/%q is stopping", ErrInvalidPhaseTransition, stopping.runID, stopping.assignmentID))
			return
		}
		activeTask := s.currentLifecycleTask()
		if activeTask.id != 0 && !(activeTask.kind == lifecycleTaskPhase && activeTask.runID == identity.runID && activeTask.assignmentID == identity.assignmentID && activeTask.phase == phase) {
			s.lifecycleMu.Unlock()
			writeError(w, http.StatusConflict, fmt.Sprintf("%v: lifecycle task %q already running", ErrInvalidPhaseTransition, activeTask.kind))
			return
		}
		nextStatus, started, err := s.state.BeginPhaseForAssignment(identity.runID, identity.assignmentID, phase)
		if err != nil {
			s.lifecycleMu.Unlock()
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if !started {
			s.lifecycleMu.Unlock()
			writeJSON(w, http.StatusOK, nextStatus)
			return
		}
		assignment := nextStatus.Assignment
		phaseCtx, phaseCancel := context.WithCancel(context.Background())
		phaseDone := make(chan struct{})
		phaseTaskID := s.storeLifecycleTask(assignment.RunID, assignment.AssignmentID, lifecycleTaskPhase, phase, phaseCancel, phaseDone)
		result := make(chan error, 1)
		go func() {
			err := s.completePhase(phaseCtx, phase, assignment)
			close(phaseDone)
			s.clearLifecycleTask(phaseTaskID)
			result <- err
		}()
		// Publishing the phase task before releasing lifecycleMu makes stop either
		// observe and cancel this task or commit stopped before this admission.
		s.lifecycleMu.Unlock()
		select {
		case err := <-result:
			if err != nil {
				if errors.Is(err, errTargetUnavailable) {
					writePhaseError(w, http.StatusServiceUnavailable, err)
					return
				}
				writePhaseError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, http.StatusOK, s.state.Status())
		case <-time.After(phaseStartGrace):
			writeJSONStatus(w, http.StatusAccepted, nextStatus)
		}
	}
}

func (s *Server) completePhase(ctx context.Context, phase Phase, assignment Assignment) error {
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

func (s *Server) storeLifecycleTask(runID, assignmentID string, kind lifecycleTaskKind, phase Phase, cancel context.CancelFunc, done <-chan struct{}) uint64 {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	s.taskSeq++
	s.activeTask = lifecycleTask{id: s.taskSeq, runID: runID, assignmentID: assignmentID, kind: kind, phase: phase, cancel: cancel, done: done}
	return s.taskSeq
}

// clearLifecycleTask removes only the exact task generation that completed.
func (s *Server) clearLifecycleTask(taskID uint64) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.activeTask.id != taskID {
		return
	}
	s.activeTask = lifecycleTask{}
}

// currentLifecycleTask returns the published task, reaping a completed generation.
func (s *Server) currentLifecycleTask() lifecycleTask {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if s.activeTask.id != 0 && lifecycleTaskDone(s.activeTask.done) {
		s.activeTask = lifecycleTask{}
	}
	return s.activeTask
}

// cancelActiveLifecycleTask requests cancellation and returns the task completion signal.
func (s *Server) cancelActiveLifecycleTask(expected assignmentIdentity) <-chan struct{} {
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

func (s *Server) runPhaseHook(ctx context.Context, phase Phase, assignment Assignment) error {
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

func (s *Server) prepareChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var request RunRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid prepare channels json")
			return
		}
	}
	identity, err := requiredAssignmentIdentity(request.RunID, request.AssignmentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.lifecycleMu.Lock()
	status := s.state.Status()
	if !identity.matches(status.Assignment) {
		s.lifecycleMu.Unlock()
		writeError(w, http.StatusConflict, assignmentIdentityConflict(status.Assignment, identity.runID, identity.assignmentID).Error())
		return
	}
	if !s.stoppingAssignment.isZero() {
		stopping := s.stoppingAssignment
		s.lifecycleMu.Unlock()
		writeError(w, http.StatusConflict, fmt.Sprintf("%v: assignment %q/%q is stopping", ErrInvalidPhaseTransition, stopping.runID, stopping.assignmentID))
		return
	}
	if status.Assignment.RunID == "" || status.Phase == PhaseIdle || status.Phase == PhaseStopped {
		s.lifecycleMu.Unlock()
		writeError(w, http.StatusConflict, "worker is not assigned")
		return
	}
	if activeTask := s.currentLifecycleTask(); activeTask.id != 0 {
		s.lifecycleMu.Unlock()
		writeError(w, http.StatusConflict, fmt.Sprintf("%v: lifecycle task %q already running", ErrInvalidPhaseTransition, activeTask.kind))
		return
	}
	runner, ok := s.runner.(PrepareChannelsRunner)
	if !ok {
		s.lifecycleMu.Unlock()
		writeJSON(w, http.StatusOK, status)
		return
	}
	prepareCtx, prepareCancel := context.WithCancel(r.Context())
	prepareDone := make(chan struct{})
	taskID := s.storeLifecycleTask(status.Assignment.RunID, status.Assignment.AssignmentID, lifecycleTaskPrepareChannels, "", prepareCancel, prepareDone)
	assignment := status.Assignment
	s.lifecycleMu.Unlock()

	err = runner.PrepareChannels(prepareCtx, assignment)
	prepareCancel()
	close(prepareDone)
	s.clearLifecycleTask(taskID)
	if err != nil {
		if errors.Is(err, errTargetUnavailable) {
			writePhaseError(w, http.StatusServiceUnavailable, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.state.Status())
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var request StopRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid stop json")
			return
		}
	}
	identity, err := requiredAssignmentIdentity(request.RunID, request.AssignmentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task := s.beginTerminalStop(identity)
	select {
	case <-task.done:
		if task.err != nil {
			statusCode := http.StatusInternalServerError
			if errors.Is(task.err, ErrActiveRunConflict) || errors.Is(task.err, ErrInvalidPhaseTransition) {
				statusCode = http.StatusConflict
			}
			writeError(w, statusCode, task.err.Error())
			return
		}
		writeJSON(w, http.StatusOK, task.status)
	case <-r.Context().Done():
		return
	}
}

func (s *Server) beginTerminalStop(expected assignmentIdentity) *terminalStopTask {
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
			if existing.err == nil {
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
	// retries for the same run join this one bounded background finalizer.
	go func() {
		task.status, task.err = s.finalizeStop(expected, activeDone)
		close(task.done)
	}()
	return task
}

func (s *Server) finalizeStop(expected assignmentIdentity, activeDone <-chan struct{}) (Status, error) {
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
	return s.state.Status(), nil
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.state.Status())
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if !s.validateEvidenceRun(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.metricsSnapshot())
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if !s.validateEvidenceRun(w, r) {
		return
	}
	status := s.state.Status()
	payload := map[string]any{
		"run_id":        status.Assignment.RunID,
		"assignment_id": status.Assignment.AssignmentID,
		"worker_id":     status.Assignment.WorkerID,
		"phase":         status.Phase,
		"metrics":       s.metricsSnapshot(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report.WorkerReport{WorkerID: status.Assignment.WorkerID, Report: data})
}

func (s *Server) validateEvidenceRun(w http.ResponseWriter, r *http.Request) bool {
	expected, err := requiredAssignmentIdentity(r.URL.Query().Get("run_id"), r.URL.Query().Get("assignment_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	status := s.state.Status()
	if expected.matches(status.Assignment) && status.Phase == PhaseStopped && status.ActivePhase == "" {
		return true
	}
	if expected.matches(status.Assignment) {
		writeError(w, http.StatusConflict, fmt.Sprintf("evidence assignment %q/%q is not terminal: phase=%q active_phase=%q", expected.runID, expected.assignmentID, status.Phase, status.ActivePhase))
		return false
	}
	writeError(w, http.StatusConflict, assignmentIdentityConflict(status.Assignment, expected.runID, expected.assignmentID).Error())
	return false
}

func (s *Server) metricsSnapshot() metrics.SnapshotData {
	if reporter, ok := s.runner.(MetricsReporter); ok {
		return normalizeMetricsSnapshot(reporter.MetricsSnapshot())
	}
	return metrics.SnapshotData{Counters: map[string]uint64{}, Gauges: map[string]float64{}, Histograms: map[string]metrics.HistogramSummary{}}
}

func normalizeMetricsSnapshot(snapshot metrics.SnapshotData) metrics.SnapshotData {
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

func (s *Server) withControl(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "missing or invalid control token")
			return
		}
		next(w, r)
	}
}

func (s *Server) authorized(r *http.Request) bool {
	if s.cfg.InsecureControl {
		return true
	}
	if s.cfg.ControlToken == "" {
		return false
	}
	if token := bearerToken(r.Header.Get("Authorization")); token == s.cfg.ControlToken {
		return true
	}
	return r.Header.Get("X-WKBench-Control-Token") == s.cfg.ControlToken
}

func bearerToken(header string) string {
	prefix := "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not found")
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET, POST")
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writePhaseError(w http.ResponseWriter, status int, err error) {
	payload := map[string]string{
		"error":       err.Error(),
		"reason_code": string(failureReasonForError(err)),
	}
	if operation := failureOperationForError(err); operation.Valid() {
		payload["operation"] = string(operation)
	}
	writeJSON(w, status, payload)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	writeJSON(w, status, v)
}
