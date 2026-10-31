//go:build integration

package chatlifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Clock barriers retain a fixed fake instant. They establish ordering between
// real owner work and real SDK/control commands; they do not simulate a 45ms
// delay, replace the scheduler, or consume DriverYieldClockProbe samples.
type ownerContinuationClockBarrier struct {
	at      int
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *ownerContinuationClockBarrier) open() { b.once.Do(func() { close(b.release) }) }

type ownerContinuationClock struct {
	base     SessionClock
	mu       sync.Mutex
	reads    int
	armed    bool
	barriers []*ownerContinuationClockBarrier
}

func (c *ownerContinuationClock) Now() time.Time {
	c.mu.Lock()
	var selected *ownerContinuationClockBarrier
	if c.armed {
		c.reads++
		for _, barrier := range c.barriers {
			if c.reads == barrier.at {
				selected = barrier
				break
			}
		}
	}
	c.mu.Unlock()
	if selected != nil {
		close(selected.entered)
		<-selected.release
	}
	return c.base.Now()
}

func (c *ownerContinuationClock) arm(points ...int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed, c.reads = true, 0
	for _, at := range points {
		c.barriers = append(c.barriers, &ownerContinuationClockBarrier{
			at: at, entered: make(chan struct{}), release: make(chan struct{}),
		})
	}
}

func (c *ownerContinuationClock) openAll() {
	for _, barrier := range c.barriers {
		barrier.open()
	}
}

func ownerContinuationAtBarrier(t *testing.T, c *ownerContinuationClock, index int) {
	t.Helper()
	synctest.Wait()
	select {
	case <-c.barriers[index].entered:
	default:
		t.Fatalf("real Engine did not reach fixed Clock boundary%d", c.barriers[index].at)
	}
}

func newOwnerContinuationFixture(t *testing.T, maxWork int) (engineTestFixture, *ownerContinuationClock, *dispatcherTestFactory) {
	t.Helper()
	transport := newDispatcherTestFactory()
	transport.base.autoAck = false
	f := newEngineTestFixture(t, engineTestLimits{
		ClientFactory: transport, OnlineUsers: 2, SessionDuration: time.Hour,
		CommandCapacity: 64, WorkCapacity: 512, MaxWorkPerAdvance: maxWork,
		InflightCapacity: 256, RetryCapacity: 256,
	})
	clock := &ownerContinuationClock{base: f.clock}
	// The original unused Engine registered these same real SessionPool hooks.
	// Rewire before Start, preserving the pool's independent original clock.
	f.pool.mu.Lock()
	f.pool.onSendack, f.pool.onAsyncSendError = nil, nil
	f.pool.mu.Unlock()
	config := driverYieldFixtureConfig(f, clock, false)
	var err error
	f.engine, err = NewEngine(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.engine.StartGeneration(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ownerContinuationClose(f, clock, transport) })
	for index := uint64(0); index < 2; index++ {
		dispatcherLogin(t, f, f.identity.UID(index), index)
	}
	// Keep one real unacknowledged attempt so the existing 32-SEND Gosched
	// branch remains exercised. The primer's SDK admission has already joined.
	at := f.clock.Now()
	if err := f.engine.SubmitGranted(f.intent(t, f.identity.UID(0), "owner-primer", 900001, TrafficGroup), at); err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	snapshot, err := f.engine.Snapshot()
	if err != nil || snapshot.InflightCurrent != 1 || snapshot.AdmissionPending != 0 || len(transport.entries()) != 1 {
		t.Fatalf("real primer not settled into inflight: %+v %v", snapshot, err)
	}
	return f, clock, transport
}

// Populate only the existing lease/due-work boundary, as driver deadline tests
// already do. Main work still crosses public Step/Advance/ApplyGrant and the
// unmodified real dispatcher, session client, owner loop and deadline checks.
func ownerContinuationQueue(t *testing.T, f engineTestFixture, count int) {
	t.Helper()
	intents := make([]TrafficIntent, count)
	for i := range intents {
		intents[i] = f.intent(t, f.identity.UID(1), "owner-work", uint64(10000+i), TrafficGroup)
	}
	at := f.clock.Now()
	response := make(chan error, 1)
	if err := f.engine.enqueueBlocking(engineCommand{run: func() {
		for _, intent := range intents {
			if !f.pool.acquireSendLease(intent.Logical.Sender) {
				response <- errors.New("fixture could not reserve real sender lease")
				return
			}
			if err := f.engine.addLeasedSendWorkBefore(intent, 0, at, at.Add(100*time.Millisecond)); err != nil {
				response <- err
				return
			}
		}
		response <- nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := <-response; err != nil {
		t.Fatal(err)
	}
}

type ownerContinuationResult struct {
	processed int
	grant     EngineGrantResult
	err       error
}

func ownerContinuationCall(f engineTestFixture, operation string, ctx context.Context) <-chan ownerContinuationResult {
	done := make(chan ownerContinuationResult, 1)
	go func() {
		at := f.clock.Now()
		result := ownerContinuationResult{processed: -1}
		switch operation {
		case "advance":
			result.processed, result.err = f.engine.Advance(at)
		case "advance_context":
			result.processed, result.err = f.engine.AdvanceContext(ctx, at)
		case "step":
			var step EngineStepSnapshot
			step, result.err = f.engine.Step(ctx, at, nil)
			result.processed = step.Advanced
		case "first_grant":
			// Zero release is valid. It isolates first-grant immediate advance
			// (whose driver turn is nil) from grant routing and new credit.
			result.grant, result.err = f.engine.ApplyGrant(ctx, at, 0)
		}
		done <- result
	}()
	return done
}

func ownerContinuationClose(f engineTestFixture, clock *ownerContinuationClock, transport *dispatcherTestFactory) {
	clock.openAll()
	transport.releaseAll()
	_ = f.engine.Stop()
}

func TestEngineOwnerContinuationSDKAndSnapshotProgress(t *testing.T) {
	for _, operation := range []string{"advance", "advance_context", "step", "first_grant"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, clock, transport := newOwnerContinuationFixture(t, 128)
				defer ownerContinuationClose(f, clock, transport)
				ownerContinuationQueue(t, f, 128)
				clock.arm(32, 65, 99)
				done := ownerContinuationCall(f, operation, context.Background())
				ownerContinuationAtBarrier(t, clock, 0)
				if len(f.engine.commands) == 0 {
					t.Fatal("controlled SDK executor did not queue its real start command")
				}
				snapshotDone := make(chan error, 1)
				go func() { _, err := f.engine.SnapshotContext(context.Background()); snapshotDone <- err }()
				synctest.Wait()
				clock.barriers[0].open()
				ownerContinuationAtBarrier(t, clock, 1)
				// This is canonical clock-sample 65, not work 65: interleaved SDK
				// starts also use that same sample. At least64 due items remain.
				if len(transport.entries()) <= 1 {
					t.Error("real SDK start remains queued behind the whole logical advance")
				}
				snapshotResponded := false
				select {
				case err := <-snapshotDone:
					snapshotResponded = true
					if err != nil {
						t.Error(err)
					}
				default:
					t.Error("public Snapshot cannot progress between SEND portions")
				}
				select {
				case result := <-done:
					t.Fatalf("logical operation replied before due work completed: %+v", result)
				default:
				}
				clock.barriers[1].open()
				ownerContinuationAtBarrier(t, clock, 2)
				// One UID lane cannot call its second SDK send until its first
				// finish command has rejoined the real owner. By sample99 it
				// must progress without waiting for all128 due sends to finish.
				if len(transport.entries()) < 3 {
					t.Error("SDK finish/lane release cannot progress between SEND portions")
				}
				select {
				case result := <-done:
					t.Fatalf("logical operation replied before its remaining SENDs: %+v", result)
				default:
				}
				clock.barriers[2].open()
				synctest.Wait()
				result := <-done
				if result.err != nil || (result.processed >= 0 && result.processed != 128) || (operation == "first_grant" && !result.grant.Admitted) {
					t.Errorf("logical reply changed across owner portions: %+v", result)
				}
				if !snapshotResponded {
					if err := <-snapshotDone; err != nil {
						t.Error(err)
					}
				}
				snapshot, err := f.engine.Snapshot()
				if err != nil || snapshot.AdmissionPending != 0 || snapshot.QueueCurrent != 0 || len(transport.entries()) != 129 {
					t.Errorf("SDK finish/credit/lane did not settle: %+v %v entries%d", snapshot, err, len(transport.entries()))
				}
			})
		})
	}
}

func TestEngineOwnerContinuationRetainsTotalWorkBudget(t *testing.T) {
	for _, operation := range []string{"advance_context", "step", "first_grant"} {
		for _, maximum := range []int{33, 64, 65} {
			t.Run(fmt.Sprintf("%s/%d", operation, maximum), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					f, clock, transport := newOwnerContinuationFixture(t, maximum)
					defer ownerContinuationClose(f, clock, transport)
					ownerContinuationQueue(t, f, 96)
					result := <-ownerContinuationCall(f, operation, context.Background())
					assertRuntimeFailure(t, result.err, RuntimeFailureEngineCPUSaturated)
					if result.processed >= 0 && result.processed != maximum {
						t.Errorf("budget restarted at a portion: %+v", result)
					}
					synctest.Wait()
					snapshot, err := f.engine.Snapshot()
					if err != nil || snapshot.QueueCurrent != 96-maximum || len(transport.entries()) != 1+maximum {
						t.Errorf("original total budget/remaining work changed: %+v %v sends%d", snapshot, err, len(transport.entries()))
					}
				})
			})
		}
	}
}

func TestEngineOwnerContinuationOriginalDispatchDeadline(t *testing.T) {
	for _, delta := range []time.Duration{100 * time.Millisecond, 101 * time.Millisecond} {
		t.Run(delta.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, clock, transport := newOwnerContinuationFixture(t, 128)
				defer ownerContinuationClose(f, clock, transport)
				ownerContinuationQueue(t, f, 128)
				at := f.clock.Now()
				clock.arm(32)
				done := ownerContinuationCall(f, "advance_context", context.Background())
				ownerContinuationAtBarrier(t, clock, 0)
				f.clock.Set(at.Add(delta))
				clock.openAll()
				synctest.Wait()
				result := <-done
				if delta == 100*time.Millisecond {
					if result.err != nil || result.processed != 128 || len(transport.entries()) != 129 {
						t.Errorf("original equal deadline was rejected: %+v sends%d", result, len(transport.entries()))
					}
				} else {
					assertRuntimeFailure(t, result.err, RuntimeFailureUnderDelivery)
					if len(transport.entries()) != 1 {
						t.Errorf("late work entered physical SDK: sends%d", len(transport.entries()))
					}
				}
			})
		})
	}
}

func TestEngineOwnerContinuationCallerCancelAfterAdmission(t *testing.T) {
	for _, operation := range []string{"advance_context", "first_grant"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, clock, transport := newOwnerContinuationFixture(t, 128)
				defer ownerContinuationClose(f, clock, transport)
				ownerContinuationQueue(t, f, 128)
				clock.arm(32)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := ownerContinuationCall(f, operation, ctx)
				ownerContinuationAtBarrier(t, clock, 0)
				cancel()
				synctest.Wait()
				select {
				case result := <-done:
					t.Fatalf("admitted generation-owned operation detached on caller cancel: %+v", result)
				default:
				}
				clock.openAll()
				synctest.Wait()
				result := <-done
				if result.err != nil || (operation == "first_grant" && !result.grant.Admitted) || len(transport.entries()) != 129 {
					t.Errorf("admitted work/caller result changed: %+v entries%d", result, len(transport.entries()))
				}
			})
		})
	}
}

func TestEngineOwnerContinuationStopJoinsBeforeNextGeneration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, clock, transport := newOwnerContinuationFixture(t, 128)
		defer ownerContinuationClose(f, clock, transport)
		ownerContinuationQueue(t, f, 128)
		clock.arm(32)
		done := ownerContinuationCall(f, "advance_context", context.Background())
		ownerContinuationAtBarrier(t, clock, 0)
		stopped := make(chan error, 1)
		go func() { stopped <- f.engine.Stop() }()
		synctest.Wait()
		select {
		case err := <-stopped:
			t.Fatalf("Stop returned while owner work was held: %v", err)
		default:
		}
		if transport.closedCount() != 0 {
			t.Error("sessions closed before owned work joined")
		}
		if err := f.engine.StartGeneration(context.Background(), 2); err == nil {
			t.Error("generation2 overtook owned advance")
		}
		clock.openAll()
		synctest.Wait()
		<-done
		if err := <-stopped; err != nil {
			t.Error(err)
		}
		terminal, err := f.engine.Snapshot()
		if err != nil || terminal.Running || terminal.ActiveLoops != 0 || transport.closedCount() != 2 {
			t.Fatalf("Stop failed to join exact generation: %+v %v closes%d", terminal, err, transport.closedCount())
		}
		if err := f.engine.StartGeneration(context.Background(), 2); err != nil {
			t.Fatal(err)
		}
		fresh, err := f.engine.Snapshot()
		if err != nil || fresh.Generation != 2 || fresh.QueueCurrent != 0 || fresh.InflightCurrent != 0 || fresh.AdmissionPending != 0 || f.verifier.Snapshot().Sent != 0 {
			t.Errorf("old continuation or admission contaminated generation2: %+v %v", fresh, err)
		}
	})
}

func TestEngineOwnerContinuationCanceledStepCannotBeOvertakenByAdvance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, clock, transport := newOwnerContinuationFixture(t, 128)
		defer ownerContinuationClose(f, clock, transport)
		ownerContinuationQueue(t, f, 128)
		clock.arm(32, 65)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		step := ownerContinuationCall(f, "step", ctx)
		ownerContinuationAtBarrier(t, clock, 0)
		cancel()
		synctest.Wait()
		select {
		case result := <-step:
			if !errors.Is(result.err, context.Canceled) {
				t.Errorf("Step caller cancellation changed: %+v", result)
			}
		default:
			t.Fatal("Step caller no longer returns promptly on its existing cancel path")
		}
		second := ownerContinuationCall(f, "advance_context", context.Background())
		clock.barriers[0].open()
		// A correct second transaction waits on stepMu, which synctest does
		// not consider durably blocked. Wait for the real clock boundary
		// directly while that mutex waiter exists, then release all work.
		<-clock.barriers[1].entered
		select {
		case result := <-second:
			t.Fatalf("second advance consumed the unfinished first logical job: %+v", result)
		default:
		}
		clock.openAll()
		synctest.Wait()
		result := <-second
		if result.err != nil || result.processed != 0 || len(transport.entries()) != 129 {
			t.Errorf("canceled caller lost/duplicated owned continuation: %+v sends%d", result, len(transport.entries()))
		}
	})
}

func TestEngineOwnerContinuationProgressUnderBoundedCommandPressure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, clock, transport := newOwnerContinuationFixture(t, 128)
		defer ownerContinuationClose(f, clock, transport)
		ownerContinuationQueue(t, f, 128)
		clock.arm(32, 65)
		done := ownerContinuationCall(f, "advance_context", context.Background())
		ownerContinuationAtBarrier(t, clock, 0)
		// A finite 256-command control workload keeps one normal command queued.
		// It neither calls nor replaces any private advancement implementation.
		var observed atomic.Uint64
		var overflow atomic.Bool
		var command engineCommand
		command.run = func() {
			if observed.Add(1) < 256 {
				select {
				case f.engine.commands <- command:
				default:
					overflow.Store(true)
				}
			}
		}
		if err := f.engine.enqueue(command); err != nil {
			t.Fatal(err)
		}
		clock.barriers[0].open()
		ownerContinuationAtBarrier(t, clock, 1)
		if count := observed.Load(); count == 0 || count == 256 {
			t.Errorf("owner arbitration starved commands or exhausted all pressure before continuing: %d", count)
		}
		clock.openAll()
		synctest.Wait()
		result := <-done
		if result.err != nil || result.processed != 128 || observed.Load() != 256 || overflow.Load() {
			t.Errorf("bounded pressure lost progress/work: %+v commands%d overflow%v", result, observed.Load(), overflow.Load())
		}
	})
}

// Unlike the AdvanceContext control, this Step caller has already detached.
// The actual owner job must join before Stop changes dispatcher admission,
// independently from whether an ordinary owner barrier could pass later.
func TestEngineOwnerContinuationDetachedStepStopKeepsDispatcherUntilJobJoined(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, clock, transport := newOwnerContinuationFixture(t, 128)
		defer ownerContinuationClose(f, clock, transport)
		ownerContinuationQueue(t, f, 128)
		clock.arm(32)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		step := ownerContinuationCall(f, "step", ctx)
		ownerContinuationAtBarrier(t, clock, 0)
		if len(f.engine.commands) == 0 || len(transport.entries()) != 1 {
			t.Fatal("fixture must retain only its primer SDK call and pending executor start")
		}
		cancel()
		synctest.Wait()
		select {
		case result := <-step:
			if !errors.Is(result.err, context.Canceled) {
				t.Fatalf("Step caller did not take its existing canceled reply: %+v", result)
			}
		default:
			t.Fatal("Step caller must have exited before testing detached-job cleanup")
		}
		stopped := make(chan error, 1)
		go func() { stopped <- f.engine.Stop() }()
		synctest.Wait()
		f.engine.dispatcher.mu.Lock()
		dispatcherClosed := f.engine.dispatcher.closed
		f.engine.dispatcher.mu.Unlock()
		if dispatcherClosed {
			t.Error("Stop closed dispatcher before the detached logical owner job joined")
		}
		if transport.closedCount() != 0 || len(transport.entries()) != 1 {
			t.Error("held logical job lost its client ownership or started physical SDK work")
		}
		select {
		case err := <-stopped:
			t.Fatalf("Stop returned before its detached logical job joined: %v", err)
		default:
		}
		clock.openAll()
		synctest.Wait()
		if err := <-stopped; err != nil {
			t.Error(err)
		}
		terminal, err := f.engine.Snapshot()
		if err != nil || terminal.Running || terminal.ActiveLoops != 0 || terminal.AdmissionPending != 0 || terminal.HarnessInvalid != 0 ||
			transport.closedCount() != 2 || len(transport.entries()) != 1 {
			t.Fatalf("detached-job cleanup introduced SDK/queue failure or lost join: %+v %v closes%d SDK%d", terminal, err, transport.closedCount(), len(transport.entries()))
		}
		if err := f.engine.StartGeneration(context.Background(), 2); err != nil {
			t.Fatal(err)
		}
		fresh, err := f.engine.Snapshot()
		if err != nil || fresh.Generation != 2 || fresh.QueueCurrent != 0 || fresh.InflightCurrent != 0 || fresh.AdmissionPending != 0 || f.verifier.Snapshot().Sent != 0 {
			t.Errorf("detached prior job contaminated generation2: %+v %v", fresh, err)
		}
	})
}
