package reactor

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/stretchr/testify/require"
)

func TestRetentionViewReportsRuntimeAndStoreState(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := testMeta("retention-view", 1, 1)
	seedRetentionRecords(t, factory, meta, 3, 3)
	cs := retentionStore(t, factory, meta)
	_, err := cs.AdoptRetentionBoundary(context.Background(), 2, "committed")
	require.NoError(t, err)
	_, err = cs.TrimMessagesThrough(context.Background(), 1, store.RetentionTrimOptions{})
	require.NoError(t, err)
	meta.RetentionThroughSeq = 2

	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))

	future := NewFuture()
	r.handleRetentionView(Event{Kind: EventRetentionView, Key: meta.Key, Future: future})
	view := awaitFutureResult(t, future).RetentionView

	require.Equal(t, meta.Key, view.Key)
	require.Equal(t, meta.ID, view.ChannelID)
	require.Equal(t, ch.RoleLeader, view.Role)
	require.Equal(t, uint64(2), view.RetentionThroughSeq)
	require.Equal(t, uint64(2), view.LocalRetentionThroughSeq)
	require.Equal(t, uint64(1), view.PhysicalRetentionThroughSeq)
	require.Equal(t, uint64(3), view.LEO)
	require.Equal(t, uint64(3), view.HW)
	require.Equal(t, uint64(3), view.CheckpointHW)
	require.Equal(t, uint64(3), view.MinISRMatchOffset)
}

func TestApplyRetentionBoundaryPublishesLogicalFloorBeforeTrim(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := testMeta("retention-apply-publish", 1, 1)
	seedRetentionRecords(t, factory, meta, 3, 3)
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))

	future := NewFuture()
	r.handleApplyRetentionBoundary(Event{
		Kind:   EventApplyRetentionBoundary,
		Key:    meta.Key,
		Future: future,
		RetentionApply: ch.RetentionApplyRequest{
			ChannelID:  meta.ID,
			ThroughSeq: 2,
			Options:    ch.RetentionApplyOptions{MaxTrimMessages: 1},
		},
	})
	require.Equal(t, uint64(2), r.channels[meta.Key].state.RetentionThroughSeq)

	result := sink.awaitResultKind(t, worker.TaskStoreRetention)
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: result})
	applied := awaitFutureResult(t, future).RetentionApply

	require.Equal(t, uint64(2), applied.ThroughSeq)
	require.Equal(t, uint64(2), applied.LocalRetentionThroughSeq)
	require.Equal(t, uint64(1), applied.PhysicalRetentionThroughSeq)
	require.Equal(t, uint64(1), applied.DeletedThroughSeq)
	require.Equal(t, 1, applied.Deleted)
	require.True(t, applied.More)
	require.Empty(t, applied.BlockedReason)
}

func TestApplyRetentionBoundaryBlocksTrimWhenCheckpointLags(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := testMeta("retention-checkpoint-lag", 1, 1)
	seedRetentionRecords(t, factory, meta, 3, 1)
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	r.channels[meta.Key].state.HW = 3

	future := NewFuture()
	r.handleApplyRetentionBoundary(Event{
		Kind:   EventApplyRetentionBoundary,
		Key:    meta.Key,
		Future: future,
		RetentionApply: ch.RetentionApplyRequest{
			ChannelID:  meta.ID,
			ThroughSeq: 2,
		},
	})
	checkpoint := sink.awaitResultKind(t, worker.TaskStoreCheckpoint)
	require.Equal(t, uint64(2), checkpoint.StoreCheckpoint.Checkpoint.HW)
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: checkpoint})
	require.Equal(t, uint64(2), r.channels[meta.Key].state.CheckpointHW)

	result := sink.awaitResultKind(t, worker.TaskStoreRetention)
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: result})
	applied := awaitFutureResult(t, future).RetentionApply

	require.Equal(t, ch.RetentionBlockedCheckpointLag, applied.BlockedReason)
	require.Equal(t, uint64(2), applied.LocalRetentionThroughSeq)
	require.Equal(t, uint64(0), applied.PhysicalRetentionThroughSeq)
	require.Zero(t, applied.Deleted)

	next := NewFuture()
	r.handleApplyRetentionBoundary(Event{
		Kind:   EventApplyRetentionBoundary,
		Key:    meta.Key,
		Future: next,
		RetentionApply: ch.RetentionApplyRequest{
			ChannelID:  meta.ID,
			ThroughSeq: 2,
		},
	})
	result = sink.awaitResultKind(t, worker.TaskStoreRetention)
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: result})
	applied = awaitFutureResult(t, next).RetentionApply

	require.Empty(t, applied.BlockedReason)
	require.Equal(t, uint64(2), applied.PhysicalRetentionThroughSeq)
	require.Equal(t, 2, applied.Deleted)
}

func TestLeaderRetentionTrimBlocksWhenMinISRLags(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := testMeta("retention-min-isr-lag", 1, 1)
	meta.Replicas = []ch.NodeID{1, 2}
	meta.ISR = []ch.NodeID{1, 2}
	meta.MinISR = 2
	seedRetentionRecords(t, factory, meta, 5, 5)
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	r.channels[meta.Key].state.Progress[2] = machine.ReplicaProgress{Match: 3}

	future := NewFuture()
	r.handleApplyRetentionBoundary(Event{
		Kind:   EventApplyRetentionBoundary,
		Key:    meta.Key,
		Future: future,
		RetentionApply: ch.RetentionApplyRequest{
			ChannelID:  meta.ID,
			ThroughSeq: 4,
		},
	})
	result := sink.awaitResultKind(t, worker.TaskStoreRetention)
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: result})
	applied := awaitFutureResult(t, future).RetentionApply

	require.Equal(t, ch.RetentionBlockedMinISRLag, applied.BlockedReason)
	require.Equal(t, uint64(4), applied.LocalRetentionThroughSeq)
	require.Equal(t, uint64(0), applied.PhysicalRetentionThroughSeq)
	require.Zero(t, applied.Deleted)
}

func TestApplyRetentionBoundaryUpdatesPhysicalProgressAfterWorkerResult(t *testing.T) {
	factory := store.NewMemoryFactory()
	meta := testMeta("retention-physical-progress", 1, 1)
	seedRetentionRecords(t, factory, meta, 2, 2)
	sink := captureCompletionSink{results: make(chan worker.Result, 8)}
	pools := newDirectTestPools(t, factory, sink)
	defer pools.Close()
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: factory, Pools: pools, MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))

	future := NewFuture()
	r.handleApplyRetentionBoundary(Event{
		Kind:   EventApplyRetentionBoundary,
		Key:    meta.Key,
		Future: future,
		RetentionApply: ch.RetentionApplyRequest{
			ChannelID:  meta.ID,
			ThroughSeq: 2,
		},
	})
	r.handleWorkerResult(Event{Kind: EventWorkerResult, Worker: sink.awaitResultKind(t, worker.TaskStoreRetention)})
	applied := awaitFutureResult(t, future).RetentionApply

	require.Equal(t, uint64(2), applied.PhysicalRetentionThroughSeq)
	require.Equal(t, uint64(2), r.channels[meta.Key].state.PhysicalRetentionThroughSeq)
	require.Equal(t, uint64(2), r.channels[meta.Key].state.LocalRetentionThroughSeq)
}

func seedRetentionRecords(t *testing.T, factory store.Factory, meta ch.Meta, records int, checkpoint uint64) {
	t.Helper()
	cs := retentionStore(t, factory, meta)
	items := make([]ch.Record, 0, records)
	for i := 1; i <= records; i++ {
		items = append(items, ch.Record{ID: uint64(i), Payload: []byte{byte(i)}, SizeBytes: 1})
	}
	_, err := cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: items})
	require.NoError(t, err)
	require.NoError(t, cs.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: checkpoint}))
}

func retentionStore(t *testing.T, factory store.Factory, meta ch.Meta) store.ChannelStore {
	t.Helper()
	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	return cs
}

func awaitRetentionResult(t *testing.T, future *Future) ch.RetentionApplyResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := future.Await(ctx)
	require.NoError(t, err)
	return result.RetentionApply
}
