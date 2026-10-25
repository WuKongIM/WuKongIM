package commit

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// requestTrace follows one optional observation across copied pending receipts.
// The submitter owns setup until channel handoff; afterward only the coordinator
// advances it. It never retains the submitting context or cancels physical work.
type requestTrace struct {
	trace *rpctrace.Trace
	span  rpctrace.Span
}

func newRequestTrace(ctx context.Context) *requestTrace {
	trace := rpctrace.From(ctx)
	if trace == nil {
		return nil
	}
	return &requestTrace{trace: trace, span: trace.Begin(rpctrace.StageCommitAdmission)}
}

func (t *requestTrace) advance(stage rpctrace.Stage, result rpctrace.Result) {
	if t == nil {
		return
	}
	t.span.End(result)
	t.span = t.trace.Begin(stage)
}

func (t *requestTrace) finish(result rpctrace.Result) {
	if t == nil {
		return
	}
	t.span.End(result)
	t.span = rpctrace.Span{}
}

// enterBatch marks physical cohort admission, not an intake-to-owner-buffer
// transfer. Terminal credit release deliberately does not call this transition.
func (r pendingRequest) enterBatch() {
	r.trace.advance(rpctrace.StageCommitCollect, rpctrace.ResultOK)
	r.releaseQueued()
}

// traceStage covers the whole shared physical phase for each participating
// logical request. These intervals overlap and must never be added as fsyncs.
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

func commitTraceResult(err error) rpctrace.Result {
	if errors.Is(err, ErrClosed) {
		return rpctrace.ResultStopped
	}
	return rpctrace.ResultFor(err)
}
