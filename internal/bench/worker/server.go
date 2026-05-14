package worker

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Config controls the worker HTTP control server.
type Config struct {
	// ControlToken is the bearer token required for /v1 control routes.
	ControlToken string
	// InsecureControl allows unauthenticated /v1 control routes when ControlToken is empty.
	InsecureControl bool
	// WorkDir stores the active assignment file current-run.json when configured.
	WorkDir string
}

// Server exposes the wkbench worker control HTTP API.
type Server struct {
	cfg   Config
	state *State
	mux   *http.ServeMux
}

// NewServer builds a worker control server with in-memory assignment state.
func NewServer(cfg Config) *Server {
	s := &Server{cfg: cfg, state: NewState(cfg.WorkDir), mux: http.NewServeMux()}
	s.routes()
	return s
}

// ServeHTTP dispatches worker control HTTP requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.healthz)
	s.mux.HandleFunc("/v1/info", s.withControl(s.info))
	s.mux.HandleFunc("/v1/assign", s.withControl(s.assign))
	s.mux.HandleFunc("/v1/phase/prepare", s.withControl(s.phase(PhasePrepare)))
	s.mux.HandleFunc("/v1/phase/connect", s.withControl(s.phase(PhaseConnect)))
	s.mux.HandleFunc("/v1/phase/warmup", s.withControl(s.phase(PhaseWarmup)))
	s.mux.HandleFunc("/v1/phase/run", s.withControl(s.phase(PhaseRun)))
	s.mux.HandleFunc("/v1/phase/cooldown", s.withControl(s.phase(PhaseCooldown)))
	s.mux.HandleFunc("/v1/stop", s.withControl(s.stop))
	s.mux.HandleFunc("/v1/status", s.withControl(s.status))
	s.mux.HandleFunc("/v1/metrics", s.withControl(s.emptyJSON))
	s.mux.HandleFunc("/v1/report", s.withControl(s.emptyJSON))
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
	if err := s.state.Assign(a); err != nil {
		switch {
		case errors.Is(err, ErrActiveRunConflict):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, s.state.Status())
}

func (s *Server) phase(phase Phase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		if err := s.state.Transition(phase); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s.state.Status())
	}
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
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

func (s *Server) emptyJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
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
	if s.cfg.ControlToken == "" {
		return s.cfg.InsecureControl
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

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
