package devsim

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"
)

const defaultShutdownTimeout = 5 * time.Second

// StatusServer exposes health and status endpoints for the development simulator.
type StatusServer struct {
	listen string
	status *Status
	server *http.Server
	mux    *http.ServeMux
}

// NewStatusServer creates a status HTTP server.
func NewStatusServer(listen string, status *Status) *StatusServer {
	s := &StatusServer{listen: listen, status: status, mux: http.NewServeMux()}
	s.routes()
	s.server = &http.Server{Addr: listen, Handler: s.mux}
	return s
}

// Handler returns the HTTP handler for tests and embedded servers.
func (s *StatusServer) Handler() http.Handler {
	return s.mux
}

// Start listens and serves until the context is canceled or the server fails.
func (s *StatusServer) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() {
		err := s.server.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}

// Close gracefully shuts down the status HTTP server.
func (s *StatusServer) Close(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *StatusServer) routes() {
	s.mux.HandleFunc("/healthz", s.healthz)
	s.mux.HandleFunc("/status", s.statusz)
}

func (s *StatusServer) healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *StatusServer) statusz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.status.Snapshot())
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", http.MethodGet)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
