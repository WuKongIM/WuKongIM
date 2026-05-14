package worker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestWorkerInsecureControlIgnoresConfiguredToken(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret", InsecureControl: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/info", nil)

	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestWorkerAssignmentPersistenceFailureReturnsServerError(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(workDir, []byte("file blocks directory"), 0o644))
	srv := NewServer(Config{ControlToken: "secret", WorkDir: workDir})

	rec := assignRecorder(t, srv, "secret", "run-a")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	status := workerStatus(t, srv, "secret")
	require.Equal(t, PhaseIdle, status.Phase)
	require.Empty(t, status.Assignment.RunID)
}

func TestWorkerStopFromIdleReturnsConflict(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})

	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/stop", "secret", nil)

	require.Equal(t, http.StatusConflict, rec.Code)
	status := workerStatus(t, srv, "secret")
	require.Equal(t, PhaseIdle, status.Phase)
	require.Empty(t, status.Assignment.RunID)
}

func TestWorkerUnknownPathReturnsJSONNotFound(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/missing", nil)

	srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"error":"not found"}`, rec.Body.String())
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

func TestWorkerSameRunAssignmentRetryPreservesPhase(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})
	assign(t, srv, "secret", "run-a")
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)

	rec := assignRecorder(t, srv, "secret", "run-a")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	status := workerStatus(t, srv, "secret")
	require.Equal(t, PhasePrepare, status.Phase)
}

func TestWorkerSameRunDifferentAssignmentReturnsConflict(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})
	assign(t, srv, "secret", "run-a")
	body := mustJSON(t, Assignment{RunID: "run-a", WorkerID: "worker-b"})

	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/assign", "secret", body)

	require.Equal(t, http.StatusConflict, rec.Code)
	status := workerStatus(t, srv, "secret")
	require.Equal(t, "worker-a", status.Assignment.WorkerID)
}

func TestWorkerDuplicatePhasePostIsIdempotent(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})
	assign(t, srv, "secret", "run-a")
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)

	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)

	status := workerStatus(t, srv, "secret")
	require.Equal(t, PhasePrepare, status.Phase)
}

func TestWorkerStopPreservesAssignment(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret"})
	assign(t, srv, "secret", "run-a")

	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/stop", "secret", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	status := workerStatus(t, srv, "secret")
	require.Equal(t, PhaseStopped, status.Phase)
	require.Equal(t, "run-a", status.Assignment.RunID)
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

func workerStatus(t *testing.T, srv *Server, token string) Status {
	t.Helper()
	rec := authorizedRecorder(t, srv, http.MethodGet, "/v1/status", token, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var status Status
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
	return status
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
