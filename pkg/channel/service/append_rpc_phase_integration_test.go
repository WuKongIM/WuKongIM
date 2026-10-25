//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/stretchr/testify/require"
)

type serviceRPCPhaseRecord struct {
	stage  rpctrace.Stage
	result rpctrace.Result
	items  []rpctrace.Result
}

func TestAppendRPCPhasesRuntimeContextCancellationAfterAdmission(t *testing.T) {
	factory := newServiceBlockingAppendFactory()
	api, err := New(Config{LocalNode: 1, Store: factory, ReactorCount: 1, AppendBatchMaxRecords: 1})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, api.Close()) })
	t.Cleanup(factory.UnblockAppends)
	id := ch.ChannelID{ID: "rpc-runtime-cancel", Type: 2}
	meta := ch.Meta{ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
	require.NoError(t, api.ApplyMeta(meta))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observer := &serviceRPCPhaseObserver{}
	done := make(chan error, 1)
	go func() {
		_, err := api.AppendBatch(rpctrace.WithAppendPhaseObserver(ctx, observer), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{MessageID: 1, Payload: []byte("accepted")}}})
		done <- err
	}()
	factory.WaitAppendStarted(t)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("the admitted append wait did not observe runtime context cancellation")
	}
	require.Equal(t, []serviceRPCPhaseRecord{
		{stage: "channel_runtime_reserve", result: rpctrace.ResultOK},
		{stage: "channel_runtime_submit", result: rpctrace.ResultOK},
		{stage: "channel_runtime_future_wait", result: rpctrace.ResultCanceled},
	}, observer.records)
	factory.UnblockAppends()
	require.NoError(t, api.Close(), "close must join accepted work after the canceled wait")
}

type serviceRPCPhaseObserver struct {
	records []serviceRPCPhaseRecord
	before  func(rpctrace.Stage)
}

func (o *serviceRPCPhaseObserver) BeginAppendPhase(stage rpctrace.Stage) rpctrace.AppendPhaseEnd {
	if o.before != nil {
		o.before(stage)
	}
	return func(common rpctrace.Result, count int, at func(int) rpctrace.Result) {
		r := serviceRPCPhaseRecord{stage: stage, result: common}
		for i := 0; i < count; i++ {
			r.items = append(r.items, at(i))
		}
		o.records = append(o.records, r)
	}
}

func TestAppendRPCPhasesRuntimeAdmissionAndFuture(t *testing.T) {
	for _, mode := range []string{"success", "reserve-closed", "submit-closed", "future-failed"} {
		t.Run(mode, func(t *testing.T) {
			cfg := Config{LocalNode: 1, Store: store.NewMemoryFactory(), ReactorCount: 1, AppendBatchMaxRecords: 1}
			if mode == "future-failed" {
				cfg.AppendAdmissionGuard = ch.AppendAdmissionGuardFunc(func(context.Context, ch.AppendAdmissionRequest) error { return context.DeadlineExceeded })
			}
			api, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, api.Close()) })
			id := ch.ChannelID{ID: "rpc-runtime", Type: 2}
			meta := ch.Meta{ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
			require.NoError(t, api.ApplyMeta(meta))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "reserve-closed" {
				require.NoError(t, api.Close())
			}
			observer := &serviceRPCPhaseObserver{}
			if mode == "submit-closed" {
				// Close at the explicit observation boundary after reservation.
				// A canceled context alone does not reject Group.Submit.
				observer.before = func(stage rpctrace.Stage) {
					if stage == rpctrace.StageChannelRuntimeSubmit {
						require.NoError(t, api.Close())
					}
				}
			}
			got, err := api.AppendBatch(rpctrace.WithAppendPhaseObserver(ctx, observer), ch.AppendBatchRequest{ChannelID: id, Messages: []ch.Message{{MessageID: 1, Payload: []byte("first")}, {MessageID: 2, Payload: []byte("second")}}})
			want := []serviceRPCPhaseRecord{{stage: "channel_runtime_reserve", result: rpctrace.ResultOK}}
			switch mode {
			case "reserve-closed":
				require.Error(t, err)
				want[0].result = rpctrace.ResultFor(err)
			case "submit-closed":
				require.ErrorIs(t, err, ch.ErrClosed)
				want = append(want, serviceRPCPhaseRecord{stage: "channel_runtime_submit", result: rpctrace.ResultError})
			case "future-failed":
				require.ErrorIs(t, err, context.DeadlineExceeded)
				want = append(want, serviceRPCPhaseRecord{stage: "channel_runtime_submit", result: rpctrace.ResultOK}, serviceRPCPhaseRecord{stage: "channel_runtime_future_wait", result: rpctrace.ResultTimeout})
			default:
				require.NoError(t, err)
				require.Len(t, got.Items, 2)
				require.Equal(t, uint64(1), got.Items[0].MessageSeq)
				require.Equal(t, uint64(2), got.Items[1].MessageSeq)
				want = append(want, serviceRPCPhaseRecord{stage: "channel_runtime_submit", result: rpctrace.ResultOK}, serviceRPCPhaseRecord{stage: "channel_runtime_future_wait", result: rpctrace.ResultOK, items: []rpctrace.Result{rpctrace.ResultOK, rpctrace.ResultOK}})
			}
			require.Equal(t, want, observer.records)
		})
	}
}
