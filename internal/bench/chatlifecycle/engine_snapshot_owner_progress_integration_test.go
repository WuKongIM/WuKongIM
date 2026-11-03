//go:build integration

package chatlifecycle

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// An external queue gauge must not hold the Engine's primary scheduler owner.
// Consistent Engine/generator capture, cancellation and joined Stop remain
// required. The controlled port gate proves coupling, not the cause of R22.
func TestWorkerRuntimeSnapshotQueueGaugeCannotHoldPrimaryAdvance(t *testing.T) {
	fixture, grantAt := newPacingFixture(t, 89)
	grant, err := fixture.engine.ApplyGrant(context.Background(), grantAt, 100)
	if err != nil || !grant.Admitted || grant.Snapshot.Released != 100 {
		t.Fatalf("primary grant: admitted=%v released=%d err=%v", grant.Admitted, grant.Snapshot.Released, err)
	}
	before, err := fixture.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil {
		t.Fatalf("initial runtime snapshot: %v", err)
	}
	if before.Generated.PrimaryReleased != 100 {
		t.Fatalf("fixture generated primary grant = %d, want 100", before.Generated.PrimaryReleased)
	}
	due := snapshotPrimaryGrantDue(t, fixture.engine, grantAt)
	if fixture.factory.sentCount() != 0 {
		t.Fatal("fixture sent primary work before its scheduled advance")
	}

	client := fixture.factory.clients()[0]
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	client.queueSnapshotEntered = entered
	client.queueSnapshotRelease = release
	snapshotCtx, cancelSnapshot := context.WithCancel(context.Background())
	advanceCtx, cancelAdvance := context.WithCancel(context.Background())
	snapshotDone := make(chan error, 1)
	advanceDone := make(chan error, 1)
	var releaseOnce sync.Once
	snapshotJoined, advanceStarted, advanceJoined := false, false, false
	t.Cleanup(func() {
		cancelAdvance()
		cancelSnapshot()
		releaseOnce.Do(func() { close(release) })
		if advanceStarted && !advanceJoined {
			select {
			case <-advanceDone:
			case <-time.After(time.Second):
				t.Error("Advance did not join after cancellation and gauge release")
			}
		}
		if !snapshotJoined {
			select {
			case <-snapshotDone:
			case <-time.After(time.Second):
				t.Error("runtime snapshot did not join after cancellation and gauge release")
			}
		}
	})
	go func() {
		_, snapshotErr := fixture.engine.WorkerRuntimeSnapshotContext(snapshotCtx)
		snapshotDone <- snapshotErr
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("fixture never entered the accepted client's queue gauge")
	}

	// Native time stays only 10ms after due, inside the unchanged 100ms arrival
	// allowance. The wall-clock guard bounds a deadlock; it grants no SEND credit.
	advanceAt := due.Add(10 * time.Millisecond)
	fixture.clock.Set(advanceAt)
	advanceStarted = true
	go func() {
		_, advanceErr := fixture.engine.AdvanceContext(advanceCtx, advanceAt)
		advanceDone <- advanceErr
	}()
	select {
	case advanceErr := <-advanceDone:
		advanceJoined = true
		if advanceErr != nil {
			t.Fatalf("Advance while queue gauge was held: %v", advanceErr)
		}
	case <-time.After(time.Second):
		t.Fatal("queue gauge held the primary scheduler owner despite a still-valid arrival deadline")
	}
	awaitSnapshotIndependentPrimarySend(t, fixture.factory)
	select {
	case snapshotErr := <-snapshotDone:
		snapshotJoined = true
		t.Fatalf("runtime snapshot returned before its queue observation was released or canceled: %v", snapshotErr)
	default:
	}
	cancelSnapshot()
	select {
	case snapshotErr := <-snapshotDone:
		snapshotJoined = true
		if !errors.Is(snapshotErr, context.Canceled) {
			t.Fatalf("canceled runtime snapshot: %v, want context.Canceled", snapshotErr)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime snapshot ignored caller cancellation while its queue gauge was held")
	}
}

// The worker runner must also finish its accepted tick while Snapshot waits on
// an external gauge. This exercises the real advanceMu/Engine dependency.
func TestWorkerRunnerSnapshotQueueGaugeCannotHoldPrimaryTick(t *testing.T) {
	fixture, grantAt := newPacingFixture(t, 89)
	grant, err := fixture.engine.ApplyGrant(context.Background(), grantAt, 100)
	if err != nil || !grant.Admitted || grant.Snapshot.Released != 100 {
		t.Fatalf("primary grant: admitted=%v released=%d err=%v", grant.Admitted, grant.Snapshot.Released, err)
	}
	before, err := fixture.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil {
		t.Fatalf("initial runtime snapshot: %v", err)
	}
	if before.Generated.PrimaryReleased != 100 {
		t.Fatalf("fixture generated primary grant = %d, want 100", before.Generated.PrimaryReleased)
	}
	due := snapshotPrimaryGrantDue(t, fixture.engine, grantAt)
	if fixture.factory.sentCount() != 0 {
		t.Fatal("fixture sent primary work before its scheduled tick")
	}
	client := fixture.factory.clients()[0]
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	client.queueSnapshotEntered = entered
	client.queueSnapshotRelease = release
	ticker := newManualWorkerGenerationTicker()
	interval := make(chan time.Duration, 1)
	generation := &engineWorkerGeneration{
		engine: fixture.engine, verifier: fixture.verifier, evidence: fixture.evidence,
		onlineTarget: 100, externalGrants: true, clock: fixture.clock,
		newTicker: func(cadence time.Duration) workerGenerationTicker {
			interval <- cadence
			return ticker
		},
		done: make(chan error, 1),
	}
	generation.trafficReady.Store(true)
	tickCtx, cancelTicks := context.WithCancel(context.Background())
	snapshotCtx, cancelSnapshot := context.WithCancel(context.Background())
	ticksDone := make(chan struct{})
	snapshotDone := make(chan error, 1)
	snapshotStarted, snapshotJoined := false, false
	var releaseOnce sync.Once
	t.Cleanup(func() {
		cancelTicks()
		cancelSnapshot()
		releaseOnce.Do(func() { close(release) })
		if snapshotStarted && !snapshotJoined {
			select {
			case <-snapshotDone:
			case <-time.After(time.Second):
				t.Error("worker snapshot did not join after cancellation and gauge release")
			}
		}
		select {
		case <-ticksDone:
		case <-time.After(time.Second):
			t.Error("worker tick did not join after cancellation and gauge release")
		}
	})
	go generation.runTicks(tickCtx, ticksDone)
	ticker.awaitReady(t)
	if cadence := <-interval; cadence != 10*time.Millisecond {
		t.Fatalf("worker scheduler cadence = %v, want unchanged 10ms", cadence)
	}
	snapshotStarted = true
	go func() {
		_, snapshotErr := generation.Snapshot(snapshotCtx)
		snapshotDone <- snapshotErr
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker snapshot never entered the accepted client's queue gauge")
	}

	fixture.clock.Set(due.Add(10 * time.Millisecond))
	ticker.tick(t)
	select {
	case <-ticker.ready:
	case <-time.After(time.Second):
		t.Fatal("queue gauge held the worker tick despite a still-valid primary arrival deadline")
	}
	awaitSnapshotIndependentPrimarySend(t, fixture.factory)
	select {
	case snapshotErr := <-snapshotDone:
		snapshotJoined = true
		t.Fatalf("worker snapshot returned before queue release or cancellation: %v", snapshotErr)
	default:
	}
	cancelSnapshot()
	select {
	case snapshotErr := <-snapshotDone:
		snapshotJoined = true
		if !errors.Is(snapshotErr, context.Canceled) {
			t.Fatalf("canceled worker snapshot: %v, want context.Canceled", snapshotErr)
		}
	case <-time.After(time.Second):
		t.Fatal("worker snapshot ignored caller cancellation")
	}
}

// Advance transfers work to the dispatcher. Wait for the same SDK port while
// the gauge remains held; no extra tick, SEND credit or business time is added.
func awaitSnapshotIndependentPrimarySend(t *testing.T, factory *engineFakeFactory) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for factory.sentCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("queue gauge blocked asynchronous primary SDK admission")
		}
		runtime.Gosched()
	}
}

// snapshotPrimaryGrantDue selects the exact primary SEND readset instead of
// NextFutureAt, which also describes unrelated bootstrap relationship activity.
func snapshotPrimaryGrantDue(t *testing.T, engine *Engine, grantAt time.Time) time.Time {
	t.Helper()
	offsets := pacingOffsets(t, engine, grantAt)
	if len(offsets) != 100 {
		t.Fatalf("fixture retained %d primary SENDs, want the exact 100-message grant", len(offsets))
	}
	first := time.Second
	for _, offset := range offsets {
		if offset < 0 || offset >= time.Second {
			t.Fatalf("primary SEND offset %v is outside the original one-second grant interval", offset)
		}
		if offset < first {
			first = offset
		}
	}
	return grantAt.Add(first)
}
