package chatlifecycle

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestWorkerTerminalCheckpointPausesExpiryAndLetsReplacementFinish(t *testing.T) {
	ctx := context.Background()
	f := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 2, NewUsersPerDay: 250_000, SessionDuration: time.Minute,
	})
	ticker := newManualWorkerGenerationTicker()
	generation := &engineWorkerGeneration{
		engine: f.engine, verifier: f.verifier, evidence: f.evidence,
		newTicker:    func(time.Duration) workerGenerationTicker { return ticker },
		onlineTarget: 2, clock: f.clock, generation: 1, done: make(chan error, 1),
	}
	server, err := NewWorkerServer(WorkerServerConfig{
		ControlToken: "control-secret", Now: f.clock.Now,
		Factory: WorkerGenerationFactoryFunc(func(WorkerAssignment) (WorkerGeneration, error) { return generation, nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := LocalConfig()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "terminal-worker", Generation: 1}
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/assign", WorkerAssignment{
		WorkerFence: fence, WorkerCount: uint64(cfg.Workload.Workers), Config: cfg,
	})
	assertWorkerSuccess(t, server, http.MethodPost, "/v1/chat-lifecycle/start", WorkerStartRequest{WorkerFence: fence})
	defer generation.Stop()
	ticker.awaitReady(t)

	old, err := f.engine.Login(ctx, SessionLogin{UID: f.identity.UID(0), UserIndex: 0, LoginOrdinal: 0})
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Set(f.clock.Now().Add(2 * time.Second))
	newer, err := f.engine.Login(ctx, SessionLogin{UID: f.identity.UID(1), UserIndex: 1, LoginOrdinal: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.Step(ctx, f.clock.Now(), f.demand(0)); err != nil {
		t.Fatal(err)
	}
	f.clock.Set(old.Deadline)
	if _, err := f.engine.Advance(old.Deadline); err != nil {
		t.Fatal(err)
	}
	assertSessionCountsEventually(t, f.pool, SessionCounts{Online: 1})

	// Exercise the same authenticated, fenced request the coordinator sends.
	request := struct {
		WorkerFence
		PauseSessionExpiry bool `json:"pause_session_expiry"`
	}{WorkerFence: fence, PauseSessionExpiry: true}
	paused := decodeWorkerSnapshotResponse(t, workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/checkpoint", request))
	if paused.Sessions.Online != 1 || paused.Sessions.CloseReasons.Expired != 1 {
		t.Fatalf("initial cut did not retain the preexisting replacement gap: %+v", paused.Sessions)
	}

	f.clock.Set(newer.Deadline)
	replacement, err := f.engine.Step(ctx, newer.Deadline, nil)
	if err != nil {
		t.Fatal(err)
	}
	replacement = f.settleScheduledLogins(t, newer.Deadline, replacement)
	final := decodeWorkerSnapshotResponse(t, workerRequest(t, server, http.MethodPost, "/v1/chat-lifecycle/checkpoint",
		WorkerCheckpointRequest{WorkerFence: fence}))
	if replacement.ReplacementLogins != 1 || final.Sessions.Online != 2 || final.Sessions.TrafficReady != 2 ||
		final.Sessions.CloseReasons.Expired != 1 || final.Sessions.Starting != 0 || final.Sessions.Closing != 0 {
		t.Fatalf("terminal checkpoint did not settle only the existing gap: %+v, %+v", replacement, final.Sessions)
	}
}
