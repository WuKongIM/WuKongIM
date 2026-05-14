package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
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

func TestWorkerDefaultRunnerConnectsAndRunsPersonShard(t *testing.T) {
	pool := newWorkerPersonClientPool()
	srv := NewServer(Config{ControlToken: "secret", WorkloadClientFactory: pool.newClient})
	assignment := Assignment{
		RunID:    "run-a",
		WorkerID: "worker-a",
		Target:   model.Target{Gateway: model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"gw-a:5100"}}}},
		Scenario: model.Scenario{
			Run:      model.RunConfig{ID: "run-a"},
			Identity: model.IdentityConfig{UIDPrefix: "bench-u", DevicePrefix: "bench-d", ClientMsgPrefix: "bench-msg"},
			Online:   model.OnlineConfig{GatewayBalance: "round_robin"},
			Messages: model.MessagesConfig{Traffic: []model.TrafficConfig{{Name: "person-send", ChannelRef: "person-a"}}},
		},
		Plan: model.WorkerPlan{WorkerID: "worker-a", Profiles: map[string]model.ProfileShard{
			"person-a": {
				Name:             "person-a",
				ChannelType:      model.ChannelTypePerson,
				ChannelRange:     model.Range{Start: 3, End: 4},
				ParticipantRange: model.Range{Start: 6, End: 8},
			},
		}},
	}
	assignFull(t, srv, "secret", assignment)

	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/connect", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/warmup", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/run", http.StatusOK)

	sender := pool.client("bench-u-6")
	recipient := pool.client("bench-u-7")
	require.Equal(t, []workerConnectCall{{uid: "bench-u-6", deviceID: "bench-d-6"}}, sender.connected)
	require.Equal(t, []workerConnectCall{{uid: "bench-u-7", deviceID: "bench-d-7"}}, recipient.connected)
	require.Len(t, sender.sentFrames, 1)
	require.Equal(t, "bench-u-7", sender.sentFrames[0].ChannelID)
	require.Equal(t, frame.ChannelTypePerson, sender.sentFrames[0].ChannelType)
	require.Contains(t, sender.sentFrames[0].ClientMsgNo, "bench-msg")
}

func TestWorkerPhaseHooksCallWorkloadRunner(t *testing.T) {
	runner := &recordingWorkloadRunner{}
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assign(t, srv, "secret", "run-a")

	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/connect", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/warmup", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/run", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/cooldown", http.StatusOK)

	require.Equal(t, []Phase{PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown}, runner.phases)
	require.Equal(t, []string{"run-a", "run-a", "run-a", "run-a"}, runner.runIDs)
}

func TestWorkerPhaseHookFailureDoesNotAdvanceStatus(t *testing.T) {
	runner := &recordingWorkloadRunner{failPhase: PhaseConnect}
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assign(t, srv, "secret", "run-a")
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)

	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/connect", "secret", nil)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, PhasePrepare, workerStatus(t, srv, "secret").Phase)
}

type recordingWorkloadRunner struct {
	phases    []Phase
	runIDs    []string
	failPhase Phase
}

func (r *recordingWorkloadRunner) Connect(ctx context.Context, assignment Assignment) error {
	return r.record(PhaseConnect, assignment)
}

func (r *recordingWorkloadRunner) Warmup(ctx context.Context, assignment Assignment) error {
	return r.record(PhaseWarmup, assignment)
}

func (r *recordingWorkloadRunner) Run(ctx context.Context, assignment Assignment) error {
	return r.record(PhaseRun, assignment)
}

func (r *recordingWorkloadRunner) Cooldown(ctx context.Context, assignment Assignment) error {
	return r.record(PhaseCooldown, assignment)
}

func (r *recordingWorkloadRunner) record(phase Phase, assignment Assignment) error {
	if phase == r.failPhase {
		return fmt.Errorf("phase %s failed", phase)
	}
	r.phases = append(r.phases, phase)
	r.runIDs = append(r.runIDs, assignment.RunID)
	return nil
}

func assignFull(t *testing.T, srv *Server, token string, assignment Assignment) {
	t.Helper()
	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/assign", token, mustJSON(t, assignment))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

type workerPersonClientPool struct {
	clients map[string]*workerPersonClient
}

func newWorkerPersonClientPool() *workerPersonClientPool {
	return &workerPersonClientPool{clients: make(map[string]*workerPersonClient)}
}

func (p *workerPersonClientPool) newClient(user benchworkload.ConnectionUser, addr string) (benchworkload.ConnectionClient, error) {
	client := &workerPersonClient{uid: user.UID, addr: addr}
	p.clients[user.UID] = client
	return client, nil
}

func (p *workerPersonClientPool) client(uid string) *workerPersonClient {
	return p.clients[uid]
}

type workerPersonClient struct {
	uid        string
	addr       string
	connected  []workerConnectCall
	sentFrames []*frame.SendPacket
	readFrames []frame.Frame
}

type workerConnectCall struct {
	uid, deviceID string
}

func (c *workerPersonClient) Connect(ctx context.Context, uid, deviceID string) error {
	c.connected = append(c.connected, workerConnectCall{uid: uid, deviceID: deviceID})
	return nil
}

func (c *workerPersonClient) Send(ctx context.Context, pkt *frame.SendPacket) error {
	cloned := *pkt
	c.sentFrames = append(c.sentFrames, &cloned)
	c.readFrames = append(c.readFrames, &frame.SendackPacket{ClientSeq: pkt.ClientSeq, ClientMsgNo: pkt.ClientMsgNo, ReasonCode: frame.ReasonSuccess})
	return nil
}

func (c *workerPersonClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	if len(c.readFrames) == 0 {
		return nil, io.EOF
	}
	f := c.readFrames[0]
	c.readFrames = c.readFrames[1:]
	return f, nil
}

func (c *workerPersonClient) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error {
	return nil
}

func (c *workerPersonClient) Close() error { return nil }

var _ benchworkload.PersonClient = (*workerPersonClient)(nil)

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
