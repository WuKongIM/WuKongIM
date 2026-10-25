//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/stretchr/testify/require"
)

func TestChannelAppendAuthorityRPCTraceRejectsWithoutFutureWait(t *testing.T) {
	for _, tc := range []struct {
		name      string
		nilGroup  bool
		paused    bool
		wrongNode bool
		cancel    bool
		wantErr   error
		wantSpan  rpctrace.Result
	}{
		{name: "unavailable group", nilGroup: true, wantErr: channelappend.ErrRouteNotReady},
		{name: "paused admission", paused: true, wantErr: channelappend.ErrRouteNotReady, wantSpan: rpctrace.ResultError},
		{name: "wrong authority", wrongNode: true, wantErr: channelappend.ErrNotChannelAuthority, wantSpan: rpctrace.ResultError},
		{name: "caller already canceled", cancel: true, wantErr: context.Canceled, wantSpan: rpctrace.ResultCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group, _ := newChannelAppendRPCTraceGroup(t)
			if tc.nilGroup {
				group = nil
			}
			if tc.paused {
				group.PauseForRestore()
			}
			target, items := channelAppendRPCTraceBatch()
			if tc.wrongNode {
				target.LeaderNodeID = 2
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			recorder, trace, ctx := newChannelAppendRPCServerTrace(t, ctx)
			results := (channelAppendAuthorityLocal{group: group}).SubmitForAuthority(ctx, target, items)
			require.Len(t, results, len(items))
			for _, result := range results {
				require.ErrorIs(t, result.Err, tc.wantErr)
			}
			sample := trace.Finish(rpctrace.ResultFor(tc.wantErr))
			require.NotNil(t, sample)
			require.Zero(t, recorder.Active())
			if tc.nilGroup {
				require.Empty(t, sample.Spans)
				return
			}
			span := requireChannelAppendRPCSpan(t, sample, "channel_submit", tc.wantSpan)
			require.False(t, span.Incomplete)
			for _, span := range sample.Spans {
				require.NotEqual(t, rpctrace.Stage("channel_future_wait"), span.Stage, "a rejected submission has no future")
			}
		})
	}
}

func TestChannelAppendAuthorityRPCTraceWaitsForDurableCompletion(t *testing.T) {
	for _, sampled := range []bool{false, true} {
		name := "unsampled"
		if sampled {
			name = "sampled"
		}
		t.Run(name, func(t *testing.T) {
			group, appender := newChannelAppendRPCTraceGroup(t)
			ctx := context.Background()
			var recorder *rpctrace.Recorder
			var trace *rpctrace.Trace
			if sampled {
				recorder, trace, ctx = newChannelAppendRPCServerTrace(t, ctx)
			}
			results := submitChannelAppendRPCTraceBatch(group, ctx)
			appender.waitStarted(t)
			select {
			case <-results:
				t.Fatal("authority returned while the durable append was still blocked")
			default:
			}
			appender.release()
			requireChannelAppendRPCSuccess(t, awaitChannelAppendRPCResults(t, results))
			require.NoError(t, awaitChannelAppendRPCBackend(t, appender))
			if sampled {
				sample := trace.Finish(rpctrace.ResultOK)
				require.NotNil(t, sample)
				require.Equal(t, rpctrace.SideServer, sample.Side)
				require.Equal(t, trace.ID(), sample.TraceID)
				submit := requireChannelAppendRPCSpan(t, sample, "channel_submit", rpctrace.ResultOK)
				wait := requireChannelAppendRPCSpan(t, sample, "channel_future_wait", rpctrace.ResultOK)
				require.LessOrEqual(t, submit.Offset+submit.Duration, wait.Offset)
				require.LessOrEqual(t, wait.Offset+wait.Duration, sample.Duration)
				require.Zero(t, recorder.Active())
			}
		})
	}
}

func TestChannelAppendAuthorityRPCTraceCallerCancellationKeepsAcceptedWork(t *testing.T) {
	group, appender := newChannelAppendRPCTraceGroup(t)
	callerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder, trace, ctx := newChannelAppendRPCServerTrace(t, callerCtx)
	results := submitChannelAppendRPCTraceBatch(group, ctx)
	appender.waitStarted(t)
	cancel()
	got := awaitChannelAppendRPCResults(t, results)
	require.Len(t, got, 2)
	for _, result := range got {
		require.ErrorIs(t, result.Err, context.Canceled)
	}
	sample := trace.Finish(rpctrace.ResultCanceled)
	require.NotNil(t, sample)
	requireChannelAppendRPCSpan(t, sample, "channel_submit", rpctrace.ResultOK)
	requireChannelAppendRPCSpan(t, sample, "channel_future_wait", rpctrace.ResultCanceled)
	frozen := sample.Clone()
	require.Zero(t, recorder.Active())
	// Cancellation only ends the caller's wait. The real Group retains the
	// accepted batch and the durable-port fake still completes after release.
	appender.release()
	require.NoError(t, awaitChannelAppendRPCBackend(t, appender))
	drainCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	require.NoError(t, group.WaitIdle(drainCtx))
	require.Equal(t, frozen, *sample)
	require.Nil(t, trace.Finish(rpctrace.ResultOK))
}

func TestChannelAppendAuthorityRPCTraceFinishedSampleIgnoresLateCompletion(t *testing.T) {
	group, appender := newChannelAppendRPCTraceGroup(t)
	recorder, trace, ctx := newChannelAppendRPCServerTrace(t, context.Background())
	results := submitChannelAppendRPCTraceBatch(group, ctx)
	appender.waitStarted(t)
	// The worker may start before SubmitLocal returns; no assumption is made
	// about whether future.Wait has begun at this independently finished cut.
	sample := trace.Finish(rpctrace.ResultTimeout)
	require.NotNil(t, sample)
	var submits int
	for _, span := range sample.Spans {
		if span.Stage == "channel_submit" {
			submits++
		}
	}
	require.Equal(t, 1, submits)
	frozen := sample.Clone()
	require.Zero(t, recorder.Active())
	appender.release()
	requireChannelAppendRPCSuccess(t, awaitChannelAppendRPCResults(t, results))
	require.NoError(t, awaitChannelAppendRPCBackend(t, appender))
	require.Equal(t, frozen, *sample)
	require.Nil(t, trace.Finish(rpctrace.ResultOK))
}

// rpcTraceLifecycleAppender reuses the existing durable-port barrier and
// exposes its terminal result to prove accepted work survives caller cancel.
type rpcTraceLifecycleAppender struct {
	*lifecycleBlockingChannelAppender
	completed chan error
}

func (a *rpcTraceLifecycleAppender) AppendBatch(ctx context.Context, req channelappend.AppendBatchRequest) (channelappend.AppendBatchResult, error) {
	result, err := a.lifecycleBlockingChannelAppender.AppendBatch(ctx, req)
	a.completed <- err
	return result, err
}

func newChannelAppendRPCTraceGroup(t *testing.T) (*channelappend.Group, *rpcTraceLifecycleAppender) {
	t.Helper()
	appender := &rpcTraceLifecycleAppender{
		lifecycleBlockingChannelAppender: newLifecycleBlockingChannelAppender(),
		completed:                        make(chan error, 1),
	}
	group := channelappend.New(channelappend.Options{
		LocalNodeID: 1, Appender: appender,
		MessageID:           &lifecycleMessageIDAllocator{next: 100},
		AuthorityShardCount: 1, AdvancePoolSize: 1, EffectPoolSize: 1,
		AdmissionCapacityPerShard: 4, ChannelBacklogHighWatermark: 4,
		PostCommitHandoffCapacity: 4,
		InboxCoalesceWindow:       -1, InboxCoalesceMaxItems: -1,
	})
	require.NoError(t, group.Start(context.Background()))
	t.Cleanup(func() {
		appender.release()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, group.Stop(ctx))
	})
	return group, appender
}

func channelAppendRPCTraceBatch() (channelappend.AuthorityTarget, []channelappend.SendBatchItem) {
	target := channelappend.AuthorityTarget{
		ChannelID:  channelappend.ChannelID{ID: "trace-room", Type: 2},
		ChannelKey: "2:trace-room", LeaderNodeID: 1,
	}
	return target, []channelappend.SendBatchItem{
		{Command: channelappend.SendCommand{FromUID: "alice", ClientMsgNo: "trace-1", ChannelID: "trace-room", ChannelType: 2, ChannelKey: "2:trace-room", Payload: []byte("first")}},
		{Command: channelappend.SendCommand{FromUID: "alice", ClientMsgNo: "trace-2", ChannelID: "trace-room", ChannelType: 2, ChannelKey: "2:trace-room", Payload: []byte("second")}},
	}
}

func submitChannelAppendRPCTraceBatch(group *channelappend.Group, ctx context.Context) <-chan []channelappend.SendBatchItemResult {
	results := make(chan []channelappend.SendBatchItemResult, 1)
	target, items := channelAppendRPCTraceBatch()
	go func() {
		results <- (channelAppendAuthorityLocal{group: group}).SubmitForAuthority(ctx, target, items)
	}()
	return results
}

func awaitChannelAppendRPCResults(t *testing.T, results <-chan []channelappend.SendBatchItemResult) []channelappend.SendBatchItemResult {
	t.Helper()
	select {
	case got := <-results:
		return got
	case <-time.After(time.Second):
		t.Fatal("authority result did not complete")
		return nil
	}
}

func awaitChannelAppendRPCBackend(t *testing.T, appender *rpcTraceLifecycleAppender) error {
	t.Helper()
	select {
	case err := <-appender.completed:
		return err
	case <-time.After(time.Second):
		t.Fatal("accepted durable append did not complete")
		return nil
	}
}

func newChannelAppendRPCServerTrace(t *testing.T, ctx context.Context) (*rpctrace.Recorder, *rpctrace.Trace, context.Context) {
	t.Helper()
	recorder, err := rpctrace.NewRecorder(1)
	require.NoError(t, err)
	trace := recorder.Start(ctx, rpctrace.SideServer, uint16(accessnode.ChannelAppendRPCServiceID))
	require.NotNil(t, trace)
	t.Cleanup(func() { trace.Finish(rpctrace.ResultError) })
	return recorder, trace, rpctrace.With(ctx, trace)
}

func requireChannelAppendRPCSuccess(t *testing.T, results []channelappend.SendBatchItemResult) {
	t.Helper()
	require.Len(t, results, 2)
	for i, result := range results {
		require.NoError(t, result.Err)
		require.Equal(t, uint64(101+i), result.Result.MessageID)
		require.Equal(t, uint64(i+1), result.Result.MessageSeq)
	}
}

func requireChannelAppendRPCSpan(t *testing.T, sample *rpctrace.Sample, stage rpctrace.Stage, result rpctrace.Result) rpctrace.SpanSample {
	t.Helper()
	var matches []rpctrace.SpanSample
	for _, span := range sample.Spans {
		if span.Stage == stage {
			matches = append(matches, span)
		}
	}
	require.Len(t, matches, 1, "sample must retain exactly one %s wrapper", stage)
	require.Equal(t, result, matches[0].Result)
	require.False(t, matches[0].Incomplete)
	return matches[0]
}
