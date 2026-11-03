package commit

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// requestTrace retains observation ownership, never caller cancellation. Its
// mutex only joins caller return with at most two coordinator-owned attempts.
type requestTrace struct {
	trace *rpctrace.Trace
	span  rpctrace.Span
	// ownerSpan transports only the three pre-build waits across an owner
	// context boundary. Shared physical work already has its bounded cohort.
	ownerSpan   rpctrace.CommitSpan
	observation rpctrace.CommitObservation
	mu          sync.Mutex
	// Submit timing joins caller return and coordinator admission independently.
	submitStarted time.Time
	submitEnded   time.Time
	submitted     bool
	// A group rejection permits at most one independent rebuild.
	attempts [rpctrace.MaxCommitCohortLinks]rpctrace.CommitAttempt
	count    uint8
	lastRef  rpctrace.CommitCohortRef
}

func newRequestTrace(ctx context.Context, observation rpctrace.CommitObservation) *requestTrace {
	trace := rpctrace.From(ctx)
	if observation.Empty() && trace != nil {
		observation = rpctrace.NewCommitObservation(trace)
	}
	if trace == nil && observation.Empty() {
		return nil
	}
	t := &requestTrace{trace: trace, observation: observation}
	if !observation.Empty() {
		t.submitStarted = time.Now()
	}
	if trace != nil {
		t.span = trace.Begin(rpctrace.StageCommitAdmission)
	}
	t.ownerSpan = observation.BeginStage(rpctrace.StageCommitAdmission, trace)
	return t
}
func (t *requestTrace) advance(stage rpctrace.Stage, result rpctrace.Result) {
	if t == nil {
		return
	}
	t.span.End(result)
	t.ownerSpan.End(result)
	t.ownerSpan = rpctrace.CommitSpan{}
	t.span = t.trace.Begin(stage)
	if stage == rpctrace.StageCommitQueue || stage == rpctrace.StageCommitCollect {
		t.ownerSpan = t.observation.BeginStage(stage, t.trace)
	}
}
func (t *requestTrace) finish(result rpctrace.Result) {
	if t == nil {
		return
	}
	t.span.End(result)
	t.ownerSpan.End(result)
	t.span = rpctrace.Span{}
	t.ownerSpan = rpctrace.CommitSpan{}
}

// finishSubmit executes in the outer Submit defer, after its synchronous
// request observer. It cannot terminalize admitted physical work.
func (t *requestTrace) finishSubmit() {
	if t == nil || t.observation.Empty() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	end := time.Now()
	t.submitEnded = end
	t.submitted = true
	for i := 0; i < int(t.count); i++ {
		t.attempts[i].EndSubmit(t.submitStarted, end)
	}
}
func (t *requestTrace) beginCohort(ref rpctrace.CommitCohortRef, ordinal uint32, at time.Time) {
	if t == nil || t.observation.Empty() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if int(t.count) == len(t.attempts) {
		t.observation.DropCommitCohort()
		return
	}
	var previous *rpctrace.CommitCohortRef
	if t.count != 0 {
		copy := t.lastRef
		previous = &copy
	}
	a := t.observation.BeginCommitCohort(ref, ordinal, previous, at)
	t.attempts[t.count] = a
	t.count++
	t.lastRef = ref
	if t.submitted {
		a.EndSubmit(t.submitStarted, t.submitEnded)
	}
}
func (t *requestTrace) currentCohort() (rpctrace.CommitAttempt, bool) {
	if t == nil {
		return rpctrace.CommitAttempt{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.count == 0 {
		return rpctrace.CommitAttempt{}, false
	}
	return t.attempts[t.count-1], true
}
func (t *requestTrace) phaseStart(phase rpctrace.CommitPhase, at time.Time) {
	if a, ok := t.currentCohort(); ok {
		a.StartPhase(phase, at)
	}
}
func (t *requestTrace) phaseEnd(phase rpctrace.CommitPhase, at time.Time) {
	if a, ok := t.currentCohort(); ok {
		a.EndPhase(phase, at)
	}
}
func (t *requestTrace) physicalEnd(at time.Time, sum time.Duration, native engine.NativeCommitStats, outcome Outcome, terminal bool) {
	if a, ok := t.currentCohort(); ok {
		a.EndPhysical(at, sum, commitNativeFacts(native), commitOutcomeName(outcome), terminal)
	}
}
func commitOutcomeName(outcome Outcome) string {
	switch outcome {
	case OutcomeCommitted:
		return "committed"
	case OutcomeDefinitelyNotCommitted:
		return "definitely_not_committed"
	default:
		return "unknown"
	}
}
func commitNativeFacts(n engine.NativeCommitStats) rpctrace.CommitNativeFacts {
	return rpctrace.CommitNativeFacts{Available: n.Available, TotalDuration: n.TotalDuration, SemaphoreWaitDuration: n.SemaphoreWaitDuration, WALQueueWaitDuration: n.WALQueueWaitDuration, MemTableWriteStallDuration: n.MemTableWriteStallDuration, L0ReadAmpWriteStallDuration: n.L0ReadAmpWriteStallDuration, WALRotationDuration: n.WALRotationDuration, CommitWaitDuration: n.CommitWaitDuration}
}

// enterBatch denotes actual cohort admission, not transfer to a priority buffer.
func (r pendingRequest) enterBatch() {
	r.trace.advance(rpctrace.StageCommitCollect, rpctrace.ResultOK)
	r.releaseQueued()
}
func (b requestBatch) traceStage(stage rpctrace.Stage, previous rpctrace.Result) {
	for _, req := range b.requests {
		req.trace.advance(stage, previous)
	}
}
func (b requestBatch) finishTrace(result rpctrace.Result) {
	for _, req := range b.requests {
		req.trace.finish(result)
	}
}

// beginCohort generates one central identity even for different recorder owners.
// No observation means no new identity, clock, owner array or global registry.
func (b requestBatch) beginCohort() *rpctrace.CommitCohortRef {
	selected := false
	var ref rpctrace.CommitCohortRef
	for _, req := range b.requests {
		if req.trace == nil || req.trace.observation.Empty() {
			continue
		}
		selected = true
		ref = rpctrace.NewCommitCohortRef(req.trace.observation)
		if ref.Instance != "" {
			break
		}
	}
	if ref.Instance == "" {
		if selected {
			for _, req := range b.requests {
				if req.trace != nil {
					req.trace.observation.DropCommitCohort()
				}
			}
		}
		return nil
	}
	at := time.Now()
	for i, req := range b.requests {
		req.trace.beginCohort(ref, uint32(i), at)
	}
	return &ref
}
func (b requestBatch) phaseStart(phase rpctrace.CommitPhase, at time.Time) {
	for _, req := range b.requests {
		req.trace.phaseStart(phase, at)
	}
}
func (b requestBatch) phaseEnd(phase rpctrace.CommitPhase, at time.Time) {
	for _, req := range b.requests {
		req.trace.phaseEnd(phase, at)
	}
}
func (b requestBatch) physicalEnd(at time.Time, sum time.Duration, native engine.NativeCommitStats, outcome Outcome) {
	for _, req := range b.requests {
		req.trace.physicalEnd(at, sum, native, outcome, true)
	}
}
func commitTraceResult(err error) rpctrace.Result {
	if errors.Is(err, ErrClosed) {
		return rpctrace.ResultStopped
	}
	return rpctrace.ResultFor(err)
}
