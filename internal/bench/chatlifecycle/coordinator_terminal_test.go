package chatlifecycle

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCoordinatorRehearsalCheckpointsAfterSettlingReplacements(t *testing.T) {
	for _, scenario := range []struct {
		name                string
		readinessTimeout    bool
		completeReplacement bool
	}{
		{name: "readiness_timeout", readinessTimeout: true},
		{name: "terminal_observation_failed"},
		{name: "replacement_completes", completeReplacement: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			checkRehearsalTerminalEvidence(t, scenario.readinessTimeout, scenario.completeReplacement)
		})
	}
}

func checkRehearsalTerminalEvidence(t *testing.T, readinessTimeout, completeReplacement bool) {
	t.Helper()
	cfg := RehearsalConfig()
	cfg.RunID = "terminal-readiness-timeout"
	cfg.RunDuration = minDirectRunDuration
	hooks := newRecordingCoordinatorRunHooks()
	if !readinessTimeout && !completeReplacement {
		hooks.terminalError = errors.New("terminal observation unavailable")
	}
	clock := newManualCoordinatorClock(time.Unix(1_700_000_000, 0))
	grantCalls := make(chan uint64, coordinatorWorkerCount)
	workers := make([]CoordinatorWorker, coordinatorWorkerCount)
	for id := range workers {
		target, err := workerOnlineTarget(cfg.Workload.OnlineUsers, uint64(id), coordinatorWorkerCount)
		if err != nil {
			t.Fatal(err)
		}
		workers[id] = &rehearsalReplacementWorker{
			recordingCoordinatorWorker: &recordingCoordinatorWorker{id: uint64(id), log: &[]string{}, grantCalled: grantCalls},
			clock:                      clock, target: target,
			hasGap:     id == 1 && (readinessTimeout || completeReplacement),
			delayPause: id == 1 && readinessTimeout,
		}
	}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		Generation: 1,
		Preflight: coordinatorPreflightFunc(func(context.Context, Config) PreflightResult {
			return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
		}),
		Setup:   coordinatorSetupFunc(func(context.Context, Config) error { return nil }),
		Workers: workers,
		Observer: coordinatorObserverFunc(func(ctx context.Context, _ Config) ObserverResult {
			<-ctx.Done()
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		}),
		Clock: clock, Hooks: hooks,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan CoordinatorResult, 1)
	go func() { done <- coordinator.Run(ctx, cfg) }()
	for range 2 {
		<-clock.created
	}
	start := <-hooks.started
	for second := range int(cfg.RunDuration / time.Second) {
		if second > 0 {
			at := start.StartedAt.Add(time.Duration(second) * time.Second)
			clock.setNowAndQueue(at, map[time.Duration]time.Time{coordinatorGrantCadence: at})
		}
		for range coordinatorWorkerCount {
			select {
			case sequence := <-grantCalls:
				if sequence != uint64(second+1) {
					t.Fatalf("grant sequence = %d, want %d", sequence, second+1)
				}
			case result := <-done:
				t.Fatalf("Run() failed before the terminal checkpoint: %+v", result)
			case <-time.After(time.Second):
				t.Fatalf("grant at second %d timed out", second)
			}
		}
	}
	clock.setNowAndQueue(start.StartedAt.Add(cfg.RunDuration), map[time.Duration]time.Time{
		cfg.Observation.Cadence: start.StartedAt.Add(cfg.RunDuration),
	})
	if readinessTimeout {
		select {
		case period := <-clock.created:
			if period != 250*time.Millisecond {
				t.Fatalf("readiness wait = %s, want the 250ms left after the pause round", period)
			}
		case <-time.After(time.Second):
			t.Fatal("coordinator did not wait for terminal readiness")
		}
		clock.advance(250 * time.Millisecond)
	}
	wantOutcome, wantCode := CoordinatorHarnessInvalid, CoordinatorCodeCheckpoint
	if completeReplacement {
		select {
		case period := <-clock.created:
			if period != time.Second {
				t.Fatalf("readiness poll = %s, want one second", period)
			}
		case <-time.After(time.Second):
			t.Fatal("coordinator did not wait for the pending replacement")
		}
		clock.advance(time.Second)
		terminal := waitCoordinatorHookCut(t, hooks.cuts, CoordinatorCutTerminal)
		var ready int
		for _, snapshot := range terminal.Snapshots {
			ready += snapshot.Sessions.TrafficReady
		}
		if ready != cfg.Workload.OnlineUsers {
			t.Fatalf("terminal cut has %d ready sessions, want %d", ready, cfg.Workload.OnlineUsers)
		}
		wantOutcome, wantCode = CoordinatorCompleted, CoordinatorCodeCompleted
	}
	select {
	case result := <-done:
		if result.Outcome != wantOutcome || result.Code != wantCode {
			t.Fatalf("outcome = %q/%q, want %q/%q", result.Outcome, result.Code, wantOutcome, wantCode)
		}
	case <-time.After(time.Second):
		t.Fatal("coordinator did not finish at the five-second readiness deadline")
	}
	select {
	case report := <-hooks.finalized:
		if report.Decision != wantOutcome || report.CheckpointFailed == completeReplacement || len(report.FinalSnapshots) != 3 {
			t.Fatalf("final report decision/snapshot count = %q/%d", report.Decision, len(report.FinalSnapshots))
		}
		for _, snapshot := range report.FinalSnapshots {
			if snapshot.Phase != WorkerPhaseFinal {
				t.Fatalf("worker %d phase = %q, want final", snapshot.WorkerID, snapshot.Phase)
			}
		}
	default:
		t.Fatal("terminal checkpoint failure discarded the drained worker evidence")
	}
}

type rehearsalReplacementWorker struct {
	*recordingCoordinatorWorker
	clock      *manualCoordinatorClock
	target     int
	hasGap     bool
	delayPause bool
	paused     bool
}

func (w *rehearsalReplacementWorker) Checkpoint(ctx context.Context, request WorkerCheckpointRequest) (WorkerSnapshot, error) {
	snapshot, err := w.recordingCoordinatorWorker.Checkpoint(ctx, request)
	ready := w.target
	if w.hasGap && (!w.paused || w.delayPause) {
		ready -= 3
	}
	if request.PauseSessionExpiry {
		w.paused = true
		if w.delayPause {
			w.clock.advance(4750 * time.Millisecond)
		}
	}
	snapshot.Sessions = WorkerSessionSnapshot{Target: w.target, Online: ready, TrafficReady: ready}
	return snapshot, err
}

func (w *rehearsalReplacementWorker) Stop(ctx context.Context, request WorkerStopRequest) (WorkerSnapshot, error) {
	snapshot, err := w.recordingCoordinatorWorker.Stop(ctx, request)
	snapshot.Sessions.Target = w.target
	return snapshot, err
}
