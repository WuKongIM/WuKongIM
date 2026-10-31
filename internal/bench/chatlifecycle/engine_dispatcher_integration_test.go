//go:build integration

package chatlifecycle

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// A bounded transport pause represents local admission work, not queue-capacity
// waiting or a product ACK delay. All assertions cross Engine/SessionClient;
// the test never observes an owner queue, heap, mutex, or goroutine count.
func TestEngineDispatcherSlowSessionDoesNotBlockOtherSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		f := newDispatcherFixture(t, transport, 16)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		a, b := f.identity.UID(0), f.identity.UID(1)
		dispatcherLogin(t, f, a, 0)
		dispatcherLogin(t, f, b, 1)
		first := f.intent(t, a, "dispatcher-a", 1, TrafficGroup)
		second := f.intent(t, a, "dispatcher-a", 2, TrafficGroup)
		other := f.intent(t, b, "dispatcher-b", 3, TrafficGroup)
		gate := transport.hold(first.Logical.ClientMsgNo, false)
		at := f.clock.Now()
		for _, intent := range []TrafficIntent{first, second, other} {
			if err := f.engine.SubmitGranted(intent, at); err != nil {
				t.Fatal(err)
			}
		}
		done := dispatcherAdvance(f, at)
		synctest.Wait()
		advanceReturned := false
		select {
		case err := <-done:
			advanceReturned = true
			if err != nil {
				t.Error(err)
			}
		default:
			t.Error("Advance waits for a slow SDK call and blocks the next worker turn")
		}
		before := transport.entries()
		if !slices.Contains(before, first.Logical.ClientMsgNo) {
			t.Fatal("controlled first admission did not start")
		}
		if !slices.Contains(before, other.Logical.ClientMsgNo) {
			t.Error("unrelated session cannot submit while first session admission is paused")
		}
		if slices.Contains(before, second.Logical.ClientMsgNo) {
			t.Error("same-session second send overtook the paused first admission")
		}
		gate.open()
		synctest.Wait()
		if !advanceReturned {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		after := transport.entries()
		if len(after) != 3 || slices.Index(after, first.Logical.ClientMsgNo) >= slices.Index(after, second.Logical.ClientMsgNo) {
			t.Fatalf("admission order or exact grant count changed: %v", after)
		}
		dispatcherAssertSettled(t, f, 3)
	})
}

// The real SessionPool read loop receives the ACK before TrySend returns.
// Snapshot must still make progress; release then proves exactly-once settlement.
func TestEngineDispatcherEarlyAckDoesNotWaitForAdmissionReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		f := newDispatcherFixture(t, transport, 8)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		intent := f.intent(t, uid, "dispatcher-early-ack", 11, TrafficGroup)
		gate := transport.hold(intent.Logical.ClientMsgNo, true)
		at := f.clock.Now()
		if err := f.engine.SubmitGranted(intent, at); err != nil {
			t.Fatal(err)
		}
		advance := dispatcherAdvance(f, at)
		synctest.Wait()
		if got := f.verifier.Snapshot().Acknowledged; got != 1 {
			t.Fatalf("controlled ACK did not reach the public verifier: %d", got)
		}
		snapshot := make(chan error, 1)
		go func() {
			got, err := f.engine.SnapshotContext(context.Background())
			if err == nil && got.HarnessInvalid != 0 {
				err = errors.New("early ACK created a harness failure")
			}
			snapshot <- err
		}()
		synctest.Wait()
		responded := false
		select {
		case err := <-snapshot:
			responded = true
			if err != nil {
				t.Error(err)
			}
		default:
			t.Error("Engine owner cannot consume completion or answer Snapshot until SDK admission returns")
		}
		gate.open()
		synctest.Wait()
		if err := <-advance; err != nil {
			t.Fatal(err)
		}
		if !responded {
			if err := <-snapshot; err != nil {
				t.Fatal(err)
			}
		}
		dispatcherAssertSettled(t, f, 1)
	})
}

func TestEngineDispatcherStopJoinsAdmissionBeforeCloseAndNextGeneration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		f := newDispatcherFixture(t, transport, 8)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		intent := f.intent(t, uid, "dispatcher-stop", 21, TrafficGroup)
		gate := transport.hold(intent.Logical.ClientMsgNo, false)
		at := f.clock.Now()
		if err := f.engine.SubmitGranted(intent, at); err != nil {
			t.Fatal(err)
		}
		advance := dispatcherAdvance(f, at)
		synctest.Wait()
		stop := make(chan error, 1)
		go func() { stop <- f.engine.Stop() }()
		synctest.Wait()
		select {
		case <-transport.canceled:
		default:
			t.Error("Stop did not cancel the generation-owned admission")
		}
		select {
		case <-stop:
			t.Fatal("Stop returned before the admitted transport call joined")
		default:
		}
		if transport.closedCount() != 0 {
			t.Error("session closed while admitted transport call still used it")
		}
		if err := f.engine.StartGeneration(context.Background(), 2); err == nil {
			t.Error("next generation started before Stop joined")
		}
		gate.open()
		synctest.Wait()
		<-advance // Cancellation outcome is not a successful send.
		if err := <-stop; err != nil {
			t.Fatal(err)
		}
		stopped, err := f.engine.Snapshot()
		if err != nil || stopped.Running || stopped.ActiveLoops != 0 || transport.closedCount() != 1 {
			t.Fatalf("joined stop = %+v, %v; closes=%d", stopped, err, transport.closedCount())
		}
		if err := f.engine.StartGeneration(context.Background(), 2); err != nil {
			t.Fatal(err)
		}
		dispatcherLogin(t, f, uid, 0)
		fresh, err := f.engine.Snapshot()
		if err != nil || fresh.Generation != 2 || fresh.InflightCurrent != 0 || fresh.QueueCurrent != 0 {
			t.Fatalf("new generation retained prior admission: %+v, %v", fresh, err)
		}
		if got := f.verifier.Snapshot(); got.Sent != 0 || got.Acknowledged != 0 {
			t.Fatalf("previous generation contaminated new counters: %+v", got)
		}
		if err := f.engine.StartGeneration(context.Background(), 1); err == nil {
			t.Error("stale generation fence was accepted")
		}
	})
}

func TestEngineDispatcherRetainedWorkRejectsCapacityWithoutSending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		f := newDispatcherFixture(t, transport, 2)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		due := f.clock.Now().Add(time.Minute)
		for i := uint64(0); i < 3; i++ {
			err := f.engine.SubmitGranted(f.intent(t, uid, "dispatcher-capacity", 31+i, TrafficGroup), due)
			if i < 2 && err != nil {
				t.Fatal(err)
			}
			if i == 2 {
				assertRuntimeFailure(t, err, RuntimeFailureEngineQueueSaturated)
			}
		}
		got, err := f.engine.Snapshot()
		if err != nil || got.QueueCurrent != 2 || got.QueueCapacity != 2 || got.QueuePeak != 2 {
			t.Fatalf("retained admission escaped its declared bound: %+v, %v", got, err)
		}
		if len(transport.entries()) != 0 || f.verifier.Snapshot().Sent != 0 {
			t.Fatal("queued future work was counted as physical sending")
		}
	})
}

// Queuing is not a physical attempt. The first call deliberately occupies its
// original one-second deadline; the second must keep credit without starting a
// deadline until its own SDK call begins. SubmitGranted has no pacing deadline.
func TestEngineDispatcherQueuedAdmissionKeepsCreditAndOriginalAttemptClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		f := newDispatcherFixture(t, transport, 2)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		first := f.intent(t, uid, "dispatcher-queued-clock", 41, TrafficGroup)
		second := f.intent(t, uid, "dispatcher-queued-clock", 42, TrafficGroup)
		gate := transport.hold(first.Logical.ClientMsgNo, false)
		at := f.clock.Now()
		for _, intent := range []TrafficIntent{first, second} {
			if err := f.engine.SubmitGranted(intent, at); err != nil {
				t.Fatal(err)
			}
		}
		done := dispatcherAdvance(f, at)
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		before, err := f.engine.Snapshot()
		if err != nil || before.AdmissionPending != 2 || before.FutureCurrent != 2 || before.QueueCurrent != 1 {
			t.Fatalf("queued/running submissions lost retained work credit: %+v, %v", before, err)
		}
		if got := f.verifier.Snapshot(); got.Sent != 1 || got.Attempts != 1 || got.Acknowledged != 0 {
			t.Fatalf("queued second submission was counted before its SDK call: %+v", got)
		}
		third := f.intent(t, uid, "dispatcher-queued-clock", 43, TrafficGroup)
		assertRuntimeFailure(t, f.engine.SubmitGranted(third, at), RuntimeFailureEngineQueueSaturated)
		f.clock.Set(at.Add(2 * time.Second))
		gate.open()
		synctest.Wait()
		after, err := f.engine.Snapshot()
		if err != nil || after.AdmissionPending != 0 || after.InflightCurrent != 0 {
			t.Fatalf("admissions did not settle after release: %+v, %v", after, err)
		}
		got := f.verifier.Snapshot()
		assertFirstDeadlineCounters(t, got, 2, 2, 1, 0)
		if got.Sent != 2 || got.Acknowledged != 2 || got.RetryAttempts != 0 {
			t.Fatalf("queueing reset the first clock or duplicated the second send: %+v", got)
		}
	})
}

func TestEngineDispatcherQueuedExternalGrantKeepsOriginalDispatchDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		transport.all = &dispatcherTestGate{release: make(chan struct{})}
		f := newEngineTestFixture(t, engineTestLimits{
			ClientFactory: transport, OnlineUsers: 100, NewUsersPerDay: 250_000,
			SessionDuration: 3 * time.Hour, WorkCapacity: 8192, MaxWorkPerAdvance: 8192, InflightCapacity: 512,
		})
		if err := f.engine.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		now := f.clock.Now().Add(30 * time.Second)
		f.clock.Set(now)
		now, _ = f.bootstrapScheduledLogins(t, now)
		if _, err := f.engine.ApplyGrant(context.Background(), now, 0); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
		f.clock.Set(now)
		grant, err := f.engine.ApplyGrant(context.Background(), now, 100)
		if err != nil || grant.Snapshot.Released != 100 {
			t.Fatalf("fixed grant = %+v, %v", grant, err)
		}
		for tick := 1; tick <= 40; tick++ {
			at := now.Add(time.Duration(tick) * 10 * time.Millisecond)
			f.clock.Set(at)
			done := dispatcherAdvance(f, at)
			synctest.Wait()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatal("worker turn waited for SDK admission instead of retaining bounded work")
			}
		}
		before, err := f.engine.Snapshot()
		started := uint64(len(transport.entries()))
		queued := before.AdmissionPending - int(started)
		if err != nil || started == 0 || queued <= 0 || f.verifier.Snapshot().Sent != started {
			t.Fatalf("control did not produce queued, uncounted submissions: %+v started=%d err=%v", before, started, err)
		}
		// All admitted ticket deadlines are <= now+500ms. Moving the clock does
		// not advance another grant or send future work still in the engine heap.
		f.clock.Set(now.Add(2 * time.Second))
		transport.all.open()
		synctest.Wait()
		after, err := f.engine.Snapshot()
		if err != nil || after.AdmissionPending != 0 || after.ActivityUnderDelivered != uint64(queued) {
			t.Fatalf("expired queued credit was not explicit underdelivery: %+v queued=%d err=%v", after, queued, err)
		}
		if uint64(len(transport.entries())) != started || f.verifier.Snapshot().Sent != started {
			t.Fatal("executor rebased an expired deadline or counted queued work as a send")
		}
		if !workerEvidenceHasCode(f.evidence.Snapshot(), FailureCodeOfferedDispatchFreshDeadline) {
			t.Fatal("executor deadline rejection lost the original fresh-deadline evidence")
		}
	})
}

func TestEngineDispatcherQueuedRetryRetainsItsCapacityAndVisibility(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		transport.base.autoAck = false
		f := newEngineTestFixture(t, engineTestLimits{
			ClientFactory: transport, OnlineUsers: 2, SessionDuration: time.Hour,
			WorkCapacity: 16, InflightCapacity: 8, RetryCapacity: 1,
		})
		if err := f.engine.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		a, b := f.identity.UID(0), f.identity.UID(1)
		dispatcherLogin(t, f, a, 0)
		dispatcherLogin(t, f, b, 1)
		first := f.intent(t, a, "dispatcher-retry-a", 51, TrafficGroup)
		blocker := f.intent(t, a, "dispatcher-retry-blocker", 52, TrafficGroup)
		other := f.intent(t, b, "dispatcher-retry-b", 53, TrafficGroup)
		transport.hold(blocker.Logical.ClientMsgNo, false)
		start := f.clock.Now()
		if err := f.engine.SubmitGranted(first, start); err != nil {
			t.Fatal(err)
		}
		if _, err := f.engine.Advance(start); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		at := start.Add(500 * time.Millisecond)
		f.clock.Set(at)
		for _, intent := range []TrafficIntent{blocker, other} {
			if err := f.engine.SubmitGranted(intent, at); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.engine.Advance(at); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		firstRetry, err := f.retry.Attempt(first.Logical, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, offset := range []time.Duration{time.Second, time.Second + firstRetry.Delay} {
			f.clock.Set(start.Add(offset))
			if _, err := f.engine.Advance(f.clock.Now()); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
		}
		before, err := f.engine.Snapshot()
		if err != nil || before.AdmissionPending != 2 || len(transport.entries()) != 3 {
			t.Fatalf("control did not queue the retry behind the same-session pause: %+v, %v", before, err)
		}
		if err != nil || before.RetryQueueDepth != 1 || before.RetryQueueCapacity != 1 {
			t.Errorf("moving a retry to the executor hid its retained credit: %+v, %v", before, err)
		}
		if f.verifier.Snapshot().RetryAttempts != 0 {
			t.Fatal("queued retry became an attempt before its SDK call")
		}
		f.clock.Set(start.Add(1500 * time.Millisecond))
		_, err = f.engine.Advance(f.clock.Now())
		assertRuntimeFailure(t, err, RuntimeFailureRetryQueueSaturated)
		after, err := f.engine.Snapshot()
		if err != nil || after.RetryQueueDepth > after.RetryQueueCapacity {
			t.Fatalf("executor enlarged declared retry capacity: %+v, %v", after, err)
		}
	})
}

func TestEngineDispatcherRetryCannotOverbookWorkCredit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		transport.base.autoAck = false
		f := newDispatcherFixture(t, transport, 2)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		intent := f.intent(t, uid, "dispatcher-work-credit", 61, TrafficGroup)
		at := f.clock.Now()
		if err := f.engine.SubmitGranted(intent, at); err != nil {
			t.Fatal(err)
		}
		if _, err := f.engine.Advance(at); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		at = at.Add(time.Second)
		f.clock.Set(at)
		if _, err := f.engine.Advance(at); err != nil {
			t.Fatal(err)
		}
		for ordinal := uint64(62); ordinal < 64; ordinal++ {
			future := f.intent(t, uid, "dispatcher-work-credit", ordinal, TrafficGroup)
			if err := f.engine.SubmitGranted(future, at.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		retry, err := f.retry.Attempt(intent.Logical, 1)
		if err != nil {
			t.Fatal(err)
		}
		f.clock.Set(at.Add(retry.Delay))
		_, advanceErr := f.engine.Advance(f.clock.Now())
		synctest.Wait()
		after, err := f.engine.Snapshot()
		if err != nil || after.QueuePeak > after.QueueCapacity || after.FutureCurrent > after.QueueCapacity {
			t.Errorf("retry transfer exceeded retained work capacity: %+v, %v", after, err)
		}
		if len(transport.entries()) != 1 || f.verifier.Snapshot().RetryAttempts != 0 {
			t.Error("retry reached the SDK without retained work credit")
		}
		assertRuntimeFailure(t, advanceErr, RuntimeFailureEngineQueueSaturated)
	})
}

// A transport result can reach the real session drain before the submitting
// call returns. Its retry decision must not regain a timeout for the old attempt.
func TestEngineDispatcherEarlyAsyncErrorDoesNotRecreateAttemptTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := newDispatcherTestFactory()
		transport.base.autoAck = false
		f := newDispatcherFixture(t, transport, 8)
		defer func() { transport.releaseAll(); _ = f.engine.Stop() }()
		uid := f.identity.UID(0)
		dispatcherLogin(t, f, uid, 0)
		intent := f.intent(t, uid, "dispatcher-early-error", 71, TrafficGroup)
		gate := transport.hold(intent.Logical.ClientMsgNo, true)
		at := f.clock.Now()
		if err := f.engine.SubmitGranted(intent, at); err != nil {
			t.Fatal(err)
		}
		if _, err := f.engine.Advance(at); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		packets := transport.base.sentPackets()
		if len(packets) != 1 {
			t.Fatalf("control did not enter the SDK once: %d", len(packets))
		}
		client := transport.base.clients()[0]
		client.readErrors <- &engineFakeReadError{kind: wkproto.ReadErrorNonTerminal, clientSeq: packets[0].ClientSeq, clientMsgNo: intent.Logical.ClientMsgNo}
		<-client.readReturned
		synctest.Wait()
		before, err := f.engine.Snapshot()
		if err != nil || before.RetryQueueDepth != 1 || before.AdmissionPending != 1 {
			t.Fatalf("control did not schedule retry before SDK return: %+v, %v", before, err)
		}
		gate.open()
		synctest.Wait()
		after, err := f.engine.Snapshot()
		if err != nil || after.AdmissionPending != 0 || after.RetryQueueDepth != 1 || after.FutureCurrent != 0 {
			t.Fatalf("SDK return recreated a resolved attempt timeout: %+v, %v", after, err)
		}
	})
}

func newDispatcherFixture(t *testing.T, factory SessionClientFactory, capacity int) engineTestFixture {
	t.Helper()
	f := newEngineTestFixture(t, engineTestLimits{
		ClientFactory: factory, OnlineUsers: 2, SessionDuration: time.Hour,
		WorkCapacity: capacity, MaxWorkPerAdvance: 16, InflightCapacity: 8,
	})
	if err := f.engine.StartGeneration(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	return f
}

func dispatcherLogin(t *testing.T, f engineTestFixture, uid string, index uint64) {
	t.Helper()
	if _, err := f.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: index, LoginOrdinal: index + 1}); err != nil {
		t.Fatal(err)
	}
}

func dispatcherAdvance(f engineTestFixture, at time.Time) <-chan error {
	done := make(chan error, 1)
	go func() { _, err := f.engine.AdvanceContext(context.Background(), at); done <- err }()
	return done
}

func dispatcherAssertSettled(t *testing.T, f engineTestFixture, count uint64) {
	t.Helper()
	synctest.Wait()
	got, err := f.engine.Snapshot()
	verified := f.verifier.Snapshot()
	if err != nil || got.InflightCurrent != 0 || got.RetryQueueDepth != 0 || got.HarnessInvalid != 0 ||
		verified.Sent != count || verified.Acknowledged != count || verified.Attempts != count || verified.RetryAttempts != 0 {
		t.Fatalf("admission/ACK did not settle exactly once: engine=%+v verifier=%+v err=%v", got, verified, err)
	}
}

type dispatcherTestGate struct {
	release  chan struct{}
	once     sync.Once
	earlyAck bool
}

func (g *dispatcherTestGate) open() { g.once.Do(func() { close(g.release) }) }

// This test adapter reuses the existing fake wire client; only local admission
// scheduling varies. No timing assertions depend on real sleeps or wall speed.
type dispatcherTestFactory struct {
	base     engineFakeFactory
	mu       sync.Mutex
	gates    map[string]*dispatcherTestGate
	all      *dispatcherTestGate
	calls    []string
	closed   int
	canceled chan struct{}
}

func newDispatcherTestFactory() *dispatcherTestFactory {
	f := &dispatcherTestFactory{gates: make(map[string]*dispatcherTestGate), canceled: make(chan struct{}, 8)}
	f.base.autoAck = true
	return f
}

func (f *dispatcherTestFactory) NewSession(ctx context.Context, uid, token string) (SessionClient, error) {
	c, err := f.base.NewSession(ctx, uid, token)
	if err != nil {
		return nil, err
	}
	return &dispatcherTestClient{SessionClient: c, factory: f}, nil
}

func (f *dispatcherTestFactory) hold(message string, earlyAck bool) *dispatcherTestGate {
	f.mu.Lock()
	defer f.mu.Unlock()
	gate := &dispatcherTestGate{release: make(chan struct{}), earlyAck: earlyAck}
	f.gates[message] = gate
	return gate
}

func (f *dispatcherTestFactory) entries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *dispatcherTestFactory) closedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *dispatcherTestFactory) releaseAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, gate := range f.gates {
		gate.open()
	}
	if f.all != nil {
		f.all.open()
	}
}

type dispatcherTestClient struct {
	SessionClient
	factory *dispatcherTestFactory
}

func (c *dispatcherTestClient) TrySend(ctx context.Context, packet *frame.SendPacket) error {
	f := c.factory
	f.mu.Lock()
	f.calls = append(f.calls, packet.ClientMsgNo)
	gate := f.gates[packet.ClientMsgNo]
	if gate == nil {
		gate = f.all
	}
	f.mu.Unlock()
	if gate == nil {
		return c.SessionClient.TrySend(ctx, packet)
	}
	if gate.earlyAck {
		if err := c.SessionClient.TrySend(ctx, packet); err != nil {
			return err
		}
	}
	select {
	case <-gate.release:
	case <-ctx.Done():
		// Tests need a cancellation notification, not one retained event per
		// worker; cleanup must not wait for an observer of every canceled call.
		select {
		case f.canceled <- struct{}{}:
		default:
		}
		// A canceled local operation still owns its client until it returns.
		<-gate.release
		return ctx.Err()
	}
	if gate.earlyAck {
		return nil
	}
	return c.SessionClient.TrySend(ctx, packet)
}

func (c *dispatcherTestClient) Close() error {
	c.factory.mu.Lock()
	c.factory.closed++
	c.factory.mu.Unlock()
	return c.SessionClient.Close()
}
