//go:build integration

package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// These tests use the actual Engine owner, scheduled SEND and SessionClient
// port. A missing optional config field keeps the old source executable: RED
// must first prove unchanged native code38, not merely fail compilation.
type driverTestClock struct {
	base        *sessionFakeClock
	mu          sync.Mutex
	reads       int
	script      []time.Time
	scriptIndex int
}

func (c *driverTestClock) Now() time.Time {
	c.mu.Lock()
	c.reads++
	if c.scriptIndex < len(c.script) {
		at := c.script[c.scriptIndex]
		c.scriptIndex++
		c.mu.Unlock()
		return at
	}
	c.mu.Unlock()
	return c.base.Now()
}
func (c *driverTestClock) count() int { c.mu.Lock(); defer c.mu.Unlock(); return c.reads }

type driverTestFactory struct {
	base   *engineFakeFactory
	onSend func()
}

func (f driverTestFactory) NewSession(ctx context.Context, uid, token string) (SessionClient, error) {
	c, err := f.base.NewSession(ctx, uid, token)
	if err != nil {
		return nil, err
	}
	return driverTestClient{SessionClient: c, onSend: f.onSend}, nil
}

type driverTestClient struct {
	SessionClient
	onSend func()
}

func (c driverTestClient) TrySend(ctx context.Context, packet *frame.SendPacket) error {
	if c.onSend != nil {
		c.onSend()
	}
	return c.SessionClient.TrySend(ctx, packet)
}

type driverTestRecord struct {
	Turn                uint64        `json:"turn"`
	Operation           string        `json:"operation"`
	Code                uint8         `json:"code"`
	ClockValid          bool          `json:"clock_valid"`
	SampledAfterDue     time.Duration `json:"sampled_after_due"`
	DeadlineAfterDue    time.Duration `json:"deadline_after_due"`
	FreshAfterDue       time.Duration `json:"fresh_after_due"`
	EntryLag            time.Duration `json:"entry_lag"`
	StepLockWait        time.Duration `json:"step_lock_wait"`
	AdmissionWait       time.Duration `json:"admission_wait"`
	SessionExpiry       time.Duration `json:"session_expiry"`
	StepPreparation     time.Duration `json:"step_preparation"`
	AdvanceWait         time.Duration `json:"advance_wait"`
	VerifierExpiry      time.Duration `json:"verifier_expiry"`
	CompletionDrain     time.Duration `json:"completion_drain"`
	WorkExecution       time.Duration `json:"work_execution"`
	SDKAdmission        time.Duration `json:"sdk_admission"`
	SDKAdmissionMax     time.Duration `json:"sdk_admission_max"`
	Processed           uint64        `json:"processed"`
	Attempts            uint64        `json:"attempts"`
	CompletionInitial   uint64        `json:"completion_initial"`
	CompletionAfterWork uint64        `json:"completion_after_work"`
	CompletionFairness  uint64        `json:"completion_fairness"`
	SessionsExpired     uint64        `json:"sessions_expired"`
	CorrelationsExpired uint64        `json:"correlations_expired"`
}
type driverTestSnapshot struct {
	Generation uint64             `json:"generation"`
	Dropped    uint64             `json:"dropped"`
	Records    []driverTestRecord `json:"records"`
}

func newDriverTestFixture(t *testing.T, enabled bool, onSend func(*sessionFakeClock, time.Time)) (engineTestFixture, *driverTestClock, string, time.Time) {
	t.Helper()
	f := newEngineTestFixture(t, engineTestLimits{OnlineUsers: 1, SessionDuration: time.Hour, WorkCapacity: 256, MaxWorkPerAdvance: 128})
	old := f.engine
	clock := &driverTestClock{base: f.clock}
	f.pool.mu.Lock()
	f.pool.onSendack = nil
	f.pool.onAsyncSendError = nil
	f.pool.mu.Unlock()
	cfg := EngineConfig{Clock: clock, Sessions: f.pool, Schedule: f.schedule, Graph: f.graph, Traffic: f.traffic, Generator: old.generator, Retry: f.retry, Verifier: f.verifier, Evidence: f.evidence, WorkerID: 0, WorkerCount: 1, CommandCapacity: old.commandCapacity, WorkCapacity: old.workCapacity, RetryCapacity: old.retryCapacity, InflightCapacity: old.inflightCapacity, MaxWorkPerAdvance: old.maxWork, AttemptTimeout: old.attemptTimeout, ColdAttemptTimeout: old.coldAttemptTimeout, ActivityEligibilityWindow: old.activityEligibilityWindow}
	if field := reflect.ValueOf(&cfg).Elem().FieldByName("EnableDriverDiagnostics"); field.IsValid() {
		field.SetBool(enabled)
	}
	var err error
	f.engine, err = NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	at := f.clock.Now()
	if onSend != nil {
		f.pool.factory = driverTestFactory{base: f.factory, onSend: func() { onSend(f.clock, at) }}
	}
	if err = f.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.engine.Stop() })
	uid := f.identity.UID(5)
	if _, err = f.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 5, LoginOrdinal: 1}); err != nil {
		t.Fatal(err)
	}
	return f, clock, uid, at
}

func driverQueueSends(t *testing.T, f engineTestFixture, uid string, at time.Time, count int) {
	t.Helper()
	done := make(chan error, 1)
	if err := f.engine.enqueueBlocking(engineCommand{run: func() {
		for i := 0; i < count; i++ {
			if !f.pool.acquireSendLease(uid) {
				done <- errors.New("sender lease unavailable")
				return
			}
			intent := f.intent(t, uid, "driver-private-channel", uint64(1000+i), TrafficGroup)
			if err := f.engine.addLeasedSendWorkBefore(intent, 0, at, at.Add(100*time.Millisecond)); err != nil {
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
func driverFinal(t *testing.T, f engineTestFixture) *driverTestSnapshot {
	t.Helper()
	if err := f.engine.Stop(); err != nil {
		t.Fatal(err)
	}
	g := engineWorkerGeneration{engine: f.engine, verifier: f.verifier, evidence: f.evidence}
	s, err := g.workerSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Driver *driverTestSnapshot `json:"driver_diagnostics"`
	}
	if err = json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Driver
}
func driverAssertNative(t *testing.T, f engineTestFixture, err error, n uint64) {
	t.Helper()
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	assertUnderDeliveryFeedbackJSON(t, f.evidence.Snapshot(), FailureCode(38), n)
	t.Logf("native_code38_confirmed count=%d value=1 classification=harness_invalid", n)
}

// The existing owner gate isolates an owner-prefix miss from asynchronous SDK
// execution. Every delay is at the injected clock; the real wait only joins it.
func driverAdvanceAfterOwnerDelay(t *testing.T, f engineTestFixture, at time.Time, step bool) error {
	t.Helper()
	entered, release := blockEngineOwner(t, f.engine)
	<-entered
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	defer open()
	done := make(chan error, 1)
	go func() {
		var err error
		if step {
			_, err = f.engine.Step(context.Background(), at, nil)
		} else {
			_, err = f.engine.AdvanceContext(context.Background(), at)
		}
		done <- err
	}()
	waitForEngineQueuedCommand(t, f.engine)
	f.clock.Set(at.Add(101 * time.Millisecond))
	open()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("owner-delayed turn did not join")
		return nil
	}
}

func TestDriverDiagnosticsOwnerWaitActualCode38PrefixAndCap(t *testing.T) {
	f, _, uid, at := newDriverTestFixture(t, true, nil)
	driverQueueSends(t, f, uid, at, 18)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	if err := f.engine.enqueueBlocking(engineCommand{run: func() { close(entered); <-release }}); err != nil {
		t.Fatal(err)
	}
	<-entered
	done := make(chan error, 1)
	go func() { _, err := f.engine.AdvanceContext(context.Background(), at); done <- err }()
	deadline := time.Now().Add(time.Second)
	for len(f.engine.commands) == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if len(f.engine.commands) == 0 {
		t.Fatal("real admission command never queued behind owner gate")
	}
	f.clock.Set(at.Add(101 * time.Millisecond))
	once.Do(func() { close(release) })
	var err error
	select {
	case err = <-done:
	case <-time.After(time.Second):
		t.Fatal("Advance did not join")
	}
	driverAssertNative(t, f, err, 18)
	live, err := f.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(live)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	if raw := fields["driver_diagnostics"]; len(raw) > 0 && string(raw) != "null" {
		t.Fatal("live snapshot exported driver records")
	}
	s := driverFinal(t, f)
	if s == nil {
		t.Fatal("actual code38 lacks final same-turn driver diagnostic")
	}
	if s.Generation != 1 || len(s.Records) != 16 || s.Dropped != 2 {
		t.Fatalf("bounded diagnostics=%+v", s)
	}
	turn := s.Records[0].Turn
	for i, r := range s.Records {
		if r.Code != 38 || r.Turn != turn || r.Operation != "advance" || !r.ClockValid || r.SampledAfterDue != 0 || r.DeadlineAfterDue != 100*time.Millisecond || r.FreshAfterDue != 101*time.Millisecond || r.AdmissionWait != 101*time.Millisecond || r.Processed != uint64(i) || r.Attempts != 0 {
			t.Fatalf("miss prefix[%d]=%+v", i, r)
		}
	}
}

func TestDriverDiagnosticsAsyncSDKDoesNotClaimOwnerPrefix(t *testing.T) {
	f, _, uid, at := newDriverTestFixture(t, true, func(clock *sessionFakeClock, at time.Time) { clock.Set(at.Add(101 * time.Millisecond)) })
	driverQueueSends(t, f, uid, at, 2)
	if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	waitForEngineAdmissions(t, f.engine)
	_, err := f.engine.AdvanceContext(context.Background(), at)
	driverAssertNative(t, f, err, 1)
	if f.factory.sentCount() != 1 {
		t.Fatalf("SEND count=%d want only preceding admitted SEND", f.factory.sentCount())
	}
	s := driverFinal(t, f)
	if s == nil || len(s.Records) != 0 {
		t.Fatalf("asynchronous SDK time fabricated an owner prefix: %+v", s)
	}
}

func TestDriverDiagnosticsDefaultOffPreservesFreshClockReads(t *testing.T) {
	f, clock, uid, at := newDriverTestFixture(t, false, nil)
	driverQueueSends(t, f, uid, at, 1)
	before := clock.count()
	if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	waitForEngineAdmissions(t, f.engine)
	if reads := clock.count() - before; reads != 2 {
		t.Fatalf("disabled dispatch reads=%d, want owner fresh-check + execution-at only", reads)
	}
	if s := driverFinal(t, f); s != nil {
		t.Fatalf("disabled diagnostic=%+v", s)
	}
}

func TestDriverDiagnosticsSampledDeadlineAndEqualityRemainUnchanged(t *testing.T) {
	for _, late := range []time.Duration{100 * time.Millisecond, 101 * time.Millisecond} {
		t.Run(late.String(), func(t *testing.T) {
			f, _, uid, at := newDriverTestFixture(t, true, nil)
			driverQueueSends(t, f, uid, at, 1)
			f.clock.Set(at.Add(late))
			_, err := f.engine.AdvanceContext(context.Background(), at.Add(late))
			if late == 100*time.Millisecond {
				waitForEngineAdmissions(t, f.engine)
				if err != nil || f.factory.sentCount() != 1 {
					t.Fatalf("deadline equality err=%v sends=%d", err, f.factory.sentCount())
				}
			} else {
				assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
				assertUnderDeliveryFeedbackJSON(t, f.evidence.Snapshot(), FailureCode(37), 1)
			}
			s := driverFinal(t, f)
			if s == nil {
				t.Fatal("enabled final diagnostic absent")
			}
			if len(s.Records) != 0 {
				t.Fatalf("non-code38 records=%+v", s)
			}
		})
	}
}

func TestDriverDiagnosticsCanceledAdmissionAndGenerationReset(t *testing.T) {
	f, _, uid, at := newDriverTestFixture(t, true, nil)
	driverQueueSends(t, f, uid, at, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	if err := f.engine.enqueueBlocking(engineCommand{run: func() { close(entered); <-release }}); err != nil {
		t.Fatal(err)
	}
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := f.engine.AdvanceContext(ctx, at); done <- err }()
	deadline := time.Now().Add(time.Second)
	for len(f.engine.commands) == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled caller did not return")
	}
	once.Do(func() { close(release) })
	s := driverFinal(t, f)
	if s == nil || len(s.Records) != 0 {
		t.Fatalf("canceled generation diagnostic=%+v", s)
	}
	if err := f.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s = driverFinal(t, f)
	if s == nil || s.Generation != 2 || len(s.Records) != 0 || s.Dropped != 0 {
		t.Fatalf("fresh generation diagnostic=%+v", s)
	}
}

func TestDriverDiagnosticsFinalSnapshotsAreDetached(t *testing.T) {
	f, _, uid, at := newDriverTestFixture(t, true, nil)
	driverQueueSends(t, f, uid, at, 1)
	err := driverAdvanceAfterOwnerDelay(t, f, at, false)
	driverAssertNative(t, f, err, 1)
	if err := f.engine.Stop(); err != nil {
		t.Fatal(err)
	}
	first, err := f.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	field := reflect.ValueOf(&first).Elem().FieldByName("DriverDiagnostics")
	if !field.IsValid() || field.IsNil() {
		t.Fatal("final diagnostic absent")
	}
	records := field.Elem().FieldByName("Records")
	if records.Len() != 1 {
		t.Fatal("missing final record")
	}
	records.Index(0).FieldByName("Turn").SetUint(999)
	second, err := f.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(second)
	var wire struct {
		Driver *driverTestSnapshot `json:"driver_diagnostics"`
	}
	_ = json.Unmarshal(body, &wire)
	if wire.Driver == nil || len(wire.Driver.Records) != 1 || wire.Driver.Records[0].Turn == 999 {
		t.Fatal("caller mutated retained generation record")
	}
}

// Review coverage extension, written after initial implementation: two actual
// code38 turns in the same generation must not share IDs or prefix totals.
func TestDriverDiagnosticsDistinctTurnsResetPrefixesWithinGeneration(t *testing.T) {
	f, _, uid, at := newDriverTestFixture(t, true, nil)
	driverQueueSends(t, f, uid, at, 1)
	err := driverAdvanceAfterOwnerDelay(t, f, at, false)
	driverAssertNative(t, f, err, 1)
	secondAt := at.Add(time.Second)
	f.clock.Set(secondAt)
	queued := make(chan error, 1)
	if err := f.engine.enqueueBlocking(engineCommand{run: func() {
		if !f.pool.acquireSendLease(uid) {
			queued <- errors.New("second-turn sender lease unavailable")
			return
		}
		intent := f.intent(t, uid, "second-driver-private-channel", 2000, TrafficGroup)
		queued <- f.engine.addLeasedSendWorkBefore(intent, 0, secondAt, secondAt.Add(100*time.Millisecond))
	}}); err != nil {
		t.Fatal(err)
	}
	if err := <-queued; err != nil {
		t.Fatal(err)
	}
	err = driverAdvanceAfterOwnerDelay(t, f, secondAt, true)
	driverAssertNative(t, f, err, 2)
	s := driverFinal(t, f)
	if s == nil || s.Generation != 1 || len(s.Records) != 2 || s.Dropped != 0 {
		t.Fatalf("two-turn final=%+v", s)
	}
	first, second := s.Records[0], s.Records[1]
	if first.Turn == 0 || second.Turn <= first.Turn || first.Operation != "advance" || second.Operation != "step" {
		t.Fatalf("distinct same-generation turn IDs/operations: first=%+v second=%+v", first, second)
	}
	for i, r := range s.Records {
		if !r.ClockValid || r.Attempts != 0 || r.SDKAdmission != 0 || r.WorkExecution != 0 || r.AdmissionWait != 101*time.Millisecond || r.Processed != 0 || r.FreshAfterDue != 101*time.Millisecond || r.SampledAfterDue != 0 || r.DeadlineAfterDue != 100*time.Millisecond {
			t.Fatalf("turn %d retained another turn's prefix: %+v", i, r)
		}
	}
}

// Review regression: the existing fresh-clock observation must update the
// diagnostic watermark, without introducing another business clock read.
func TestDriverDiagnosticsExistingFreshClockRegressionStaysUnknown(t *testing.T) {
	f, clock, uid, at := newDriverTestFixture(t, true, nil)
	driverQueueSends(t, f, uid, at, 2)
	// Pin the current enabled boundary sequence at the SessionClock port. The
	// 14th/19th reads are the two unchanged native fresh checks. All preceding
	// boundaries are zero; after first fresh101ms, boundaries regress to50ms.
	script := make([]time.Time, 32)
	for i := range script {
		script[i] = at
		if i >= 14 {
			script[i] = at.Add(50 * time.Millisecond)
		}
	}
	script[13] = at.Add(101 * time.Millisecond)
	script[18] = at.Add(101 * time.Millisecond)
	clock.mu.Lock()
	clock.script = script
	clock.scriptIndex = 0
	clock.mu.Unlock()
	_, err := f.engine.AdvanceContext(context.Background(), at)
	driverAssertNative(t, f, err, 2)
	s := driverFinal(t, f)
	if s == nil || len(s.Records) != 2 {
		t.Fatalf("two-miss clock diagnostic=%+v", s)
	}
	first, second := s.Records[0], s.Records[1]
	if !first.ClockValid || first.FreshAfterDue != 101*time.Millisecond {
		t.Fatalf("first fresh observation=%+v", first)
	}
	if second.ClockValid {
		t.Fatalf("regression after existing fresh101ms to boundary50ms incorrectly stayed clock_valid: %+v", second)
	}
	if first.Turn != second.Turn || second.FreshAfterDue != 101*time.Millisecond {
		t.Fatalf("native same-turn fresh relation changed: first=%+v second=%+v", first, second)
	}
}

// Review regression: after the16th retained miss, only the original fresh
// checks for the remaining native SEND rejections may keep reading the clock.
func TestDriverDiagnosticsFullCapStopsSameTurnDiagnosticClockReads(t *testing.T) {
	var reads [2]int
	for index, count := range []int{16, 18} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f, clock, uid, at := newDriverTestFixture(t, true, nil)
			driverQueueSends(t, f, uid, at, count)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			if err := f.engine.enqueueBlocking(engineCommand{run: func() { close(entered); <-release }}); err != nil {
				t.Fatal(err)
			}
			<-entered
			before := clock.count()
			done := make(chan error, 1)
			go func() { _, err := f.engine.AdvanceContext(context.Background(), at); done <- err }()
			deadline := time.Now().Add(time.Second)
			for len(f.engine.commands) == 0 && time.Now().Before(deadline) {
				runtime.Gosched()
			}
			if len(f.engine.commands) == 0 {
				t.Fatal("admission never reached owner gate")
			}
			f.clock.Set(at.Add(101 * time.Millisecond))
			once.Do(func() { close(release) })
			select {
			case err := <-done:
				driverAssertNative(t, f, err, uint64(count))
			case <-time.After(time.Second):
				t.Fatal("Advance did not join")
			}
			reads[index] = clock.count() - before
			s := driverFinal(t, f)
			if s == nil || len(s.Records) != 16 || s.Dropped != uint64(count-16) {
				t.Fatalf("cap changed native accounting: %+v", s)
			}
		})
	}
	if extra := reads[1] - reads[0]; extra != 2 {
		t.Fatalf("same-turn cap kept diagnostic clock reads:16miss=%d 18miss=%d extra=%d, want only2 original fresh checks", reads[0], reads[1], extra)
	}
}

// Late review coverage uses the existing blocking SDK port, not a production
// hook. Caller cancellation after dispatch does not own the SDK lifetime;
// Stop must join the executor before a final diagnostic copy is available.
func TestDriverDiagnosticsCanceledAfterAdmissionStopJoinsFinalCopy(t *testing.T) {
	f, _, uid, at := newDriverTestFixture(t, true, nil)
	f.factory.sendStarted = make(chan context.Context, 1)
	f.factory.sendCanceled = make(chan struct{}, 1)
	f.factory.sendReturn = make(chan struct{})
	f.factory.sendAbort = make(chan struct{})
	var abortOnce sync.Once
	advanceDone, stopDone := make(chan error, 1), make(chan error, 1)
	advanceJoined, stopStarted, stopJoined := false, false, false
	t.Cleanup(func() {
		abortOnce.Do(func() { close(f.factory.sendAbort) })
		if !advanceJoined {
			select {
			case <-advanceDone:
			case <-time.After(time.Second):
				t.Error("admitted Advance failed to join cleanup")
			}
		}
		if stopStarted && !stopJoined {
			select {
			case <-stopDone:
			case <-time.After(time.Second):
				t.Error("Stop failed to join cleanup")
			}
		}
	})
	driverQueueSends(t, f, uid, at, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, err := f.engine.AdvanceContext(ctx, at); advanceDone <- err }()
	select {
	case <-f.factory.sendStarted:
	case <-time.After(time.Second):
		t.Fatal("actual admitted SDK was not reached")
	}
	f.clock.Set(at.Add(101 * time.Millisecond))
	cancel()
	select {
	case err := <-advanceDone:
		advanceJoined = true
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("dispatch caller result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Advance still waits for the SDK call")
	}
	stopStarted = true
	go func() { stopDone <- f.engine.Stop() }()
	select {
	case <-f.factory.sendCanceled:
	case <-time.After(time.Second):
		t.Fatal("Stop cancellation did not reach admitted SDK")
	}
	select {
	case err := <-stopDone:
		stopJoined = true
		t.Fatalf("Stop returned before admitted SDK owner joined: %v", err)
	default:
	}
	if _, err := f.engine.WorkerRuntimeSnapshotContext(context.Background()); !errors.Is(err, errEngineNotRunning) {
		t.Fatalf("final copy available before owner join: %v", err)
	}
	close(f.factory.sendReturn)
	select {
	case err := <-stopDone:
		stopJoined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not join after SDK release")
	}
	if evidence := f.evidence.Snapshot(); evidence.Classification != "" {
		t.Fatalf("generation cancellation manufactured dispatch evidence: %+v", evidence)
	}
	first := driverFinal(t, f)
	if first == nil || len(first.Records) != 0 {
		t.Fatalf("joined final prefix=%+v", first)
	}
	second := driverFinal(t, f)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) || f.engine.activeLoops.Load() != 0 {
		t.Fatal("diagnostic or owner changed after complete join")
	}
}
