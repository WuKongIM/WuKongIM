//go:build integration

package chatlifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// snapshotProbeClient changes only the existing diagnostic client port.
type snapshotProbeClient struct {
	SessionClient
	probe func(context.Context) (SessionQueueSnapshot, error)
}

func (c snapshotProbeClient) QueueSnapshot(ctx context.Context) (SessionQueueSnapshot, error) {
	return c.probe(ctx)
}

func newSnapshotProbeFixture(t *testing.T, probe func(context.Context) (SessionQueueSnapshot, error)) engineTestFixture {
	t.Helper()
	fixture := newEngineTestFixture(t, engineTestLimits{OnlineUsers: 1})
	uid := fixture.identity.UID(0)
	client, err := fixture.factory.NewSession(context.Background(), uid, "snapshot-probe")
	if err != nil {
		t.Fatal(err)
	}
	fixture.pool.factory = fixedSessionClientFactory{client: snapshotProbeClient{SessionClient: client, probe: probe}}
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.engine.Stop() })
	if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 0, LoginOrdinal: 0}); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestEngineSnapshotQueueProbeWaiterCancelsWithoutParallelRead(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	fixture := newSnapshotProbeFixture(t, func(ctx context.Context) (SessionQueueSnapshot, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return SessionQueueSnapshot{Depth: 2, Capacity: 7, Inflight: 3}, nil
		case <-ctx.Done():
			return SessionQueueSnapshot{}, ctx.Err()
		}
	})
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	first, second := make(chan error, 1), make(chan error, 1)
	firstJoined, secondStarted, secondJoined := false, false, false
	t.Cleanup(func() {
		cancelFirst()
		cancelSecond()
		close(release)
		if !firstJoined {
			<-first
		}
		if secondStarted && !secondJoined {
			<-second
		}
	})
	go func() { _, err := fixture.engine.WorkerRuntimeSnapshotContext(firstCtx); first <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first external queue probe never entered")
	}
	secondStarted = true
	go func() { _, err := fixture.engine.SnapshotContext(secondCtx); second <- err }()
	select {
	case <-entered:
		t.Fatal("concurrent snapshot started a second external probe while the first still owned it")
	case <-time.After(25 * time.Millisecond):
	}
	cancelSecond()
	select {
	case err := <-second:
		secondJoined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled snapshot waiter: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot waiter ignored cancellation")
	}
	if calls.Load() != 1 {
		t.Fatalf("external queue reads = %d, want one owned read", calls.Load())
	}
	cancelFirst()
	<-first
	firstJoined = true
}

func TestEngineSnapshotStopJoinsQueueProbeBeforeClientCloseAndRestart(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	fixture := newSnapshotProbeFixture(t, func(ctx context.Context) (SessionQueueSnapshot, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		// Cancellation is observed, but the exact call has not yet returned.
		<-release
		return SessionQueueSnapshot{}, ctx.Err()
	})
	closed := make(chan struct{}, 1)
	fixture.factory.closeCalled = closed
	snapshotCtx, cancelSnapshot := context.WithCancel(context.Background())
	snapshotDone, stopDone := make(chan error, 1), make(chan error, 1)
	snapshotJoined, stopStarted, stopJoined, released := false, false, false, false
	t.Cleanup(func() {
		cancelSnapshot()
		if !released {
			close(release)
		}
		if !snapshotJoined {
			<-snapshotDone
		}
		if stopStarted && !stopJoined {
			<-stopDone
		}
	})
	go func() { _, err := fixture.engine.WorkerRuntimeSnapshotContext(snapshotCtx); snapshotDone <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("external queue probe never entered")
	}
	stopStarted = true
	go func() { stopDone <- fixture.engine.Stop() }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("generation stop did not cancel its queue probe")
	}
	select {
	case <-closed:
		t.Fatal("Stop closed a client before its queue probe returned")
	case <-stopDone:
		stopJoined = true
		t.Fatal("Stop returned before its queue probe returned")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	released = true
	snapshotErr := <-snapshotDone
	snapshotJoined = true
	if snapshotErr == nil {
		t.Fatal("old-generation snapshot published success after generation cancellation")
	}
	stopErr := <-stopDone
	stopJoined = true
	if stopErr != nil {
		t.Fatal(stopErr)
	}
	select {
	case <-closed:
	default:
		t.Fatal("joined Stop did not close its client")
	}
	if err := fixture.engine.StartGeneration(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	fresh, err := fixture.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil || fresh.Engine.Generation != 2 || fresh.Engine.Online != 0 || fresh.Generated.PrimaryReleased != 0 {
		t.Fatalf("fresh generation snapshot: generation=%d online=%d generated=%d err=%v", fresh.Engine.Generation, fresh.Engine.Online, fresh.Generated.PrimaryReleased, err)
	}
}

func TestEngineSnapshotQueueProbeFailureNeverBecomesSuccessfulZero(t *testing.T) {
	probeError := errors.New("controlled queue observation failure")
	var fail atomic.Bool
	fail.Store(true)
	fixture := newSnapshotProbeFixture(t, func(context.Context) (SessionQueueSnapshot, error) {
		if fail.Load() {
			return SessionQueueSnapshot{}, probeError
		}
		return SessionQueueSnapshot{Depth: 2, Capacity: 7, Inflight: 3}, nil
	})
	if _, err := fixture.engine.WorkerRuntimeSnapshotContext(context.Background()); !errors.Is(err, probeError) {
		t.Fatalf("queue observation error = %v, want original error", err)
	}
	fail.Store(false)
	snapshot, err := fixture.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil || snapshot.Engine.TransportQueueDepth != 2 || snapshot.Engine.TransportQueueCapacity != 7 || snapshot.Engine.TransportInflight != 3 {
		t.Fatalf("exact queue gauges: depth=%d capacity=%d inflight=%d err=%v", snapshot.Engine.TransportQueueDepth, snapshot.Engine.TransportQueueCapacity, snapshot.Engine.TransportInflight, err)
	}
}

func TestEngineSnapshotKeepsCapturedEngineAndGeneratorDuringLaterGrant(t *testing.T) {
	fixture, grantAt := newPacingFixture(t, 89)
	if _, err := fixture.engine.ApplyGrant(context.Background(), grantAt, 100); err != nil {
		t.Fatal(err)
	}
	client := fixture.factory.clients()[0]
	entered, release := make(chan struct{}, 1), make(chan struct{})
	client.queueSnapshotEntered, client.queueSnapshotRelease = entered, release
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan EngineWorkerRuntimeSnapshot, 1)
	errorsDone, grantDone := make(chan error, 1), make(chan error, 1)
	snapshotJoined, grantStarted, grantJoined, released := false, false, false, false
	t.Cleanup(func() {
		cancel()
		if !released {
			close(release)
		}
		if !snapshotJoined {
			<-errorsDone
		}
		if grantStarted && !grantJoined {
			<-grantDone
		}
	})
	go func() {
		snapshot, err := fixture.engine.WorkerRuntimeSnapshotContext(ctx)
		result <- snapshot
		errorsDone <- err
	}()
	<-entered
	grantStarted = true
	go func() { _, err := fixture.engine.ApplyGrant(ctx, grantAt, 100); grantDone <- err }()
	select {
	case err := <-grantDone:
		grantJoined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("held external observation blocked a later valid primary grant")
	}
	// The successful projection must retain its original coherent owner tuple.
	close(release)
	released = true
	snapshotErr := <-errorsDone
	snapshotJoined = true
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	captured := <-result
	if captured.Engine.QueueCurrent != 100 || captured.Generated.PrimaryReleased != 100 {
		t.Fatalf("mixed owner tuple: queued=%d generated=%d, want original 100/100", captured.Engine.QueueCurrent, captured.Generated.PrimaryReleased)
	}
	fresh, err := fixture.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil || fresh.Engine.QueueCurrent != 200 || fresh.Generated.PrimaryReleased != 200 {
		t.Fatalf("later owner tuple: queued=%d generated=%d err=%v, want 200/200", fresh.Engine.QueueCurrent, fresh.Generated.PrimaryReleased, err)
	}
}
