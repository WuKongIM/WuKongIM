package devsim

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServerRoutes(t *testing.T) {
	status := NewStatus("dev-sim-run")
	status.SetRunning(20, 5, 2)
	status.SetConnectionStats(17, 4)
	srv := NewStatusServer("127.0.0.1:0", status)

	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(healthResp, healthReq)
	require.Equal(t, http.StatusOK, healthResp.Code)

	statusReq := httptest.NewRequest(http.MethodGet, "/status", nil)
	statusResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(statusResp, statusReq)
	require.Equal(t, http.StatusOK, statusResp.Code)

	var snapshot Snapshot
	require.NoError(t, json.NewDecoder(statusResp.Body).Decode(&snapshot))
	require.Equal(t, StateRunning, snapshot.State)
	require.Equal(t, "dev-sim-run", snapshot.RunID)
	require.Equal(t, 20, snapshot.ConnectedUsers)
	require.Equal(t, 17, snapshot.ActiveUsers)
	require.Equal(t, uint64(4), snapshot.ReconnectedUsers)
}

func TestServerRoutesRejectMutationMethods(t *testing.T) {
	server := NewStatusServer("127.0.0.1:0", NewStatus("dev-sim-run"))
	for _, path := range []string{"/healthz", "/status"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		resp := httptest.NewRecorder()
		server.Handler().ServeHTTP(resp, req)
		if resp.Code != http.StatusMethodNotAllowed || resp.Header().Get("Allow") != http.MethodGet {
			t.Fatalf("POST %s = %d Allow=%q", path, resp.Code, resp.Header().Get("Allow"))
		}
	}
	if err := server.Close(t.Context()); err != nil {
		t.Fatalf("Close(unstarted) error = %v", err)
	}
}
