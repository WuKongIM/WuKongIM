package chatlifecycle

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"
)

// Failure cases: a whole second's grant shares one due time; the worker only
// advances once per second; subsecond advances mint extra login/primary credit;
// delayed advances replay old arrivals as catch-up load; future admission loses
// its session lease at TTL; stop/drain drops future work or leaks those leases.
// Existing worker-server tests also fence duplicate grants and caller cancellation.
func TestEngineExternalGrantUsesDeterministicSubsecondArrivals(t *testing.T) {
	var previous []time.Duration
	for index, seed := range []uint64{89, 89, 90} {
		fixture, now := newPacingFixture(t, seed)
		grant, err := fixture.engine.ApplyGrant(context.Background(), now, 100)
		if err != nil || !grant.Admitted || grant.Snapshot.Released != 100 || grant.Snapshot.Person != 90 || grant.Snapshot.Group != 10 {
			t.Fatalf("grant = %+v, %v", grant, err)
		}
		offsets := pacingOffsets(t, fixture.engine, now)
		if len(offsets) != 100 {
			t.Fatalf("scheduled SENDs = %d, want 100", len(offsets))
		}
		buckets := [10]int{}
		for _, offset := range offsets {
			if offset < 0 || offset >= time.Second {
				t.Fatalf("arrival offset %v outside one grant interval", offset)
			}
			buckets[int(offset/(100*time.Millisecond))]++
		}
		for bucket, count := range buckets {
			if count == 0 || count > 30 {
				t.Fatalf("seed %d collapsed scheduled arrivals: bucket %d = %d, all=%v", seed, bucket, count, buckets)
			}
		}
		if index == 1 && !reflect.DeepEqual(previous, offsets) {
			t.Fatal("same seed and grant identity changed arrival offsets")
		}
		if index == 2 && reflect.DeepEqual(previous, offsets) {
			t.Fatal("different seed reused arrival offsets")
		}
		previous = offsets
		if fixture.factory.sentCount() != 0 {
			t.Fatal("subsequent grant sent future work before scheduler advancement")
		}
		if err := fixture.engine.FencePlannedShutdown(context.Background()); err != errEngineNotDrained {
			t.Fatalf("future SENDs must block shutdown fence: %v", err)
		}
		advancePacingWindow(t, fixture, now)
		if sent := fixture.verifier.Snapshot().Sent; sent != 100 {
			t.Fatalf("completed grant sent %d, want unchanged exact budget 100", sent)
		}
		fixture.engine.Stop()
		assertPacingLeasesReleased(t, fixture)
	}
}

func TestEngineExternalGrantDoesNotReplayLateArrivals(t *testing.T) {
	fixture, now := newPacingFixture(t, 89)
	if _, err := fixture.engine.ApplyGrant(context.Background(), now, 100); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Set(now.Add(2 * time.Second))
	_, err := fixture.engine.AdvanceContext(context.Background(), fixture.clock.Now())
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	if sent := fixture.verifier.Snapshot().Sent; sent != 0 {
		t.Fatalf("late scheduler replayed %d stale SENDs as catch-up load", sent)
	}
	if !workerEvidenceHasCode(fixture.evidence.Snapshot(), FailureCodeOfferedLoadUnderDelivery) {
		t.Fatal("discarded arrival credit lost explicit harness under-delivery evidence")
	}
	assertPacingLeasesReleased(t, fixture)
}

func TestEngineExternalGrantKeepsAdmissionLeaseAcrossSessionTTL(t *testing.T) {
	fixture, now := newPacingFixture(t, 89)
	fixture.pool.mu.Lock()
	for _, session := range fixture.pool.online {
		session.snapshot.Deadline = now.Add(time.Millisecond)
	}
	fixture.pool.mu.Unlock()
	if _, err := fixture.engine.ApplyGrant(context.Background(), now, 100); err != nil {
		t.Fatal(err)
	}
	advancePacingWindow(t, fixture, now)
	if sent := fixture.verifier.Snapshot().Sent; sent != 100 {
		t.Fatalf("future SEND lost its admission lease after TTL: sent=%d", sent)
	}
	fixture.engine.Stop()
	assertPacingLeasesReleased(t, fixture)
}

// An advance can wait after sampling its input time, and a temporarily
// unroutable group must preserve both edges of the original arrival window.
func TestEngineExternalGrantChecksActualDispatchTimeAfterOwnerDelay(t *testing.T) {
	fixture, now := newPacingFixture(t, 89)
	if _, err := fixture.engine.ApplyGrant(context.Background(), now, 100); err != nil {
		t.Fatal(err)
	}
	sampledAt := now.Add(50 * time.Millisecond)
	fixture.clock.Set(sampledAt)
	entered, release := blockEngineOwner(t, fixture.engine)
	<-entered
	result := make(chan error, 1)
	go func() {
		_, err := fixture.engine.AdvanceContext(context.Background(), sampledAt)
		result <- err
	}()
	waitForEngineQueuedCommand(t, fixture.engine)
	fixture.clock.Set(now.Add(2 * time.Second))
	close(release)
	assertRuntimeFailure(t, <-result, RuntimeFailureUnderDelivery)
	if sent := fixture.verifier.Snapshot().Sent; sent != 0 {
		t.Fatalf("stale sampled advance time admitted %d late SENDs", sent)
	}
}

func TestEngineDeferredGrantPreservesArrivalWindow(t *testing.T) {
	for _, scenario := range []string{"early recovery", "late roster recovery", "late dispatch after recovery"} {
		fixture := newEngineTestFixture(t, engineTestLimits{WorkCapacity: 256, MaxWorkPerAdvance: 256})
		fixture.factory.autoAck = true
		if err := fixture.engine.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { fixture.engine.Stop() })
		group, err := fixture.engine.generator.catalog.Group(0)
		if err != nil {
			t.Fatal(err)
		}
		firstIndex, _ := group.MemberIndex(0)
		intent := fixture.intent(t, fixture.identity.UID(firstIndex), group.ID, 1, TrafficGroup)
		now := fixture.clock.Now()
		due := now.Add(800 * time.Millisecond)
		result := make(chan error, 1)
		if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
			admit := func(routed TrafficIntent) bool { return fixture.pool.acquireSendAndCorrelationLease(routed, now) }
			result <- fixture.engine.admitGrantedIntentBefore(intent, now, due, due.Add(100*time.Millisecond), admit)
		}}); err != nil {
			t.Fatal(err)
		}
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		for member := 0; member < 2; member++ {
			index, err := group.MemberIndex(member)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{UID: fixture.identity.UID(index), UserIndex: index, LoginOrdinal: uint64(member)}); err != nil {
				t.Fatal(err)
			}
		}
		advanceAt := now.Add(10 * time.Millisecond)
		if scenario == "late roster recovery" {
			advanceAt = now.Add(2 * time.Second)
		}
		fixture.clock.Set(advanceAt)
		_, err = fixture.engine.AdvanceContext(context.Background(), advanceAt)
		if err != nil {
			t.Fatal(err)
		}
		wantInitial := uint64(0)
		if scenario == "late roster recovery" {
			wantInitial = 1 // Existing roster gaps retain their separate eligibility budget.
		}
		if sent := fixture.verifier.Snapshot().Sent; sent != wantInitial {
			t.Fatalf("deferred route ignored arrival window: %s sent=%d want=%d", scenario, sent, wantInitial)
		}
		if scenario != "late roster recovery" {
			dispatchAt := due
			if scenario == "late dispatch after recovery" {
				dispatchAt = now.Add(2 * time.Second)
			}
			fixture.clock.Set(dispatchAt)
			_, err := fixture.engine.AdvanceContext(context.Background(), dispatchAt)
			wantSent := uint64(1)
			if scenario == "late dispatch after recovery" {
				assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
				wantSent = 0
			} else if err != nil {
				t.Fatal(err)
			}
			if sent := fixture.verifier.Snapshot().Sent; sent != wantSent {
				t.Fatalf("recovered route %s sent=%d want=%d", scenario, sent, wantSent)
			}
		}
		fixture.engine.Stop()
		assertPacingLeasesReleased(t, fixture)
	}
}

func TestWorkerExternalGrantAdvancesSubsecondWithoutExtraPrimaryCredit(t *testing.T) {
	fixture, now := newPacingFixture(t, 89)
	ticker := newManualWorkerGenerationTicker()
	intervals := make(chan time.Duration, 1)
	generation := &engineWorkerGeneration{
		engine: fixture.engine, verifier: fixture.verifier, evidence: fixture.evidence,
		onlineTarget: 100, externalGrants: true, clock: fixture.clock,
		newTicker: func(interval time.Duration) workerGenerationTicker { intervals <- interval; return ticker },
	}
	generation.trafficReady.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go generation.runTicks(ctx, done)
	defer func() { cancel(); <-done }()
	ticker.awaitReady(t)
	if interval := <-intervals; interval != 10*time.Millisecond {
		t.Fatalf("external worker scheduler interval = %v, want bounded 10ms advancement", interval)
	}
	if _, err := generation.ApplyGrant(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	loginBefore := fixture.engine.scheduler.loginOrdinal
	for tick := 1; tick <= 99; tick++ {
		fixture.clock.Set(now.Add(time.Duration(tick) * 10 * time.Millisecond))
		ticker.tickAndWait(t)
		if tick == 50 {
			sent := fixture.verifier.Snapshot().Sent
			if sent < 20 || sent > 80 {
				t.Fatalf("half-second dispatched %d/100, want seeded subsecond arrivals", sent)
			}
		}
	}
	if fixture.engine.scheduler.loginOrdinal != loginBefore {
		t.Fatal("subsecond worker advances minted steady-login credit")
	}
	if generated := fixture.engine.generator.Snapshot().PrimaryReleased; generated != 100 {
		t.Fatalf("subsecond worker minted primary credit: %d", generated)
	}
}

func newPacingFixture(t *testing.T, seed uint64) (engineTestFixture, time.Time) {
	t.Helper()
	fixture := newEngineTestFixture(t, engineTestLimits{
		IdentitySeed: seed, OnlineUsers: 100, NewUsersPerDay: 250_000,
		SessionDuration: 3 * time.Hour, WorkCapacity: 8_192, MaxWorkPerAdvance: 8_192, InflightCapacity: 512,
	})
	fixture.factory.autoAck = true
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fixture.engine.Stop() })
	now := fixture.clock.Now().Add(30 * time.Second)
	fixture.clock.Set(now)
	now, _ = fixture.bootstrapScheduledLogins(t, now)
	// Zero primary preserves the first-grant bootstrap barrier without leaving
	// earlier SENDs in the fixture's budget or latency evidence.
	if _, err := fixture.engine.ApplyGrant(context.Background(), now, 0); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	fixture.clock.Set(now)
	return fixture, now
}

func pacingOffsets(t *testing.T, engine *Engine, origin time.Time) []time.Duration {
	t.Helper()
	result := make(chan []time.Duration, 1)
	if err := engine.enqueueBlocking(engineCommand{run: func() {
		var sends []*engineWork
		for _, work := range engine.work {
			if work.kind == engineWorkSend && work.attempt == 0 {
				sends = append(sends, work)
			}
		}
		sort.Slice(sends, func(i, j int) bool { return sends[i].order < sends[j].order })
		offsets := make([]time.Duration, len(sends))
		for i, work := range sends {
			offsets[i] = work.due.Sub(origin)
		}
		result <- offsets
	}}); err != nil {
		t.Fatal(err)
	}
	return <-result
}

func advancePacingWindow(t *testing.T, fixture engineTestFixture, origin time.Time) {
	t.Helper()
	for tick := 1; tick <= 100; tick++ {
		fixture.clock.Set(origin.Add(time.Duration(tick) * 10 * time.Millisecond))
		if _, err := fixture.engine.AdvanceContext(context.Background(), fixture.clock.Now()); err != nil {
			t.Fatalf("advance tick %d: %v", tick, err)
		}
		waitForEngineCompletions(t, fixture.engine, "paced SEND")
	}
}

func assertPacingLeasesReleased(t *testing.T, fixture engineTestFixture) {
	t.Helper()
	fixture.pool.mu.Lock()
	defer fixture.pool.mu.Unlock()
	if len(fixture.pool.sendLeases) != 0 || len(fixture.pool.correlationLeases) != 0 {
		t.Fatalf("paced SEND leases leaked: send=%d correlation=%d", len(fixture.pool.sendLeases), len(fixture.pool.correlationLeases))
	}
}
