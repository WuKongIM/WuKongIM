package worker

import (
	"context"
	"encoding/json"
	"errors"
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

	phaseMu sync.Mutex
	cancel  phaseCancel
}

type phaseCancel struct {
	runID  string
	phase  Phase
	cancel context.CancelFunc
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
	before := s.state.Status()
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
		if starter, ok := s.runner.(AssignmentStarter); ok {
			starter.BeginAssignment(status.Assignment)
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func assignmentStarted(before, after Status) bool {
	if after.Phase != PhaseAssigned || strings.TrimSpace(after.Assignment.RunID) == "" {
		return false
	}
	if strings.TrimSpace(before.Assignment.RunID) == "" {
		return true
	}
	if before.Phase == PhaseStopped {
		return true
	}
	return before.Assignment.RunID != after.Assignment.RunID
}

func (s *Server) phase(phase Phase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		status := s.state.Status()
		nextStatus, started, err := s.state.BeginPhaseForAssignment(status.Assignment.RunID, phase)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if !started {
			writeJSON(w, http.StatusOK, nextStatus)
			return
		}
		assignment := nextStatus.Assignment
		phaseCtx, phaseCancel := context.WithCancel(context.Background())
		s.storePhaseCancel(assignment.RunID, phase, phaseCancel)
		done := make(chan error, 1)
		go func() {
			done <- s.completePhase(phaseCtx, phase, assignment)
		}()
		select {
		case err := <-done:
			s.clearPhaseCancel(assignment.RunID, phase)
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
	s.clearPhaseCancel(assignment.RunID, phase)
	completeErr := s.state.CompletePhaseForAssignment(assignment.RunID, phase, err)
	if errors.Is(err, context.Canceled) && errors.Is(completeErr, ErrInvalidPhaseTransition) {
		return nil
	}
	if err != nil {
		return err
	}
	return completeErr
}

func (s *Server) storePhaseCancel(runID string, phase Phase, cancel context.CancelFunc) {
	s.phaseMu.Lock()
	defer s.phaseMu.Unlock()
	s.cancel = phaseCancel{runID: runID, phase: phase, cancel: cancel}
}

func (s *Server) clearPhaseCancel(runID string, phase Phase) {
	s.phaseMu.Lock()
	defer s.phaseMu.Unlock()
	if s.cancel.runID != runID || s.cancel.phase != phase {
		return
	}
	s.cancel = phaseCancel{}
}

func (s *Server) cancelActivePhase() {
	s.phaseMu.Lock()
	cancel := s.cancel.cancel
	s.cancel = phaseCancel{}
	s.phaseMu.Unlock()
	if cancel != nil {
		cancel()
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
	status := s.state.Status()
	if status.Assignment.RunID == "" || status.Phase == PhaseIdle || status.Phase == PhaseStopped {
		writeError(w, http.StatusConflict, "worker is not assigned")
		return
	}
	if runner, ok := s.runner.(PrepareChannelsRunner); ok {
		if err := runner.PrepareChannels(r.Context(), status.Assignment); err != nil {
			if errors.Is(err, errTargetUnavailable) {
				writePhaseError(w, http.StatusServiceUnavailable, err)
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, s.state.Status())
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.cancelActivePhase()
	if err := s.state.Stop(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.state.Status())
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
	writeJSON(w, http.StatusOK, s.metricsSnapshot())
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	status := s.state.Status()
	payload := map[string]any{
		"run_id":    status.Assignment.RunID,
		"worker_id": status.Assignment.WorkerID,
		"phase":     status.Phase,
		"metrics":   s.metricsSnapshot(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report.WorkerReport{WorkerID: status.Assignment.WorkerID, Report: data})
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
	writeJSON(w, status, map[string]string{
		"error":       err.Error(),
		"reason_code": string(failureReasonForError(err)),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	writeJSON(w, status, v)
}
