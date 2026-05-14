package worker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkerRequiresControlToken(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/info", nil)

	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWorkerRejectsDifferentActiveRun(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})
	assign(t, srv, "secret", "run-a")

	rec := assignRecorder(t, srv, "secret", "run-b")

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestWorkerHealthzDoesNotRequireControlToken(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestWorkerRejectsV1RoutesWhenControlIsNotExplicitlyConfigured(t *testing.T) {
	srv := NewServer(Config{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/info", nil)

	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWorkerAllowsV1RoutesWhenInsecureControlIsExplicit(t *testing.T) {
	srv := NewServer(Config{InsecureControl: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/info", nil)

	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestWorkerAcceptsLegacyControlTokenHeader(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})

	rec := assignRecorderWithHeader(t, srv, "X-WKBench-Control-Token", "secret", "run-a")

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestWorkerPhaseEndpointsAdvanceStatus(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})
	assign(t, srv, "secret", "run-a")
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/connect", http.StatusOK)

	rec := authorizedRecorder(t, srv, http.MethodGet, "/v1/status", "secret", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	var status Status
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
	require.Equal(t, "run-a", status.Assignment.RunID)
	require.Equal(t, PhaseConnect, status.Phase)
}

func TestWorkerStopAllowsNewRunAssignment(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})
	assign(t, srv, "secret", "run-a")
	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/stop", "secret", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = assignRecorder(t, srv, "secret", "run-b")

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestWorkerMetricsAndReportReturnMinimalJSON(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})

	for _, path := range []string{"/v1/metrics", "/v1/report"} {
		rec := authorizedRecorder(t, srv, http.MethodGet, path, "secret", nil)
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{}`, rec.Body.String())
	}
}

func assign(t *testing.T, srv *Server, token, runID string) {
	t.Helper()
	rec := assignRecorder(t, srv, token, runID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func assignRecorder(t *testing.T, srv *Server, token, runID string) *httptest.ResponseRecorder {
	t.Helper()
	return assignRecorderWithHeader(t, srv, "Authorization", "Bearer "+token, runID)
}

func assignRecorderWithHeader(t *testing.T, srv *Server, header, value, runID string) *httptest.ResponseRecorder {
	t.Helper()
	body := mustJSON(t, Assignment{RunID: runID, WorkerID: "worker-a"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/assign", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(header, value)
	srv.ServeHTTP(rec, req)
	return rec
}

func postPhase(t *testing.T, srv *Server, token, path string, want int) {
	t.Helper()
	rec := authorizedRecorder(t, srv, http.MethodPost, path, token, nil)
	require.Equal(t, want, rec.Code, rec.Body.String())
}

func authorizedRecorder(t *testing.T, srv *Server, method, path, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	srv.ServeHTTP(rec, req)
	return rec
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
