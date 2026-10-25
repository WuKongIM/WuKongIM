package channelappend

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// appendPhaseObserver retains only sampled Trace pointers and bounded storage
// indexes. It never retains original items, payloads, Futures or caller contexts.
// ownersByItem maps this invocation's unique storage rows to every first-effect
// owner, including callers coalesced out of the physical append request.
type appendPhaseObserver struct {
	traces       []*rpctrace.Trace
	ownersByItem [][]int
}

// firstAppendTrace intersects active items with the exact effect already
// claimed by writer.selectAppendTraces; later effects cannot acquire ownership.
func (o *submissionTrace) firstAppendTrace(sequence uint64) *rpctrace.Trace {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done || !o.selected || o.firstSequence != sequence {
		return nil
	}
	return o.trace
}

func newAppendPhaseObserver(sequence uint64, batch idempotentAppendBatch) *appendPhaseObserver {
	original := batch.items
	if batch.ownerByItem != nil {
		original = batch.original
	}
	var observer *appendPhaseObserver
	var traceOwners map[*rpctrace.Trace]int
	type binding struct{ item, owner int }
	var seen map[binding]struct{}
	for index, item := range original {
		if item.future == nil {
			continue
		}
		trace := item.future.rpcTrace.firstAppendTrace(sequence)
		if trace == nil {
			continue
		}
		if observer == nil {
			observer = &appendPhaseObserver{ownersByItem: make([][]int, len(batch.items))}
			traceOwners = make(map[*rpctrace.Trace]int)
			seen = make(map[binding]struct{})
		}
		owner, ok := traceOwners[trace]
		if !ok {
			owner = len(observer.traces)
			traceOwners[trace] = owner
			observer.traces = append(observer.traces, trace)
		}
		unique := index
		if batch.ownerByItem != nil {
			unique = batch.ownerByItem[index]
		}
		key := binding{item: unique, owner: owner}
		if _, ok := seen[key]; !ok {
			observer.ownersByItem[unique] = append(observer.ownersByItem[unique], owner)
			seen[key] = struct{}{}
		}
	}
	return observer
}

// project retains exactly the owners of retryIndexes, in the retry request's
// order. Recovered/omitted owners disappear; an unobserved retry returns nil.
func (o *appendPhaseObserver) project(retryIndexes []int) *appendPhaseObserver {
	if o == nil {
		return nil
	}
	var projected *appendPhaseObserver
	var owners map[int]int
	for next, index := range retryIndexes {
		for _, owner := range o.ownersByItem[index] {
			if projected == nil {
				projected = &appendPhaseObserver{ownersByItem: make([][]int, len(retryIndexes))}
				owners = make(map[int]int)
			}
			mapped, ok := owners[owner]
			if !ok {
				mapped = len(projected.traces)
				owners[owner] = mapped
				projected.traces = append(projected.traces, o.traces[owner])
			}
			projected.ownersByItem[next] = append(projected.ownersByItem[next], mapped)
		}
	}
	return projected
}

// withContext must start from the runtime context, never an earlier observed
// attempt. Thus a nil projection cannot inherit recovered owners on a retry.
func (o *appendPhaseObserver) withContext(ctx context.Context) context.Context {
	if o == nil {
		return ctx
	}
	return rpctrace.WithAppendPhaseObserver(ctx, o)
}

// BeginAppendPhase fans out once per Trace and folds only that Trace's item
// indexes. It leaves interrupted spans unfinished and accepts completion once.
func (o *appendPhaseObserver) BeginAppendPhase(stage rpctrace.Stage) rpctrace.AppendPhaseEnd {
	if o == nil {
		return nil
	}
	spans := make([]rpctrace.Span, len(o.traces))
	for index, trace := range o.traces {
		spans[index] = trace.Begin(stage)
	}
	var once sync.Once
	return func(common rpctrace.Result, count int, resultAt func(int) rpctrace.Result) {
		once.Do(func() {
			if count == -1 && resultAt == nil {
				for _, span := range spans {
					span.End(appendPhaseResult(common))
				}
				return
			}
			if count != len(o.ownersByItem) || resultAt == nil {
				for _, span := range spans {
					span.End(rpctrace.ResultUnfinished)
				}
				return
			}
			results := make([]rpctrace.Result, len(spans))
			for index := range results {
				results[index] = rpctrace.ResultOK
			}
			// Consume each actual result once, synchronously. The reader is never
			// retained by the handle; unrelated owners cannot inherit its outcome.
			for index, owners := range o.ownersByItem {
				result := appendPhaseResult(resultAt(index))
				for _, owner := range owners {
					results[owner] = mergeAppendPhaseResult(results[owner], result)
				}
			}
			for index, span := range spans {
				span.End(results[index])
			}
		})
	}
}

func appendPhaseResult(result rpctrace.Result) rpctrace.Result {
	switch result {
	case rpctrace.ResultOK, rpctrace.ResultError, rpctrace.ResultTimeout, rpctrace.ResultCanceled, rpctrace.ResultBusy, rpctrace.ResultStopped, rpctrace.ResultUnfinished:
		return result
	default:
		return rpctrace.ResultUnfinished
	}
}

// Unknown evidence dominates even a known sibling failure. Otherwise distinct
// known failures fold to error without changing the public business results.
func mergeAppendPhaseResult(current, next rpctrace.Result) rpctrace.Result {
	if current == rpctrace.ResultUnfinished || next == rpctrace.ResultUnfinished {
		return rpctrace.ResultUnfinished
	}
	return mergeSubmissionTraceResult(current, next)
}
