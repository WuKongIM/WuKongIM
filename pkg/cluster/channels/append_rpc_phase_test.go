package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/stretchr/testify/require"
)

type appendRPCPhaseRecord struct {
	stage  rpctrace.Stage
	result rpctrace.Result
	items  []rpctrace.Result
}

// appendRPCPhaseRecorder consumes aligned outcomes synchronously, just like
// the first-effect owner port; it never stores the callback or request context.
type appendRPCPhaseRecorder struct {
	records []appendRPCPhaseRecord
	began   []rpctrace.Stage
}

func (o *appendRPCPhaseRecorder) BeginAppendPhase(stage rpctrace.Stage) rpctrace.AppendPhaseEnd {
	o.began = append(o.began, stage)
	return func(common rpctrace.Result, count int, at func(int) rpctrace.Result) {
		r := appendRPCPhaseRecord{stage: stage, result: common}
		for i := 0; i < count; i++ {
			r.items = append(r.items, at(i))
		}
		o.records = append(o.records, r)
	}
}

func TestAppendRPCPhasesKeepReturnedCardinality(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		svc, _, runtime, single := routeFencedAppendFixture(t)
		runtime.appendBatch.Items = make([]ch.AppendBatchItemResult, count)
		req := routeAppendBatch(single)
		req.Messages = append(req.Messages, codecContractMessage(2))
		observer := &appendRPCPhaseRecorder{}
		got, err := svc.AppendBatch(rpctrace.WithAppendPhaseObserver(context.Background(), observer), req)
		require.NoError(t, err)
		require.Equal(t, runtime.appendBatch, got)
		require.Len(t, observer.records, 3)
		require.Len(t, observer.records[2].items, count, "invalid cardinality must reach the owner unchanged so it can mark unknown")
	}
}

type appendRPCPanicRuntime struct{ fakeRuntime }

func (*appendRPCPanicRuntime) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	panic("test append panic")
}

func TestAppendRPCPhasesPanicDoesNotFinishAsSuccess(t *testing.T) {
	svc, source, _, single := routeFencedAppendFixture(t)
	single.ExpectedRouteGeneration = 0
	svc.metaSource = NewStaticMetaSource([]ch.Meta{source.meta})
	svc.runtime = &appendRPCPanicRuntime{}
	observer := &appendRPCPhaseRecorder{}
	require.Panics(t, func() {
		_, _ = svc.AppendBatch(rpctrace.WithAppendPhaseObserver(context.Background(), observer), routeAppendBatch(single))
	})
	require.Equal(t, []rpctrace.Stage{"channel_meta_resolve", "channel_meta_apply", "channel_runtime_append"}, observer.began)
	require.Len(t, observer.records, 2, "the unreturned runtime phase must remain unfinished")
}

func TestAppendRPCPhasesPreserveOwnItemOutcomes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "prepared"}[prepared], func(t *testing.T) {
			svc, source, runtime, single := routeFencedAppendFixture(t)
			if !prepared {
				single.ExpectedRouteGeneration = 0
				svc.metaSource = NewStaticMetaSource([]ch.Meta{source.meta})
			}
			runtime.appendBatch = ch.AppendBatchResult{Items: []ch.AppendBatchItemResult{
				{MessageID: 1, MessageSeq: 1}, {MessageID: 2, Err: context.DeadlineExceeded},
			}}
			req := routeAppendBatch(single)
			req.Messages = append(req.Messages, codecContractMessage(2))
			observer := &appendRPCPhaseRecorder{}
			ctx := rpctrace.WithAppendPhaseObserver(context.Background(), observer)
			require.Nil(t, rpctrace.From(ctx), "the observer is not a single caller Trace")
			got, err := svc.AppendBatch(ctx, req)
			require.NoError(t, err)
			require.Equal(t, runtime.appendBatch, got, "instrumentation must not rewrite business results")
			require.Equal(t, []appendRPCPhaseRecord{
				{stage: "channel_meta_resolve", result: rpctrace.ResultOK},
				{stage: "channel_meta_apply", result: rpctrace.ResultOK},
				{stage: "channel_runtime_append", result: rpctrace.ResultOK, items: []rpctrace.Result{rpctrace.ResultOK, rpctrace.ResultTimeout}},
			}, observer.records)
		})
	}
}

func TestAppendRPCPhasesStopAtActualFailure(t *testing.T) {
	for _, phase := range []string{"resolve", "apply", "runtime"} {
		t.Run(phase, func(t *testing.T) {
			svc, source, runtime, single := routeFencedAppendFixture(t)
			switch phase {
			case "resolve":
				source.fail = context.DeadlineExceeded
			case "apply":
				runtime.applyErr = context.DeadlineExceeded
			case "runtime":
				runtime.appendBatchErr = context.DeadlineExceeded
			}
			observer := &appendRPCPhaseRecorder{}
			_, err := svc.AppendBatch(rpctrace.WithAppendPhaseObserver(context.Background(), observer), routeAppendBatch(single))
			require.ErrorIs(t, err, context.DeadlineExceeded)
			count := map[string]int{"resolve": 1, "apply": 2, "runtime": 3}[phase]
			require.Len(t, observer.records, count)
			for i, record := range observer.records {
				want := rpctrace.ResultOK
				if i == count-1 {
					want = rpctrace.ResultTimeout
				}
				require.Equal(t, want, record.result)
				require.Empty(t, record.items, "a common failure is not an invented result vector")
			}
		})
	}
}

func TestAppendRPCPhasesFreshRetryDoesNotInventLocalWork(t *testing.T) {
	id := ch.ChannelID{ID: "trace-fresh-route", Type: 2}
	old := ch.Meta{ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive}
	fresh := old
	fresh.Leader, fresh.LeaderEpoch, fresh.RouteGeneration = 3, 2, 2
	source := &countingMetaSource{metas: []ch.Meta{old, fresh}}
	forward := &failoverDialForward{}
	svc, err := NewService(Config{LocalNode: 1, Runtime: &fakeRuntime{}, MetaSource: source, Forward: forward})
	require.NoError(t, err)
	_, err = svc.ResolveAppendAuthority(context.Background(), id)
	require.NoError(t, err)
	observer := &appendRPCPhaseRecorder{}
	_, err = svc.AppendBatch(rpctrace.WithAppendPhaseObserver(context.Background(), observer), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{MessageID: 9}}})
	require.NoError(t, err)
	require.Equal(t, []ch.NodeID{2, 3}, forward.nodes)
	require.Equal(t, []appendRPCPhaseRecord{
		{stage: "channel_meta_resolve", result: rpctrace.ResultOK},
		{stage: "channel_meta_resolve", result: rpctrace.ResultOK},
	}, observer.records, "forwarding has no local apply or runtime wait")
}
