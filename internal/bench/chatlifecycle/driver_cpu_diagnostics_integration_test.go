//go:build integration

package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Failure inventory was frozen in the task's FAILURE-MATRIX.md before this
// file. These tests exercise actual native dispatch branches through the
// Engine and SessionClient ports; only the independent OS-clock probe is fake.
// Root owns compilation and behavioral RED against a minimal production stub.
type driverCPUProbeScript struct {
	mu      sync.Mutex
	samples []DriverCPUClockSample
	calls   int
}

func (p *driverCPUProbeScript) read() DriverCPUClockSample {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.calls
	p.calls++
	if index >= len(p.samples) {
		return DriverCPUClockSample{}
	}
	return p.samples[index]
}

func (p *driverCPUProbeScript) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func driverCPUValidPair() (DriverCPUClockSample, DriverCPUClockSample) {
	return DriverCPUClockSample{MonotonicBeforeNanos: 1000, ProcessCPUNanos: 200, MonotonicAfterNanos: 1010, ReadMask: 7},
		DriverCPUClockSample{MonotonicBeforeNanos: 1100, ProcessCPUNanos: 250, MonotonicAfterNanos: 1110, ReadMask: 7}
}

// This is the existing driver fixture with a config-only diagnostic port. The
// CPU option explicitly depends on the old driver opt-in; its probe must
// never become an additional SessionClock read or an implicit opt-in.
func newDriverCPUFixture(t *testing.T, probe func() DriverCPUClockSample, timing bool, onSend func(*sessionFakeClock, time.Time)) (engineTestFixture, *driverTestClock, string, time.Time) {
	t.Helper()
	f := newEngineTestFixture(t, engineTestLimits{OnlineUsers: 1, SessionDuration: time.Hour, WorkCapacity: 256, MaxWorkPerAdvance: 128})
	old := f.engine
	clock := &driverTestClock{base: f.clock}
	f.pool.mu.Lock()
	f.pool.onSendack = nil
	f.pool.onAsyncSendError = nil
	f.pool.mu.Unlock()
	config := EngineConfig{
		Clock: clock, Sessions: f.pool, Schedule: f.schedule, Graph: f.graph,
		Traffic: f.traffic, Generator: old.generator, Retry: f.retry,
		Verifier: f.verifier, Evidence: f.evidence, WorkerID: 0, WorkerCount: 1,
		CommandCapacity: old.commandCapacity, WorkCapacity: old.workCapacity,
		RetryCapacity: old.retryCapacity, InflightCapacity: old.inflightCapacity,
		MaxWorkPerAdvance: old.maxWork, AttemptTimeout: old.attemptTimeout,
		ColdAttemptTimeout:        old.coldAttemptTimeout,
		ActivityEligibilityWindow: old.activityEligibilityWindow,
		EnableDriverDiagnostics:   timing, DriverCPUClockProbe: probe,
	}
	var err error
	f.engine, err = NewEngine(config)
	if err != nil {
		t.Fatal(err)
	}
	at := f.clock.Now()
	if onSend != nil {
		f.pool.factory = driverTestFactory{base: f.factory, onSend: func() { onSend(f.clock, at) }}
	}
	if err := f.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.engine.Stop() })
	uid := f.identity.UID(5)
	if _, err := f.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 5, LoginOrdinal: 1}); err != nil {
		t.Fatal(err)
	}
	return f, clock, uid, at
}

func driverCPUFinal(t *testing.T, f engineTestFixture) *DriverCPUDiagnosticsSnapshot {
	t.Helper()
	if err := f.engine.Stop(); err != nil {
		t.Fatal(err)
	}
	snapshot := f.engine.DriverCPUDiagnosticsAfterStop()
	if snapshot == nil {
		t.Fatal("actual native deadline miss lacks joined private CPU-clock observation")
	}
	return snapshot
}

func TestDriverCPUClockRetainsOneValidPairForEachNativeDispatchCode(t *testing.T) {
	for _, code := range []FailureCode{37, 38} {
		t.Run(string(rune('A'+code-37)), func(t *testing.T) {
			entry, miss := driverCPUValidPair()
			probe := &driverCPUProbeScript{samples: []DriverCPUClockSample{entry, miss}}
			f, _, uid, at := newDriverCPUFixture(t, probe.read, true, nil)
			if got := f.engine.DriverCPUDiagnosticsAfterStop(); got != nil {
				t.Fatalf("live owner storage escaped before Stop: %+v", got)
			}
			driverQueueSends(t, f, uid, at, 4)
			f.clock.Set(at.Add(101 * time.Millisecond))
			sampled := at
			if code == 37 {
				sampled = at.Add(101 * time.Millisecond)
			}
			_, err := f.engine.AdvanceContext(context.Background(), sampled)
			assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
			assertUnderDeliveryFeedbackJSON(t, f.evidence.Snapshot(), code, 4)
			if f.factory.sentCount() != 0 {
				t.Fatal("unstarted deadline-rejected SEND entered the SDK")
			}
			snapshot := driverCPUFinal(t, f)
			if snapshot.Generation != 1 || len(snapshot.Records) != 1 || snapshot.DroppedTurns != 0 {
				t.Fatalf("same-Turn misses consumed separate retention slots: %+v", snapshot)
			}
			record := snapshot.Records[0]
			if record.Turn == 0 || record.Operation != "advance" || record.Code != code || record.OwnerEntry != entry || record.Miss != miss || record.State != "known" || record.Reason != "none" || record.ProcessCPUUpperBoundNanos != 50 {
				t.Fatalf("valid numeric pair changed or was not classified: %+v", record)
			}
			if got := probe.count(); got != 2 {
				t.Fatalf("four native misses in one Turn invoked %d probes, want entry+first miss only", got)
			}
		})
	}
}

func TestDriverCPUClockInvalidPairsRemainUnknownAndKeepOriginalBounds(t *testing.T) {
	entry, miss := driverCPUValidPair()
	tests := []struct {
		name   string
		entry  DriverCPUClockSample
		miss   DriverCPUClockSample
		reason string
	}{
		{"entry_read_failed", DriverCPUClockSample{MonotonicBeforeNanos: 1000, ProcessCPUNanos: 200, MonotonicAfterNanos: 1010, ReadMask: 6}, miss, "read_failed"},
		{"process_read_failed", entry, DriverCPUClockSample{MonotonicBeforeNanos: 1100, ProcessCPUNanos: 250, MonotonicAfterNanos: 1110, ReadMask: 5}, "read_failed"},
		{"miss_read_failed", entry, DriverCPUClockSample{MonotonicBeforeNanos: 1100, ProcessCPUNanos: 250, MonotonicAfterNanos: 1110, ReadMask: 3}, "read_failed"},
		{"unknown_read_mask", entry, DriverCPUClockSample{MonotonicBeforeNanos: 1100, ProcessCPUNanos: 250, MonotonicAfterNanos: 1110, ReadMask: 15}, "read_failed"},
		{"entry_negative", DriverCPUClockSample{MonotonicBeforeNanos: -1, ProcessCPUNanos: 200, MonotonicAfterNanos: 1010, ReadMask: 7}, miss, "negative_clock"},
		{"cpu_negative", entry, DriverCPUClockSample{MonotonicBeforeNanos: 1100, ProcessCPUNanos: -1, MonotonicAfterNanos: 1110, ReadMask: 7}, "negative_clock"},
		{"entry_bracket_reversed", DriverCPUClockSample{MonotonicBeforeNanos: 1010, ProcessCPUNanos: 200, MonotonicAfterNanos: 1000, ReadMask: 7}, miss, "monotonic_regressed"},
		{"miss_bracket_reversed", entry, DriverCPUClockSample{MonotonicBeforeNanos: 1110, ProcessCPUNanos: 250, MonotonicAfterNanos: 1100, ReadMask: 7}, "monotonic_regressed"},
		{"cross_pair_regression", entry, DriverCPUClockSample{MonotonicBeforeNanos: 1005, ProcessCPUNanos: 250, MonotonicAfterNanos: 1015, ReadMask: 7}, "monotonic_regressed"},
		{"process_cpu_regression", entry, DriverCPUClockSample{MonotonicBeforeNanos: 1100, ProcessCPUNanos: 199, MonotonicAfterNanos: 1110, ReadMask: 7}, "process_cpu_regressed"},
		{"entry_bracket_too_wide", DriverCPUClockSample{MonotonicBeforeNanos: 1000, ProcessCPUNanos: 200, MonotonicAfterNanos: 1001001, ReadMask: 7}, DriverCPUClockSample{MonotonicBeforeNanos: 2000000, ProcessCPUNanos: 250, MonotonicAfterNanos: 2000010, ReadMask: 7}, "bracket_wide"},
		{"miss_bracket_too_wide", entry, DriverCPUClockSample{MonotonicBeforeNanos: 1100, ProcessCPUNanos: 250, MonotonicAfterNanos: 1001101, ReadMask: 7}, "bracket_wide"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &driverCPUProbeScript{samples: []DriverCPUClockSample{test.entry, test.miss}}
			f, _, uid, at := newDriverCPUFixture(t, probe.read, true, nil)
			driverQueueSends(t, f, uid, at, 1)
			f.clock.Set(at.Add(101 * time.Millisecond))
			_, err := f.engine.AdvanceContext(context.Background(), at)
			assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
			assertUnderDeliveryFeedbackJSON(t, f.evidence.Snapshot(), FailureCode(38), 1)
			snapshot := driverCPUFinal(t, f)
			if len(snapshot.Records) != 1 {
				t.Fatalf("invalid diagnostic lost native miss evidence: %+v", snapshot)
			}
			record := snapshot.Records[0]
			if record.State != "unknown" || record.Reason != test.reason || record.ProcessCPUUpperBoundNanos != 0 || record.OwnerEntry != test.entry || record.Miss != test.miss {
				t.Fatalf("invalid pair upgraded, changed raw bounds or retained attribution: %+v", record)
			}
		})
	}
}

func TestDriverCPUClockAcceptsBracketBoundaryAndProcessWideCPUAboveWall(t *testing.T) {
	// Independent process threads may accumulate more CPU than elapsed wall.
	// Exactly1ms is the accepted diagnostic bracket boundary, not a syscall SLA.
	entry := DriverCPUClockSample{MonotonicBeforeNanos: 1, ProcessCPUNanos: 10, MonotonicAfterNanos: 1000001, ReadMask: 7}
	miss := DriverCPUClockSample{MonotonicBeforeNanos: 1000001, ProcessCPUNanos: 2000010, MonotonicAfterNanos: 1000011, ReadMask: 7}
	probe := &driverCPUProbeScript{samples: []DriverCPUClockSample{entry, miss}}
	f, _, uid, at := newDriverCPUFixture(t, probe.read, true, nil)
	driverQueueSends(t, f, uid, at, 1)
	f.clock.Set(at.Add(101 * time.Millisecond))
	_, err := f.engine.AdvanceContext(context.Background(), at)
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	snapshot := driverCPUFinal(t, f)
	if len(snapshot.Records) != 1 || snapshot.Records[0].State != "known" || snapshot.Records[0].ProcessCPUUpperBoundNanos != 2000000 {
		t.Fatalf("process CPU was incorrectly treated as single-thread wall CPU: %+v", snapshot)
	}
}

func TestDriverCPUClockSuccessfulTurnDoesNotConsumeFailureCapacityOrReuseEntry(t *testing.T) {
	entry, miss := driverCPUValidPair()
	previous := DriverCPUClockSample{MonotonicBeforeNanos: 100, ProcessCPUNanos: 1, MonotonicAfterNanos: 101, ReadMask: 7}
	probe := &driverCPUProbeScript{samples: []DriverCPUClockSample{previous, entry, miss}}
	f, _, uid, at := newDriverCPUFixture(t, probe.read, true, nil)
	driverQueueSends(t, f, uid, at, 1)
	if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	if probe.count() != 1 {
		t.Fatal("successful Turn performed an unsolicited miss probe")
	}
	next := at.Add(time.Second)
	f.clock.Set(next)
	driverQueueSends(t, f, uid, next, 1)
	f.clock.Set(next.Add(101 * time.Millisecond))
	_, err := f.engine.AdvanceContext(context.Background(), next)
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	snapshot := driverCPUFinal(t, f)
	if len(snapshot.Records) != 1 || snapshot.DroppedTurns != 0 || snapshot.Records[0].Turn <= 1 || snapshot.Records[0].OwnerEntry != entry || snapshot.Records[0].Miss != miss || probe.count() != 3 {
		t.Fatalf("successful Turn consumed capacity or supplied a stale owner entry: snapshot=%+v probes=%d", snapshot, probe.count())
	}
}

func TestDriverCPUClockCapCountsFailedTurnsAndStopsEveryExtraProbe(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	probe := func() DriverCPUClockSample {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return DriverCPUClockSample{MonotonicBeforeNanos: int64(calls * 100), ProcessCPUNanos: int64(calls * 10), MonotonicAfterNanos: int64(calls*100 + 1), ReadMask: 7}
	}
	f, _, uid, at := newDriverCPUFixture(t, probe, true, nil)
	for turn := 0; turn < 18; turn++ {
		now := at.Add(time.Duration(turn) * time.Second)
		f.clock.Set(now)
		driverQueueSends(t, f, uid, now, 3)
		f.clock.Set(now.Add(101 * time.Millisecond))
		var err error
		if turn%2 == 0 {
			_, err = f.engine.AdvanceContext(context.Background(), now)
		} else {
			_, err = f.engine.Step(context.Background(), now, nil)
		}
		assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	}
	assertUnderDeliveryFeedbackJSON(t, f.evidence.Snapshot(), FailureCode(38), 54)
	snapshot := driverCPUFinal(t, f)
	if len(snapshot.Records) != 16 || snapshot.DroppedTurns != 2 {
		t.Fatalf("retention counted SENDs instead of distinct failed Turns: %+v", snapshot)
	}
	for i, record := range snapshot.Records {
		operation := "advance"
		if i%2 != 0 {
			operation = "step"
		}
		if record.State != "known" || record.Code != FailureCode(38) || record.Operation != operation || record.Turn == 0 || (i > 0 && record.Turn <= snapshot.Records[i-1].Turn) {
			t.Fatalf("failed Turn[%d] is missing, aliased or unordered: %+v", i, record)
		}
	}
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 32 {
		t.Fatalf("full recorder still probed: calls=%d want16 entry+first-miss pairs", got)
	}
}

func TestDriverCPUClockDefaultOffAndEnabledKeepBusinessClockReadsAndNativeEvidence(t *testing.T) {
	var readCounts [3]int
	var native [3][]byte
	// Original default-off, existing driver timing alone, then the same timing
	// configuration plus CPU observation. Only the latter two are CPU parity.
	// A controlled owner delay keeps both misses within the measured owner
	// prefix; asynchronous SDK work must not manufacture that attribution.
	for index := 0; index < 3; index++ {
		enabled := index == 2
		entry, miss := driverCPUValidPair()
		probe := &driverCPUProbeScript{samples: []DriverCPUClockSample{entry, miss}}
		var sample func() DriverCPUClockSample
		if enabled {
			sample = probe.read
		}
		f, clock, uid, at := newDriverCPUFixture(t, sample, index > 0, nil)
		driverQueueSends(t, f, uid, at, 2)
		before := clock.count()
		err := driverAdvanceAfterOwnerDelay(t, f, at, false)
		readCounts[index] = clock.count() - before
		assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
		assertUnderDeliveryFeedbackJSON(t, f.evidence.Snapshot(), FailureCode(38), 2)
		if f.factory.sentCount() != 0 {
			t.Fatalf("diagnostic changed original admitted SEND count: %d", f.factory.sentCount())
		}
		native[index], err = json.Marshal(f.evidence.Snapshot())
		if err != nil {
			t.Fatal(err)
		}
		if err := f.engine.Stop(); err != nil {
			t.Fatal(err)
		}
		cpu := f.engine.DriverCPUDiagnosticsAfterStop()
		if enabled {
			if cpu == nil || len(cpu.Records) != 1 || probe.count() != 2 {
				t.Fatalf("enabled private pair absent: snapshot=%+v calls=%d", cpu, probe.count())
			}
		} else if cpu != nil || probe.count() != 0 {
			t.Fatalf("default-off observation had effects: snapshot=%+v calls=%d", cpu, probe.count())
		}
		runtimeSnapshot, err := f.engine.WorkerRuntimeSnapshotContext(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if (runtimeSnapshot.DriverDiagnostics != nil) != (index > 0) {
			t.Fatal("CPU observation changed explicit existing driver timing configuration")
		}
		body, err := json.Marshal(runtimeSnapshot)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"driver_cpu", "drivercpu", "process_cpu_upper_bound", "processcpuupperbound", "monotonic_before", "monotonicbefore", "monotonic_after", "monotonicafter"} {
			if strings.Contains(strings.ToLower(string(body)), forbidden) {
				t.Fatalf("private CPU field escaped through original runtime wire: %s", forbidden)
			}
		}
	}
	if readCounts[0] != 2 || readCounts[1] != readCounts[2] {
		t.Fatalf("CPU observation changed clock reads: original=%d driver-only=%d driver+CPU=%d", readCounts[0], readCounts[1], readCounts[2])
	}
	if string(native[0]) != string(native[1]) || string(native[1]) != string(native[2]) || workerProtocolVersion != 15 {
		t.Fatal("optional CPU observation changed original native evidence or protocol15")
	}
}

func TestDriverCPUClockRequiresExplicitExistingDriverOptIn(t *testing.T) {
	f := newEngineTestFixture(t, engineTestLimits{OnlineUsers: 1})
	old := f.engine
	probe := &driverCPUProbeScript{}
	config := EngineConfig{
		Clock: f.clock, Sessions: f.pool, Schedule: f.schedule, Graph: f.graph,
		Traffic: f.traffic, Generator: old.generator, Retry: f.retry,
		Verifier: f.verifier, Evidence: f.evidence, WorkerID: 0, WorkerCount: 1,
		CommandCapacity: old.commandCapacity, WorkCapacity: old.workCapacity,
		RetryCapacity: old.retryCapacity, InflightCapacity: old.inflightCapacity,
		MaxWorkPerAdvance: old.maxWork, AttemptTimeout: old.attemptTimeout,
		ColdAttemptTimeout:        old.coldAttemptTimeout,
		ActivityEligibilityWindow: old.activityEligibilityWindow,
		DriverCPUClockProbe:       probe.read,
	}
	engine, err := NewEngine(config)
	if err == nil {
		t.Fatalf("CPU option silently enabled or accepted absent driver opt-in: engine=%p", engine)
	}
	if probe.count() != 0 {
		t.Fatal("invalid startup configuration invoked diagnostic OS-clock probe")
	}
}

func TestDriverCPUClockSampledDeadlineKeepsOriginalShortCircuitClockReads(t *testing.T) {
	var reads [2]int
	for index := range reads {
		entry, miss := driverCPUValidPair()
		probe := &driverCPUProbeScript{samples: []DriverCPUClockSample{entry, miss}}
		var sample func() DriverCPUClockSample
		if index == 1 {
			sample = probe.read
		}
		f, clock, uid, at := newDriverCPUFixture(t, sample, true, nil)
		driverQueueSends(t, f, uid, at, 3)
		f.clock.Set(at.Add(101 * time.Millisecond))
		before := clock.count()
		_, err := f.engine.AdvanceContext(context.Background(), at.Add(101*time.Millisecond))
		reads[index] = clock.count() - before
		assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
		assertUnderDeliveryFeedbackJSON(t, f.evidence.Snapshot(), FailureCode(37), 3)
		if err := f.engine.Stop(); err != nil {
			t.Fatal(err)
		}
	}
	if reads[0] != reads[1] {
		t.Fatalf("CPU observation bypassed native code37 short-circuit: driver-only=%d driver+CPU=%d", reads[0], reads[1])
	}
}

func TestDriverCPUClockEntryDoesNotProbeWhileCallerAwaitsOwnerAdmission(t *testing.T) {
	entry, miss := driverCPUValidPair()
	probe := &driverCPUProbeScript{samples: []DriverCPUClockSample{entry, miss}}
	f, _, uid, at := newDriverCPUFixture(t, probe.read, true, nil)
	driverQueueSends(t, f, uid, at, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
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
		t.Fatal("caller never queued the existing owner time-admission command")
	}
	if probe.count() != 0 {
		t.Fatal("caller/admission wait was mislabeled as owner-entry CPU window")
	}
	f.clock.Set(at.Add(101 * time.Millisecond))
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-done:
		assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	case <-time.After(time.Second):
		t.Fatal("Advance did not join after owner gate release")
	}
	snapshot := driverCPUFinal(t, f)
	if len(snapshot.Records) != 1 || probe.count() != 2 {
		t.Fatalf("owner-entry+miss pair absent after current owner admission: %+v probes=%d", snapshot, probe.count())
	}
}

func TestDriverCPUClockJoinedCopiesAreDetachedAndGenerationLocal(t *testing.T) {
	entry, miss := driverCPUValidPair()
	probe := &driverCPUProbeScript{samples: []DriverCPUClockSample{entry, miss}}
	f, _, uid, at := newDriverCPUFixture(t, probe.read, true, nil)
	driverQueueSends(t, f, uid, at, 1)
	f.clock.Set(at.Add(101 * time.Millisecond))
	_, err := f.engine.AdvanceContext(context.Background(), at)
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	first := driverCPUFinal(t, f)
	if len(first.Records) != 1 {
		t.Fatalf("joined record absent: %+v", first)
	}
	untouched := *first
	untouched.Records = append([]DriverCPUDeadlineMiss(nil), first.Records...)
	var readers sync.WaitGroup
	for i := 0; i < 16; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			copy := f.engine.DriverCPUDiagnosticsAfterStop()
			if copy == nil || len(copy.Records) != 1 {
				t.Error("concurrent joined read lost record")
				return
			}
			copy.Records[0].Turn = math.MaxUint64
			copy.Records[0].OwnerEntry.ProcessCPUNanos = -1
		}()
	}
	readers.Wait()
	if !reflect.DeepEqual(f.engine.DriverCPUDiagnosticsAfterStop(), &untouched) {
		t.Fatal("returned slice aliases retained generation or races other final readers")
	}
	if err := f.engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.engine.DriverCPUDiagnosticsAfterStop(); got != nil {
		t.Fatalf("previous generation leaked into running successor: %+v", got)
	}
	second := driverCPUFinal(t, f)
	if second.Generation != 2 || len(second.Records) != 0 || second.DroppedTurns != 0 || !reflect.DeepEqual(first, &untouched) {
		t.Fatalf("restart inherited capacity or modified detached old record: second=%+v old=%+v", second, first)
	}
}

func TestDriverCPUClockNeverPublishesBeforeAdmittedOwnerJoinsStop(t *testing.T) {
	entry, miss := driverCPUValidPair()
	probe := &driverCPUProbeScript{samples: []DriverCPUClockSample{entry, miss}}
	f, _, uid, at := newDriverCPUFixture(t, probe.read, true, nil)
	f.factory.sendStarted = make(chan context.Context, 1)
	f.factory.sendCanceled = make(chan struct{}, 1)
	f.factory.sendReturn = make(chan struct{})
	f.factory.sendAbort = make(chan struct{})
	var abort sync.Once
	advanceDone, stopDone := make(chan error, 1), make(chan error, 1)
	advanceJoined, stopStarted, stopJoined := false, false, false
	t.Cleanup(func() {
		abort.Do(func() { close(f.factory.sendAbort) })
		if !advanceJoined {
			select {
			case <-advanceDone:
			case <-time.After(time.Second):
				t.Error("Advance cleanup did not join")
			}
		}
		if stopStarted && !stopJoined {
			select {
			case <-stopDone:
			case <-time.After(time.Second):
				t.Error("Stop cleanup did not join")
			}
		}
	})
	driverQueueSends(t, f, uid, at, 2)
	go func() { _, err := f.engine.AdvanceContext(context.Background(), at); advanceDone <- err }()
	select {
	case <-f.factory.sendStarted:
	case <-time.After(time.Second):
		t.Fatal("actual admitted SDK was not reached")
	}
	f.clock.Set(at.Add(101 * time.Millisecond))
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
		t.Fatalf("Stop returned before owner release: %v", err)
	default:
	}
	if got := f.engine.DriverCPUDiagnosticsAfterStop(); got != nil {
		t.Fatalf("stopping owner storage was exported before join: %+v", got)
	}
	close(f.factory.sendReturn)
	select {
	case err := <-advanceDone:
		advanceJoined = true
		if err != nil && !errors.Is(err, errEngineNotRunning) && !errors.Is(err, context.Canceled) {
			var runtimeErr *RuntimeError
			if !errors.As(err, &runtimeErr) || runtimeErr.Code() != RuntimeFailureUnderDelivery {
				t.Fatalf("unexpected admitted Stop outcome: %v", err)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("Advance did not join after SDK release")
	}
	select {
	case err := <-stopDone:
		stopJoined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not join after SDK release")
	}
	if got := f.engine.DriverCPUDiagnosticsAfterStop(); got == nil || got.Generation != 1 || f.engine.activeLoops.Load() != 0 {
		t.Fatalf("joined terminal diagnostic unavailable or owner still active: %+v", got)
	}
}

func TestDriverCPUClockMaximumPrivateEnvelopeIsBoundedAndIdentityFree(t *testing.T) {
	entry := DriverCPUClockSample{MonotonicBeforeNanos: math.MaxInt64, ProcessCPUNanos: math.MaxInt64, MonotonicAfterNanos: math.MaxInt64, ReadMask: 7}
	miss := DriverCPUClockSample{MonotonicBeforeNanos: math.MaxInt64, ProcessCPUNanos: math.MaxInt64 - 1, MonotonicAfterNanos: math.MaxInt64, ReadMask: 7}
	snapshot := DriverCPUDiagnosticsSnapshot{Generation: math.MaxUint64, DroppedTurns: math.MaxUint64}
	for i := 0; i < 16; i++ {
		snapshot.Records = append(snapshot.Records, DriverCPUDeadlineMiss{
			Turn: math.MaxUint64 - uint64(15-i), Operation: "advance", Code: 38,
			OwnerEntry: entry, Miss: miss, State: "unknown", Reason: "process_cpu_regressed",
		})
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 64<<10 {
		t.Fatalf("one-worker bounded private snapshot exceeds sidecar budget: %d", len(body))
	}
	for _, forbidden := range []string{"client_msg_no", "uid", "channel", "payload", "raw_error", "actor_cpu", "pure_cpu"} {
		if strings.Contains(strings.ToLower(string(body)), forbidden) {
			t.Fatalf("private bounds contain identity or unsupported CPU attribution: %s", forbidden)
		}
	}
}
