package chatlifecycle

import (
	"math"
	"sync/atomic"
	"time"
)

const driverDiagnosticCapacity = 16

// DriverDeadlineMiss is a bounded, identity-free prefix of one real driver
// turn at the original fresh-clock dispatch rejection. Durations share the
// injected SessionClock; SDKAdmission is nested within WorkExecution.
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
	SDKAdmission     time.Duration `json:"sdk_admission"`
	SDKAdmissionMax  time.Duration `json:"sdk_admission_max"`
	// Residual is elapsed sampled-to-fresh time outside the disjoint segments.
	Residual            time.Duration `json:"residual"`
	Processed           uint64        `json:"processed"`
	Attempts            uint64        `json:"attempts"`
	Retries             uint64        `json:"retries"`
	CompletionInitial   uint64        `json:"completion_initial"`
	CompletionAfterWork uint64        `json:"completion_after_work"`
	CompletionFairness  uint64        `json:"completion_fairness"`
	SessionsExpired     uint64        `json:"sessions_expired"`
	// CorrelationsExpired uses the verifier's existing acknowledged-expiry count;
	// it is not a count of all popped heap entries or released attempt receipts.
	CorrelationsExpired uint64 `json:"correlations_expired"`
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
}

// driverTurn transfers from the stepMu-serialized caller to its admitted owner
// command. It retains only numeric evidence, never a context, work or client.
type driverTurn struct {
	record  DriverDeadlineMiss
	sampled time.Time
	last    time.Time
}

func (e *Engine) beginDriverTurn(sampled time.Time, operation string) *driverTurn {
	if e.driverDiagnostics == nil || e.driverDiagnostics.full.Load() {
		return nil
	}
	at := e.clock.Now()
	t := &driverTurn{record: DriverDeadlineMiss{Operation: operation, ClockValid: true}, sampled: sampled, last: at}
	t.record.EntryLag = t.duration(sampled, at)
	return t
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
	return &DriverDiagnosticsSnapshot{Generation: e.generation, Dropped: d.dropped, Records: append([]DriverDeadlineMiss{}, d.records[:d.count]...)}
}

type driverDrainLocation uint8

const (
	driverDrainInitial driverDrainLocation = iota
	driverDrainAfterWork
	driverDrainFairness
)

// drainDriverCompletions keeps the original unbounded-until-empty semantics.
// The disabled path calls the unchanged drain without per-completion overhead.
func (e *Engine) drainDriverCompletions(t *driverTurn, location driverDrainLocation) {
	if t == nil || e.driverDiagnostics.full.Load() {
		e.drainCompletions()
		return
	}
	started := e.driverNow(t)
	var count uint64
	for {
		select {
		case completion := <-e.completions:
			e.observeCompletion(completion)
			count++
		default:
			t.record.CompletionDrain += e.driverElapsed(t, started)
			switch location {
			case driverDrainInitial:
				t.record.CompletionInitial += count
			case driverDrainAfterWork:
				t.record.CompletionAfterWork += count
			case driverDrainFairness:
				t.record.CompletionFairness += count
			}
			return
		}
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
