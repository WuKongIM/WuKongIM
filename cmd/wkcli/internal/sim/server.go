package sim

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
)

// statusServer exposes local simulation health and status endpoints.
type statusServer struct {
	// listenAddr is the configured address passed to net.Listen.
	listenAddr string
	// status provides snapshots for the /status endpoint.
	status *statusModel

	// mu protects boundAddr publication after the listener is opened.
	mu sync.Mutex
	// boundAddr is the actual TCP address, including an OS-selected port.
	boundAddr string
}

// newStatusServer creates a local HTTP status server.
func newStatusServer(addr string, status *statusModel) *statusServer {
	return &statusServer{
		listenAddr: addr,
		status:     status,
	}
}

// start serves status HTTP requests until the context is canceled.
func (s *statusServer) start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return err
	}
	s.setAddr(listener.Addr().String())

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/status", s.handleStatus)
	server := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// addr returns the actual listener address after start opens the socket.
func (s *statusServer) addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.boundAddr
}

// setAddr publishes the actual listener address for tests and callers.
func (s *statusServer) setAddr(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.boundAddr = addr
}

// handleHealthz serves the local health probe endpoint.
func (s *statusServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleStatus serves the current simulation snapshot as JSON.
func (s *statusServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.status.snapshot())
}
