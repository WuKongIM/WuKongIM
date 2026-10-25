//go:build integration

package channelappend

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

func TestGroupRPCTraceAcceptedLifecycle(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := newStartedTestGroup(t, Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender, InboxCoalesceWindow: -time.Nanosecond})
	ctx, trace := channelRPCTraceForTest(t)
	target := localTargetForAppendTest("trace-lifecycle")
	future, err := group.SubmitLocal(ctx, target, []SendBatchItem{testSendItem("sender", target.ChannelID.ID)})
	if err != nil {
		t.Fatal(err)
	}
	started := appender.waitStarted(t)
	started.Release()
	requireAppendSuccessAnyID(t, waitFutureForTest(t, future), 0)
	sample := trace.Finish(rpctrace.ResultOK)
	requireChannelRPCStages(t, sample, "channel_prepare_wait", "channel_prepare", "channel_first_append_pending", "channel_first_append_queue", "channel_first_append", "channel_after_first_append")
	for i, span := range sample.Spans {
		if span.Incomplete || span.Result != rpctrace.ResultOK {
			t.Fatalf("phase %d not completed successfully: %+v", i, span)
		}
		if i > 0 && span.Offset < sample.Spans[i-1].Offset+sample.Spans[i-1].Duration {
			t.Fatalf("sequential runtime phases overlap: %+v", sample.Spans)
		}
	}
}

func channelRPCTraceForTest(t *testing.T) (context.Context, *rpctrace.Trace) {
	t.Helper()
	recorder, err := rpctrace.NewRecorder(8)
	if err != nil {
		t.Fatal(err)
	}
	trace := recorder.Start(context.Background(), rpctrace.SideServer, 21)
	t.Cleanup(func() { trace.Finish(rpctrace.ResultStopped) })
	return rpctrace.With(context.Background(), trace), trace
}

func requireChannelRPCStages(t *testing.T, sample *rpctrace.Sample, expected ...string) {
	t.Helper()
	if sample == nil || len(sample.Spans) != len(expected) {
		t.Fatalf("runtime phases = %+v, want %v", sample, expected)
	}
	for i, stage := range expected {
		if string(sample.Spans[i].Stage) != stage {
			t.Fatalf("phase %d = %s, want %s", i, sample.Spans[i].Stage, stage)
		}
		if sample.Spans[i].Offset < 0 || sample.Spans[i].Duration < 0 || sample.Spans[i].Offset+sample.Spans[i].Duration > sample.Duration {
			t.Fatalf("phase escapes owner window: %+v", sample.Spans[i])
		}
	}
}

func TestGroupRPCTraceSharedEffectKeepsEveryCaller(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	authorizer := channelRPCTraceAuthorizer(func(_ context.Context, cmd SendCommand) (Decision, error) {
		if cmd.ChannelID == "block-writer" {
			close(entered)
			<-release
		}
		return Decision{Allowed: true, Reason: ReasonSuccess}, nil
	})
	recorded := newRecordingAppenderForAppendTest()
	appender := channelRPCTraceAppender(func(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
		if rpctrace.From(ctx) != nil {
			t.Error("shared append inherited one caller trace")
		}
		return recorded.AppendBatch(ctx, req)
	})
	group := newStartedTestGroup(t, Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender, Authorizer: authorizer, AdvancePoolSize: 1, InboxCoalesceWindow: -time.Nanosecond})
	blocker, err := group.SubmitLocal(context.Background(), localTargetForAppendTest("block-writer"), []SendBatchItem{testSendItem("block", "block-writer")})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	target := localTargetForAppendTest("shared")
	var futures []*Future
	var traces []*rpctrace.Trace
	for _, sender := range []string{"a", "b"} {
		ctx, trace := channelRPCTraceForTest(t)
		f, err := group.SubmitLocal(ctx, target, []SendBatchItem{appendSendItemForTest(sender, "shared", "one"), appendSendItemForTest(sender, "shared", "two")})
		if err != nil {
			t.Fatal(err)
		}
		futures = append(futures, f)
		traces = append(traces, trace)
	}
	close(release)
	waitFutureForTest(t, blocker)
	for i, f := range futures {
		results := waitFutureForTest(t, f)
		if len(results) != 2 || results[0].Err != nil || results[1].Err != nil {
			t.Fatalf("caller %d results: %+v", i, results)
		}
		requireChannelRPCStages(t, traces[i].Finish(rpctrace.ResultOK), "channel_prepare_wait", "channel_prepare", "channel_first_append_pending", "channel_first_append_queue", "channel_first_append", "channel_after_first_append")
	}
	requests := recorded.Requests()
	found := false
	for _, req := range requests {
		if req.ChannelID == target.ChannelID && len(req.Messages) == 4 {
			found = true
		}
	}
	if !found {
		t.Fatalf("fixture did not coalesce four items from two Futures: %+v", requests)
	}
}

func TestGroupRPCTraceTerminalPathsDoNotInventAppend(t *testing.T) {
	for _, kind := range []string{"invalid", "empty", "realtime", "state_busy", "remote", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			appender := newRecordingAppenderForAppendTest()
			group := newStartedTestGroup(t, Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender, ChannelBacklogHighWatermark: 1, InboxCoalesceWindow: -time.Nanosecond})
			ctx, trace := channelRPCTraceForTest(t)
			target := localTargetForAppendTest("terminal")
			items := []SendBatchItem{testSendItem("sender", "terminal")}
			expected := []string{"channel_prepare_wait", "channel_prepare"}
			switch kind {
			case "invalid":
				items[0].Command.Payload = nil
			case "empty":
				items = nil
				expected = nil
			case "realtime":
				items[0].Command.NoPersist = true
			case "state_busy":
				items = append(items, testSendItem("other", "terminal"))
			case "remote":
				target.LeaderNodeID = 2
				expected = nil
			case "disabled":
				ctx = context.Background()
				expected = nil
			}
			future, err := group.SubmitLocal(ctx, target, items)
			if kind == "remote" {
				if !errors.Is(err, ErrNotChannelAuthority) || future != nil {
					t.Fatalf("remote admission changed: %v %v", future, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				results := waitFutureForTest(t, future)
				if len(results) != len(items) {
					t.Fatalf("cardinality %d, want %d", len(results), len(items))
				}
				if kind == "state_busy" {
					for _, result := range results {
						if !errors.Is(result.Err, ErrChannelBusy) {
							t.Fatalf("state admission changed: %+v", result)
						}
					}
				}
				if kind == "disabled" && future.rpcTrace != nil {
					t.Fatal("unsampled Future allocated trace ownership")
				}
			}
			sample := trace.Finish(rpctrace.ResultOK)
			requireChannelRPCStages(t, sample, expected...)
			if kind == "invalid" && sample.Spans[1].Result != rpctrace.ResultError {
				t.Fatalf("denied preparation reported success: %+v", sample.Spans[1])
			}
			if kind != "disabled" && appender.Calls() != 0 {
				t.Fatalf("terminal path issued %d durable appends", appender.Calls())
			}
		})
	}
}

func TestGroupRPCTraceCallerExitFreezesSampleWithoutCancelingAppend(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := newStartedTestGroup(t, Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender, InboxCoalesceWindow: -time.Nanosecond})
	traceCtx, trace := channelRPCTraceForTest(t)
	ctx, cancel := context.WithCancel(traceCtx)
	target := localTargetForAppendTest("caller-exit")
	future, err := group.SubmitLocal(ctx, target, []SendBatchItem{testSendItem("sender", target.ChannelID.ID)})
	if err != nil {
		t.Fatal(err)
	}
	started := appender.waitStarted(t)
	cancel()
	if _, err := future.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v", err)
	}
	sample := trace.Finish(rpctrace.ResultCanceled)
	requireChannelRPCStages(t, sample, "channel_prepare_wait", "channel_prepare", "channel_first_append_pending", "channel_first_append_queue", "channel_first_append")
	if !sample.Spans[4].Incomplete || sample.Spans[4].Result != rpctrace.ResultUnfinished {
		t.Fatalf("live append was misreported complete: %+v", sample.Spans[4])
	}
	frozen := sample.Clone()
	started.Release()
	requireAppendSuccessAnyID(t, waitFutureForTest(t, future), 0)
	if !reflect.DeepEqual(*sample, frozen) || trace.Finish(rpctrace.ResultOK) != nil {
		t.Fatal("late durable completion changed/re-emitted endpoint snapshot")
	}
}

func TestGroupRPCTraceAppendPanicClosesObservedRun(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: channelRPCTraceAppender(func(context.Context, AppendBatchRequest) (AppendBatchResult, error) {
		panic("controlled append failure")
	}), InboxCoalesceWindow: -time.Nanosecond})
	ctx, trace := channelRPCTraceForTest(t)
	target := localTargetForAppendTest("panic")
	future, err := group.SubmitLocal(ctx, target, []SendBatchItem{testSendItem("sender", target.ChannelID.ID)})
	if err != nil {
		t.Fatal(err)
	}
	result := waitFutureForTest(t, future)
	if len(result) != 1 || !errors.Is(result[0].Err, ErrEffectPanic) {
		t.Fatalf("panic result changed: %+v", result)
	}
	sample := trace.Finish(rpctrace.ResultError)
	requireChannelRPCStages(t, sample, "channel_prepare_wait", "channel_prepare", "channel_first_append_pending", "channel_first_append_queue", "channel_first_append", "channel_after_first_append")
	if sample.Spans[4].Result != rpctrace.ResultError || sample.Spans[4].Incomplete || sample.Spans[5].Result != rpctrace.ResultError {
		t.Fatalf("panic left live/success append phase: %+v", sample.Spans[4])
	}
}

type channelRPCTraceAuthorizer func(context.Context, SendCommand) (Decision, error)

func (f channelRPCTraceAuthorizer) AuthorizeSend(ctx context.Context, cmd SendCommand) (Decision, error) {
	return f(ctx, cmd)
}

type channelRPCTraceAppender func(context.Context, AppendBatchRequest) (AppendBatchResult, error)

func (f channelRPCTraceAppender) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	return f(ctx, req)
}

// The bubble holds real Group workers at boundary ports without wall-clock
// sleeps. A shared Channel waits in pending; a second Channel waits for the
// occupied append pool. Neither interval is inferred from whole Future.Wait.
func TestGroupRPCTraceSeparatesPendingFromPoolWait(t *testing.T) {
	for _, sameChannel := range []bool{true, false} {
		name := "pool"
		if sameChannel {
			name = "pending"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				appender := newBlockingAppenderForAppendTest()
				group := New(Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender, AdvancePoolSize: 2, EffectPoolSize: 1, AppendInflightBatchesPerChannel: 1, InboxCoalesceWindow: -time.Nanosecond})
				if err := group.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := group.Stop(context.Background()); err != nil {
						t.Error(err)
					}
				}()
				firstTarget := localTargetForAppendTest("first")
				first, err := group.SubmitLocal(context.Background(), firstTarget, []SendBatchItem{testSendItem("first", "first")})
				if err != nil {
					t.Fatal(err)
				}
				firstStarted := appender.waitStarted(t)
				secondTarget := localTargetForAppendTest("second")
				if sameChannel {
					secondTarget = firstTarget
				}
				ctx, trace := channelRPCTraceForTest(t)
				second, err := group.SubmitLocal(ctx, secondTarget, []SendBatchItem{testSendItem("second", secondTarget.ChannelID.ID)})
				if err != nil {
					firstStarted.Release()
					t.Fatal(err)
				}
				synctest.Wait()
				time.Sleep(7 * time.Millisecond)
				sample := trace.Finish(rpctrace.ResultTimeout)
				firstStarted.Release()
				secondStarted := appender.waitStarted(t)
				secondStarted.Release()
				waitFutureForTest(t, first)
				waitFutureForTest(t, second)
				expected := []string{"channel_prepare_wait", "channel_prepare", "channel_first_append_pending"}
				if !sameChannel {
					expected = append(expected, "channel_first_append_queue")
				}
				requireChannelRPCStages(t, sample, expected...)
				tail := sample.Spans[len(sample.Spans)-1]
				if !tail.Incomplete || tail.Duration < 7*time.Millisecond {
					t.Fatalf("blocked %s phase missing: %+v", name, tail)
				}
			})
		})
	}
}

func TestGroupRPCTraceAfterFirstAppendIncludesOrderedPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		appender := newBlockingAppenderForAppendTest()
		group := New(Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender, EffectPoolSize: 2, AppendInflightBatchesPerChannel: 2, InboxCoalesceWindow: -time.Nanosecond})
		if err := group.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := group.Stop(context.Background()); err != nil {
				t.Error(err)
			}
		}()
		target := localTargetForAppendTest("ordered")
		first, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("first", target.ChannelID.ID)})
		if err != nil {
			t.Fatal(err)
		}
		firstStarted := appender.waitStarted(t)
		ctx, trace := channelRPCTraceForTest(t)
		second, err := group.SubmitLocal(ctx, target, []SendBatchItem{testSendItem("second", target.ChannelID.ID)})
		if err != nil {
			firstStarted.Release()
			t.Fatal(err)
		}
		secondStarted := appender.waitStarted(t)
		secondStarted.Release()
		synctest.Wait()
		time.Sleep(9 * time.Millisecond)
		sample := trace.Finish(rpctrace.ResultTimeout)
		firstStarted.Release()
		waitFutureForTest(t, first)
		waitFutureForTest(t, second)
		requireChannelRPCStages(t, sample, "channel_prepare_wait", "channel_prepare", "channel_first_append_pending", "channel_first_append_queue", "channel_first_append", "channel_after_first_append")
		if sample.Spans[4].Incomplete || !sample.Spans[5].Incomplete || sample.Spans[5].Duration < 9*time.Millisecond {
			t.Fatalf("ordered publication was folded into completed append: %+v", sample.Spans)
		}
	})
}

func TestGroupRPCTraceClosedAppendPoolHasNoRun(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: newRecordingAppenderForAppendTest(), InboxCoalesceWindow: -time.Nanosecond})
	// Controlled dependency failure: the real pool rejects accepted scheduling.
	// Normal Stop drains before releasing this pool; no product lifecycle changes.
	group.appendPool.pool.Release()
	ctx, trace := channelRPCTraceForTest(t)
	target := localTargetForAppendTest("closed-pool")
	future, err := group.SubmitLocal(ctx, target, []SendBatchItem{testSendItem("sender", target.ChannelID.ID)})
	if err != nil {
		t.Fatal(err)
	}
	results := waitFutureForTest(t, future)
	if len(results) != 1 || !errors.Is(results[0].Err, ErrBackpressured) {
		t.Fatalf("pool failure result changed: %+v", results)
	}
	sample := trace.Finish(rpctrace.ResultBusy)
	requireChannelRPCStages(t, sample, "channel_prepare_wait", "channel_prepare", "channel_first_append_pending", "channel_first_append_queue")
	if sample.Spans[3].Incomplete || sample.Spans[3].Result != rpctrace.ResultBusy {
		t.Fatalf("rejected queue misclassified: %+v", sample.Spans[3])
	}
}

func TestGroupRPCTraceOverflowDoesNotChangeAcceptedResult(t *testing.T) {
	group := newStartedTestGroup(t, Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: newRecordingAppenderForAppendTest(), InboxCoalesceWindow: -time.Nanosecond})
	ctx, trace := channelRPCTraceForTest(t)
	for i := 0; i < 32; i++ {
		trace.Begin(rpctrace.StageHandler).End(rpctrace.ResultOK)
	}
	target := localTargetForAppendTest("overflow")
	future, err := group.SubmitLocal(ctx, target, []SendBatchItem{testSendItem("sender", target.ChannelID.ID)})
	if err != nil {
		t.Fatal(err)
	}
	requireAppendSuccessAnyID(t, waitFutureForTest(t, future), 0)
	sample := trace.Finish(rpctrace.ResultOK)
	if len(sample.Spans) != 32 || sample.DroppedSpans != 6 {
		t.Fatalf("fixed observation bound lost: %+v", sample)
	}
}

func TestGroupRPCTraceSharedEffectOutcomesRemainPerFuture(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	authorizer := channelRPCTraceAuthorizer(func(_ context.Context, cmd SendCommand) (Decision, error) {
		if cmd.ChannelID == "hold" {
			close(entered)
			<-release
		}
		return Decision{Allowed: true, Reason: ReasonSuccess}, nil
	})
	appender := channelRPCTraceAppender(func(_ context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
		results := make([]AppendBatchItemResult, len(req.Messages))
		for i, msg := range req.Messages {
			results[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: uint64(i + 1), Message: msg}
			if msg.FromUID == "failed" {
				results[i].Err = context.DeadlineExceeded
			}
		}
		return AppendBatchResult{Items: results}, nil
	})
	group := newStartedTestGroup(t, Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender, Authorizer: authorizer, AdvancePoolSize: 1, InboxCoalesceWindow: -time.Nanosecond})
	hold, err := group.SubmitLocal(context.Background(), localTargetForAppendTest("hold"), []SendBatchItem{testSendItem("hold", "hold")})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	var futures []*Future
	var traces []*rpctrace.Trace
	for _, sender := range []string{"successful", "failed"} {
		ctx, trace := channelRPCTraceForTest(t)
		f, err := group.SubmitLocal(ctx, localTargetForAppendTest("mixed"), []SendBatchItem{testSendItem(sender, "mixed")})
		if err != nil {
			t.Fatal(err)
		}
		futures = append(futures, f)
		traces = append(traces, trace)
	}
	close(release)
	waitFutureForTest(t, hold)
	for i, f := range futures {
		results := waitFutureForTest(t, f)
		expected := rpctrace.ResultOK
		if i == 1 {
			expected = rpctrace.ResultTimeout
			if results[0].Err != nil || results[0].Result.Reason != ReasonSystemError {
				t.Fatalf("failed item changed: %+v", results)
			}
		} else if results[0].Err != nil {
			t.Fatalf("successful sibling failed: %+v", results)
		}
		sample := traces[i].Finish(expected)
		requireChannelRPCStages(t, sample, "channel_prepare_wait", "channel_prepare", "channel_first_append_pending", "channel_first_append_queue", "channel_first_append", "channel_after_first_append")
		terminalExpected := expected
		if i == 1 {
			terminalExpected = rpctrace.ResultError
		} // Existing item errors map to the public reason, while the append retains its typed trace error.
		if sample.Spans[4].Result != expected || sample.Spans[5].Result != terminalExpected {
			t.Fatalf("Future %d inherited a sibling outcome or false completion: %+v", i, sample.Spans)
		}
	}
}

func TestGroupRPCTraceQueuedStopDoesNotDeclareCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		appender := newBlockingAppenderForAppendTest()
		group := New(Options{LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(1), Appender: appender, InboxCoalesceWindow: -time.Nanosecond})
		if err := group.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		target := localTargetForAppendTest("stop-queued")
		first, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{testSendItem("first", target.ChannelID.ID)})
		if err != nil {
			t.Fatal(err)
		}
		started := appender.waitStarted(t)
		ctx, trace := channelRPCTraceForTest(t)
		second, err := group.SubmitLocal(ctx, target, []SendBatchItem{testSendItem("second", target.ChannelID.ID)})
		if err != nil {
			started.Release()
			t.Fatal(err)
		}
		synctest.Wait()
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		stopErr := group.Stop(stopCtx)
		cancel()
		sample := trace.Finish(rpctrace.ResultTimeout)
		started.Release()
		next := appender.waitStarted(t)
		next.Release()
		waitFutureForTest(t, first)
		requireAppendSuccessAnyID(t, waitFutureForTest(t, second), 0)
		if err := group.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(stopErr, context.DeadlineExceeded) {
			t.Fatalf("Stop dropped pending work: %v", stopErr)
		}
		requireChannelRPCStages(t, sample, "channel_prepare_wait", "channel_prepare", "channel_first_append_pending")
		if !sample.Spans[2].Incomplete || sample.Spans[2].Result != rpctrace.ResultUnfinished {
			t.Fatalf("queued Stop fabricated success: %+v", sample.Spans[2])
		}
	})
}
