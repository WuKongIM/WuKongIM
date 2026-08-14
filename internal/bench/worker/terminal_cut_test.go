package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestWorkerExternalTerminalCutRequiresExactAuthenticatedAcknowledgement(t *testing.T) {
	runner := newTerminalCutTestRunner()
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assignment := terminalCutTestAssignment(500 * time.Millisecond)
	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/assign", "secret", mustJSON(t, assignment))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for _, path := range []string{"/v1/phase/prepare", "/v1/phase/connect", "/v1/phase/warmup", "/v1/phase/run"} {
		postPhase(t, srv, "secret", path, http.StatusOK)
	}

	cooldown := authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/cooldown", "secret", nil)
	require.Equal(t, http.StatusAccepted, cooldown.Code, cooldown.Body.String())
	require.Eventually(t, func() bool {
		status := workerStatus(t, srv, "secret")
		return status.Phase == PhaseRun && status.ActivePhase == PhaseCooldown &&
			status.Lifecycle != nil && status.Lifecycle.TerminalCutReady &&
			status.Lifecycle.ActiveConnections == 3
	}, time.Second, 5*time.Millisecond)

	wrongGeneration := terminalCutRequestForTest(assignment, time.Now().UTC())
	wrongGeneration.AssignmentID = "different-generation"
	rec = authorizedRecorder(t, srv, http.MethodPost, "/v1/terminal-cut", "secret", mustJSON(t, wrongGeneration))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	badDigest := terminalCutRequestForTest(assignment, time.Now().UTC())
	badDigest.ProductMetricsSHA256 = "ABC"
	rec = authorizedRecorder(t, srv, http.MethodPost, "/v1/terminal-cut", "secret", mustJSON(t, badDigest))
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	request := terminalCutRequestForTest(assignment, time.Now().UTC())
	rec = authorizedRecorder(t, srv, http.MethodPost, "/v1/terminal-cut", "secret", mustJSON(t, request))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Eventually(t, func() bool {
		status := workerStatus(t, srv, "secret")
		return status.Phase == PhaseCooldown && status.ActivePhase == ""
	}, time.Second, 5*time.Millisecond)

	rec = authorizedRecorder(t, srv, http.MethodPost, "/v1/terminal-cut", "secret", mustJSON(t, request))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String(), "an exact ACK retry must be idempotent")
	different := request
	different.StorageOverlapSHA256 = request.ProductMetricsSHA256
	rec = authorizedRecorder(t, srv, http.MethodPost, "/v1/terminal-cut", "secret", mustJSON(t, different))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String(), "a different payload must not replace the binding")
	rec = authorizedRecorder(t, srv, http.MethodPost, "/v1/stop", "secret", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	status := workerStatus(t, srv, "secret")
	require.Equal(t, PhaseStopped, status.Phase)
	require.NotNil(t, status.Lifecycle)
	require.True(t, status.Lifecycle.TerminalPreClose)
	require.NotNil(t, status.Lifecycle.TerminalCut)
	require.Equal(t, assignment.RunID, status.Lifecycle.TerminalCut.RunID)
	require.Equal(t, assignment.AssignmentID, status.Lifecycle.TerminalCut.AssignmentID)
	require.Equal(t, request.ProductMetricsSHA256, status.Lifecycle.TerminalCut.ProductMetricsSHA256)
	require.Equal(t, request.StorageOverlapSHA256, status.Lifecycle.TerminalCut.StorageOverlapSHA256)
}

func TestWorkerExternalTerminalCutRouteRequiresControlAuthentication(t *testing.T) {
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: newTerminalCutTestRunner()})
	rec := rawAuthorizedRecorder(t, srv, http.MethodPost, "/v1/terminal-cut", "", []byte(`{}`))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestWorkerExternalTerminalCutCannotFreezeWithoutReceiveSealer(t *testing.T) {
	runner := &terminalCutNoSealerRunner{inner: newTerminalCutTestRunner()}
	srv := NewServer(Config{ControlToken: "secret", WorkloadRunner: runner})
	assignment := terminalCutTestAssignment(500 * time.Millisecond)
	rec := authorizedRecorder(t, srv, http.MethodPost, "/v1/assign", "secret", mustJSON(t, assignment))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	for _, path := range []string{"/v1/phase/prepare", "/v1/phase/connect", "/v1/phase/warmup", "/v1/phase/run"} {
		postPhase(t, srv, "secret", path, http.StatusOK)
	}
	rec = authorizedRecorder(t, srv, http.MethodPost, "/v1/phase/cooldown", "secret", nil)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.Eventually(t, func() bool { return runner.TerminalCutStatus().Ready }, time.Second, 5*time.Millisecond)
	request := terminalCutRequestForTest(assignment, time.Now().UTC())
	rec = authorizedRecorder(t, srv, http.MethodPost, "/v1/terminal-cut", "secret", mustJSON(t, request))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Eventually(t, func() bool {
		status := workerStatus(t, srv, "secret")
		return status.Phase == PhaseCooldown && status.ActivePhase == ""
	}, time.Second, 5*time.Millisecond)
	rec = authorizedRecorder(t, srv, http.MethodPost, "/v1/stop", "secret", mustJSON(t, StopRequest{
		RunID: assignment.RunID, AssignmentID: assignment.AssignmentID,
	}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	status := workerStatus(t, srv, "secret")
	require.Equal(t, PhaseStopped, status.Phase)
	require.True(t, status.Lifecycle == nil || !status.Lifecycle.TerminalPreClose,
		"a coordinator alone cannot certify the receive stop boundary: %+v", status.Lifecycle)
}

func TestDefaultRunnerExternalTerminalCutTimesOutInsideCooldownBudget(t *testing.T) {
	runner := NewDefaultWorkloadRunner(nil).(*defaultWorkloadRunner)
	assignment := terminalCutTestAssignment(35 * time.Millisecond)
	runner.BeginAssignment(assignment)

	started := time.Now()
	err := runner.Cooldown(context.Background(), assignment)

	require.ErrorContains(t, err, "terminal cut")
	require.Less(t, time.Since(started), 250*time.Millisecond)
	status := runner.LifecycleStatus()
	require.True(t, status.TerminalCutRequired)
	require.True(t, status.TerminalCutReady)
	require.Nil(t, status.TerminalCut)
}

func TestDefaultRunnerPreparesTargetAndFencesEverySessionBeforeTerminalCutReady(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient).(*defaultWorkloadRunner)
	assignment := terminalCutExactGroupAssignment()
	assignment.AssignmentID = "terminal-cut-generation-1"
	assignment.Scenario.Run.Duration = 0
	assignment.Scenario.Run.ExternalTerminalCut = true
	assignment.Scenario.Run.Cooldown = 2 * time.Second
	grant := frame.TerminalFenceGrant{Epoch: 91, Capability: "bounded-terminal-capability"}
	var prepared TerminalFencePrepareObservation
	runner.terminalFencePrepare = func(_ context.Context, got Assignment, expectedSessions int) (frame.TerminalFenceGrant, error) {
		prepared = TerminalFencePrepareObservation{RunID: got.RunID, AssignmentID: got.AssignmentID, ExpectedSessions: expectedSessions}
		return grant, nil
	}
	runner.BeginAssignment(assignment)
	require.NoError(t, runner.Connect(context.Background(), assignment))
	t.Cleanup(func() { _ = runner.EndAssignment(assignment) })

	done := make(chan error, 1)
	go func() { done <- runner.Cooldown(context.Background(), assignment) }()
	require.Eventually(t, func() bool { return runner.TerminalCutStatus().Ready }, time.Second, 5*time.Millisecond)
	require.Equal(t, TerminalFencePrepareObservation{
		RunID: assignment.RunID, AssignmentID: assignment.AssignmentID, ExpectedSessions: 2,
	}, prepared)
	for _, client := range pool.clients {
		require.Equal(t, grant, client.terminalFenceGrant)
	}
	_, err := runner.AcknowledgeTerminalCut(terminalCutRequestForRunner(runner, assignment))
	require.NoError(t, err)
	require.NoError(t, <-done)
}

func TestDefaultRunnerIncompleteFanoutProofCannotMakeTerminalCutReady(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient).(*defaultWorkloadRunner)
	installTerminalFencePrepareTestGrant(runner)
	assignment := terminalCutExactGroupAssignment()
	assignment.Scenario.Run.Cooldown = 500 * time.Millisecond
	runner.BeginAssignment(assignment)
	proof, err := fanoutProofForAssignment(assignment)
	require.NoError(t, err)
	require.NoError(t, runner.installAssignmentFanoutProof(assignment, proof))
	require.NoError(t, runner.Connect(context.Background(), assignment))
	t.Cleanup(func() { _ = runner.EndAssignment(assignment) })
	proof.ObserveGroupRecv("", nil)

	err = runner.Cooldown(context.Background(), assignment)

	require.ErrorContains(t, err, "fanout proof is incomplete")
	require.False(t, runner.TerminalCutStatus().Ready)
	require.Nil(t, runner.TerminalCutStatus().Binding)
}

func TestDefaultRunnerCompleteFanoutMismatchCanBeSealedAsProductEvidence(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient).(*defaultWorkloadRunner)
	installTerminalFencePrepareTestGrant(runner)
	assignment := terminalCutExactGroupAssignment()
	assignment.Scenario.Run.Cooldown = 2 * time.Second
	runner.BeginAssignment(assignment)
	proof, err := fanoutProofForAssignment(assignment)
	require.NoError(t, err)
	require.NoError(t, runner.installAssignmentFanoutProof(assignment, proof))
	require.NoError(t, runner.Connect(context.Background(), assignment))
	t.Cleanup(func() { _ = runner.EndAssignment(assignment) })
	proof.ExpectGroup("missing-delivery", "group-a", "bench-u-0", []string{"bench-u-0", "bench-u-1"})
	require.True(t, proof.Snapshot().Complete())
	require.False(t, proof.Snapshot().Matches())

	done := make(chan error, 1)
	go func() { done <- runner.Cooldown(context.Background(), assignment) }()
	require.Eventually(t, func() bool { return runner.TerminalCutStatus().Ready }, time.Second, 5*time.Millisecond)
	_, err = runner.AcknowledgeTerminalCut(terminalCutRequestForRunner(runner, assignment))
	require.NoError(t, err)
	require.NoError(t, <-done)
	require.False(t, runner.LifecycleStatus().ReceiveDrain.FanoutProof.Matches(),
		"identity mismatch is product evidence and must not be erased or reclassified as missing harness evidence")
}

type TerminalFencePrepareObservation struct {
	RunID            string
	AssignmentID     string
	ExpectedSessions int
}

func TestDefaultRunnerGenericCooldownDoesNotWaitForTerminalCut(t *testing.T) {
	runner := NewDefaultWorkloadRunner(nil).(*defaultWorkloadRunner)
	assignment := terminalCutTestAssignment(250 * time.Millisecond)
	assignment.Scenario.Run.ExternalTerminalCut = false
	runner.BeginAssignment(assignment)

	started := time.Now()
	require.NoError(t, runner.Cooldown(context.Background(), assignment))
	require.Less(t, time.Since(started), 100*time.Millisecond)
	status := runner.LifecycleStatus()
	require.False(t, status.TerminalCutRequired)
	require.False(t, status.TerminalCutReady)
}

func TestTerminalCutWaitTreatsConcurrentAcceptedACKAsSuccess(t *testing.T) {
	assignment := terminalCutTestAssignment(time.Second)
	for iteration := 0; iteration < 500; iteration++ {
		var barrier terminalCutBarrier
		barrier.begin(assignment)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		done := make(chan error, 1)
		go func() { done <- barrier.wait(ctx, assignment) }()
		require.Eventually(t, func() bool { return barrier.status().Ready }, time.Second, time.Microsecond)
		request := terminalCutRequestForTest(assignment, time.Now().UTC())
		_, ackErr := barrier.acknowledge(request)
		cancel()
		waitErr := <-done
		if ackErr == nil {
			require.NoError(t, waitErr, "iteration %d accepted a binding but failed cooldown", iteration)
		}
	}
}

func TestDefaultRunnerTerminalCutACKRevalidatesLiveReceiveDrain(t *testing.T) {
	pool := newReceiveDrainWorkerClientPool(0)
	runner := NewDefaultWorkloadRunner(pool.newClient).(*defaultWorkloadRunner)
	installTerminalFencePrepareTestGrant(runner)
	assignment := terminalCutExactGroupAssignment()
	assignment.AssignmentID = "terminal-cut-generation-1"
	assignment.Scenario.Run.Duration = 0
	assignment.Scenario.Run.ExternalTerminalCut = true
	assignment.Scenario.Run.Cooldown = 2 * time.Second
	runner.BeginAssignment(assignment)
	require.NoError(t, runner.Connect(context.Background(), assignment))
	t.Cleanup(func() { _ = runner.EndAssignment(assignment) })

	require.NotNil(t, runner.autoRecvAck)
	done := make(chan error, 1)
	go func() { done <- runner.Cooldown(context.Background(), assignment) }()
	require.Eventually(t, func() bool {
		return runner.TerminalCutStatus().Ready
	}, 1500*time.Millisecond, 5*time.Millisecond)
	pool.setDepth(1)

	_, err := runner.AcknowledgeTerminalCut(terminalCutRequestForRunner(runner, assignment))
	require.ErrorIs(t, err, ErrTerminalCutNotReady)
	require.Nil(t, runner.TerminalCutStatus().Binding)
	pool.setDepth(0)
	require.Eventually(t, func() bool {
		_, ackErr := runner.AcknowledgeTerminalCut(terminalCutRequestForRunner(runner, assignment))
		return ackErr == nil
	}, time.Second, 25*time.Millisecond)
	require.NoError(t, <-done)
}

func TestDefaultRunnerTerminalCutLateFrameRebuildsCurrentReceiveProof(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient).(*defaultWorkloadRunner)
	installTerminalFencePrepareTestGrant(runner)
	assignment := terminalCutExactGroupAssignment()
	assignment.AssignmentID = "terminal-cut-generation-1"
	assignment.Scenario.Run.Duration = 0
	assignment.Scenario.Run.ExternalTerminalCut = true
	assignment.Scenario.Run.Cooldown = 2 * time.Second
	runner.BeginAssignment(assignment)
	require.NoError(t, runner.Connect(context.Background(), assignment))
	t.Cleanup(func() { _ = runner.EndAssignment(assignment) })

	done := make(chan error, 1)
	go func() { done <- runner.Cooldown(context.Background(), assignment) }()
	require.Eventually(t, func() bool { return runner.TerminalCutStatus().Ready }, time.Second, 5*time.Millisecond)
	stable := runner.LifecycleStatus().ReceiveDrain
	require.True(t, stable.TerminalProofComplete(), "%+v", stable)
	staleRequest := terminalCutRequestForRunner(runner, assignment)

	recipient := pool.client("bench-u-1")
	require.NotNil(t, recipient)
	recipient.mu.Lock()
	recipient.readFrames = append(recipient.readFrames, &frame.RecvPacket{
		MessageID: 101, MessageSeq: 12, ClientMsgNo: "late-terminal-frame",
	})
	recipient.signalLocked()
	recipient.mu.Unlock()
	var late model.ReceiveDrainSnapshot
	require.Eventually(t, func() bool {
		late = runner.LifecycleStatus().ReceiveDrain
		return late.ReceiveFramesObserved > stable.ReceiveFramesObserved
	}, time.Second, time.Millisecond)

	require.Greater(t, late.ReceiveFramesObserved, stable.ReceiveFramesObserved)
	require.False(t, late.TerminalProofComplete(), "late traffic must invalidate the old stable-zero proof: %+v", late)
	require.Eventually(t, func() bool {
		current := runner.LifecycleStatus().ReceiveDrain
		return current.TerminalProofComplete() && current.ReceiveFramesObserved > stable.ReceiveFramesObserved
	}, time.Second, 5*time.Millisecond)

	_, err := runner.AcknowledgeTerminalCut(staleRequest)
	require.ErrorIs(t, err, ErrTerminalCutNotReady, "a re-proved late frame must invalidate the candidate's receive digest")
	require.Nil(t, runner.TerminalCutStatus().Binding)
	_, err = runner.AcknowledgeTerminalCut(terminalCutRequestForRunner(runner, assignment))
	require.NoError(t, err)
	require.NoError(t, <-done)
	frozen := runner.LifecycleStatus().ReceiveDrain
	require.Greater(t, frozen.ReceiveFramesObserved, stable.ReceiveFramesObserved,
		"terminal evidence must retain the re-proved live counter, not the stale first proof")
}

func TestDefaultRunnerTerminalReceiveSealIncludesFrameArrivingAfterACK(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient).(*defaultWorkloadRunner)
	installTerminalFencePrepareTestGrant(runner)
	assignment := terminalCutExactGroupAssignment()
	assignment.AssignmentID = "terminal-cut-generation-1"
	assignment.Scenario.Run.Duration = 0
	assignment.Scenario.Run.ExternalTerminalCut = true
	assignment.Scenario.Run.Cooldown = 2 * time.Second
	runner.BeginAssignment(assignment)
	require.NoError(t, runner.Connect(context.Background(), assignment))
	t.Cleanup(func() { _ = runner.EndAssignment(assignment) })

	done := make(chan error, 1)
	go func() { done <- runner.Cooldown(context.Background(), assignment) }()
	require.Eventually(t, func() bool { return runner.TerminalCutStatus().Ready }, time.Second, 5*time.Millisecond)
	before := runner.LifecycleStatus().ReceiveDrain
	_, err := runner.AcknowledgeTerminalCut(terminalCutRequestForRunner(runner, assignment))
	require.NoError(t, err)
	require.NoError(t, <-done)

	recipient := pool.client("bench-u-1")
	require.NotNil(t, recipient)
	recipient.mu.Lock()
	recipient.readFrames = append(recipient.readFrames, &frame.RecvPacket{
		MessageID: 102, MessageSeq: 13, ClientMsgNo: "after-terminal-ack",
	})
	recipient.signalLocked()
	recipient.mu.Unlock()

	sealCtx, cancel := context.WithDeadline(context.Background(), runner.TerminalCutStatus().DeadlineAt)
	defer cancel()
	require.ErrorContains(t, runner.SealTerminalReceive(sealCtx, assignment), "proof changed after acknowledgement")
	sealed := runner.LifecycleStatus().ReceiveDrain
	require.False(t, sealed.TerminalProofComplete(), "%+v", sealed)
	require.Greater(t, sealed.ReceiveFramesObserved, before.ReceiveFramesObserved)
	require.Nil(t, runner.terminalSealedLifecycle, "a changed proof must not become terminal_pre_close evidence")
	require.Nil(t, runner.autoRecvAck, "terminal receive readers must be detached only after their joined cut is verified")
}

func TestDefaultRunnerTerminalReceiveSealRetainsAssignmentFanoutProof(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient).(*defaultWorkloadRunner)
	installTerminalFencePrepareTestGrant(runner)
	assignment := terminalCutExactGroupAssignment()
	assignment.Scenario.Run.Cooldown = 2 * time.Second
	runner.BeginAssignment(assignment)
	proof, err := fanoutProofForAssignment(assignment)
	require.NoError(t, err)
	require.NoError(t, runner.installAssignmentFanoutProof(assignment, proof))
	require.NoError(t, runner.Connect(context.Background(), assignment))
	t.Cleanup(func() { _ = runner.EndAssignment(assignment) })

	done := make(chan error, 1)
	go func() { done <- runner.Cooldown(context.Background(), assignment) }()
	require.Eventually(t, func() bool { return runner.TerminalCutStatus().Ready }, time.Second, 5*time.Millisecond)
	candidate := runner.LifecycleStatus()
	require.True(t, candidate.ReceiveDrain.FanoutProof.Required)
	require.True(t, candidate.ReceiveDrain.FanoutProof.Complete(), "%+v", candidate.ReceiveDrain.FanoutProof)
	_, err = runner.AcknowledgeTerminalCut(terminalCutRequestForRunner(runner, assignment))
	require.NoError(t, err)
	require.NoError(t, <-done)

	sealCtx, cancel := context.WithDeadline(context.Background(), runner.TerminalCutStatus().DeadlineAt)
	defer cancel()
	require.NoError(t, runner.SealTerminalReceive(sealCtx, assignment))
	sealed := runner.LifecycleStatus()
	require.Equal(t, candidate.ReceiveDrain.FanoutProof, sealed.ReceiveDrain.FanoutProof)
	require.Equal(t, model.ReceiveDrainFingerprint(sealed.ReceiveDrain), sealed.ReceiveDrainSHA256)
	require.NotNil(t, runner.terminalSealedLifecycle)
}

func terminalCutTestAssignment(cooldown time.Duration) Assignment {
	return Assignment{
		RunID:        "terminal-cut-run",
		AssignmentID: "terminal-cut-generation-1",
		WorkerID:     "worker-a",
		Scenario: model.Scenario{Run: model.RunConfig{
			ID: "terminal-cut-run", Cooldown: cooldown, ExternalTerminalCut: true,
		}},
	}
}

func terminalCutExactGroupAssignment() Assignment {
	assignment := idleHeavyGroupAssignment()
	assignment.AssignmentID = "terminal-cut-generation-1"
	assignment.Plan.IdentityRange = model.Range{Start: 0, End: 2}
	assignment.Scenario.Run.Duration = 0
	assignment.Scenario.Run.ExternalTerminalCut = true
	assignment.Scenario.Channels.Profiles[0].Members.Overlap = "allowed"
	assignment.Scenario.Channels.Profiles[0].Shard.Mode = "hash"
	shard := assignment.Plan.Profiles["group-a"]
	shard.MemberReusePolicy = "allowed"
	assignment.Plan.Profiles["group-a"] = shard
	assignment.Scenario.Messages.Traffic[0].RecvAck = true
	assignment.Scenario.Messages.Traffic[0].Verify.Recv.Mode = "none"
	return assignment
}

func terminalCutRequestForTest(assignment Assignment, observedAt time.Time) TerminalCutRequest {
	digest := sha256.Sum256([]byte("candidate pre-close metrics"))
	storageDigest := sha256.Sum256([]byte("terminal storage overlap"))
	return TerminalCutRequest{
		RunID: assignment.RunID, AssignmentID: assignment.AssignmentID,
		ObservedAt: observedAt,
		ReceiveDrainSHA256: model.ReceiveDrainFingerprint(model.ReceiveDrainSnapshot{
			Required: true, EvidenceComplete: true, DrainComplete: true,
			ClientCount: 3, ActiveDrains: 3, QueueSnapshotClients: 3,
			StableZeroObservations: model.ReceiveDrainStableZeroObservations,
		}),
		ProductMetricsSHA256: hex.EncodeToString(digest[:]),
		StorageOverlapSHA256: hex.EncodeToString(storageDigest[:]),
	}
}

func terminalCutRequestForRunner(runner *defaultWorkloadRunner, assignment Assignment) TerminalCutRequest {
	request := terminalCutRequestForTest(assignment, time.Now().UTC())
	request.ReceiveDrainSHA256 = runner.LifecycleStatus().ReceiveDrainSHA256
	return request
}

func installTerminalFencePrepareTestGrant(runner *defaultWorkloadRunner) {
	runner.terminalFencePrepare = func(context.Context, Assignment, int) (frame.TerminalFenceGrant, error) {
		return frame.TerminalFenceGrant{Epoch: 91, Capability: "bounded-terminal-capability"}, nil
	}
}

type terminalCutTestRunner struct {
	snapshotRunner

	mu      sync.Mutex
	status  TerminalCutStatus
	ack     chan struct{}
	binding *TerminalCutBinding
}

func newTerminalCutTestRunner() *terminalCutTestRunner {
	return &terminalCutTestRunner{ack: make(chan struct{})}
}

func (r *terminalCutTestRunner) BeginAssignment(assignment Assignment) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = TerminalCutStatus{Required: assignment.Scenario.Run.ExternalTerminalCut}
	r.ack = make(chan struct{})
	r.binding = nil
}

func (r *terminalCutTestRunner) Cooldown(ctx context.Context, assignment Assignment) error {
	if !assignment.Scenario.Run.ExternalTerminalCut {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, assignment.Scenario.Run.Cooldown)
	defer cancel()
	deadline, _ := ctx.Deadline()
	r.mu.Lock()
	r.status.Required = true
	r.status.Ready = true
	r.status.ReadyAt = time.Now().UTC()
	r.status.DeadlineAt = deadline.UTC()
	ack := r.ack
	r.mu.Unlock()
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *terminalCutTestRunner) TerminalCutStatus() TerminalCutStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := r.status
	if r.binding != nil {
		binding := *r.binding
		status.Binding = &binding
	}
	return status
}

func (r *terminalCutTestRunner) AcknowledgeTerminalCut(request TerminalCutRequest) (TerminalCutBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	binding := TerminalCutBinding{
		RunID: request.RunID, AssignmentID: request.AssignmentID,
		ReadyAt: r.status.ReadyAt, DeadlineAt: r.status.DeadlineAt, ObservedAt: request.ObservedAt,
		ReceiveDrainSHA256:   request.ReceiveDrainSHA256,
		ProductMetricsSHA256: request.ProductMetricsSHA256,
		StorageOverlapSHA256: request.StorageOverlapSHA256,
		AcknowledgedAt:       time.Now().UTC(),
	}
	r.binding = &binding
	close(r.ack)
	return binding, nil
}

func (r *terminalCutTestRunner) LifecycleStatus() LifecycleStatus {
	status := r.TerminalCutStatus()
	receiveDrain := model.ReceiveDrainSnapshot{
		Required: true, EvidenceComplete: true, DrainComplete: true,
		ClientCount: 3, ActiveDrains: 3, QueueSnapshotClients: 3,
		StableZeroObservations: model.ReceiveDrainStableZeroObservations,
	}
	return LifecycleStatus{
		ActiveConnections:   3,
		ReceiveDrain:        receiveDrain,
		ReceiveDrainSHA256:  model.ReceiveDrainFingerprint(receiveDrain),
		TerminalCutRequired: status.Required,
		TerminalCutReady:    status.Ready,
		TerminalCut:         status.Binding,
	}
}

func (r *terminalCutTestRunner) SealTerminalReceive(context.Context, Assignment) error { return nil }

func (r *terminalCutTestRunner) EndAssignment(Assignment) error { return nil }

type terminalCutNoSealerRunner struct {
	snapshotRunner
	inner *terminalCutTestRunner
}

func (r *terminalCutNoSealerRunner) BeginAssignment(assignment Assignment) {
	r.inner.BeginAssignment(assignment)
}
func (r *terminalCutNoSealerRunner) Cooldown(ctx context.Context, assignment Assignment) error {
	return r.inner.Cooldown(ctx, assignment)
}
func (r *terminalCutNoSealerRunner) TerminalCutStatus() TerminalCutStatus {
	return r.inner.TerminalCutStatus()
}
func (r *terminalCutNoSealerRunner) AcknowledgeTerminalCut(request TerminalCutRequest) (TerminalCutBinding, error) {
	return r.inner.AcknowledgeTerminalCut(request)
}
func (r *terminalCutNoSealerRunner) LifecycleStatus() LifecycleStatus {
	return r.inner.LifecycleStatus()
}
func (r *terminalCutNoSealerRunner) EndAssignment(assignment Assignment) error {
	return r.inner.EndAssignment(assignment)
}
