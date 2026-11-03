//go:build integration

package chatlifecycle

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Tests cross the real Engine owner, fixed dispatcher and SessionClient port.
// Scripted diagnostic time verifies boundaries, never physical scheduler CPU.
// The separate owner-continuation test is intentionally not modified here.
type sdkAdmissionProbe struct {
	value atomic.Int64
	calls atomic.Int64
	mode  string
}

func (p *sdkAdmissionProbe) read() (int64, bool) {
	index := p.calls.Add(1)
	switch p.mode {
	case "unavailable":
		return 0, false
	case "negative":
		return -1, true
	case "regressing":
		return math.MaxInt64 - index, true
	case "zero":
		return 0, true
	default:
		return p.value.Add(1000), true
	}
}

func newSDKAdmissionFixture(t *testing.T, transport *dispatcherTestFactory, online, capacity int, parent, enabled bool, probe func() (int64, bool), yield func() (int64, bool)) (engineTestFixture, *driverTestClock) {
	t.Helper()
	f := newEngineTestFixture(t, engineTestLimits{ClientFactory: transport, OnlineUsers: online,
		SessionDuration: time.Hour, WorkCapacity: capacity, MaxWorkPerAdvance: 128,
		InflightCapacity: capacity, RetryCapacity: capacity})
	clock := &driverTestClock{base: f.clock}
	f.pool.mu.Lock()
	f.pool.onSendack, f.pool.onAsyncSendError = nil, nil
	f.pool.mu.Unlock()
	cfg := driverYieldFixtureConfig(f, clock, parent)
	cfg.EnableDriverAdmissionDiagnostics = enabled
	cfg.DriverAdmissionClockProbe = probe
	cfg.DriverYieldClockProbe = yield
	var err error
	f.engine, err = NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.engine.StartGeneration(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	return f, clock
}

func sdkAdmissionQueue(t *testing.T, f engineTestFixture, intents []TrafficIntent, due time.Time) {
	t.Helper()
	done := make(chan error, 1)
	if err := f.engine.enqueueBlocking(engineCommand{run: func() {
		for _, intent := range intents {
			if !f.pool.acquireSendLease(intent.Logical.Sender) {
				done <- errors.New("controlled sender lease unavailable")
				return
			}
			if err := f.engine.addLeasedSendWorkBefore(intent, 0, due, due.Add(100*time.Millisecond)); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func sdkAdmissionFinal(t *testing.T, f engineTestFixture) *DriverDiagnosticsSnapshot {
	t.Helper()
	if err := f.engine.Stop(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.DriverDiagnostics
}

func sdkAdmissionRequireChild(t *testing.T, snapshot *DriverDiagnosticsSnapshot, count int) *DriverSDKAdmissionSnapshot {
	t.Helper()
	if snapshot == nil || len(snapshot.Records) != 0 {
		t.Fatalf("SDK start invented or lost an owner-turn prefix: %+v", snapshot)
	}
	child := snapshot.SDKAdmission
	if child == nil || child.Version != 1 || child.Emitter != "sdk_start" || child.Code != 38 || len(child.Records) != count {
		t.Fatalf("semantic RED: original SDK code38 lacks bounded exact-emitter phases: %+v", child)
	}
	return child
}

func TestDriverSDKAdmissionRealSameUIDMissKeepsOriginalDueAndNativeGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		probe := &sdkAdmissionProbe{}
		f, _ := newSDKAdmissionFixture(t, transport, 1, 32, true, true, probe.read, nil)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		at := f.clock.Now()
		first := f.intent(t, uid, "sdk-admission-a", 10001, TrafficGroup)
		second := f.intent(t, uid, "sdk-admission-a", 10002, TrafficGroup)
		gate := transport.hold(first.Logical.ClientMsgNo, false)
		sdkAdmissionQueue(t, f, []TrafficIntent{first, second}, at)
		f.clock.Set(at.Add(20 * time.Millisecond))
		if _, err := f.engine.AdvanceContext(context.Background(), at.Add(20*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if entries := transport.entries(); len(entries) != 1 || entries[0] != first.Logical.ClientMsgNo {
			t.Fatalf("same UID FIFO control did not hold second ticket: %v", entries)
		}
		live, err := f.engine.WorkerRuntimeSnapshotContext(context.Background())
		if err != nil || live.DriverDiagnostics != nil {
			t.Fatal("active SDK diagnostic escaped final-only contract")
		}
		probe.value.Add(int64(81 * time.Millisecond))
		f.clock.Set(at.Add(101 * time.Millisecond))
		gate.open()
		synctest.Wait()
		waitForEngineAdmissions(t, f.engine)
		_, err = f.engine.AdvanceContext(context.Background(), at.Add(101*time.Millisecond))
		driverAssertNative(t, f, err, 1)
		if got := f.verifier.Snapshot(); got.Sent != 1 || got.Attempts != 1 || len(transport.entries()) != 1 {
			t.Fatalf("diagnostic registered the rejected ticket: %+v", got)
		}
		child := sdkAdmissionRequireChild(t, sdkAdmissionFinal(t, f), 1)
		r := child.Records[0]
		if r.Ordinal != 1 || r.KnownMask != 15 || r.SampledAfterDue != 20*time.Millisecond || r.DeadlineAfterDue != 100*time.Millisecond || r.FreshAfterDue != 101*time.Millisecond || r.QueuedToPickup < 81*time.Millisecond || r.PickupToOffer <= 0 || r.OfferToOwner <= 0 {
			t.Fatalf("wrong original due, phase ownership, or unknown-as-zero: %+v", r)
		}
		child.Records[0].QueuedToPickup = 999
		again, err := f.engine.WorkerRuntimeSnapshotContext(context.Background())
		if err != nil || again.DriverDiagnostics == nil || again.DriverDiagnostics.SDKAdmission == nil || again.DriverDiagnostics.SDKAdmission.Records[0].QueuedToPickup == 999 {
			t.Fatal("actual final caller mutated retained SDK evidence")
		}
		if err := f.engine.StartGeneration(context.Background(), 2); err != nil {
			t.Fatal(err)
		}
		fresh := sdkAdmissionFinal(t, f)
		if fresh == nil || fresh.Generation != 2 || (fresh.SDKAdmission != nil && (len(fresh.SDKAdmission.Records) != 0 || fresh.SDKAdmission.Dropped != 0)) {
			t.Fatal("previous SDK records survived generation join/reset")
		}
	})
}

func TestDriverSDKAdmissionFixedExecutorWaitRetainsCredit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		probe := &sdkAdmissionProbe{}
		f, _ := newSDKAdmissionFixture(t, transport, engineAdmissionWorkers+1, 64, true, true, probe.read, nil)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		at := f.clock.Now()
		intents := make([]TrafficIntent, engineAdmissionWorkers+1)
		for i := range intents {
			uid := f.identity.UID(uint64(i))
			dispatcherLogin(t, f, uid, uint64(i))
			intents[i] = f.intent(t, uid, "sdk-fixed-workers", uint64(11000+i), TrafficGroup)
			if i < engineAdmissionWorkers {
				transport.hold(intents[i].Logical.ClientMsgNo, false)
			}
		}
		sdkAdmissionQueue(t, f, intents, at)
		if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if len(transport.entries()) != engineAdmissionWorkers {
			t.Fatalf("fixed executor control admits %d, want%d", len(transport.entries()), engineAdmissionWorkers)
		}
		snapshot, err := f.engine.Snapshot()
		if err != nil || snapshot.AdmissionPending != engineAdmissionWorkers+1 || snapshot.QueuePeak > snapshot.QueueCapacity {
			t.Fatalf("queued/executing credit changed: %+v %v", snapshot, err)
		}
		probe.value.Add(int64(101 * time.Millisecond))
		f.clock.Set(at.Add(101 * time.Millisecond))
		transport.releaseAll()
		synctest.Wait()
		waitForEngineAdmissions(t, f.engine)
		_, err = f.engine.AdvanceContext(context.Background(), at.Add(101*time.Millisecond))
		driverAssertNative(t, f, err, 1)
		child := sdkAdmissionRequireChild(t, sdkAdmissionFinal(t, f), 1)
		if child.Records[0].QueuedToPickup < 101*time.Millisecond || len(transport.entries()) != engineAdmissionWorkers {
			t.Fatal("new observation bypassed the fixed pool or hid its wait")
		}
	})
}

func TestDriverSDKAdmissionOfferToOwnerIncludesHandoffAndPublishesBeforeOffer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		probe := &sdkAdmissionProbe{}
		f, _ := newSDKAdmissionFixture(t, transport, 1, 32, true, true, probe.read, nil)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		at := f.clock.Now()
		intent := f.intent(t, uid, "sdk-owner-handoff", 12001, TrafficGroup)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		open := func() { once.Do(func() { close(release) }) }
		defer open()
		if err := f.engine.enqueueBlocking(engineCommand{run: func() {
			if !f.pool.acquireSendLease(uid) {
				t.Error("sender lease unavailable")
				close(entered)
				return
			}
			// This is the real processWork->queueAttempt path on the real owner.
			if err := f.engine.processWork(context.Background(), &engineWork{kind: engineWorkSend, intent: intent, due: at, dispatchDeadline: at.Add(100 * time.Millisecond)}, at); err != nil {
				t.Error(err)
			}
			close(entered)
			<-release
		}}); err != nil {
			t.Fatal(err)
		}
		<-entered
		synctest.Wait()
		if len(f.engine.commands) == 0 {
			t.Fatal("real SDK executor did not offer its start command behind the owner")
		}
		probe.value.Add(int64(101 * time.Millisecond))
		f.clock.Set(at.Add(101 * time.Millisecond))
		open()
		synctest.Wait()
		waitForEngineAdmissions(t, f.engine)
		_, err := f.engine.AdvanceContext(context.Background(), at.Add(101*time.Millisecond))
		driverAssertNative(t, f, err, 1)
		child := sdkAdmissionRequireChild(t, sdkAdmissionFinal(t, f), 1)
		if r := child.Records[0]; r.KnownMask != 15 || r.OfferToOwner < 101*time.Millisecond || r.QueuedToPickup < 0 || r.PickupToOffer <= 0 || len(transport.entries()) != 0 {
			t.Fatalf("handoff missing or owner observed post-publication mutation: %+v", r)
		}
	})
}

func TestDriverSDKAdmissionInvalidAndZeroProbeNeverChangeNativeCode38(t *testing.T) {
	for _, mode := range []string{"unavailable", "negative", "regressing", "zero"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				transport := newDispatcherTestFactory()
				probe := &sdkAdmissionProbe{mode: mode}
				f, _ := newSDKAdmissionFixture(t, transport, 1, 32, true, true, probe.read, nil)
				defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
				uid := f.identity.UID(0)
				dispatcherLogin(t, f, uid, 0)
				at := f.clock.Now()
				a := f.intent(t, uid, "sdk-unknown", 13001, TrafficGroup)
				b := f.intent(t, uid, "sdk-unknown", 13002, TrafficGroup)
				gate := transport.hold(a.Logical.ClientMsgNo, false)
				sdkAdmissionQueue(t, f, []TrafficIntent{a, b}, at)
				if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				f.clock.Set(at.Add(101 * time.Millisecond))
				gate.open()
				synctest.Wait()
				waitForEngineAdmissions(t, f.engine)
				_, err := f.engine.AdvanceContext(context.Background(), at.Add(101*time.Millisecond))
				driverAssertNative(t, f, err, 1)
				row := sdkAdmissionRequireChild(t, sdkAdmissionFinal(t, f), 1).Records[0]
				want := uint8(8)
				if mode == "zero" {
					want = 15
				}
				if row.KnownMask != want || row.QueuedToPickup != 0 || row.PickupToOffer != 0 || row.OfferToOwner != 0 {
					t.Fatalf("unknown normalized into measured zero or valid zero lost: %+v", row)
				}
			})
		})
	}
}

func TestDriverSDKAdmissionCapDoesNotConsumeOldYieldProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		transport.base.autoAck = false
		probe := &sdkAdmissionProbe{}
		var yieldCalls atomic.Int64
		yield := func() (int64, bool) { return yieldCalls.Add(1), true }
		f, _ := newSDKAdmissionFixture(t, transport, 1, 128, true, true, probe.read, yield)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		at := f.clock.Now()
		intents := make([]TrafficIntent, 20)
		for i := range intents {
			intents[i] = f.intent(t, uid, "sdk-retention", uint64(14000+i), TrafficGroup)
		}
		gate := transport.hold(intents[0].Logical.ClientMsgNo, false)
		sdkAdmissionQueue(t, f, intents, at)
		if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		f.clock.Set(at.Add(101 * time.Millisecond))
		gate.open()
		synctest.Wait()
		waitForEngineAdmissions(t, f.engine)
		_, err := f.engine.AdvanceContext(context.Background(), at.Add(101*time.Millisecond))
		driverAssertNative(t, f, err, 19)
		before := probe.calls.Load()
		if yieldCalls.Load() != 0 {
			t.Fatal("admission probe consumed yield probe without an original fairness yield")
		}
		later := at.Add(200 * time.Millisecond)
		f.clock.Set(later)
		more := make([]TrafficIntent, 33)
		for i := range more {
			more[i] = f.intent(t, uid, "sdk-post-cap-control", uint64(15000+i), TrafficGroup)
		}
		sdkAdmissionQueue(t, f, more, later)
		_, latchedErr := f.engine.AdvanceContext(context.Background(), later)
		driverAssertNative(t, f, latchedErr, 19)
		synctest.Wait()
		waitForEngineAdmissions(t, f.engine)
		if probe.calls.Load() != before || yieldCalls.Load() != 2 {
			t.Fatalf("new cap changed old2reads/yield or kept admission sampling: sdk%d->%d yield%d", before, probe.calls.Load(), yieldCalls.Load())
		}
		child := sdkAdmissionRequireChild(t, sdkAdmissionFinal(t, f), 16)
		if child.Dropped != 3 {
			t.Fatalf("first16 SDK miss cap lost later misses: %+v", child)
		}
		for i, row := range child.Records {
			if row.Ordinal != uint64(i+1) {
				t.Fatal("ordinal is not bounded miss order")
			}
		}
	})
}

func TestDriverSDKAdmissionDefaultOffAndExplicitConfiguration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		transport.base.autoAck = false
		f, clock := newSDKAdmissionFixture(t, transport, 1, 32, false, false, nil, nil)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		at := f.clock.Now()
		sdkAdmissionQueue(t, f, []TrafficIntent{f.intent(t, uid, "sdk-off", 16001, TrafficGroup)}, at)
		before := clock.count()
		if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		waitForEngineAdmissions(t, f.engine)
		if got := clock.count() - before; got != 2 {
			t.Fatalf("default-off added business clock reads: %d", got)
		}
		if got := sdkAdmissionFinal(t, f); got != nil {
			t.Fatal("disabled diagnostic retained new evidence")
		}
	})
	for _, tc := range []struct{ parent, enabled, probe bool }{{false, true, false}, {false, true, true}, {true, false, true}} {
		f := newEngineTestFixture(t, engineTestLimits{OnlineUsers: 1, SessionDuration: time.Hour, WorkCapacity: 16})
		cfg := driverYieldFixtureConfig(f, f.clock, tc.parent)
		cfg.EnableDriverAdmissionDiagnostics = tc.enabled
		var reads atomic.Int64
		if tc.probe {
			cfg.DriverAdmissionClockProbe = func() (int64, bool) { return reads.Add(1), true }
		}
		if _, err := NewEngine(cfg); err == nil || reads.Load() != 0 {
			t.Fatal("incomplete opt-in constructed/sampled the diagnostic")
		}
	}
}

func TestDriverSDKAdmissionProcessWorkEmitterIsStillOwnerTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		probe := &sdkAdmissionProbe{}
		f, _ := newSDKAdmissionFixture(t, transport, 1, 32, true, true, probe.read, nil)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		at := f.clock.Now()
		sdkAdmissionQueue(t, f, []TrafficIntent{f.intent(t, uid, "sdk-owner-control", 17001, TrafficGroup)}, at)
		err := driverAdvanceAfterOwnerDelay(t, f, at, false)
		driverAssertNative(t, f, err, 1)
		final := sdkAdmissionFinal(t, f)
		if final == nil || len(final.Records) != 1 || final.Records[0].Operation != "advance" || final.Records[0].Turn == 0 || (final.SDKAdmission != nil && len(final.SDKAdmission.Records) != 0) || probe.calls.Load() != 0 {
			t.Fatal("owner code38 became a fake SDK start or consumed SDK phase clocks")
		}
	})
}

func TestDriverSDKAdmissionCancellationJoinsBeforeClientClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		probe := &sdkAdmissionProbe{}
		f, _ := newSDKAdmissionFixture(t, transport, 1, 32, true, true, probe.read, nil)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		at := f.clock.Now()
		first := f.intent(t, uid, "sdk-cancel", 18001, TrafficGroup)
		second := f.intent(t, uid, "sdk-cancel", 18002, TrafficGroup)
		gate := transport.hold(first.Logical.ClientMsgNo, false)
		sdkAdmissionQueue(t, f, []TrafficIntent{first, second}, at)
		if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		stopped := make(chan error, 1)
		go func() { stopped <- f.engine.Stop() }()
		synctest.Wait()
		select {
		case <-transport.canceled:
		default:
			t.Fatal("original executing admission was not canceled")
		}
		select {
		case <-stopped:
			t.Fatal("observation let Stop return before SDK admission joined")
		default:
		}
		if transport.closedCount() != 0 {
			t.Fatal("client closed while ticket still owns it")
		}
		gate.open()
		synctest.Wait()
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		final := sdkAdmissionFinal(t, f)
		if final == nil || len(final.Records) != 0 || (final.SDKAdmission != nil && len(final.SDKAdmission.Records) != 0) || len(transport.entries()) != 1 {
			t.Fatal("queued cancellation fabricated SDK start/code38 or lost exact join")
		}
	})
}

func TestDriverSDKAdmissionOriginalExecutionDeadlineEquality(t *testing.T) {
	for _, late := range []time.Duration{100 * time.Millisecond, 101 * time.Millisecond} {
		t.Run(late.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				transport := newDispatcherTestFactory()
				probe := &sdkAdmissionProbe{}
				f, _ := newSDKAdmissionFixture(t, transport, 1, 32, true, true, probe.read, nil)
				defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
				uid := f.identity.UID(0)
				dispatcherLogin(t, f, uid, 0)
				at := f.clock.Now()
				first := f.intent(t, uid, "sdk-equality", 19001, TrafficGroup)
				second := f.intent(t, uid, "sdk-equality", 19002, TrafficGroup)
				gate := transport.hold(first.Logical.ClientMsgNo, false)
				sdkAdmissionQueue(t, f, []TrafficIntent{first, second}, at)
				if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				// Independent phase time is intentionally unrelated to the native clock.
				probe.value.Add(int64(time.Hour))
				f.clock.Set(at.Add(late))
				gate.open()
				synctest.Wait()
				waitForEngineAdmissions(t, f.engine)
				_, err := f.engine.AdvanceContext(context.Background(), at.Add(late))
				if late == 100*time.Millisecond {
					if err != nil || len(transport.entries()) != 2 {
						t.Fatal("diagnostic time changed original strict deadline equality")
					}
					final := sdkAdmissionFinal(t, f)
					if final == nil || (final.SDKAdmission != nil && len(final.SDKAdmission.Records) != 0) {
						t.Fatal("successful equality retained a fabricated miss")
					}
				} else {
					driverAssertNative(t, f, err, 1)
					sdkAdmissionRequireChild(t, sdkAdmissionFinal(t, f), 1)
				}
			})
		})
	}
}
