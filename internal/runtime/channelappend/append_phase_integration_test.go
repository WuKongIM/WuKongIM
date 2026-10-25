//go:build integration

package channelappend

import (
	"context"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

type bridgeAppender func(context.Context, AppendBatchRequest) (AppendBatchResult, error)

func (a bridgeAppender) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	return a(ctx, req)
}

func bridgeItem(t *testing.T, trace *rpctrace.Trace, key string) preparedSend {
	t.Helper()
	f := newFuture(1)
	if trace != nil {
		f.rpcTrace = newSubmissionTrace(rpctrace.With(context.Background(), trace), 1)
		f.rpcTrace.admitDurable()
	}
	return preparedSend{Context: context.Background(), Command: SendCommand{FromUID: "sender", ClientMsgNo: key, Payload: []byte(key)}, future: f}
}
func bridgeTrace(t *testing.T) *rpctrace.Trace {
	t.Helper()
	r, err := rpctrace.NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := r.Start(context.Background(), rpctrace.SideServer, 21)
	t.Cleanup(func() { trace.Finish(rpctrace.ResultStopped) })
	return trace
}
func bridgePhases(s *rpctrace.Sample) []rpctrace.SpanSample {
	var out []rpctrace.SpanSample
	for _, span := range s.Spans {
		if span.Stage == rpctrace.StageChannelRuntimeAppend || span.Stage == rpctrace.StageChannelMetaResolve {
			out = append(out, span)
		}
	}
	return out
}
func bridgeResults(t *testing.T, trace *rpctrace.Trace, want ...rpctrace.Result) {
	t.Helper()
	spans := bridgePhases(trace.Finish(rpctrace.ResultOK))
	got := make([]rpctrace.Result, len(spans))
	for i, s := range spans {
		got[i] = s.Result
	}
	if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
		t.Fatalf("child outcomes %v want %v; spans %+v", got, want, spans)
	}
}
func bridgeRun(seq uint64, items []preparedSend, appender Appender, idempotency IdempotencyStore) appendCompletedEvent {
	selectAppendTraces(seq, items)
	return (appendEffect{seq: seq, items: items}).run(context.Background(), appendPorts{appender: appender, idempotency: idempotency})
}

func TestAppendPhaseFanoutUsesAllOriginalOwners(t *testing.T) {
	a, b, c, inactive, later := bridgeTrace(t), bridgeTrace(t), bridgeTrace(t), bridgeTrace(t), bridgeTrace(t)
	items := []preparedSend{bridgeItem(t, a, "dedup"), bridgeItem(t, b, "dedup"), bridgeItem(t, a, "other"), bridgeItem(t, c, "other"), bridgeItem(t, inactive, "inactive"), bridgeItem(t, later, "later"), bridgeItem(t, nil, "unsampled")}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	items[4].Context = canceled
	items[5].future.rpcTrace.selectFirstAppend(9)
	calls := 0
	result := bridgeRun(10, items, bridgeAppender(func(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
		if ctx.Err() != nil || rpctrace.From(ctx) != nil {
			t.Fatal("caller context/trace leaked")
		}
		if len(req.Messages) != 4 {
			t.Fatalf("unique active items %d", len(req.Messages))
		}
		end := rpctrace.BeginAppendPhase(ctx, rpctrace.StageChannelRuntimeAppend)
		end.EndItems(4, func(i int) rpctrace.Result {
			calls++
			if i == 1 {
				return rpctrace.ResultTimeout
			}
			return rpctrace.ResultOK
		})
		end.EndItems(4, func(int) rpctrace.Result { t.Fatal("duplicate completion read callback"); return rpctrace.ResultOK })
		return AppendBatchResult{Items: make([]AppendBatchItemResult, 4)}, nil
	}), nil)
	if len(result.items) != len(items) || calls != 4 {
		t.Fatalf("completion alignment/read count %d/%d", len(result.items), calls)
	}
	bridgeResults(t, a, rpctrace.ResultTimeout)
	bridgeResults(t, b, rpctrace.ResultOK)
	bridgeResults(t, c, rpctrace.ResultTimeout)
	bridgeResults(t, inactive)
	bridgeResults(t, later)
}

func TestAppendPhaseResultsUnknownAndCommon(t *testing.T) {
	for _, kind := range []string{"short", "extra", "nil_reader", "invalid", "unknown_mixed", "negative", "common", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			trace := bridgeTrace(t)
			items := []preparedSend{bridgeItem(t, trace, "one"), bridgeItem(t, trace, "two")}
			want := rpctrace.ResultUnfinished
			bridgeRun(1, items, bridgeAppender(func(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
				end := rpctrace.BeginAppendPhase(ctx, rpctrace.StageChannelRuntimeAppend)
				switch kind {
				case "short":
					end.EndItems(1, func(int) rpctrace.Result { return rpctrace.ResultOK })
				case "extra":
					end.EndItems(3, func(int) rpctrace.Result { return rpctrace.ResultOK })
				case "nil_reader":
					end.EndItems(2, nil)
				case "invalid":
					end.EndItems(2, func(int) rpctrace.Result { return "invalid" })
				case "negative":
					end.EndItems(-1, nil)
				case "unknown_mixed":
					end.EndItems(2, func(i int) rpctrace.Result {
						if i == 0 {
							return "unknown"
						}
						return rpctrace.ResultError
					})
				case "common":
					want = rpctrace.ResultBusy
					end.End(rpctrace.ResultBusy)
				case "mixed":
					want = rpctrace.ResultError
					end.EndItems(2, func(i int) rpctrace.Result {
						if i == 0 {
							return rpctrace.ResultTimeout
						}
						return rpctrace.ResultCanceled
					})
				}
				return AppendBatchResult{Items: make([]AppendBatchItemResult, 2)}, nil
			}), nil)
			bridgeResults(t, trace, want)
		})
	}
}

func TestAppendPhaseRetryProjectsDedupSiblings(t *testing.T) {
	for _, sampleRetry := range []bool{true, false} {
		t.Run(map[bool]string{true: "sampled_retry", false: "unsampled_retry"}[sampleRetry], func(t *testing.T) {
			recovered, omitted := bridgeTrace(t), bridgeTrace(t)
			var retryA, retryB *rpctrace.Trace
			if sampleRetry {
				retryA, retryB = bridgeTrace(t), bridgeTrace(t)
			}
			items := []preparedSend{bridgeItem(t, recovered, "hit"), bridgeItem(t, retryA, "miss"), bridgeItem(t, retryB, "miss"), bridgeItem(t, omitted, "omitted")}
			cancelCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			items[3].Context = cancelCtx
			idempotency := &sequencedIdempotencyForAppendTest{results: []idempotencyResultForAppendTest{{ok: true, result: SendResult{Reason: ReasonSuccess}}, {ok: false}, {ok: false}}}
			calls := 0
			bridgeRun(5, items, bridgeAppender(func(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
				calls++
				end := rpctrace.BeginAppendPhase(ctx, rpctrace.StageChannelRuntimeAppend)
				if calls == 1 {
					if len(req.Messages) != 3 {
						t.Fatalf("unique first %d", len(req.Messages))
					}
					end.End(rpctrace.ResultError)
					cancel()
					return AppendBatchResult{}, ErrAppendFailed
				}
				if len(req.Messages) != 1 || req.Messages[0].ClientMsgNo != "miss" {
					t.Fatalf("retry request %+v", req)
				}
				if !sampleRetry && end != nil {
					t.Fatal("empty projection inherited recovered owner")
				}
				end.EndItems(1, func(int) rpctrace.Result { return rpctrace.ResultTimeout })
				return AppendBatchResult{Items: []AppendBatchItemResult{{Err: context.DeadlineExceeded}}}, nil
			}), idempotency)
			if calls != 2 {
				t.Fatalf("retry calls %d", calls)
			}
			bridgeResults(t, recovered, rpctrace.ResultError)
			bridgeResults(t, omitted, rpctrace.ResultError)
			if sampleRetry {
				bridgeResults(t, retryA, rpctrace.ResultError, rpctrace.ResultTimeout)
				bridgeResults(t, retryB, rpctrace.ResultError, rpctrace.ResultTimeout)
			}
		})
	}
}

func TestAppendPhaseFinishFreezeAndUnfinished(t *testing.T) {
	for _, kind := range []string{"finish_before", "finish_during", "future_done", "no_end"} {
		t.Run(kind, func(t *testing.T) {
			trace := bridgeTrace(t)
			item := bridgeItem(t, trace, "one")
			var frozen *rpctrace.Sample
			if kind == "finish_before" {
				frozen = trace.Finish(rpctrace.ResultTimeout)
			}
			if kind == "future_done" {
				item.future.rpcTrace.finish(rpctrace.ResultStopped)
			}
			bridgeRun(2, []preparedSend{item}, bridgeAppender(func(ctx context.Context, _ AppendBatchRequest) (AppendBatchResult, error) {
				end := rpctrace.BeginAppendPhase(ctx, rpctrace.StageChannelRuntimeAppend)
				if kind == "future_done" && end != nil {
					t.Fatal("done Future observed")
				}
				if kind == "finish_during" {
					frozen = trace.Finish(rpctrace.ResultTimeout)
					before := frozen.Clone()
					end.End(rpctrace.ResultOK)
					if !reflect.DeepEqual(before, *frozen) {
						t.Fatal("late end mutated sample")
					}
				}
				if kind != "no_end" {
					end.End(rpctrace.ResultOK)
				}
				return AppendBatchResult{Items: make([]AppendBatchItemResult, 1)}, nil
			}), nil)
			if frozen == nil {
				frozen = trace.Finish(rpctrace.ResultOK)
			}
			spans := bridgePhases(frozen)
			if kind == "finish_before" || kind == "future_done" {
				if len(spans) != 0 {
					t.Fatal("late span")
				}
				return
			}
			if len(spans) != 1 || !spans[0].Incomplete || spans[0].Result != rpctrace.ResultUnfinished {
				t.Fatalf("unfinished hidden: %+v", spans)
			}
		})
	}
}

func TestAppendPhaseUnsampledDoesNotConsumeResults(t *testing.T) {
	bridgeRun(3, []preparedSend{bridgeItem(t, nil, "one")}, bridgeAppender(func(ctx context.Context, _ AppendBatchRequest) (AppendBatchResult, error) {
		end := rpctrace.BeginAppendPhase(ctx, rpctrace.StageChannelRuntimeAppend)
		if end != nil {
			t.Fatal("unsampled observer allocated")
		}
		end.EndItems(1, func(int) rpctrace.Result { t.Fatal("unsampled callback"); return rpctrace.ResultOK })
		return AppendBatchResult{Items: make([]AppendBatchItemResult, 1)}, nil
	}), nil)
}
