package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/hashslot"
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
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusAccepted)
	require.Eventually(t, func() bool {
		return workerStatus(t, srv, "secret").Phase == PhasePrepare
	}, time.Second, 10*time.Millisecond)

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
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusAccepted)
	require.Eventually(t, func() bool {
		return workerStatus(t, srv, "secret").Phase == PhasePrepare
	}, time.Second, 10*time.Millisecond)

	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)

	status := workerStatus(t, srv, "secret")
	require.Equal(t, PhasePrepare, status.Phase)
}

func TestWorkerPhasePostAcceptsLongRunningHookWithoutWaiting(t *testing.T) {
	runner := newBlockingPrepareRunner()
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assign(t, srv, "secret", "run-a")
	defer runner.release()

	phaseDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		phaseDone <- authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/prepare", "secret", nil)
	}()
	require.True(t, runner.waitForCalls(1, time.Second), "prepare hook did not start")

	select {
	case rec := <-phaseDone:
		require.Contains(t, []int{http.StatusAccepted, http.StatusOK}, rec.Code, rec.Body.String())
	case <-time.After(50 * time.Millisecond):
		t.Fatal("phase POST waited for the long-running hook")
	}

	status := workerStatusMap(t, srv, "secret")
	require.Equal(t, string(PhaseAssigned), status["phase"])
	require.Equal(t, string(PhasePrepare), status["active_phase"])
	require.Equal(t, string(PhaseAssigned), status["completed_phase"])
	require.Empty(t, status["last_error"])

	runner.release()
	require.Eventually(t, func() bool {
		status := workerStatusMap(t, srv, "secret")
		return status["phase"] == string(PhasePrepare) &&
			status["completed_phase"] == string(PhasePrepare) &&
			status["active_phase"] == nil &&
			status["last_error"] == nil
	}, time.Second, 10*time.Millisecond)
}

func TestWorkerDuplicateInProgressAndCompletedPhasePostsDoNotRunHookTwice(t *testing.T) {
	runner := newBlockingPrepareRunner()
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assign(t, srv, "secret", "run-a")
	defer runner.release()

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/prepare", "secret", nil)
	}()
	require.True(t, runner.waitForCalls(1, time.Second), "prepare hook did not start")
	select {
	case rec := <-firstDone:
		require.Contains(t, []int{http.StatusAccepted, http.StatusOK}, rec.Code, rec.Body.String())
	case <-time.After(50 * time.Millisecond):
		t.Fatal("initial phase POST waited for the long-running hook")
	}

	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		secondDone <- authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/prepare", "secret", nil)
	}()
	select {
	case rec := <-secondDone:
		require.Contains(t, []int{http.StatusAccepted, http.StatusOK}, rec.Code, rec.Body.String())
	case <-time.After(50 * time.Millisecond):
		t.Fatal("duplicate in-progress phase POST waited for the hook")
	}
	require.Equal(t, int32(1), runner.calls.Load())

	runner.release()
	require.Eventually(t, func() bool {
		return workerStatus(t, srv, "secret").Phase == PhasePrepare
	}, time.Second, 10*time.Millisecond)

	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/prepare", "secret", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, int32(1), runner.calls.Load())
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

func TestWorkerMetricsAndReportExposeSnapshots(t *testing.T) {
	runner := &snapshotRunner{metrics: metrics.SnapshotData{Counters: map[string]uint64{"connect_success_total": 2}}}
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assignFull(t, srv, "secret", Assignment{RunID: "run-a", WorkerID: "worker-a"})

	metricsRec := authorizedRecorder(t, srv, http.MethodGet, "/v1/metrics", "secret", nil)
	reportRec := authorizedRecorder(t, srv, http.MethodGet, "/v1/report", "secret", nil)

	require.Equal(t, http.StatusOK, metricsRec.Code)
	require.JSONEq(t, `{"counters":{"connect_success_total":2},"gauges":{},"histograms":{},"errors":null}`, metricsRec.Body.String())
	require.Equal(t, http.StatusOK, reportRec.Code)
	var wr report.WorkerReport
	require.NoError(t, json.Unmarshal(reportRec.Body.Bytes(), &wr))
	require.Equal(t, "worker-a", wr.WorkerID)
	require.JSONEq(t, `{"run_id":"run-a","worker_id":"worker-a","phase":"assigned","metrics":{"counters":{"connect_success_total":2},"gauges":{},"histograms":{},"errors":null}}`, string(wr.Report))
}

func TestWorkerDefaultRunnerConnectsAndRunsPersonShard(t *testing.T) {
	pool := newWorkerPersonClientPool()
	srv := NewServer(Config{ControlToken: "secret", WorkloadClientFactory: pool.newClient})
	assignment := personShardAssignment()
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
	require.Equal(t, "bench-u-7@bench-u-6", sender.sentFrames[0].ChannelID)
	require.Equal(t, frame.ChannelTypePerson, sender.sentFrames[0].ChannelType)
	require.Contains(t, sender.sentFrames[0].ClientMsgNo, "bench-msg")
}

func TestWorkerDefaultRunnerRecoverTrafficReconnectsFailedSession(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient)
	assignment := personShardAssignment()

	require.NoError(t, runner.Connect(context.Background(), assignment))
	firstSender := pool.client("bench-u-6")
	firstRecipient := pool.client("bench-u-7")
	require.NotNil(t, firstSender)
	require.NotNil(t, firstRecipient)

	recovered := assignment
	recovered.Scenario.Identity.ClientMsgPrefix = "bench-msg-r1"
	recoverer := runner.(TrafficRecoverer)
	require.NoError(t, recoverer.RecoverTraffic(context.Background(), recovered, &benchworkload.SessionError{
		UID:       "bench-u-6",
		Operation: "person send",
		Err:       io.EOF,
	}))

	secondSender := pool.client("bench-u-6")
	require.NotNil(t, secondSender)
	require.NotSame(t, firstSender, secondSender)
	require.Same(t, firstRecipient, pool.client("bench-u-7"))
	require.Equal(t, 1, firstSender.closed)
	require.Equal(t, []workerConnectCall{{uid: "bench-u-6", deviceID: "bench-d-6"}}, secondSender.connected)

	require.NoError(t, runner.Run(context.Background(), recovered))
	require.Len(t, secondSender.sentFrames, 1)
	require.Contains(t, secondSender.sentFrames[0].ClientMsgNo, "bench-msg-r1")
}

func TestWorkerDefaultRunnerStartsAutoRecvAckDrain(t *testing.T) {
	pool := newWorkerPersonClientPool()
	pool.initialFrames = map[string][]frame.Frame{
		"bench-u-7": {
			&frame.RecvPacket{MessageID: 88, MessageSeq: 9, ClientMsgNo: "msg-a"},
		},
	}
	runner := NewDefaultWorkloadRunner(pool.newClient)
	assignment := personShardAssignment()
	assignment.Scenario.Messages.Traffic[0].RecvAck = true

	require.NoError(t, runner.Connect(context.Background(), assignment))
	recipient := pool.client("bench-u-7")
	require.Eventually(t, func() bool {
		return recipient.recvAckCallCount() == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, []workerRecvAckCall{{messageID: 88, messageSeq: 9}}, recipient.recvAckSnapshot())
}

func TestWorkerDefaultRunnerStartsRecvDrainWithoutRecvAck(t *testing.T) {
	pool := newWorkerPersonClientPool()
	pool.initialFrames = map[string][]frame.Frame{
		"bench-u-7": {
			&frame.RecvPacket{MessageID: 89, MessageSeq: 10, ClientMsgNo: "msg-drain"},
		},
	}
	runner := NewDefaultWorkloadRunner(pool.newClient)
	assignment := personShardAssignment()
	assignment.Scenario.Messages.Traffic[0].RecvAck = false
	assignment.Scenario.Messages.Traffic[0].Verify.Recv.Mode = "none"

	require.NoError(t, runner.Connect(context.Background(), assignment))
	recipient := pool.client("bench-u-7")
	require.Eventually(t, func() bool {
		return recipient.readFrameCount() == 0
	}, time.Second, 10*time.Millisecond)
	require.Empty(t, recipient.recvAckSnapshot())
}

func TestWorkerDefaultRunnerStartsRecvDrainOnlyForTrafficUsers(t *testing.T) {
	pool := newWorkerPersonClientPool()
	pool.initialFrames = map[string][]frame.Frame{
		"bench-u-1": {
			&frame.RecvPacket{MessageID: 90, MessageSeq: 11, ClientMsgNo: "msg-member"},
		},
		"bench-u-99": {
			&frame.RecvPacket{MessageID: 91, MessageSeq: 12, ClientMsgNo: "msg-idle"},
		},
	}
	runner := NewDefaultWorkloadRunner(pool.newClient)
	assignment := idleHeavyGroupAssignment()

	require.NoError(t, runner.Connect(context.Background(), assignment))

	member := pool.client("bench-u-1")
	require.NotNil(t, member)
	require.Eventually(t, func() bool {
		return member.readFrameCount() == 0
	}, time.Second, 10*time.Millisecond)

	idle := pool.client("bench-u-99")
	require.NotNil(t, idle)
	require.Equal(t, 1, idle.readFrameCount())
}

func TestWorkerAutoRecvAckDropsFramesWhenRecvVerificationDisabled(t *testing.T) {
	assignment := personShardAssignment()
	assignment.Scenario.Messages.Traffic[0].RecvAck = true
	assignment.Scenario.Messages.Traffic[0].Verify.Recv.Mode = "none"

	opts := autoRecvAckOptionsForAssignment(assignment)

	require.False(t, opts.BufferRecvFrames)
	require.False(t, opts.DisableRecvAck)
}

func TestWorkerAutoRecvDrainDisablesAckWhenRecvAckDisabled(t *testing.T) {
	assignment := personShardAssignment()
	assignment.Scenario.Messages.Traffic[0].RecvAck = false
	assignment.Scenario.Messages.Traffic[0].Verify.Recv.Mode = "none"

	opts := autoRecvAckOptionsForAssignment(assignment)

	require.False(t, opts.BufferRecvFrames)
	require.True(t, opts.DisableRecvAck)
	require.True(t, assignmentWantsRecvDrain(assignment))
}

func TestWorkerAutoRecvAckBuffersFramesWhenAnyTrafficVerifiesRecv(t *testing.T) {
	assignment := personShardAssignment()
	assignment.Scenario.Messages.Traffic[0].RecvAck = true
	assignment.Scenario.Messages.Traffic[0].Verify.Recv.Mode = "full"

	opts := autoRecvAckOptionsForAssignment(assignment)

	require.True(t, opts.BufferRecvFrames)
	require.False(t, opts.DisableRecvAck)
}

func TestWorkerDefaultRunnerConnectsAssignedIdentityRange(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient)
	assignment := Assignment{
		RunID:    "run-a",
		WorkerID: "worker-a",
		Target:   model.Target{Gateway: model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"gw-a:5100"}}}},
		Scenario: model.Scenario{
			Run:      model.RunConfig{ID: "run-a"},
			Identity: model.IdentityConfig{UIDPrefix: "bench-u", DevicePrefix: "bench-d"},
			Online:   model.OnlineConfig{GatewayBalance: "round_robin"},
		},
		Plan: model.WorkerPlan{
			WorkerID:      "worker-a",
			IdentityRange: model.Range{Start: 0, End: 3},
			Profiles:      map[string]model.ProfileShard{},
		},
	}

	require.NoError(t, runner.Connect(context.Background(), assignment))

	for idx := 0; idx < 3; idx++ {
		uid := fmt.Sprintf("bench-u-%d", idx)
		client := pool.client(uid)
		require.NotNil(t, client)
		require.Equal(t, []workerConnectCall{{uid: uid, deviceID: fmt.Sprintf("bench-d-%d", idx)}}, client.connected)
	}
}

func TestWorkerDefaultRunnerHoldsConnectionOnlyRunForDuration(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient)
	assignment := connectionOnlyAssignment(30 * time.Millisecond)
	require.NoError(t, runner.Connect(context.Background(), assignment))

	done := make(chan error, 1)
	go func() {
		done <- runner.Run(context.Background(), assignment)
	}()

	select {
	case err := <-done:
		t.Fatalf("connection-only run returned before duration: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	active, _ := runner.(ConnectionStatusReporter).ConnectionStatus()
	require.Equal(t, 3, active)
	require.NoError(t, <-done)
}

func TestWorkerDefaultRunnerHoldsConnectionOnlyCooldownBeforeClose(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient)
	assignment := connectionOnlyAssignment(30 * time.Millisecond)
	assignment.Scenario.Run.Cooldown = assignment.Scenario.Run.Duration
	require.NoError(t, runner.Connect(context.Background(), assignment))

	done := make(chan error, 1)
	go func() {
		done <- runner.Cooldown(context.Background(), assignment)
	}()

	select {
	case err := <-done:
		t.Fatalf("connection-only cooldown returned before duration: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	active, _ := runner.(ConnectionStatusReporter).ConnectionStatus()
	require.Equal(t, 3, active)
	require.NoError(t, <-done)
	active, _ = runner.(ConnectionStatusReporter).ConnectionStatus()
	require.Zero(t, active)
}

func TestWorkerDefaultRunnerMetricsIncludeConnectionOnlyHeartbeatActivity(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient)
	assignment := connectionOnlyAssignment(30 * time.Millisecond)
	assignment.Scenario.Run.Cooldown = 20 * time.Millisecond
	assignment.Scenario.Online.Heartbeat = model.HeartbeatConfig{
		Enabled:  true,
		Interval: 5 * time.Millisecond,
		Timeout:  20 * time.Millisecond,
	}
	require.NoError(t, runner.Connect(context.Background(), assignment))

	require.NoError(t, runner.Run(context.Background(), assignment))
	runSnap := runner.(MetricsReporter).MetricsSnapshot()
	require.GreaterOrEqual(t, runSnap.Counters["heartbeat_success_total"], uint64(1))

	require.NoError(t, runner.Cooldown(context.Background(), assignment))
	finalSnap := runner.(MetricsReporter).MetricsSnapshot()
	require.GreaterOrEqual(t, finalSnap.Counters["heartbeat_success_total"], runSnap.Counters["heartbeat_success_total"])
}

func TestWorkerDefaultRunnerMetricsSurviveCooldown(t *testing.T) {
	pool := newWorkerPersonClientPool()
	srv := NewServer(Config{ControlToken: "secret", WorkloadClientFactory: pool.newClient})
	assignFull(t, srv, "secret", personShardAssignment())
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/connect", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/warmup", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/run", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/cooldown", http.StatusOK)

	rec := authorizedRecorder(t, srv, http.MethodGet, "/v1/metrics", "secret", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	var snap metrics.SnapshotData
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &snap))
	require.Equal(t, uint64(1), snap.Counters["person_send_success_total{channel_type=person,phase=run,profile=person-a,traffic=person-send}"])
}

func TestWorkerDefaultRunnerNewRunResetsConnectMetricsAfterStop(t *testing.T) {
	pool := newWorkerPersonClientPool()
	srv := NewServer(Config{ControlToken: "secret", WorkloadClientFactory: pool.newClient})
	assignment := personShardAssignment()
	assignFull(t, srv, "secret", assignment)
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/connect", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/stop", http.StatusOK)

	assignment.RunID = "run-b"
	assignment.Scenario.Run.ID = "run-b"
	assignment.Plan.Profiles["person-a"] = model.ProfileShard{
		Name:             "person-a",
		ChannelType:      model.ChannelTypePerson,
		ChannelRange:     model.Range{Start: 4, End: 5},
		ParticipantRange: model.Range{Start: 8, End: 10},
	}
	assignFull(t, srv, "secret", assignment)

	rec := authorizedRecorder(t, srv, http.MethodGet, "/v1/metrics", "secret", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	var snap metrics.SnapshotData
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &snap))
	require.Zero(t, snap.Counters["connect_attempt_total"])
	require.Zero(t, snap.Counters["connect_success_total"])
}

func TestWorkerDefaultRunnerRunsStoredWorkloadsConcurrently(t *testing.T) {
	runner := &defaultWorkloadRunner{
		runID:           "run-a",
		metrics:         metrics.NewRegistry(),
		personWorkloads: []*benchworkload.PersonWorkload{nil, nil},
	}
	var calls atomic.Int32
	bothStarted := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- runner.runPhase(context.Background(), Assignment{RunID: "run-a"}, func(context.Context, *benchworkload.PersonWorkload, *benchworkload.GroupWorkload) error {
			if calls.Add(1) == 2 {
				close(bothStarted)
			}
			<-release
			return nil
		})
	}()

	select {
	case <-bothStarted:
	case <-time.After(50 * time.Millisecond):
		close(release)
		t.Fatal("stored workloads did not start concurrently")
	}
	close(release)
	require.NoError(t, <-done)
}

func TestWorkerDefaultRunnerRunsPersonAndGroupTrafficConcurrently(t *testing.T) {
	runner := &defaultWorkloadRunner{
		runID:           "run-a",
		metrics:         metrics.NewRegistry(),
		personWorkloads: []*benchworkload.PersonWorkload{{}},
		groupWorkloads:  []*benchworkload.GroupWorkload{{}},
	}
	var calls atomic.Int32
	bothStarted := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- runner.runPhase(context.Background(), Assignment{RunID: "run-a"}, func(context.Context, *benchworkload.PersonWorkload, *benchworkload.GroupWorkload) error {
			if calls.Add(1) == 2 {
				close(bothStarted)
			}
			<-release
			return nil
		})
	}()

	select {
	case <-bothStarted:
	case <-time.After(50 * time.Millisecond):
		close(release)
		t.Fatal("person and group workloads did not start concurrently")
	}
	close(release)
	require.NoError(t, <-done)
}

func TestWorkerDefaultRunnerCancelsOtherWorkloadsOnPhaseError(t *testing.T) {
	runner := &defaultWorkloadRunner{
		runID:           "run-a",
		metrics:         metrics.NewRegistry(),
		personWorkloads: []*benchworkload.PersonWorkload{{}},
		groupWorkloads:  []*benchworkload.GroupWorkload{{}},
	}
	phaseErr := errors.New("person failed")
	groupCanceled := make(chan struct{})

	err := runner.runPhase(context.Background(), Assignment{RunID: "run-a"}, func(ctx context.Context, person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
		if person != nil {
			return phaseErr
		}
		<-ctx.Done()
		close(groupCanceled)
		return ctx.Err()
	})

	require.ErrorIs(t, err, phaseErr)
	require.Eventually(t, func() bool {
		select {
		case <-groupCanceled:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestWorkerDefaultRunnerPreparesConnectsAndRunsGroupShard(t *testing.T) {
	type seenRequest struct {
		path  string
		batch string
	}
	seen := make([]seenRequest, 0)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/channels":
			var req model.BatchChannelsRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			seen = append(seen, seenRequest{path: r.URL.Path, batch: req.BatchID})
			require.Equal(t, []model.ChannelItem{{ChannelID: "bench-run-huge-group-0", ChannelType: uint8(frame.ChannelTypeGroup), Large: true}}, req.Channels)
		case "/bench/v1/channels/subscribers":
			var req model.BatchSubscribersRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			seen = append(seen, seenRequest{path: r.URL.Path, batch: req.BatchID})
			require.Len(t, req.Items, 1)
			require.False(t, req.Items[0].Reset)
			require.LessOrEqual(t, len(req.Items[0].Subscribers), 2)
		default:
			t.Fatalf("unexpected target path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	pool := newWorkerPersonClientPool()
	srv := NewServer(Config{ControlToken: "secret", WorkloadClientFactory: pool.newClient})
	assignment := groupShardAssignment(target.URL)
	assignFull(t, srv, "secret", assignment)

	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/connect", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/warmup", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/run", http.StatusOK)

	require.Equal(t, []seenRequest{
		{path: "/bench/v1/channels", batch: "bench-run-channels-huge-group-worker-a-0-1"},
		{path: "/bench/v1/channels/subscribers", batch: "bench-run-subs-huge-group-worker-a-0-2"},
		{path: "/bench/v1/channels/subscribers", batch: "bench-run-subs-huge-group-worker-a-2-4"},
	}, seen)
	sender := pool.client("bench-u-0")
	require.NotNil(t, sender)
	require.Equal(t, []workerConnectCall{{uid: "bench-u-0", deviceID: "bench-d-0"}}, sender.connected)
	require.Len(t, sender.sentFrames, 1)
	require.Equal(t, "bench-run-huge-group-0", sender.sentFrames[0].ChannelID)
	require.Equal(t, frame.ChannelTypeGroup, sender.sentFrames[0].ChannelType)
	require.Contains(t, sender.sentFrames[0].ClientMsgNo, "bench-msg")
}

func TestWorkerHTTPUsesSamePhysicalHashSlotChannelsForPrepareAndTraffic(t *testing.T) {
	prepared := make([]string, 0, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/channels":
			var req model.BatchChannelsRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			for _, channel := range req.Channels {
				prepared = append(prepared, channel.ChannelID)
			}
		case "/bench/v1/channels/subscribers":
		default:
			t.Fatalf("unexpected target path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	assignment := groupShardAssignment(target.URL)
	assignment.Scenario.Identity.TotalUsers = 2
	assignment.Scenario.Online.TotalUsers = 2
	assignment.Scenario.Channels.Profiles[0] = model.ChannelProfile{
		Name: "max-group", ChannelType: model.ChannelTypeGroup, Count: 2,
		Members: model.MembersConfig{Count: 1, Overlap: "allowed"},
		Online:  model.ChannelOnlineConfig{MemberRatio: 1},
		Shard:   model.ShardConfig{Mode: "hash", HashSlotSpread: true, HashSlotCount: 2},
		Prepare: model.ChannelPrepareConfig{SubscribersBatchSize: 2},
	}
	assignment.Scenario.Messages.Traffic[0].ChannelRef = "max-group"
	assignment.Plan.IdentityRange = model.Range{Start: 0, End: 2}
	assignment.Plan.Profiles = map[string]model.ProfileShard{"max-group": {
		Name: "max-group", ChannelType: model.ChannelTypeGroup,
		ChannelRange: model.Range{Start: 0, End: 2}, MemberRange: model.Range{Start: 0, End: 2}, MemberReusePolicy: "allowed",
	}}

	pool := newWorkerPersonClientPool()
	srv := NewServer(Config{ControlToken: "secret", WorkloadClientFactory: pool.newClient})
	assignFull(t, srv, "secret", assignment)
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/connect", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/warmup", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/run", http.StatusOK)

	require.Len(t, prepared, 2)
	trafficChannels := make(map[string]struct{}, 2)
	for _, client := range pool.clients {
		for _, sent := range client.sentFrames {
			trafficChannels[sent.ChannelID] = struct{}{}
		}
	}
	for slot, channelID := range prepared {
		require.Equal(t, uint16(slot), hashslot.HashSlotForKey(channelID, 2))
		_, usedByTraffic := trafficChannels[channelID]
		require.True(t, usedByTraffic, "prepared channel %q was not used by traffic", channelID)
	}
}

func TestBuildGroupWorkloadsUsesTrafficRatePerStream(t *testing.T) {
	assignment := groupShardAssignment("http://target.invalid")
	assignment.Scenario.Run.Duration = time.Second
	assignment.Scenario.Messages.Traffic = []model.TrafficConfig{
		{Name: "slow", ChannelRef: "huge-group", RatePerChannel: model.Rate{PerSecond: 1}},
		{Name: "fast", ChannelRef: "huge-group", RatePerChannel: model.Rate{PerSecond: 3}},
	}
	assignment.Plan.Profiles["huge-group"] = model.ProfileShard{
		Name:                   "huge-group",
		ChannelType:            model.ChannelTypeGroup,
		ChannelRange:           model.Range{Start: 0, End: 1},
		MemberRange:            model.Range{Start: 0, End: 4},
		GlobalRate:             model.Rate{PerSecond: 4},
		LocalRate:              model.Rate{PerSecond: 4},
		TrafficPartitionCount:  4,
		OwnedTrafficPartitions: []int{0, 1},
	}
	clients := map[string]benchworkload.PersonClient{
		"bench-u-0": &workerPersonClient{},
		"bench-u-1": &workerPersonClient{},
		"bench-u-2": &workerPersonClient{},
		"bench-u-3": &workerPersonClient{},
	}

	workloads, err := buildGroupWorkloads(assignment, groupBundlesForTest(t, assignment), clients)
	require.NoError(t, err)

	for _, wl := range workloads {
		require.NoError(t, wl.Run(context.Background()))
	}
	require.Len(t, clients["bench-u-0"].(*workerPersonClient).sentFrames, 2)
}

func TestBuildGroupWorkloadsAppliesTrafficAckTimeout(t *testing.T) {
	assignment := groupShardAssignment("http://target.invalid")
	assignment.Scenario.Run.Duration = time.Second
	assignment.Scenario.Messages.Traffic = []model.TrafficConfig{
		{Name: "group-send", ChannelRef: "huge-group", RatePerChannel: model.Rate{PerSecond: 1}, AckTimeout: 25 * time.Millisecond},
	}
	clients := map[string]benchworkload.PersonClient{
		"bench-u-0": &deadlineObservingPersonClient{observed: make(chan time.Duration, 1)},
		"bench-u-1": &workerPersonClient{},
		"bench-u-2": &workerPersonClient{},
		"bench-u-3": &workerPersonClient{},
	}

	workloads, err := buildGroupWorkloads(assignment, groupBundlesForTest(t, assignment), clients)
	require.NoError(t, err)

	require.Error(t, workloads[0].Run(context.Background()))
	observed := <-clients["bench-u-0"].(*deadlineObservingPersonClient).observed
	require.Less(t, observed, 100*time.Millisecond)
}

func TestConnectionAckTimeoutUsesLargestTrafficWindow(t *testing.T) {
	assignment := groupShardAssignment("http://target.invalid")
	assignment.Scenario.Run.Warmup = 20 * time.Second
	assignment.Scenario.Messages.Traffic = []model.TrafficConfig{
		{Name: "default", ChannelRef: "huge-group"},
		{Name: "slow", ChannelRef: "huge-group", AckTimeout: 15 * time.Second},
	}

	timeout := connectionAckTimeout(assignment)
	require.Greater(t, timeout, 20*time.Second)
}

func TestWorkerDefaultRunnerUsesChannelOwnersForHugeGroupPrepare(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/channels":
			var req model.BatchChannelsRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.Len(t, req.Channels, 1)
			require.Equal(t, "bench-run-huge-group-0", req.Channels[0].ChannelID)
		case "/bench/v1/channels/subscribers":
			var req model.BatchSubscribersRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		default:
			t.Fatalf("unexpected target path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	srv := NewServer(Config{ControlToken: "secret"})
	assignment := groupShardAssignment(target.URL)
	assignment.Plan.Profiles["huge-group"] = model.ProfileShard{
		Name:                   "huge-group",
		ChannelType:            model.ChannelTypeGroup,
		ChannelRange:           model.Range{Start: 0, End: 1},
		MemberRange:            model.Range{Start: 2500, End: 5000},
		TrafficPartitionCount:  4,
		OwnedTrafficPartitions: []int{0},
	}
	assignment.ChannelOwners = map[string]map[int]string{"huge-group": {0: "worker-a"}}
	assignFull(t, srv, "secret", assignment)

	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)

	status := workerStatus(t, srv, "secret")
	require.Equal(t, PhasePrepare, status.Phase)
}

func TestWorkerDefaultRunnerSkipsHugeGroupPrepareWhenAnotherWorkerOwnsChannel(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/channels":
			t.Fatalf("unexpected channel prepare request for non-owner")
		case "/bench/v1/channels/subscribers":
			var req model.BatchSubscribersRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.Len(t, req.Items, 1)
			require.False(t, req.Items[0].Reset)
		default:
			t.Fatalf("unexpected target path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	srv := NewServer(Config{ControlToken: "secret"})
	assignment := groupShardAssignment(target.URL)
	assignment.Plan.Profiles["huge-group"] = model.ProfileShard{
		Name:                   "huge-group",
		ChannelType:            model.ChannelTypeGroup,
		ChannelRange:           model.Range{Start: 0, End: 1},
		MemberRange:            model.Range{Start: 0, End: 2500},
		TrafficPartitionCount:  4,
		OwnedTrafficPartitions: []int{0},
	}
	assignment.ChannelOwners = map[string]map[int]string{"huge-group": {0: "worker-b"}}
	assignFull(t, srv, "secret", assignment)

	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)

	status := workerStatus(t, srv, "secret")
	require.Equal(t, PhasePrepare, status.Phase)
}

func TestWorkerDefaultRunnerRejectsPersonShardWithoutTraffic(t *testing.T) {
	pool := newWorkerPersonClientPool()
	srv := NewServer(Config{ControlToken: "secret", WorkloadClientFactory: pool.newClient})
	assignment := personShardAssignment()
	assignment.Scenario.Messages.Traffic = nil
	assignFull(t, srv, "secret", assignment)
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)

	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/connect", "secret", nil)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), "no matching traffic")
	require.Equal(t, PhasePrepare, workerStatus(t, srv, "secret").Phase)
}

func TestWorkerConcurrentDuplicatePhaseRunsHookOnce(t *testing.T) {
	runner := newBlockingConnectRunner()
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assign(t, srv, "secret", "run-a")
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)

	var wg sync.WaitGroup
	recorders := make([]*httptest.ResponseRecorder, 2)
	wg.Add(2)
	for i := range recorders {
		go func(idx int) {
			defer wg.Done()
			recorders[idx] = authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/connect", "secret", nil)
		}(i)
	}

	require.True(t, runner.waitForCalls(1, time.Second), "first hook call did not start")
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(1), runner.calls.Load())
	runner.release()
	wg.Wait()

	for _, rec := range recorders {
		require.Contains(t, []int{http.StatusAccepted, http.StatusOK}, rec.Code, rec.Body.String())
	}
	require.Equal(t, int32(1), runner.calls.Load())
	require.Eventually(t, func() bool {
		return workerStatus(t, srv, "secret").Phase == PhaseConnect
	}, time.Second, 10*time.Millisecond)
}

func TestWorkerStopCancelsActiveAsyncPhase(t *testing.T) {
	runner := newBlockingPrepareRunner()
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assign(t, srv, "secret", "run-a")

	phaseDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		phaseDone <- authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/prepare", "secret", nil)
	}()
	require.True(t, runner.waitForCalls(1, time.Second), "prepare hook did not start")
	select {
	case rec := <-phaseDone:
		require.Contains(t, []int{http.StatusAccepted, http.StatusOK}, rec.Code, rec.Body.String())
	case <-time.After(50 * time.Millisecond):
		t.Fatal("phase request did not return promptly")
	}

	postPhase(t, srv, "secret", "/v1/stop", http.StatusOK)

	require.True(t, runner.waitCanceled(time.Second), "stop did not cancel active phase context")
	status := workerStatus(t, srv, "secret")
	require.Equal(t, PhaseStopped, status.Phase)
	require.Empty(t, status.ActivePhase)
	require.Empty(t, status.LastError)
}

func TestWorkerOldPhaseHookDoesNotAdvanceNewAssignment(t *testing.T) {
	runner := newBlockingPrepareRunner()
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assign(t, srv, "secret", "run-a")
	defer runner.release()

	phaseDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		phaseDone <- authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/prepare", "secret", nil)
	}()
	require.True(t, runner.waitForCalls(1, time.Second), "prepare hook did not start")
	select {
	case rec := <-phaseDone:
		require.Contains(t, []int{http.StatusAccepted, http.StatusOK}, rec.Code, rec.Body.String())
	case <-time.After(50 * time.Millisecond):
		t.Fatal("old phase request did not return promptly")
	}

	postPhase(t, srv, "secret", "/v1/stop", http.StatusOK)
	rec := assignRecorder(t, srv, "secret", "run-b")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	runner.release()
	require.Eventually(t, func() bool {
		status := workerStatus(t, srv, "secret")
		return status.Assignment.RunID == "run-b" && status.Phase == PhaseAssigned
	}, time.Second, 10*time.Millisecond)
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

	require.Equal(t, []Phase{PhasePrepare, PhaseConnect, PhaseWarmup, PhaseRun, PhaseCooldown}, runner.phases)
	require.Equal(t, []string{"run-a", "run-a", "run-a", "run-a", "run-a"}, runner.runIDs)
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

func TestWorkerAsyncPhaseHookFailureIsVisibleInStatus(t *testing.T) {
	runner := newBlockingConnectRunner()
	runner.err = fmt.Errorf("phase connect failed")
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assign(t, srv, "secret", "run-a")
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	require.Eventually(t, func() bool {
		return workerStatus(t, srv, "secret").CompletedPhase == PhasePrepare
	}, time.Second, 10*time.Millisecond)
	defer runner.release()

	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/connect", "secret", nil)

	require.Contains(t, []int{http.StatusAccepted, http.StatusOK}, rec.Code, rec.Body.String())
	require.True(t, runner.waitForCalls(1, time.Second), "connect hook did not start")
	runner.release()
	require.Eventually(t, func() bool {
		status := workerStatusMap(t, srv, "secret")
		lastError, _ := status["last_error"].(string)
		return status["phase"] == string(PhasePrepare) &&
			status["completed_phase"] == string(PhasePrepare) &&
			status["active_phase"] == nil &&
			strings.Contains(lastError, "phase connect failed")
	}, time.Second, 10*time.Millisecond)
}

func TestWorkerTargetUnavailableHookFailureReturnsServiceUnavailable(t *testing.T) {
	runner := &recordingWorkloadRunner{failPhase: PhaseConnect, failErr: fmt.Errorf("%w: dial tcp", errTargetUnavailable)}
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assign(t, srv, "secret", "run-a")
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)

	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/connect", "secret", nil)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "target unavailable")
	require.Equal(t, PhasePrepare, workerStatus(t, srv, "secret").Phase)
}

type recordingWorkloadRunner struct {
	phases    []Phase
	runIDs    []string
	failPhase Phase
	failErr   error
}

func (r *recordingWorkloadRunner) Prepare(ctx context.Context, assignment Assignment) error {
	return r.record(PhasePrepare, assignment)
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
		if r.failErr != nil {
			return r.failErr
		}
		return fmt.Errorf("phase %s failed", phase)
	}
	r.phases = append(r.phases, phase)
	r.runIDs = append(r.runIDs, assignment.RunID)
	return nil
}

func personShardAssignment() Assignment {
	return Assignment{
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
}

func connectionOnlyAssignment(duration time.Duration) Assignment {
	return Assignment{
		RunID:    "run-a",
		WorkerID: "worker-a",
		Target:   model.Target{Gateway: model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"gw-a:5100"}}}},
		Scenario: model.Scenario{
			Run:      model.RunConfig{ID: "run-a", Duration: duration},
			Identity: model.IdentityConfig{UIDPrefix: "bench-u", DevicePrefix: "bench-d"},
			Online:   model.OnlineConfig{GatewayBalance: "round_robin"},
		},
		Plan: model.WorkerPlan{
			WorkerID:      "worker-a",
			IdentityRange: model.Range{Start: 0, End: 3},
			Profiles:      map[string]model.ProfileShard{},
		},
	}
}

func idleHeavyGroupAssignment() Assignment {
	return Assignment{
		RunID:    "run-a",
		WorkerID: "worker-a",
		Target:   model.Target{Gateway: model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"gw-a:5100"}}}},
		Scenario: model.Scenario{
			Run:      model.RunConfig{ID: "run-a"},
			Identity: model.IdentityConfig{UIDPrefix: "bench-u", DevicePrefix: "bench-d", ClientMsgPrefix: "bench-msg"},
			Online:   model.OnlineConfig{GatewayBalance: "round_robin"},
			Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
				Name:        "group-a",
				ChannelType: model.ChannelTypeGroup,
				Count:       1,
				Members:     model.MembersConfig{Count: 2, Overlap: "disallowed"},
				Online:      model.ChannelOnlineConfig{MemberRatio: 1},
			}}},
			Messages: model.MessagesConfig{Traffic: []model.TrafficConfig{{
				Name:       "group-send",
				ChannelRef: "group-a",
				RecvAck:    false,
				Verify:     model.VerifyConfig{Recv: model.RecvVerifyConfig{Mode: "none"}},
			}}},
		},
		Plan: model.WorkerPlan{
			WorkerID:      "worker-a",
			IdentityRange: model.Range{Start: 0, End: 100},
			Profiles: map[string]model.ProfileShard{
				"group-a": {
					Name:              "group-a",
					ChannelType:       model.ChannelTypeGroup,
					ChannelRange:      model.Range{Start: 0, End: 1},
					MemberRange:       model.Range{Start: 0, End: 2},
					MemberReusePolicy: "disallowed",
				},
			},
		},
	}
}

func groupBundlesForTest(t *testing.T, assignment Assignment) []groupWorkloadBundle {
	t.Helper()
	plan, err := buildGroupExecutionPlan(assignment)
	require.NoError(t, err)
	return plan.bundles
}

func groupShardAssignment(targetURL string) Assignment {
	return Assignment{
		RunID:    "bench-run",
		WorkerID: "worker-a",
		Target: model.Target{
			BenchAPI: model.BenchAPIConfig{Addrs: []string{targetURL}},
			Gateway:  model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"gw-a:5100"}}},
		},
		Scenario: model.Scenario{
			Run:      model.RunConfig{ID: "bench-run"},
			Identity: model.IdentityConfig{UIDPrefix: "bench-u", DevicePrefix: "bench-d", ClientMsgPrefix: "bench-msg"},
			Online:   model.OnlineConfig{GatewayBalance: "round_robin"},
			Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
				Name:        "huge-group",
				ChannelType: model.ChannelTypeGroup,
				Count:       1,
				Members:     model.MembersConfig{Count: 4},
				Shard:       model.ShardConfig{Mode: model.ShardModeSplitMembersAndTraffic},
				Prepare:     model.ChannelPrepareConfig{SubscribersBatchSize: 2},
			}}},
			Messages: model.MessagesConfig{Traffic: []model.TrafficConfig{{Name: "group-send", ChannelRef: "huge-group"}}},
		},
		Plan: model.WorkerPlan{WorkerID: "worker-a", Profiles: map[string]model.ProfileShard{
			"huge-group": {
				Name:                   "huge-group",
				ChannelType:            model.ChannelTypeGroup,
				ChannelRange:           model.Range{Start: 0, End: 1},
				MemberRange:            model.Range{Start: 0, End: 4},
				TrafficPartitionCount:  4,
				OwnedTrafficPartitions: []int{0},
			},
		}},
	}
}

type blockingPrepareRunner struct {
	calls      atomic.Int32
	entered    chan struct{}
	done       chan struct{}
	canceled   chan struct{}
	once       sync.Once
	cancelOnce sync.Once
}

func newBlockingPrepareRunner() *blockingPrepareRunner {
	return &blockingPrepareRunner{entered: make(chan struct{}, 2), done: make(chan struct{}), canceled: make(chan struct{})}
}

func (r *blockingPrepareRunner) Prepare(ctx context.Context, assignment Assignment) error {
	r.calls.Add(1)
	r.entered <- struct{}{}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		r.cancelOnce.Do(func() { close(r.canceled) })
		return ctx.Err()
	}
}

func (r *blockingPrepareRunner) Connect(ctx context.Context, assignment Assignment) error { return nil }
func (r *blockingPrepareRunner) Warmup(ctx context.Context, assignment Assignment) error  { return nil }
func (r *blockingPrepareRunner) Run(ctx context.Context, assignment Assignment) error     { return nil }
func (r *blockingPrepareRunner) Cooldown(ctx context.Context, assignment Assignment) error {
	return nil
}

func (r *blockingPrepareRunner) waitForCalls(want int32, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for r.calls.Load() < want {
		select {
		case <-r.entered:
		case <-deadline:
			return false
		}
	}
	return true
}

func (r *blockingPrepareRunner) release() {
	r.once.Do(func() { close(r.done) })
}

func (r *blockingPrepareRunner) waitCanceled(timeout time.Duration) bool {
	select {
	case <-r.canceled:
		return true
	case <-time.After(timeout):
		return false
	}
}

type blockingConnectRunner struct {
	calls   atomic.Int32
	entered chan struct{}
	done    chan struct{}
	err     error
	once    sync.Once
}

func newBlockingConnectRunner() *blockingConnectRunner {
	return &blockingConnectRunner{entered: make(chan struct{}, 2), done: make(chan struct{})}
}

func (r *blockingConnectRunner) Prepare(ctx context.Context, assignment Assignment) error { return nil }

func (r *blockingConnectRunner) Connect(ctx context.Context, assignment Assignment) error {
	r.calls.Add(1)
	r.entered <- struct{}{}
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *blockingConnectRunner) Warmup(ctx context.Context, assignment Assignment) error { return nil }
func (r *blockingConnectRunner) Run(ctx context.Context, assignment Assignment) error    { return nil }
func (r *blockingConnectRunner) Cooldown(ctx context.Context, assignment Assignment) error {
	return nil
}

func (r *blockingConnectRunner) waitForCalls(want int32, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for r.calls.Load() < want {
		select {
		case <-r.entered:
		case <-deadline:
			return false
		}
	}
	return true
}

func (r *blockingConnectRunner) release() {
	r.once.Do(func() { close(r.done) })
}

func assignFull(t *testing.T, srv *Server, token string, assignment Assignment) {
	t.Helper()
	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/assign", token, mustJSON(t, assignment))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

type workerPersonClientPool struct {
	clients       map[string]*workerPersonClient
	initialFrames map[string][]frame.Frame
	createdUIDs   []string
}

func newWorkerPersonClientPool() *workerPersonClientPool {
	return &workerPersonClientPool{clients: make(map[string]*workerPersonClient)}
}

func (p *workerPersonClientPool) newClient(user benchworkload.ConnectionUser, addr string) (benchworkload.ConnectionClient, error) {
	client := &workerPersonClient{uid: user.UID, addr: addr, readFrames: append([]frame.Frame(nil), p.initialFrames[user.UID]...)}
	p.clients[user.UID] = client
	p.createdUIDs = append(p.createdUIDs, user.UID)
	return client, nil
}

func (p *workerPersonClientPool) createdCount(uid string) int {
	count := 0
	for _, createdUID := range p.createdUIDs {
		if createdUID == uid {
			count++
		}
	}
	return count
}

func (p *workerPersonClientPool) client(uid string) *workerPersonClient {
	return p.clients[uid]
}

type workerPersonClient struct {
	mu           sync.Mutex
	uid          string
	addr         string
	connected    []workerConnectCall
	closed       int
	sentFrames   []*frame.SendPacket
	readFrames   []frame.Frame
	recvAckCalls []workerRecvAckCall
	notify       chan struct{}
	pingCalls    atomic.Int32
}

type workerConnectCall struct {
	uid, deviceID string
}

type workerRecvAckCall struct {
	messageID  int64
	messageSeq uint64
}

type deadlineObservingPersonClient struct {
	workerPersonClient
	observed chan time.Duration
}

func (c *deadlineObservingPersonClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	deadline, ok := ctx.Deadline()
	if ok {
		c.observed <- time.Until(deadline)
	} else {
		c.observed <- time.Hour
	}
	return nil, context.DeadlineExceeded
}

func (c *workerPersonClient) Connect(ctx context.Context, uid, deviceID string) error {
	c.connected = append(c.connected, workerConnectCall{uid: uid, deviceID: deviceID})
	return nil
}

func (c *workerPersonClient) Send(ctx context.Context, pkt *frame.SendPacket) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cloned := *pkt
	c.sentFrames = append(c.sentFrames, &cloned)
	c.readFrames = append(c.readFrames, &frame.SendackPacket{ClientSeq: pkt.ClientSeq, ClientMsgNo: pkt.ClientMsgNo, ReasonCode: frame.ReasonSuccess})
	c.signalLocked()
	return nil
}

func (c *workerPersonClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	for {
		c.mu.Lock()
		if len(c.readFrames) > 0 {
			f := c.readFrames[0]
			c.readFrames = c.readFrames[1:]
			c.mu.Unlock()
			return f, nil
		}
		if c.closed > 0 {
			c.mu.Unlock()
			return nil, io.EOF
		}
		notify := c.notifyLocked()
		c.mu.Unlock()
		select {
		case <-notify:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *workerPersonClient) readFrameCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.readFrames)
}

func (c *workerPersonClient) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recvAckCalls = append(c.recvAckCalls, workerRecvAckCall{messageID: messageID, messageSeq: messageSeq})
	return nil
}

func (c *workerPersonClient) recvAckCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.recvAckCalls)
}

func (c *workerPersonClient) recvAckSnapshot() []workerRecvAckCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]workerRecvAckCall(nil), c.recvAckCalls...)
}

func (c *workerPersonClient) Ping(ctx context.Context) error {
	c.pingCalls.Add(1)
	return nil
}

func (c *workerPersonClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
	c.signalLocked()
	return nil
}

func (c *workerPersonClient) notifyLocked() <-chan struct{} {
	if c.notify == nil {
		c.notify = make(chan struct{})
	}
	return c.notify
}

func (c *workerPersonClient) signalLocked() {
	if c.notify == nil {
		return
	}
	close(c.notify)
	c.notify = make(chan struct{})
}

var _ benchworkload.PersonClient = (*workerPersonClient)(nil)

type snapshotRunner struct {
	metrics metrics.SnapshotData
}

func (r *snapshotRunner) Prepare(context.Context, Assignment) error  { return nil }
func (r *snapshotRunner) Connect(context.Context, Assignment) error  { return nil }
func (r *snapshotRunner) Warmup(context.Context, Assignment) error   { return nil }
func (r *snapshotRunner) Run(context.Context, Assignment) error      { return nil }
func (r *snapshotRunner) Cooldown(context.Context, Assignment) error { return nil }
func (r *snapshotRunner) MetricsSnapshot() metrics.SnapshotData      { return r.metrics }

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
	if strings.HasPrefix(path, "/v1/phase/") && (want == http.StatusOK || want == http.StatusAccepted) {
		require.Contains(t, []int{http.StatusOK, http.StatusAccepted}, rec.Code, rec.Body.String())
		if rec.Code == http.StatusAccepted {
			phase := Phase(strings.TrimPrefix(path, "/v1/phase/"))
			require.Eventually(t, func() bool {
				status := workerStatus(t, srv, token)
				return status.CompletedPhase == phase && status.ActivePhase == "" && status.LastError == ""
			}, time.Second, 10*time.Millisecond)
		}
		return
	}
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

func workerStatusMap(t *testing.T, srv *Server, token string) map[string]any {
	t.Helper()
	rec := authorizedRecorder(t, srv, http.MethodGet, "/v1/status", token, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var status map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &status))
	return status
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

func TestWorkerDefaultRunnerPreparesBenchAPITokensBeforeConnect(t *testing.T) {
	type seenTokenRequest struct {
		batch string
		uids  []string
	}
	seen := make([]seenTokenRequest, 0)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/users/tokens":
			var req model.BatchTokensRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			uids := make([]string, 0, len(req.Users))
			for _, user := range req.Users {
				uids = append(uids, user.UID)
				require.Equal(t, "bench-token-"+user.UID, user.Token)
			}
			seen = append(seen, seenTokenRequest{batch: req.BatchID, uids: uids})
		default:
			t.Fatalf("unexpected target path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	pool := newWorkerPersonClientPool()
	srv := NewServer(Config{ControlToken: "secret", WorkloadClientFactory: pool.newClient})
	assignment := personShardAssignment()
	assignment.Target.BenchAPI.Addrs = []string{target.URL}
	assignment.Scenario.Identity.Token.Mode = "bench_api"
	assignFull(t, srv, "secret", assignment)

	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/connect", http.StatusOK)

	require.Equal(t, []seenTokenRequest{{batch: "run-a-tokens-worker-a-0-2", uids: []string{"bench-u-6", "bench-u-7"}}}, seen)
	require.NotNil(t, pool.client("bench-u-6"))
}

func TestWorkerDefaultRunnerUsesPlannedMemberRangeForSmallGroups(t *testing.T) {
	type seenSubscribers struct {
		channelID string
		uids      []string
	}
	seen := make([]seenSubscribers, 0)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/channels":
			var req model.BatchChannelsRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		case "/bench/v1/channels/subscribers":
			var req model.BatchSubscribersRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			for _, item := range req.Items {
				seen = append(seen, seenSubscribers{channelID: item.ChannelID, uids: item.Subscribers})
			}
		default:
			t.Fatalf("unexpected target path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	srv := NewServer(Config{ControlToken: "secret"})
	assignment := groupShardAssignment(target.URL)
	assignment.Scenario.Channels.Profiles[0].Shard.Mode = "hash"
	assignment.Scenario.Channels.Profiles[0].Count = 2
	assignment.Plan.Profiles["huge-group"] = model.ProfileShard{
		Name:         "huge-group",
		ChannelType:  model.ChannelTypeGroup,
		ChannelRange: model.Range{Start: 1, End: 2},
		MemberRange:  model.Range{Start: 5000, End: 5004},
	}
	assignFull(t, srv, "secret", assignment)

	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)

	require.Equal(t, []seenSubscribers{{channelID: "bench-run-huge-group-1", uids: []string{"bench-u-5000", "bench-u-5001"}}, {channelID: "bench-run-huge-group-1", uids: []string{"bench-u-5002", "bench-u-5003"}}}, seen)
}

func TestWorkerKeepsPreparedOfflineGroupMembersDisconnected(t *testing.T) {
	var subscriberUIDs []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/channels":
		case "/bench/v1/channels/subscribers":
			var req model.BatchSubscribersRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			for _, item := range req.Items {
				subscriberUIDs = append(subscriberUIDs, item.Subscribers...)
			}
		default:
			t.Fatalf("unexpected target path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	pool := newWorkerPersonClientPool()
	srv := NewServer(Config{ControlToken: "secret", WorkloadClientFactory: pool.newClient})
	assignment := Assignment{
		RunID: "run-offline-members", WorkerID: "worker-a",
		Target: model.Target{
			BenchAPI: model.BenchAPIConfig{Addrs: []string{target.URL}},
			Gateway:  model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"gw-a:5100"}}},
		},
		Scenario: model.Scenario{
			Run:      model.RunConfig{ID: "run-offline-members"},
			Identity: model.IdentityConfig{TotalUsers: 100, UIDPrefix: "bench-u", DevicePrefix: "bench-d"},
			Online:   model.OnlineConfig{TotalUsers: 10, GatewayBalance: "round_robin"},
			Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
				Name: "group-a", ChannelType: model.ChannelTypeGroup, Count: 1,
				Members: model.MembersConfig{Count: 10, Overlap: "allowed"},
				Online:  model.ChannelOnlineConfig{MemberRatio: 0.2},
				Prepare: model.ChannelPrepareConfig{SubscribersBatchSize: 100},
			}}},
			Messages: model.MessagesConfig{Traffic: []model.TrafficConfig{{Name: "group-send", ChannelRef: "group-a"}}},
		},
		Plan: model.WorkerPlan{
			WorkerID: "worker-a", IdentityRange: model.Range{Start: 0, End: 10},
			Profiles: map[string]model.ProfileShard{"group-a": {
				Name: "group-a", ChannelType: model.ChannelTypeGroup,
				ChannelRange: model.Range{Start: 0, End: 1}, MemberRange: model.Range{Start: 0, End: 100},
				MemberReusePolicy: "allowed",
			}},
		},
	}
	assignFull(t, srv, "secret", assignment)

	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/connect", http.StatusOK)

	require.Len(t, subscriberUIDs, 10)
	onlineSubscribers := 0
	for _, uid := range subscriberUIDs {
		var index int
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(uid, "bench-u-")), &index))
		if index < 10 {
			onlineSubscribers++
		}
	}
	require.Equal(t, 2, onlineSubscribers)
	require.Len(t, pool.clients, 10)
	for uid := range pool.clients {
		var index int
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(uid, "bench-u-")), &index))
		require.Less(t, index, 10)
	}
}

func TestWorkerHTTPAppliesWeightedEightyTwentyGroupSenders(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bench/v1/channels", "/bench/v1/channels/subscribers":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected target path %s", r.URL.Path)
		}
	}))
	defer target.Close()

	pool := newWorkerPersonClientPool()
	srv := NewServer(Config{ControlToken: "secret", WorkloadClientFactory: pool.newClient})
	assignment := Assignment{
		RunID: "run-weighted-senders", WorkerID: "worker-a",
		Target: model.Target{
			BenchAPI: model.BenchAPIConfig{Addrs: []string{target.URL}},
			Gateway:  model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"gw-a:5100"}}},
		},
		Scenario: model.Scenario{
			Run:      model.RunConfig{ID: "run-weighted-senders", Duration: 5 * time.Nanosecond},
			Identity: model.IdentityConfig{TotalUsers: 5, UIDPrefix: "bench-u", DevicePrefix: "bench-d"},
			Online:   model.OnlineConfig{TotalUsers: 5, GatewayBalance: "round_robin"},
			Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
				Name: "group-a", ChannelType: model.ChannelTypeGroup, Count: 1,
				Members: model.MembersConfig{Count: 5, Overlap: "allowed"},
				Online:  model.ChannelOnlineConfig{MemberRatio: 1},
			}}},
			Messages: model.MessagesConfig{Traffic: []model.TrafficConfig{{
				Name: "group-send", ChannelRef: "group-a", SenderPick: "weighted_80_20",
				RatePerChannel: model.Rate{PerSecond: 1_000_000_000},
			}}},
		},
		Plan: model.WorkerPlan{
			WorkerID: "worker-a", IdentityRange: model.Range{Start: 0, End: 5},
			Profiles: map[string]model.ProfileShard{"group-a": {
				Name: "group-a", ChannelType: model.ChannelTypeGroup,
				ChannelRange: model.Range{Start: 0, End: 1}, MemberRange: model.Range{Start: 0, End: 5},
				MemberReusePolicy: "allowed",
			}},
		},
	}
	assignFull(t, srv, "secret", assignment)
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/connect", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/warmup", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/run", http.StatusOK)

	require.Len(t, pool.client("bench-u-0").sentFrames, 4)
	require.Len(t, pool.client("bench-u-1").sentFrames, 1)
	for _, uid := range []string{"bench-u-2", "bench-u-3", "bench-u-4"} {
		require.Empty(t, pool.client(uid).sentFrames)
	}
}

func TestWorkerHTTPChurnReconnectsAndSwapsOfflineIdentity(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bench/v1/users/tokens" {
			t.Fatalf("unexpected target path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	pool := newWorkerPersonClientPool()
	srv := NewServer(Config{ControlToken: "secret", WorkloadClientFactory: pool.newClient})
	assignment := Assignment{
		RunID: "run-churn", WorkerID: "worker-a",
		Target: model.Target{
			BenchAPI: model.BenchAPIConfig{Addrs: []string{target.URL}},
			Gateway:  model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"gw-a:5100"}}},
		},
		Scenario: model.Scenario{
			Run:      model.RunConfig{ID: "run-churn", Duration: 2 * time.Nanosecond},
			Identity: model.IdentityConfig{TotalUsers: 8, UIDPrefix: "bench-u", DevicePrefix: "bench-d", Token: model.TokenConfig{Mode: "bench_api"}},
			Online: model.OnlineConfig{TotalUsers: 4, GatewayBalance: "round_robin", Churn: model.ChurnConfig{
				Enabled: true, Interval: time.Nanosecond, Ratio: 0.5,
				SameUserRatio: 0.5, IdentitySwapRatio: 0.5, HistorySync: false,
			}},
			Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
				Name: "person-a", ChannelType: model.ChannelTypePerson, Count: 2,
			}}},
			Messages: model.MessagesConfig{Traffic: []model.TrafficConfig{{
				Name: "person-send", ChannelRef: "person-a", RatePerChannel: model.Rate{PerSecond: 1_000_000_000},
			}}},
		},
		Plan: model.WorkerPlan{
			WorkerID: "worker-a", IdentityRange: model.Range{Start: 0, End: 4},
			Profiles: map[string]model.ProfileShard{"person-a": {
				Name: "person-a", ChannelType: model.ChannelTypePerson,
				ChannelRange: model.Range{Start: 0, End: 2}, ParticipantRange: model.Range{Start: 0, End: 4},
			}},
		},
	}
	assignFull(t, srv, "secret", assignment)
	postPhase(t, srv, "secret", "/v1/phase/prepare", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/connect", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/warmup", http.StatusOK)
	postPhase(t, srv, "secret", "/v1/phase/run", http.StatusOK)

	require.Equal(t, 2, pool.createdCount("bench-u-0"), "same-user half should reconnect")
	require.Equal(t, 1, pool.createdCount("bench-u-5"), "identity-swap half should connect an offline user")
	require.Len(t, pool.clients, 5)
	clientMsgNos := make(map[string]struct{})
	for _, client := range pool.clients {
		for _, sent := range client.sentFrames {
			_, duplicate := clientMsgNos[sent.ClientMsgNo]
			require.False(t, duplicate, "churn windows must not reuse client message number %q", sent.ClientMsgNo)
			clientMsgNos[sent.ClientMsgNo] = struct{}{}
		}
	}
	require.NotEmpty(t, clientMsgNos)
}

func TestGroupChannelsForShardAllowedOverlapStaysInsideSharedPool(t *testing.T) {
	shard := model.ProfileShard{
		Name:              "group-hot",
		ChannelType:       model.ChannelTypeGroup,
		ChannelRange:      model.Range{Start: 0, End: 2},
		MemberRange:       model.Range{Start: 0, End: 100},
		MemberReusePolicy: "allowed",
	}
	profile := model.ChannelProfile{
		Name:        "group-hot",
		ChannelType: model.ChannelTypeGroup,
		Count:       2,
		Members:     model.MembersConfig{Count: 60, Overlap: "allowed", Pick: "deterministic_hash"},
	}

	channels := groupChannelsForShard("run-a", shard, profile, model.IdentityConfig{UIDPrefix: "bench-u"}, 0, model.WorkerPlan{})

	require.Len(t, channels, 2)
	for _, ch := range channels {
		require.Len(t, ch.OnlineMembers, 60)
		for _, uid := range ch.OnlineMembers {
			require.Regexp(t, `^bench-u-([0-9]|[1-9][0-9])$`, uid)
		}
	}
}
