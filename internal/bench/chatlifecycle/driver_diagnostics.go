package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"math"
	"sync/atomic"
	"time"
)

const driverDiagnosticCapacity = 16

// DriverDeadlineMiss is a bounded, identity-free prefix of one real driver
// turn at the original fresh-clock dispatch rejection. Original phase durations
// share the injected SessionClock. Only work in that owner turn is covered;
// asynchronous SDK execution has no timing or CPU attribution in this prefix.
type DriverDeadlineMiss struct {
	Turn             uint64        `json:"turn"`
	Operation        string        `json:"operation"`
	Code             FailureCode   `json:"code"`
	ClockValid       bool          `json:"clock_valid"`
	SampledAfterDue  time.Duration `json:"sampled_after_due"`
	DeadlineAfterDue time.Duration `json:"deadline_after_due"`
	FreshAfterDue    time.Duration `json:"fresh_after_due"`
	EntryLag         time.Duration `json:"entry_lag"`
	StepLockWait     time.Duration `json:"step_lock_wait"`
	AdmissionWait    time.Duration `json:"admission_wait"`
	SessionExpiry    time.Duration `json:"session_expiry"`
	StepPreparation  time.Duration `json:"step_preparation"`
	AdvanceWait      time.Duration `json:"advance_wait"`
	VerifierExpiry   time.Duration `json:"verifier_expiry"`
	CompletionDrain  time.Duration `json:"completion_drain"`
	WorkExecution    time.Duration `json:"work_execution"`
	// SDKAdmission and SDKAdmissionMax retain historical synchronous subspans
	// of WorkExecution. The dispatcher leaves them zero; zero does not measure
	// or bound asynchronous SDK execution outside the owner turn.
	SDKAdmission    time.Duration `json:"sdk_admission"`
	SDKAdmissionMax time.Duration `json:"sdk_admission_max"`
	// Residual is elapsed sampled-to-fresh time outside the disjoint segments.
	Residual  time.Duration `json:"residual"`
	Processed uint64        `json:"processed"`
	// Attempts and Retries are historical synchronous calls within this prefix,
	// not global attempt totals or asynchronous dispatcher observations.
	Attempts            uint64 `json:"attempts"`
	Retries             uint64 `json:"retries"`
	CompletionInitial   uint64 `json:"completion_initial"`
	CompletionAfterWork uint64 `json:"completion_after_work"`
	CompletionFairness  uint64 `json:"completion_fairness"`
	SessionsExpired     uint64 `json:"sessions_expired"`
	// CorrelationsExpired uses the verifier's existing acknowledged-expiry count;
	// it is not a count of all popped heap entries or released attempt receipts.
	CorrelationsExpired uint64 `json:"correlations_expired"`
	// YieldWall independently measures completed Gosched calls in this prefix.
	// It overlaps Residual; absence is unknown, never observed zero or actor CPU.
	YieldWall *DriverYieldWall `json:"yield_wall,omitempty"`
}

// DriverYieldWall retains a fixed aggregate, not per-yield or per-user history.
// Invalid clocks retain Count but clear derived wall and remain unknown.
type DriverYieldWall struct {
	Count      uint64 `json:"count"`
	WallNanos  int64  `json:"wall_nanos"`
	ClockValid bool   `json:"clock_valid"`
}

// UnmarshalJSON isolates only the named optional yield child. The original
// record remains strict; a malformed child cannot erase native driver evidence.
func (r *DriverDeadlineMiss) UnmarshalJSON(encoded []byte) error {
	type plainDriverDeadlineMiss DriverDeadlineMiss
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return err
	}
	optional, exists := fields["yield_wall"]
	if exists {
		delete(fields, "yield_wall")
		var err error
		encoded, err = json.Marshal(fields)
		if err != nil {
			return err
		}
	}
	var plain plainDriverDeadlineMiss
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plain); err != nil {
		return err
	}
	// The initial json.Unmarshal already required exactly one complete value.
	*r = DriverDeadlineMiss(plain)
	r.YieldWall = nil // Only the exact named, separately validated child is used.
	if exists {
		r.YieldWall = decodeDriverYieldWall(optional, r.Processed)
	}
	return nil
}

func decodeDriverYieldWall(encoded []byte, processed uint64) *DriverYieldWall {
	var fields map[string]json.RawMessage
	if json.Unmarshal(encoded, &fields) != nil || len(fields) != 3 {
		return nil
	}
	count, hasCount := fields["count"]
	wall, hasWall := fields["wall_nanos"]
	valid, hasValid := fields["clock_valid"]
	// Struct decoding alone accepts null as zero/false. Require the original
	// scalar types before accepting an observation of zero completed yields.
	if !hasCount || !hasWall || !hasValid || bytes.Equal(bytes.TrimSpace(count), []byte("null")) ||
		bytes.Equal(bytes.TrimSpace(wall), []byte("null")) ||
		(!bytes.Equal(bytes.TrimSpace(valid), []byte("true")) && !bytes.Equal(bytes.TrimSpace(valid), []byte("false"))) {
		return nil
	}
	var result DriverYieldWall
	if json.Unmarshal(count, &result.Count) != nil || json.Unmarshal(wall, &result.WallNanos) != nil ||
		json.Unmarshal(valid, &result.ClockValid) != nil || !validDriverYieldWall(&result, processed) {
		return nil
	}
	return &result
}

func validDriverYieldWall(wall *DriverYieldWall, processed uint64) bool {
	return wall == nil || (wall.Count <= processed/completionFairnessQuantum && wall.WallNanos >= 0 &&
		((wall.Count > 0 && wall.ClockValid) || wall.WallNanos == 0))
}

// DriverDiagnosticsSnapshot is optional final-only private worker evidence.
// Missing or invalid diagnostics carry no attribution and never change gates.
type DriverDiagnosticsSnapshot struct {
	Generation uint64               `json:"generation"`
	Dropped    uint64               `json:"dropped"`
	Records    []DriverDeadlineMiss `json:"records"`
}

// engineDriverRecorder allocates only when explicitly enabled. Record storage
// is owner-only; full lets the serialized caller stop sampling without racing
// the owner. Start resets it only after the previous generation has joined.
type engineDriverRecorder struct {
	records  [driverDiagnosticCapacity]DriverDeadlineMiss
	count    int
	dropped  uint64
	nextTurn uint64 // stepMu-owned, independent of owner record appends.
	full     atomic.Bool
	// yieldProbe is a trusted, nonblocking owner-only monotonic observation.
	// It is initialized only with the existing explicit driver opt-in.
	yieldProbe func() (int64, bool)
}

// driverTurn transfers from the stepMu-serialized caller to its admitted owner
// command. It retains only numeric evidence, never a context, work or client.
type driverTurn struct {
	record  DriverDeadlineMiss
	sampled time.Time
	last    time.Time
	// These optional numeric fields transfer to the command owner together
	// with the Turn. Only that owner reads or mutates them after admission.
	cpuOwnerEntry DriverCPUClockSample
	cpuMissSeen   bool
	// Value storage avoids allocating for successful turns or each yield.
	yieldWall    DriverYieldWall
	yieldLast    int64
	yieldHasLast bool
}

func newEngineDriverRecorder(probe func() (int64, bool)) *engineDriverRecorder {
	if probe == nil {
		origin := time.Now()
		probe = func() (int64, bool) { return time.Since(origin).Nanoseconds(), true }
	}
	return &engineDriverRecorder{yieldProbe: probe}
}

func (e *Engine) beginDriverTurn(sampled time.Time, operation string) *driverTurn {
	if e.driverDiagnostics == nil {
		return nil
	}
	if e.driverDiagnostics.full.Load() {
		if e.driverCPURecorder == nil {
			return nil
		}
		// CPU observations retain failed Turns, while timing retains misses.
		// Continue only the anonymous identity after the timing cap; every
		// driver timing helper already short-circuits without clock reads.
		// Even after the CPU cap, this sentinel counts dropped Turns once.
		return &driverTurn{record: DriverDeadlineMiss{Operation: operation}}
	}
	at := e.clock.Now()
	t := &driverTurn{record: DriverDeadlineMiss{Operation: operation, ClockValid: true}, sampled: sampled, last: at, yieldWall: DriverYieldWall{ClockValid: true}}
	t.record.EntryLag = t.duration(sampled, at)
	return t
}

// beginDriverYield and endDriverYield surround the unchanged Gosched call.
// They never read SessionClock or change fairness, deadlines or owner work.
func (e *Engine) beginDriverYield(t *driverTurn) (int64, bool) {
	if t == nil || e.driverDiagnostics.full.Load() {
		return 0, false
	}
	return e.driverDiagnostics.yieldProbe()
}

func (e *Engine) endDriverYield(t *driverTurn, before int64, beforeOK bool) {
	if t == nil || e.driverDiagnostics.full.Load() {
		return
	}
	after, afterOK := e.driverDiagnostics.yieldProbe()
	wall := &t.yieldWall
	if wall.Count == math.MaxUint64 {
		wall.ClockValid = false
	} else {
		wall.Count++
	}
	if !beforeOK || !afterOK || before < 0 || after < before || (t.yieldHasLast && before < t.yieldLast) {
		wall.ClockValid = false
	}
	t.yieldLast, t.yieldHasLast = after, true
	if wall.ClockValid {
		delta := after - before
		if wall.WallNanos > math.MaxInt64-delta {
			wall.ClockValid = false
		} else {
			wall.WallNanos += delta
		}
	}
	if !wall.ClockValid {
		wall.WallNanos = 0 // Unknown, not a measured zero interval.
	}
}

// driverNow samples only an enabled turn and flags regressions without changing
// the Engine's business clock validation or fabricating a negative duration.
func (e *Engine) driverNow(t *driverTurn) time.Time {
	if t == nil || e.driverDiagnostics.full.Load() {
		return time.Time{}
	}
	at := e.clock.Now()
	if at.Before(t.last) {
		t.record.ClockValid = false
	}
	t.last = at
	return at
}
func (t *driverTurn) duration(from, to time.Time) time.Duration {
	if to.Before(from) {
		t.record.ClockValid = false
		return 0
	}
	return to.Sub(from)
}
func (e *Engine) driverElapsed(t *driverTurn, from time.Time) time.Duration {
	if t == nil || e.driverDiagnostics.full.Load() {
		return 0
	}
	return t.duration(from, e.driverNow(t))
}
func (e *Engine) driverLockAcquired(t *driverTurn) {
	if t == nil {
		return
	}
	e.driverDiagnostics.nextTurn++
	t.record.Turn = e.driverDiagnostics.nextTurn
	t.record.StepLockWait = e.driverElapsed(t, t.last)
}

// recordDriverMiss runs only on the owner at the unchanged fresh-clock branch.
// Its copy is the prefix before this SEND's rejection and settlement.
func (e *Engine) recordDriverMiss(work *engineWork, sampled, fresh time.Time) {
	d := e.driverDiagnostics
	if d == nil {
		return
	}
	if d.count == driverDiagnosticCapacity {
		if d.dropped < math.MaxUint64 {
			d.dropped++
		}
		return
	}
	t := e.driverTurn
	if t == nil {
		return
	} // Only Step/Advance turns can carry this phased attribution.
	r := t.record
	yieldWall := t.yieldWall
	r.YieldWall = &yieldWall // Freeze only completed calls before this miss.
	r.Code = FailureCodeOfferedDispatchFreshDeadline
	r.SampledAfterDue = t.duration(work.due, sampled)
	r.DeadlineAfterDue = t.duration(work.due, work.dispatchDeadline)
	r.FreshAfterDue = t.duration(work.due, fresh)
	if fresh.Before(t.last) {
		t.record.ClockValid = false
	}
	// Reuse this already-observed business read as the regression watermark;
	// phase start times remain their separate local values.
	t.last = fresh
	r.ClockValid = t.record.ClockValid
	accounted := r.EntryLag + r.StepLockWait + r.AdmissionWait + r.SessionExpiry + r.StepPreparation + r.AdvanceWait + r.VerifierExpiry + r.CompletionDrain + r.WorkExecution
	elapsed := t.duration(t.sampled, fresh)
	if accounted > elapsed {
		r.ClockValid = false
	} else {
		r.Residual = elapsed - accounted
	}
	d.records[d.count] = r
	d.count++
	if d.count == driverDiagnosticCapacity {
		d.full.Store(true)
	}
}

// finalDriverDiagnostics is called under lifecycleMu only after the complete
// Engine/worker join. Each caller receives a detached final record slice.
func (e *Engine) finalDriverDiagnostics() *DriverDiagnosticsSnapshot {
	if e.driverDiagnostics == nil {
		return nil
	}
	d := e.driverDiagnostics
	return copyDriverDiagnostics(&DriverDiagnosticsSnapshot{Generation: e.generation, Dropped: d.dropped, Records: d.records[:d.count]})
}

// copyDriverDiagnostics detaches final records and their optional children.
// Invalid yield data is independently omitted without erasing original rows.
func copyDriverDiagnostics(d *DriverDiagnosticsSnapshot) *DriverDiagnosticsSnapshot {
	if d == nil {
		return nil
	}
	out := *d
	out.Records = append([]DriverDeadlineMiss{}, d.Records...)
	for index := range out.Records {
		record := &out.Records[index]
		if !validDriverYieldWall(record.YieldWall, record.Processed) {
			record.YieldWall = nil
		} else if record.YieldWall != nil {
			wall := *record.YieldWall
			record.YieldWall = &wall
		}
	}
	return &out
}

type driverDrainLocation uint8

const (
	driverDrainInitial driverDrainLocation = iota
	driverDrainAfterWork
	driverDrainFairness
)

// drainDriverCompletions observes the same bounded owner share with diagnostics
// enabled or disabled; observation cannot change dispatch or ACK fairness.
func (e *Engine) drainDriverCompletions(t *driverTurn, location driverDrainLocation) {
	if t == nil || e.driverDiagnostics.full.Load() {
		e.drainCompletions()
		return
	}
	started := e.driverNow(t)
	count := e.drainCompletionBatch(completionFairnessQuantum)
	t.record.CompletionDrain += e.driverElapsed(t, started)
	switch location {
	case driverDrainInitial:
		t.record.CompletionInitial += count
	case driverDrainAfterWork:
		t.record.CompletionAfterWork += count
	case driverDrainFairness:
		t.record.CompletionFairness += count
	}
}

func validDriverDiagnostics(d *DriverDiagnosticsSnapshot, generation uint64) bool {
	if d == nil {
		return true
	}
	if d.Generation != generation || len(d.Records) > driverDiagnosticCapacity {
		return false
	}
	var previous uint64
	for _, r := range d.Records {
		if r.Turn == 0 || r.Turn < previous || (r.Operation != "advance" && r.Operation != "step") || r.Code != FailureCodeOfferedDispatchFreshDeadline {
			return false
		}
		if r.ClockValid && (r.SampledAfterDue < 0 || r.SampledAfterDue > r.DeadlineAfterDue || r.DeadlineAfterDue != grantDispatchTolerance || r.FreshAfterDue <= r.DeadlineAfterDue || r.EntryLag < 0 || r.StepLockWait < 0 || r.AdmissionWait < 0 || r.SessionExpiry < 0 || r.StepPreparation < 0 || r.AdvanceWait < 0 || r.VerifierExpiry < 0 || r.CompletionDrain < 0 || r.WorkExecution < 0 || r.SDKAdmission < 0 || r.SDKAdmissionMax < 0 || r.Residual < 0) {
			return false
		}
		previous = r.Turn
	}
	return true
}
