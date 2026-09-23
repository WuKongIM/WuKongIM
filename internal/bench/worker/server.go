package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/report"
)

const (
	phaseStartGrace = 25 * time.Millisecond
	// TerminalReceiveSealFailureReasonCode identifies a failed post-ACK receive
	// reproof without exposing session or transport error details.
	TerminalReceiveSealFailureReasonCode = "terminal_receive_seal_failed"
)

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

// Server authenticates and translates HTTP control requests. Assignment state,
// runner hooks and evidence fencing belong exclusively to assignmentLifecycle.
type Server struct {
	cfg         Config
	assignments *assignmentLifecycle
	mux         *http.ServeMux
}

// NewServer builds the HTTP adapter and its worker-assignment owner.
func NewServer(cfg Config) *Server {
	runner := cfg.WorkloadRunner
	if runner == nil {
		runner = newDefaultWorkloadRunner(cfg.WorkloadClientFactory)
	}
	s := &Server{cfg: cfg, assignments: newAssignmentLifecycle(cfg.WorkDir, runner), mux: http.NewServeMux()}
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
	s.mux.HandleFunc("/v1/terminal-cut", s.withControl(s.terminalCut))
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
	status, err := s.assignments.Assign(a)
	if err != nil {
		switch {
		case errors.Is(err, ErrActiveRunConflict), errors.Is(err, ErrInvalidPhaseTransition):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrAssignmentPersistence):
			writeError(w, http.StatusInternalServerError, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, status)
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
		nextStatus, result, err := s.assignments.StartPhase(identity, phase)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if result == nil {
			writeJSON(w, http.StatusOK, nextStatus)
			return
		}
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
			writeJSON(w, http.StatusOK, s.assignments.controlStatus())
		case <-time.After(phaseStartGrace):
			writeJSONStatus(w, http.StatusAccepted, nextStatus)
		}
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
	status, err := s.assignments.PrepareChannels(r.Context(), identity)
	if err != nil {
		var executionErr *assignmentExecutionError
		if !errors.As(err, &executionErr) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, errTargetUnavailable) {
			writePhaseError(w, http.StatusServiceUnavailable, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
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
	task := s.assignments.Stop(identity)
	select {
	case <-task.done:
		if task.err != nil {
			if errors.Is(task.err, errTerminalReceiveSealFailed) {
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error":       errTerminalReceiveSealFailed.Error(),
					"reason_code": TerminalReceiveSealFailureReasonCode,
				})
				return
			}
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

func (s *Server) terminalCut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	request, err := decodeTerminalCutRequest(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	binding, err := s.assignments.AcknowledgeTerminalCut(request)
	if err != nil {
		statusCode := http.StatusBadRequest
		if errors.Is(err, ErrTerminalCutNotReady) || errors.Is(err, ErrTerminalCutAlreadyAcknowledged) || errors.Is(err, ErrActiveRunConflict) || errors.Is(err, errTerminalCutDisabled) {
			statusCode = http.StatusConflict
		}
		writeError(w, statusCode, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, binding)
}

func decodeTerminalCutRequest(reader io.Reader) (TerminalCutRequest, error) {
	if reader == nil {
		return TerminalCutRequest{}, fmt.Errorf("terminal cut json is required")
	}
	limited := &io.LimitedReader{R: reader, N: 4097}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var request TerminalCutRequest
	if err := decoder.Decode(&request); err != nil {
		return TerminalCutRequest{}, fmt.Errorf("invalid terminal cut json")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return TerminalCutRequest{}, fmt.Errorf("invalid terminal cut json")
	}
	if limited.N <= 0 {
		return TerminalCutRequest{}, fmt.Errorf("terminal cut json exceeds 4096 bytes")
	}
	return request, nil
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.assignments.Status())
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	evidence, ok := s.readEvidence(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, evidence.metrics)
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	evidence, ok := s.readEvidence(w, r)
	if !ok {
		return
	}
	status := evidence.status
	payload := map[string]any{
		"run_id":        status.Assignment.RunID,
		"assignment_id": status.Assignment.AssignmentID,
		"worker_id":     status.Assignment.WorkerID,
		"phase":         status.Phase,
		"metrics":       evidence.metrics,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report.WorkerReport{WorkerID: status.Assignment.WorkerID, Report: data})
}

func (s *Server) readEvidence(w http.ResponseWriter, r *http.Request) (assignmentEvidence, bool) {
	expected, err := requiredAssignmentIdentity(r.URL.Query().Get("run_id"), r.URL.Query().Get("assignment_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return assignmentEvidence{}, false
	}
	evidence, err := s.assignments.Evidence(expected)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return assignmentEvidence{}, false
	}
	return evidence, true
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
	reason := failureReasonForError(err)
	payload := map[string]string{
		"error":       safeFailureMessage(reason),
		"reason_code": string(reason),
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
