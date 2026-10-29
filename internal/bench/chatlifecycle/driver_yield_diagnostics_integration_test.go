//go:build integration

package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

// These tests cross the actual Engine and SessionClient boundary. The optional
// independent monotonic probe is scripted only to verify measurement contracts;
// no scripted duration is presented as a physical scheduler reproduction.
type driverYieldProbeRead struct {
	at int64
	ok bool
}

type driverYieldProbeScript struct {
	mu     sync.Mutex
	reads  []driverYieldProbeRead
	calls  int
	onRead func(int)
}

func (p *driverYieldProbeScript) read() (int64, bool) {
	p.mu.Lock()
	index := p.calls
	p.calls++
	value := driverYieldProbeRead{at: int64(index+1) * 1000, ok: true}
	if index < len(p.reads) {
		value = p.reads[index]
	}
	onRead := p.onRead
	p.mu.Unlock()
	if onRead != nil {
		onRead(index)
	}
	return value.at, value.ok
}

func (p *driverYieldProbeScript) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func driverYieldSetProbe(config *EngineConfig, probe func() (int64, bool)) bool {
	field := reflect.ValueOf(config).Elem().FieldByName("DriverYieldClockProbe")
	if !field.IsValid() {
		return false
	}
	value := reflect.ValueOf(probe)
	if field.Kind() != reflect.Func || value.Type() != field.Type() {
		return false
	}
	field.Set(value)
	return true
}

func driverYieldFixtureConfig(f engineTestFixture, clock SessionClock, enabled bool) EngineConfig {
	old := f.engine
	return EngineConfig{
		Clock: clock, Sessions: f.pool, Schedule: f.schedule, Graph: f.graph,
		Traffic: f.traffic, Generator: old.generator, Retry: f.retry,
		Verifier: f.verifier, Evidence: f.evidence, WorkerID: 0, WorkerCount: 1,
		CommandCapacity: old.commandCapacity, WorkCapacity: old.workCapacity,
		RetryCapacity: old.retryCapacity, InflightCapacity: old.inflightCapacity,
		MaxWorkPerAdvance: old.maxWork, AttemptTimeout: old.attemptTimeout,
		ColdAttemptTimeout:        old.coldAttemptTimeout,
		ActivityEligibilityWindow: old.activityEligibilityWindow,
		EnableDriverDiagnostics:   enabled,
	}
}

func newDriverYieldFixture(t *testing.T, probe func() (int64, bool), cpu func() DriverCPUClockSample) (engineTestFixture, *driverTestClock, string, time.Time, bool) {
	t.Helper()
	f := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 1, SessionDuration: time.Hour, WorkCapacity: 1024,
		InflightCapacity: 256, RetryCapacity: 256, MaxWorkPerAdvance: 128,
	})
	clock := &driverTestClock{base: f.clock}
	f.pool.mu.Lock()
	f.pool.onSendack, f.pool.onAsyncSendError = nil, nil
	f.pool.mu.Unlock()
	config := driverYieldFixtureConfig(f, clock, true)
	config.DriverCPUClockProbe = cpu
	available := driverYieldSetProbe(&config, probe)
	var err error
	f.engine, err = NewEngine(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.engine.StartGeneration(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.engine.Stop() })
	uid := f.identity.UID(5)
	if _, err := f.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 5, LoginOrdinal: 1}); err != nil {
		t.Fatal(err)
	}
	return f, clock, uid, f.clock.Now(), available
}

func driverYieldPrimeInflight(t *testing.T, f engineTestFixture, uid string, at time.Time) {
	t.Helper()
	if err := f.engine.SubmitGranted(f.intent(t, uid, "yield-prime", 900001, TrafficGroup), at); err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	waitForEngineAdmissions(t, f.engine)
	if snapshot, err := f.engine.Snapshot(); err != nil || snapshot.InflightCurrent != 1 {
		t.Fatalf("real unacknowledged SDK attempt absent: inflight=%d err=%v", snapshot.InflightCurrent, err)
	}
}

func driverYieldQueue(t *testing.T, f engineTestFixture, uid string, at time.Time, count int) {
	t.Helper()
	result := make(chan error, 1)
	if err := f.engine.enqueueBlocking(engineCommand{run: func() {
		for i := 0; i < count; i++ {
			if !f.pool.acquireSendLease(uid) {
				result <- errors.New("yield fixture sender lease unavailable")
				return
			}
			intent := f.intent(t, uid, "yield-private-channel", uint64(10000+i), TrafficGroup)
			if err := f.engine.addLeasedSendWorkBefore(intent, 0, at, at.Add(100*time.Millisecond)); err != nil {
				result <- err
				return
			}
		}
		result <- nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

// The existing driver timing path reads first native fresh at14, then once
// per5 boundary reads, with2 more diagnostic reads after each fairness drain.
// The independent yield probe must add no SessionClock reads. This clock can
// first produce original code38 on old source before new evidence is asserted.
func driverYieldMissScript(clock *driverTestClock, at time.Time, indices []int, lateAfterFirst bool, inflight bool) {
	script := make([]time.Time, 2048)
	for i := range script {
		script[i] = at
	}
	for _, index := range indices {
		fairnessReads := 0
		if inflight {
			fairnessReads = 2 * (index / 32)
		}
		readIndex := 14 + 5*index + fairnessReads - 1
		script[readIndex] = at.Add(101 * time.Millisecond)
		if lateAfterFirst {
			for i := readIndex; i < len(script); i++ {
				script[i] = at.Add(101 * time.Millisecond)
			}
			break
		}
	}
	clock.mu.Lock()
	clock.script, clock.scriptIndex = script, 0
	clock.mu.Unlock()
}

func driverYieldFinal(t *testing.T, f engineTestFixture) driverYieldTestSnapshot {
	t.Helper()
	if err := f.engine.Stop(); err != nil {
		t.Fatal(err)
	}
	final, err := f.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return driverYieldTestDecode(t, final.DriverDiagnostics)
}

func TestDriverYieldActualCode38RetainsCompletedBoundaryPrefix(t *testing.T) {
	probe := &driverYieldProbeScript{reads: []driverYieldProbeRead{{1000, true}, {45_864_372, true}}}
	f, clock, uid, at, available := newDriverYieldFixture(t, probe.read, nil)
	driverYieldPrimeInflight(t, f, uid, at)
	driverYieldQueue(t, f, uid, at, 41)
	driverYieldMissScript(clock, at, []int{40}, true, true)
	_, err := f.engine.AdvanceContext(context.Background(), at)
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	waitForEngineAdmissions(t, f.engine)
	// Confirm native RED first, independently of optional interface availability.
	assertUnderDeliveryFeedbackJSON(t, f.evidence.Snapshot(), FailureCode(38), 41)
	t.Log("native_code38_confirmed before asserting optional yield evidence; not a new product RED")
	snapshot := driverYieldFinal(t, f)
	if len(snapshot.Records) != 1 || snapshot.Records[0].Processed != 40 {
		t.Fatalf("actual fresh-miss prefix moved: %+v", snapshot)
	}
	row := snapshot.Records[0]
	if !available || row.YieldWall == nil {
		t.Fatal("diagnostic contract RED: native code38 has no optional completed-yield wall prefix")
	}
	if *row.YieldWall != (driverYieldTestWall{Count: 1, WallNanos: 45_863_372, ClockValid: true}) || probe.count() != 2 {
		t.Fatalf("yield not measured exactly once: wall=%+v reads=%d", row.YieldWall, probe.count())
	}
	if row.Residual != 101*time.Millisecond {
		t.Fatalf("new clock rewrote old Residual instead of reporting an overlapping subspan: %v", row.Residual)
	}
}

func TestDriverYieldOriginalQuantumAndInflightControls(t *testing.T) {
	for _, prior := range []int{0, 31, 32, 63, 64} {
		for _, inflight := range []bool{false, true} {
			t.Run(fmt.Sprintf("prior%d/inflight%t", prior, inflight), func(t *testing.T) {
				probe := &driverYieldProbeScript{}
				f, clock, uid, at, available := newDriverYieldFixture(t, probe.read, nil)
				if inflight {
					driverYieldPrimeInflight(t, f, uid, at)
				}
				driverYieldQueue(t, f, uid, at, prior+1)
				driverYieldMissScript(clock, at, []int{prior}, true, inflight)
				_, err := f.engine.AdvanceContext(context.Background(), at)
				assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
				snapshot := driverYieldFinal(t, f)
				if len(snapshot.Records) != 1 || snapshot.Records[0].Processed != uint64(prior) || !available || snapshot.Records[0].YieldWall == nil {
					t.Fatalf("exact original prefix/optional boundary absent: %+v port=%t", snapshot, available)
				}
				wantCount, wantReads := uint64(0), 0
				if inflight {
					wantCount, wantReads = uint64(prior/32), 2*((prior+1)/32)
				}
				wall := snapshot.Records[0].YieldWall
				if wall.Count != wantCount || !wall.ClockValid || wall.WallNanos != int64(wantCount)*1000 || probe.count() != wantReads {
					t.Fatalf("original32/inflight or prefix-copy changed: got=%+v reads=%d wantCount=%d wantReads=%d", wall, probe.count(), wantCount, wantReads)
				}
			})
		}
	}
}

func TestDriverYieldInvalidIndependentClockStaysUnknownWithoutChangingNative(t *testing.T) {
	for _, test := range []struct {
		name  string
		reads []driverYieldProbeRead
	}{
		{"read_failed", []driverYieldProbeRead{{1000, false}, {1100, true}, {1200, true}, {1300, true}}},
		{"negative", []driverYieldProbeRead{{-1, true}, {1100, true}, {1200, true}, {1300, true}}},
		{"pair_regression", []driverYieldProbeRead{{1100, true}, {1000, true}, {1200, true}, {1300, true}}},
		{"between_pair_regression", []driverYieldProbeRead{{1000, true}, {1100, true}, {1050, true}, {1200, true}}},
		{"negative_extreme_range", []driverYieldProbeRead{{math.MinInt64, true}, {math.MaxInt64, true}, {0, true}, {1, true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			probe := &driverYieldProbeScript{reads: test.reads}
			f, clock, uid, at, available := newDriverYieldFixture(t, probe.read, nil)
			driverYieldPrimeInflight(t, f, uid, at)
			driverYieldQueue(t, f, uid, at, 65)
			driverYieldMissScript(clock, at, []int{64}, true, true)
			_, err := f.engine.AdvanceContext(context.Background(), at)
			assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
			snapshot := driverYieldFinal(t, f)
			if len(snapshot.Records) != 1 || !available || snapshot.Records[0].YieldWall == nil {
				t.Fatal("native fresh miss lost original or optional diagnostics")
			}
			row := snapshot.Records[0]
			if !row.ClockValid || row.Residual != 101*time.Millisecond || row.YieldWall.ClockValid || row.YieldWall.WallNanos != 0 || row.YieldWall.Count != 2 || probe.count() != 4 {
				t.Fatalf("independent bad clock poisoned native timing or became known: row=%+v yield=%+v reads=%d", row, row.YieldWall, probe.count())
			}
		})
	}
}

func TestDriverYieldEarlierMissCannotObserveLaterSameTurnYieldOrSnapshotMutation(t *testing.T) {
	probe := &driverYieldProbeScript{reads: []driverYieldProbeRead{{1000, true}, {1050, true}, {1100, true}, {1170, true}}}
	f, clock, uid, at, available := newDriverYieldFixture(t, probe.read, nil)
	driverYieldPrimeInflight(t, f, uid, at)
	driverYieldQueue(t, f, uid, at, 65)
	// Regressing only the business test clock after a fresh rejection lets the
	// same real turn complete another original yield before a second miss.
	driverYieldMissScript(clock, at, []int{32, 64}, false, true)
	_, err := f.engine.AdvanceContext(context.Background(), at)
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	waitForEngineAdmissions(t, f.engine)
	assertUnderDeliveryFeedbackJSON(t, f.evidence.Snapshot(), FailureCode(38), 2)
	snapshot := driverYieldFinal(t, f)
	if !available || len(snapshot.Records) != 2 || snapshot.Records[0].YieldWall == nil || snapshot.Records[1].YieldWall == nil {
		t.Fatalf("two real miss prefixes absent: %+v", snapshot)
	}
	first, second := snapshot.Records[0], snapshot.Records[1]
	if first.Turn != second.Turn || first.Processed != 32 || second.Processed != 64 || *first.YieldWall != (driverYieldTestWall{Count: 1, WallNanos: 50, ClockValid: true}) || *second.YieldWall != (driverYieldTestWall{Count: 2, WallNanos: 120, ClockValid: true}) || probe.count() != 4 {
		t.Fatalf("earlier prefix aliased later aggregate: first=%+v second=%+v", first, second)
	}
	if !first.ClockValid || second.ClockValid {
		t.Fatal("independent yield clock changed existing business-clock regression semantics")
	}
	final, err := f.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	record := reflect.ValueOf(final.DriverDiagnostics).Elem().FieldByName("Records").Index(0)
	yield := record.FieldByName("YieldWall")
	if !yield.IsValid() || yield.IsNil() {
		t.Fatal("proposed optional YieldWall field absent")
	}
	yield.Elem().FieldByName("Count").SetUint(999)
	later, err := f.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := driverYieldTestDecode(t, later.DriverDiagnostics).Records[0].YieldWall; got == nil || got.Count != 1 {
		t.Fatalf("detached final-copy mutation escaped to retained evidence: %+v", got)
	}
}

func TestDriverYieldTimingCapStopsProbeEvenWithCPUFullSentinel(t *testing.T) {
	probe := &driverYieldProbeScript{}
	var cpuMu sync.Mutex
	cpuCalls := int64(0)
	cpu := func() DriverCPUClockSample {
		cpuMu.Lock()
		defer cpuMu.Unlock()
		cpuCalls++
		return DriverCPUClockSample{MonotonicBeforeNanos: cpuCalls * 100, ProcessCPUNanos: cpuCalls * 10, MonotonicAfterNanos: cpuCalls*100 + 1, ReadMask: 7}
	}
	f, clock, uid, at, available := newDriverYieldFixture(t, probe.read, cpu)
	driverYieldQueue(t, f, uid, at, 18)
	driverYieldMissScript(clock, at, []int{0}, true, false)
	_, err := f.engine.AdvanceContext(context.Background(), at)
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	if !f.engine.driverDiagnostics.full.Load() {
		t.Fatal("existing16row timing cap did not fill")
	}
	next := at.Add(time.Second)
	clock.mu.Lock()
	clock.script, clock.scriptIndex = nil, 0
	clock.mu.Unlock()
	f.clock.Set(next)
	driverYieldPrimeInflight(t, f, uid, next)
	driverYieldQueue(t, f, uid, next, 64)
	if _, err := f.engine.AdvanceContext(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if !available || probe.count() != 0 {
		t.Fatalf("full timing recorder/CPU sentinel still sampled yield: port=%t reads=%d", available, probe.count())
	}
	final := driverYieldFinal(t, f)
	if len(final.Records) != 16 || final.Dropped != 2 {
		t.Fatalf("yield changed original cap/dropped accounting: %+v", final)
	}
}

func TestDriverYieldDefaultOffKeepsOriginalBusinessReadsNativeAndNoExport(t *testing.T) {
	f, clock, uid, at := newDriverTestFixture(t, false, nil)
	driverQueueSends(t, f, uid, at, 1)
	before := clock.count()
	if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	waitForEngineAdmissions(t, f.engine)
	if reads := clock.count() - before; reads != 2 {
		t.Fatalf("default off added business/probe clock reads: %d", reads)
	}
	if err := f.engine.Stop(); err != nil {
		t.Fatal(err)
	}
	final, err := f.engine.WorkerRuntimeSnapshotContext(context.Background())
	if err != nil || final.DriverDiagnostics != nil {
		t.Fatal("default off created optional diagnostics")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(driverYieldTestJSON(t, final), &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body["driver_diagnostics"]; exists {
		t.Fatal("default off expanded the original native wire")
	}
}

func TestDriverYieldProbeRequiresExplicitDriverDiagnostics(t *testing.T) {
	f := newEngineTestFixture(t, engineTestLimits{})
	probe := &driverYieldProbeScript{}
	config := driverYieldFixtureConfig(f, f.clock, false)
	available := driverYieldSetProbe(&config, probe.read)
	_, err := NewEngine(config)
	if !available || !errors.Is(err, errEngineConfig) || probe.count() != 0 {
		t.Fatalf("optional probe bypassed explicit driver opt-in: port=%t err=%v reads=%d", available, err, probe.count())
	}
}

func TestDriverYieldGenerationResetDoesNotRetainInvalidClockOrPrefix(t *testing.T) {
	probe := &driverYieldProbeScript{reads: []driverYieldProbeRead{{-1, true}, {100, true}}}
	f, clock, uid, at, available := newDriverYieldFixture(t, probe.read, nil)
	driverYieldPrimeInflight(t, f, uid, at)
	driverYieldQueue(t, f, uid, at, 33)
	driverYieldMissScript(clock, at, []int{32}, true, true)
	_, err := f.engine.AdvanceContext(context.Background(), at)
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	first := driverYieldFinal(t, f)
	if !available || len(first.Records) != 1 || first.Records[0].YieldWall == nil || first.Records[0].YieldWall.ClockValid {
		t.Fatal("first generation did not retain unknown yield")
	}
	clock.mu.Lock()
	clock.script, clock.scriptIndex = nil, 0
	clock.mu.Unlock()
	next := at.Add(time.Second)
	f.clock.Set(next)
	probe.mu.Lock()
	probe.reads, probe.calls = []driverYieldProbeRead{{1000, true}, {1070, true}}, 0
	probe.mu.Unlock()
	if err := f.engine.StartGeneration(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 5, LoginOrdinal: 2}); err != nil {
		t.Fatal(err)
	}
	driverYieldPrimeInflight(t, f, uid, next)
	driverYieldQueue(t, f, uid, next, 33)
	driverYieldMissScript(clock, next, []int{32}, true, true)
	_, err = f.engine.AdvanceContext(context.Background(), next)
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	second := driverYieldFinal(t, f)
	if second.Generation != 2 || second.Dropped != 0 || len(second.Records) != 1 || second.Records[0].YieldWall == nil || *second.Records[0].YieldWall != (driverYieldTestWall{Count: 1, WallNanos: 70, ClockValid: true}) || probe.count() != 2 {
		t.Fatalf("new generation inherited clock quality/count/wall: %+v reads=%d", second, probe.count())
	}
}
