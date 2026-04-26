package replica

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestDurableStoreBeginEpochValidatesLEOAndMonotonicHistory(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 4
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, env.snapshots)
	ctx := context.Background()

	point := channel.EpochPoint{Epoch: 8, StartOffset: 4}
	require.NoError(t, store.BeginEpoch(ctx, point, 4))
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, point}, env.history.points)

	require.NoError(t, store.BeginEpoch(ctx, point, 4), "same epoch point should be idempotent")
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, point}, env.history.points)

	err := store.BeginEpoch(ctx, channel.EpochPoint{Epoch: 9, StartOffset: 4}, 3)
	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, point}, env.history.points)

	err = store.BeginEpoch(ctx, channel.EpochPoint{Epoch: 7, StartOffset: 5}, 4)
	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, point}, env.history.points)
}

func TestDurableStoreAppendLeaderBatchSyncsAndReturnsLEORange(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 2
	store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, env.snapshots)

	oldLEO, newLEO, err := store.AppendLeaderBatch(context.Background(), []channel.Record{
		{Payload: []byte("a"), SizeBytes: 1},
		{Payload: []byte("b"), SizeBytes: 1},
	})
	require.NoError(t, err)
	require.Equal(t, uint64(2), oldLEO)
	require.Equal(t, uint64(4), newLEO)
	require.Equal(t, uint64(4), env.log.LEO())
	require.Equal(t, 1, env.log.appendCount)
	require.Equal(t, 1, env.log.syncCount)
}

func TestDurableStoreApplyFollowerBatchFallbackPersistsRecordsAndCheckpointWithoutEpoch(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 3
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 2}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, env.snapshots)
	checkpoint := channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 5}

	newLEO, err := store.ApplyFollowerBatch(context.Background(), channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 2,
		Records: []channel.Record{
			{Index: 4, Payload: []byte("d"), SizeBytes: 1},
			{Index: 5, Payload: []byte("e"), SizeBytes: 1},
		},
		Checkpoint: &checkpoint,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, uint64(5), newLEO)
	require.Equal(t, uint64(5), env.log.LEO())
	require.Equal(t, 1, env.log.appendCount)
	require.Equal(t, 1, env.log.syncCount)
	require.Equal(t, checkpoint, env.checkpoints.lastStored())
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}, env.history.points)
}

func TestDurableStoreApplyFollowerBatchSplitFallbackPersistsEpochRecordsAndCheckpoint(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 3
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 2}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, env.snapshots)
	checkpoint := channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 4}

	leo, err := store.ApplyFollowerBatch(context.Background(), channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 2,
		Records:             []channel.Record{{Index: 4, Payload: []byte("d"), SizeBytes: 1}},
		Checkpoint:          &checkpoint,
	}, &channel.EpochPoint{Epoch: 8, StartOffset: 3})

	require.NoError(t, err)
	require.Equal(t, uint64(4), leo)
	require.Equal(t, uint64(4), env.log.LEO())
	require.Equal(t, 1, env.log.appendCount)
	require.Equal(t, checkpoint, env.checkpoints.lastStored())
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, {Epoch: 8, StartOffset: 3}}, env.history.points)
}

func TestDurableStoreApplyFollowerBatchPlainStorePersistsEpochBeforeApplyFetch(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 3
	env.applyFetch = &fakeApplyFetchStore{leo: 3}
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 2}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	store := newDurableReplicaStore(env.log, env.checkpoints, env.applyFetch, env.history, env.snapshots)

	leo, err := store.ApplyFollowerBatch(context.Background(), channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 2,
		Records:             []channel.Record{{Index: 4, Payload: []byte("d"), SizeBytes: 1}},
		Checkpoint:          &channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 4},
	}, &channel.EpochPoint{Epoch: 8, StartOffset: 3})

	require.NoError(t, err)
	require.Equal(t, uint64(4), leo)
	require.Equal(t, 1, env.applyFetch.calls)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, {Epoch: 8, StartOffset: 3}}, env.history.points)
}

func TestDurableStoreApplyFollowerBatchUsesAtomicStoreWhenAvailable(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 3
	combined := &fakeCombinedApplyFetchStore{leo: 3, history: env.history, checkpoints: env.checkpoints}
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 2}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	store := newDurableReplicaStore(env.log, env.checkpoints, combined, env.history, env.snapshots)
	checkpoint := channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 4}

	newLEO, err := store.ApplyFollowerBatch(context.Background(), channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 2,
		Records:             []channel.Record{{Index: 4, Payload: []byte("d"), SizeBytes: 1}},
		Checkpoint:          &checkpoint,
	}, &channel.EpochPoint{Epoch: 8, StartOffset: 3})

	require.NoError(t, err)
	require.Equal(t, uint64(4), newLEO)
	require.Equal(t, 1, combined.combinedCalls)
	require.Equal(t, 0, combined.calls)
	require.Equal(t, 0, env.log.appendCount, "apply-fetch store owns record durability when configured")
	require.Equal(t, checkpoint, env.checkpoints.lastStored())
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, {Epoch: 8, StartOffset: 3}}, env.history.points)
}

func TestDurableStoreApplyFollowerBatchRejectsCheckpointBeyondNewLEOBeforeStore(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 3
	combined := &fakeCombinedApplyFetchStore{leo: 3, history: env.history, checkpoints: env.checkpoints}
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 2}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	store := newDurableReplicaStore(env.log, env.checkpoints, combined, env.history, env.snapshots)

	_, err := store.ApplyFollowerBatch(context.Background(), channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 2,
		Records:             []channel.Record{{Index: 4, Payload: []byte("d"), SizeBytes: 1}},
		Checkpoint:          &channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 5},
	}, &channel.EpochPoint{Epoch: 8, StartOffset: 3})

	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Zero(t, combined.combinedCalls)
	require.Empty(t, env.checkpoints.stored)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}, env.history.points)
}

func TestDurableStoreApplyFollowerBatchRejectsPlainCheckpointBeyondNewLEOBeforeStore(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 3
	env.applyFetch = &fakeApplyFetchStore{leo: 3}
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 2}
	store := newDurableReplicaStore(env.log, env.checkpoints, env.applyFetch, env.history, env.snapshots)

	_, err := store.ApplyFollowerBatch(context.Background(), channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 2,
		Records:             []channel.Record{{Index: 4, Payload: []byte("d"), SizeBytes: 1}},
		Checkpoint:          &channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 5},
	}, nil)

	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Zero(t, env.applyFetch.calls)
	require.Empty(t, env.checkpoints.stored)
}

func TestDurableStoreApplyFollowerBatchSplitFallbackRejectsCheckpointRegressionBeforeMutation(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 3
	env.log.records = []channel.Record{
		{Index: 1, Payload: []byte("a"), SizeBytes: 1},
		{Index: 2, Payload: []byte("b"), SizeBytes: 1},
		{Index: 3, Payload: []byte("c"), SizeBytes: 1},
	}
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 9, LogStartOffset: 1, HW: 4}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, env.snapshots)

	_, err := store.ApplyFollowerBatch(context.Background(), channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 3,
		Records:             []channel.Record{{Index: 4, Payload: []byte("d"), SizeBytes: 1}},
		Checkpoint:          &channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 4},
	}, &channel.EpochPoint{Epoch: 8, StartOffset: 3})

	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Equal(t, uint64(3), env.log.LEO())
	require.Equal(t, []channel.Record{
		{Index: 1, Payload: []byte("a"), SizeBytes: 1},
		{Index: 2, Payload: []byte("b"), SizeBytes: 1},
		{Index: 3, Payload: []byte("c"), SizeBytes: 1},
	}, env.log.records)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}, env.history.points)
	require.Empty(t, env.checkpoints.stored)
}

func TestDurableStoreApplyFollowerBatchPlainStoreRejectsCheckpointRegressionBeforeMutation(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 3
	env.applyFetch = &fakeApplyFetchStore{leo: 3}
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 9, LogStartOffset: 1, HW: 4}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	store := newDurableReplicaStore(env.log, env.checkpoints, env.applyFetch, env.history, env.snapshots)

	_, err := store.ApplyFollowerBatch(context.Background(), channel.ApplyFetchStoreRequest{
		PreviousCommittedHW: 3,
		Records:             []channel.Record{{Index: 4, Payload: []byte("d"), SizeBytes: 1}},
		Checkpoint:          &channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 4},
	}, &channel.EpochPoint{Epoch: 8, StartOffset: 3})

	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Zero(t, env.applyFetch.calls)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}, env.history.points)
	require.Empty(t, env.checkpoints.stored)
}

func TestDurableStoreTruncateLogAndHistory(t *testing.T) {
	env := newTestEnv(t)
	env.log.records = []channel.Record{
		{Payload: []byte("1"), SizeBytes: 1},
		{Payload: []byte("2"), SizeBytes: 1},
		{Payload: []byte("3"), SizeBytes: 1},
		{Payload: []byte("4"), SizeBytes: 1},
		{Payload: []byte("5"), SizeBytes: 1},
	}
	env.log.leo = 5
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{
		{Epoch: 7, StartOffset: 0},
		{Epoch: 8, StartOffset: 3},
		{Epoch: 9, StartOffset: 5},
	}
	store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, env.snapshots)

	require.NoError(t, store.TruncateLogAndHistory(context.Background(), 3))

	require.Equal(t, uint64(3), env.log.LEO())
	require.Equal(t, []uint64{3}, env.log.truncateCalls)
	require.Equal(t, 1, env.log.syncCount)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, {Epoch: 8, StartOffset: 3}}, env.history.points)
	require.Equal(t, []uint64{3}, env.history.truncate)
}

func TestDurableStoreStoreCheckpointMonotonicValidation(t *testing.T) {
	tests := []struct {
		name       string
		current    channel.Checkpoint
		next       channel.Checkpoint
		visibleHW  uint64
		leo        uint64
		wantErr    error
		wantStored bool
	}{
		{
			name:       "rejects checkpoint above visible high watermark",
			current:    channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3},
			next:       channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 5},
			visibleHW:  4,
			leo:        5,
			wantErr:    channel.ErrCorruptState,
			wantStored: false,
		},
		{
			name:       "rejects checkpoint above leo",
			current:    channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3},
			next:       channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 5},
			visibleHW:  5,
			leo:        4,
			wantErr:    channel.ErrCorruptState,
			wantStored: false,
		},
		{
			name:       "rejects high watermark regression",
			current:    channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3},
			next:       channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 2},
			visibleHW:  3,
			leo:        3,
			wantErr:    channel.ErrCorruptState,
			wantStored: false,
		},
		{
			name:       "rejects log start beyond high watermark",
			current:    channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3},
			next:       channel.Checkpoint{Epoch: 7, LogStartOffset: 4, HW: 3},
			visibleHW:  3,
			leo:        3,
			wantErr:    channel.ErrCorruptState,
			wantStored: false,
		},
		{
			name:       "rejects epoch regression even when high watermark advances",
			current:    channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 3},
			next:       channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 5},
			visibleHW:  5,
			leo:        5,
			wantErr:    channel.ErrCorruptState,
			wantStored: false,
		},
		{
			name:       "accepts monotonic checkpoint",
			current:    channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3},
			next:       channel.Checkpoint{Epoch: 8, LogStartOffset: 1, HW: 5},
			visibleHW:  5,
			leo:        5,
			wantStored: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.checkpoints.loadErr = nil
			env.checkpoints.checkpoint = tt.current
			store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, env.snapshots)

			err := store.StoreCheckpointMonotonic(context.Background(), tt.next, tt.visibleHW, tt.leo)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Empty(t, env.checkpoints.stored)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantStored, len(env.checkpoints.stored) == 1)
			require.Equal(t, tt.next, env.checkpoints.lastStored())
		})
	}
}

func TestDurableStoreInstallSnapshotAtomicallyPersistsPayloadCheckpointAndEpoch(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 8
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 6}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, env.snapshots)
	snap := channel.Snapshot{ChannelKey: "group-10", Epoch: 8, EndOffset: 8, Payload: []byte("snap")}
	checkpoint := channel.Checkpoint{Epoch: 8, LogStartOffset: 8, HW: 8}
	point := channel.EpochPoint{Epoch: 8, StartOffset: 8}

	leo, err := store.InstallSnapshotAtomically(context.Background(), snap, checkpoint, point)

	require.NoError(t, err)
	require.Equal(t, uint64(8), leo)
	require.Equal(t, []channel.Snapshot{snap}, env.snapshots.installed)
	require.Equal(t, checkpoint, env.checkpoints.lastStored())
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}, point}, env.history.points)
}

func TestDurableStoreInstallSnapshotSplitFallbackRejectsCheckpointRegressionBeforeMutation(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 4
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 9, LogStartOffset: 3, HW: 4}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, env.snapshots)

	_, err := store.InstallSnapshotAtomically(
		context.Background(),
		channel.Snapshot{ChannelKey: "group-10", Epoch: 8, EndOffset: 4, Payload: []byte("stale-snap")},
		channel.Checkpoint{Epoch: 8, LogStartOffset: 4, HW: 4},
		channel.EpochPoint{Epoch: 8, StartOffset: 4},
	)

	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Empty(t, env.snapshots.installed)
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}, env.history.points)
	require.Empty(t, env.checkpoints.stored)
}

func TestDurableStorePublicInstallSnapshotUsesDurableAdapter(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	adapterErr := errors.New("adapter called")
	spy := &spyDurableStore{installErr: adapterErr}
	env.replica.durable = spy

	err := env.replica.InstallSnapshot(context.Background(), channel.Snapshot{
		ChannelKey: "group-10",
		Epoch:      7,
		EndOffset:  8,
		Payload:    []byte("snap"),
	})

	require.ErrorIs(t, err, adapterErr)
	require.Equal(t, 1, spy.installCalls)
	require.Equal(t, channel.Checkpoint{Epoch: 7, LogStartOffset: 8, HW: 8}, spy.checkpoint)
	require.Equal(t, channel.EpochPoint{Epoch: 7, StartOffset: 0}, spy.epochPoint)
}

func TestDurableStoreRecoverRejectsUnsafePartialStates(t *testing.T) {
	tests := []struct {
		name       string
		leo        uint64
		checkpoint channel.Checkpoint
		history    []channel.EpochPoint
		snapshot   *channel.Snapshot
	}{
		{
			name:       "checkpoint beyond leo",
			leo:        4,
			checkpoint: channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 5},
			history:    []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
		},
		{
			name:       "history start beyond leo",
			leo:        4,
			checkpoint: channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 4},
			history:    []channel.EpochPoint{{Epoch: 7, StartOffset: 5}},
		},
		{
			name:       "snapshot checkpoint without compatible history",
			leo:        4,
			checkpoint: channel.Checkpoint{Epoch: 7, LogStartOffset: 4, HW: 4},
			history:    nil,
		},
		{
			name:       "checkpoint epoch incompatible with committed history",
			leo:        5,
			checkpoint: channel.Checkpoint{Epoch: 8, LogStartOffset: 0, HW: 5},
			history:    []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
		},
		{
			name:       "snapshot checkpoint and history without payload",
			leo:        4,
			checkpoint: channel.Checkpoint{Epoch: 7, LogStartOffset: 4, HW: 4},
			history:    []channel.EpochPoint{{Epoch: 7, StartOffset: 4}},
		},
		{
			name:       "snapshot payload without compatible checkpoint",
			leo:        4,
			checkpoint: channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 4},
			history:    []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
			snapshot:   &channel.Snapshot{ChannelKey: "group-10", Epoch: 7, EndOffset: 4, Payload: []byte("snap")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.log.leo = tt.leo
			env.checkpoints.loadErr = nil
			env.checkpoints.checkpoint = tt.checkpoint
			env.history.loadErr = nil
			env.history.points = append([]channel.EpochPoint(nil), tt.history...)
			if tt.snapshot != nil {
				env.snapshots.installed = []channel.Snapshot{cloneSnapshot(*tt.snapshot)}
			}
			store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, env.snapshots)

			_, err := store.Recover(context.Background())

			require.True(t, errors.Is(err, channel.ErrCorruptState), "Recover() error = %v", err)
		})
	}
}

func TestDurableStoreRecoverAllowsSnapshotCheckpointWhenPayloadLoaderUnavailable(t *testing.T) {
	env := newTestEnv(t)
	env.log.leo = 4
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 4, HW: 4}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 4}}
	store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, writeOnlySnapshotApplier{})

	view, err := store.Recover(context.Background())

	require.NoError(t, err)
	require.Equal(t, uint64(4), view.LEO)
	require.Equal(t, env.checkpoints.checkpoint, view.Checkpoint)
	require.Equal(t, env.history.points, view.EpochHistory)
}

func TestDurableStoreRecoverValidatesPayloadOnlySnapshotLoader(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantErr error
	}{
		{name: "missing payload", wantErr: channel.ErrCorruptState},
		{name: "present payload", payload: []byte("snap")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.log.leo = 4
			env.checkpoints.loadErr = nil
			env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 4, HW: 4}
			env.history.loadErr = nil
			env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 4}}
			store := newDurableReplicaStore(env.log, env.checkpoints, nil, env.history, payloadSnapshotApplier{payload: tt.payload})

			_, err := store.Recover(context.Background())

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

type fakeCombinedApplyFetchStore struct {
	calls         int
	combinedCalls int
	leo           uint64
	lastReq       channel.ApplyFetchStoreRequest
	lastEpoch     *channel.EpochPoint
	history       *fakeEpochHistoryStore
	checkpoints   *fakeCheckpointStore
}

func (f *fakeCombinedApplyFetchStore) StoreApplyFetch(req channel.ApplyFetchStoreRequest) (uint64, error) {
	f.calls++
	return 0, errors.New("plain apply-fetch path used")
}

func (f *fakeCombinedApplyFetchStore) StoreApplyFetchWithEpoch(req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint) (uint64, error) {
	f.combinedCalls++
	f.lastReq = cloneApplyFetchStoreRequest(req)
	if epochPoint != nil {
		point := *epochPoint
		f.lastEpoch = &point
		if f.history != nil {
			if err := f.history.Append(point); err != nil {
				return 0, err
			}
		}
	}
	if req.Checkpoint != nil && f.checkpoints != nil {
		if err := f.checkpoints.Store(*req.Checkpoint); err != nil {
			return 0, err
		}
	}
	f.leo += uint64(len(req.Records))
	return f.leo, nil
}

type spyDurableStore struct {
	recoverView      durableView
	beginEpochCalls  int
	beginEpochPoint  channel.EpochPoint
	beginExpectedLEO uint64
	appendCalls      int
	appendBase       uint64
	appendLEO        uint64
	appendRecords    []channel.Record
	truncateCalls    int
	truncateTo       uint64
	installCalls     int
	installErr       error
	snapshot         channel.Snapshot
	checkpoint       channel.Checkpoint
	epochPoint       channel.EpochPoint
	applyCalls       int
	applyLEO         uint64
	applyErr         error
	applyReq         channel.ApplyFetchStoreRequest
	applyEpochPoint  *channel.EpochPoint
	checkpointCalls  int
	storedCheckpoint channel.Checkpoint
	visibleHW        uint64
	checkpointLEO    uint64
}

func (s *spyDurableStore) Recover(context.Context) (durableView, error) {
	return s.recoverView, nil
}

func (s *spyDurableStore) BeginEpoch(_ context.Context, point channel.EpochPoint, expectedLEO uint64) error {
	s.beginEpochCalls++
	s.beginEpochPoint = point
	s.beginExpectedLEO = expectedLEO
	return nil
}

func (s *spyDurableStore) AppendLeaderBatch(_ context.Context, records []channel.Record) (uint64, uint64, error) {
	s.appendCalls++
	s.appendRecords = cloneRecords(records)
	return s.appendBase, s.appendLEO, nil
}

func (s *spyDurableStore) ApplyFollowerBatch(_ context.Context, req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint) (uint64, error) {
	s.applyCalls++
	s.applyReq = cloneApplyFetchStoreRequest(req)
	if epochPoint != nil {
		point := *epochPoint
		s.applyEpochPoint = &point
	}
	return s.applyLEO, s.applyErr
}

func (s *spyDurableStore) TruncateLogAndHistory(_ context.Context, to uint64) error {
	s.truncateCalls++
	s.truncateTo = to
	return nil
}

func (s *spyDurableStore) StoreCheckpointMonotonic(_ context.Context, checkpoint channel.Checkpoint, visibleHW uint64, leo uint64) error {
	s.checkpointCalls++
	s.storedCheckpoint = checkpoint
	s.visibleHW = visibleHW
	s.checkpointLEO = leo
	return nil
}

func (s *spyDurableStore) InstallSnapshotAtomically(_ context.Context, snap channel.Snapshot, checkpoint channel.Checkpoint, epochPoint channel.EpochPoint) (uint64, error) {
	s.installCalls++
	s.snapshot = cloneSnapshot(snap)
	s.checkpoint = checkpoint
	s.epochPoint = epochPoint
	return 0, s.installErr
}

type writeOnlySnapshotApplier struct{}

func (writeOnlySnapshotApplier) InstallSnapshot(context.Context, channel.Snapshot) error {
	return nil
}

type payloadSnapshotApplier struct {
	payload []byte
}

func (p payloadSnapshotApplier) InstallSnapshot(context.Context, channel.Snapshot) error {
	return nil
}

func (p payloadSnapshotApplier) LoadSnapshotPayload(context.Context) ([]byte, error) {
	if p.payload == nil {
		return nil, channel.ErrEmptyState
	}
	return append([]byte(nil), p.payload...), nil
}
