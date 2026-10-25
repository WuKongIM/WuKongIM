package channelappend

import (
	"context"
	"errors"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// submissionTrace observes one admitted Future without retaining its caller
// context. First-append phases cover only its first selected effect, which may
// also contain other Futures; they are neither total work nor pure storage time.
type submissionTrace struct {
	mu       sync.Mutex
	trace    *rpctrace.Trace
	span     rpctrace.Span
	selected bool
	done     bool
	// firstSequence fences result attribution when effects share Futures. It
	// stays private to this observation and is never exported as an identity.
	firstSequence uint64
	appendResult  rpctrace.Result
}

func newSubmissionTrace(ctx context.Context, itemCount int) *submissionTrace {
	trace := rpctrace.From(ctx)
	if trace == nil || itemCount == 0 {
		return nil
	}
	return &submissionTrace{trace: trace, span: trace.Begin(rpctrace.StageChannelPrepareWait)}
}

func (o *submissionTrace) beginPrepare() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done {
		return
	}
	o.span.End(rpctrace.ResultOK)
	o.span = o.trace.Begin(rpctrace.StageChannelPrepare)
}

func (o *submissionTrace) endPrepare(results []SendBatchItemResult) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.span.End(submissionTraceResult(results))
	o.span = rpctrace.Span{}
}

// admitDurable begins pending only after this Future has actual durable items
// in the writer state. Rejected and realtime-only work never enters this phase.
func (o *submissionTrace) admitDurable() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.done {
		o.span = o.trace.Begin(rpctrace.StageChannelFirstAppendPending)
	}
}

func (o *submissionTrace) selectFirstAppend(sequence uint64) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done || o.selected {
		return false
	}
	o.selected = true
	o.firstSequence = sequence
	o.appendResult = rpctrace.ResultOK
	o.span.End(rpctrace.ResultOK)
	o.span = o.trace.Begin(rpctrace.StageChannelFirstAppendQueue)
	return true
}

func (o *submissionTrace) beginAppend() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done {
		return
	}
	o.span.End(rpctrace.ResultOK)
	o.span = o.trace.Begin(rpctrace.StageChannelFirstAppend)
}

// endAppend starts the residual until the entire original Future completes.
// It includes ordered publication and any remaining split effects, not solely
// publication, scheduler resumption, or post-commit work.
func (o *submissionTrace) endAppend() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done {
		return
	}
	o.span.End(o.appendResult)
	o.span = o.trace.Begin(rpctrace.StageChannelAfterFirstAppend)
}

func (o *submissionTrace) rejectAppend() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.span.End(rpctrace.ResultBusy)
	o.span = rpctrace.Span{}
}

func (o *submissionTrace) finish(result rpctrace.Result) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done {
		return
	}
	o.done = true
	o.span.End(result)
	o.span = rpctrace.Span{}
}

// selectAppendTraces retains only sampled Futures selected for their first
// effect. The existing bounded item list is scanned without a dedup map, and
// unobserved effects allocate no additional backing storage.
func selectAppendTraces(sequence uint64, items []preparedSend) []*submissionTrace {
	var observations []*submissionTrace
	for i := range items {
		future := items[i].future
		if future != nil && future.rpcTrace.selectFirstAppend(sequence) {
			observations = append(observations, future.rpcTrace)
		}
	}
	return observations
}

// recordAppendResult keeps failures from a shared effect scoped to their own
// original Future. The caller scans completions once, never once per observer.
func (o *submissionTrace) recordAppendResult(sequence uint64, result SendBatchItemResult, traceErr error) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.selected && o.firstSequence == sequence && !o.done {
		// Item-level append errors retain their typed observation cause even
		// when the public result intentionally maps them to a closed Reason.
		if traceErr != nil {
			result.Err = traceErr
		}
		o.appendResult = mergeSubmissionTraceResult(o.appendResult, submissionItemTraceResult(result))
	}
}

func submissionTraceResult(results []SendBatchItemResult) rpctrace.Result {
	result := rpctrace.ResultOK
	for _, item := range results {
		result = mergeSubmissionTraceResult(result, submissionItemTraceResult(item))
	}
	return result
}

func submissionItemTraceResult(item SendBatchItemResult) rpctrace.Result {
	if errors.Is(item.Err, ErrBackpressured) || errors.Is(item.Err, ErrChannelBusy) || (item.Err == nil && item.Result.Reason == ReasonSystemBusy) {
		return rpctrace.ResultBusy
	}
	if item.Err != nil {
		return rpctrace.ResultFor(item.Err)
	}
	if item.Result.Reason != ReasonSuccess {
		return rpctrace.ResultError
	}
	return rpctrace.ResultOK
}

func mergeSubmissionTraceResult(current, next rpctrace.Result) rpctrace.Result {
	if next == rpctrace.ResultOK {
		return current
	}
	if current == rpctrace.ResultOK {
		return next
	}
	if current != next {
		return rpctrace.ResultError
	}
	return current
}

// finishRPCTrace observes the immutable completed result vector only for sampled
// Futures. It must run after releasing Future.mu and before notifying Wait.
func (f *Future) finishRPCTrace() {
	if f.rpcTrace == nil {
		return
	}
	f.mu.Lock()
	result := submissionTraceResult(f.results)
	f.mu.Unlock()
	f.rpcTrace.finish(result)
}
