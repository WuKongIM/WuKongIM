package replica

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/stretchr/testify/require"
)

func TestReplicaApplyRetentionBoundaryPublishesLogicalFloorBeforePhysicalTrim(t *testing.T) {
	env := newTestEnv(t)
	env.replica = newReplicaFromEnv(t, env)

	require.NoError(t, env.replica.ApplyRetentionBoundary(context.Background(), 5))

	status := env.replica.Status()
	require.Equal(t, uint64(5), status.RetentionThroughSeq)
	require.Equal(t, uint64(6), status.MinAvailableSeq)
	require.Zero(t, status.PhysicalRetentionThroughSeq)
	require.Empty(t, env.log.trimCalls)
}

func TestReplicaApplyRetentionBoundaryRepeatedCompletedTrimIsNoop(t *testing.T) {
	env := newRetentionRecoveredEnv(t, 5, 5, 5)
	env.replica = newReplicaFromEnv(t, env)

	require.NoError(t, env.replica.ApplyRetentionBoundary(context.Background(), 5))
	require.Equal(t, []uint64{5}, env.log.adoptCalls)
	require.Equal(t, []uint64{5}, env.log.trimCalls)

	require.NoError(t, env.replica.ApplyRetentionBoundary(context.Background(), 5))
	require.Equal(t, []uint64{5}, env.log.adoptCalls)
	require.Equal(t, []uint64{5}, env.log.trimCalls)
}

func TestReplicaApplyRetentionBoundaryRetriesLaggingPhysicalTrimForEqualBoundary(t *testing.T) {
	env := newRetentionRecoveredEnv(t, 5, 5, 5)
	env.log.retention = channel.RetentionState{LocalRetentionThroughSeq: 5, PhysicalRetentionThroughSeq: 2, RetainedMaxSeq: 5}
	env.replica = newReplicaFromEnv(t, env)
	env.replica.state.RetentionThroughSeq = 5
	env.replica.publishStateLocked()

	require.NoError(t, env.replica.ApplyRetentionBoundary(context.Background(), 5))

	status := env.replica.Status()
	require.Equal(t, uint64(5), status.PhysicalRetentionThroughSeq)
	require.Empty(t, env.log.adoptCalls)
	require.Equal(t, []uint64{5}, env.log.trimCalls)
}

func TestReplicaApplyRetentionBoundaryLowerBoundaryIsNoop(t *testing.T) {
	env := newRetentionRecoveredEnv(t, 5, 5, 5)
	env.replica = newReplicaFromEnv(t, env)
	require.NoError(t, env.replica.ApplyRetentionBoundary(context.Background(), 5))
	env.log.adoptCalls = nil
	env.log.trimCalls = nil

	require.NoError(t, env.replica.ApplyRetentionBoundary(context.Background(), 4))

	status := env.replica.Status()
	require.Equal(t, uint64(5), status.RetentionThroughSeq)
	require.Equal(t, uint64(6), status.MinAvailableSeq)
	require.Empty(t, env.log.adoptCalls)
	require.Empty(t, env.log.trimCalls)
}

func TestReplicaApplyRetentionBoundaryPhysicalTrimRequiresAllGates(t *testing.T) {
	testCases := []struct {
		name        string
		commitReady bool
		checkpoint  uint64
		hw          uint64
		leo         uint64
		wantTrim    bool
	}{
		{name: "commit not ready", commitReady: false, checkpoint: 5, hw: 5, leo: 5},
		{name: "checkpoint lagging", commitReady: true, checkpoint: 4, hw: 5, leo: 5},
		{name: "hw lagging", commitReady: true, checkpoint: 5, hw: 4, leo: 5},
		{name: "leo behind", commitReady: true, checkpoint: 4, hw: 4, leo: 4},
		{name: "all gates pass", commitReady: true, checkpoint: 5, hw: 5, leo: 5, wantTrim: true},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			env := newRetentionRecoveredEnv(t, tt.leo, tt.hw, tt.checkpoint)
			env.replica = newReplicaFromEnv(t, env)
			env.replica.state.LEO = tt.leo
			env.replica.state.HW = tt.hw
			env.replica.state.CheckpointHW = tt.checkpoint
			env.replica.state.CommitReady = tt.commitReady
			env.replica.publishStateLocked()

			require.NoError(t, env.replica.ApplyRetentionBoundary(context.Background(), 5))

			if tt.wantTrim {
				require.Equal(t, []uint64{5}, env.log.trimCalls)
			} else {
				require.Empty(t, env.log.trimCalls)
			}
		})
	}
}

func TestReplicaApplyRetentionBoundaryTrimFailureKeepsLogicalFloor(t *testing.T) {
	env := newRetentionRecoveredEnv(t, 5, 5, 5)
	env.log.trimErr = errors.New("trim failed")
	env.replica = newReplicaFromEnv(t, env)

	err := env.replica.ApplyRetentionBoundary(context.Background(), 5)

	require.ErrorContains(t, err, "trim failed")
	status := env.replica.Status()
	require.Equal(t, uint64(5), status.RetentionThroughSeq)
	require.Equal(t, uint64(6), status.MinAvailableSeq)
	require.Zero(t, status.PhysicalRetentionThroughSeq)
}

func TestReplicaRetentionViewMinISRMatchOffsetDoesNotAdvanceUnknownFollowersByDefault(t *testing.T) {
	env := newRetentionRecoveredEnv(t, 10, 10, 10)
	env.replica = newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(7, 1, 2)
	meta.RetentionThroughSeq = 4
	require.NoError(t, env.replica.BecomeLeader(meta))

	view, err := env.replica.RetentionView()

	require.NoError(t, err)
	require.Equal(t, uint64(4), view.MinISRMatchOffset)
	require.Equal(t, uint64(4), view.RetentionThroughSeq)
	require.Equal(t, uint64(5), view.MinAvailableSeq)
}

func TestReplicaRecoveryLoadsLocalRetentionStateAndRetainedLEOFloor(t *testing.T) {
	env := newRetentionRecoveredEnv(t, 1, 0, 0)
	env.log.retention = channel.RetentionState{LocalRetentionThroughSeq: 5, PhysicalRetentionThroughSeq: 4, RetainedMaxSeq: 5}

	r := newReplicaFromEnv(t, env)
	status := r.Status()

	require.Equal(t, uint64(5), status.LocalRetentionThroughSeq)
	require.Equal(t, uint64(4), status.PhysicalRetentionThroughSeq)
	require.Equal(t, uint64(5), status.LEO)
	require.Zero(t, status.RetentionThroughSeq)
	require.Zero(t, status.LogStartOffset)
}

func TestReplicaApplyRetentionBoundaryLocalLEOBehindAppliesResetAndStaysNotReady(t *testing.T) {
	env := newTestEnv(t)
	env.replica = newReplicaFromEnv(t, env)

	require.NoError(t, env.replica.ApplyRetentionBoundary(context.Background(), 5))
	status := env.replica.Status()

	require.Equal(t, uint64(5), status.LocalRetentionThroughSeq)
	require.Equal(t, uint64(5), status.LEO)
	require.False(t, status.CommitReady)
}

func TestReplicaRetentionResetOldNonISRReplicaCatchesUpFromBoundary(t *testing.T) {
	leaderEnv := newRetentionRecoveredEnv(t, 6, 6, 6)
	leaderEnv.log.records = retentionFetchRecords(6)
	leaderEnv.replica = newReplicaFromEnv(t, leaderEnv)
	meta := activeMetaWithMinISR(7, 1, 1)
	meta.Replicas = []channel.NodeID{1, 2}
	meta.ISR = []channel.NodeID{1}
	meta.RetentionThroughSeq = 5
	require.NoError(t, leaderEnv.replica.BecomeLeader(meta))

	followerEnv := newTestEnv(t)
	followerEnv.localNode = 2
	followerEnv.replica = newReplicaFromEnv(t, followerEnv)
	require.NoError(t, followerEnv.replica.BecomeFollower(meta))
	require.NoError(t, followerEnv.replica.ApplyRetentionBoundary(context.Background(), 5))
	resetStatus := followerEnv.replica.Status()
	require.Equal(t, uint64(5), resetStatus.LocalRetentionThroughSeq)
	require.Equal(t, uint64(5), resetStatus.LEO)
	require.False(t, resetStatus.CommitReady)

	result, err := leaderEnv.replica.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		ReplicaID:   2,
		FetchOffset: 5,
		OffsetEpoch: meta.Epoch,
		MaxBytes:    1024,
	})
	require.NoError(t, err)
	require.Nil(t, result.RetentionReset)
	require.NotEmpty(t, result.Records)
	require.Equal(t, uint64(6), result.Records[0].Index)
	require.NoError(t, followerEnv.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
		ChannelKey: meta.Key,
		Epoch:      result.Epoch,
		Leader:     1,
		Records:    result.Records,
		LeaderHW:   result.HW,
	}))

	caughtUp := followerEnv.replica.Status()
	require.Equal(t, uint64(6), caughtUp.LEO)
	require.Equal(t, channel.ReplicaRoleFollower, caughtUp.Role)
	followerEnv.replica.mu.RLock()
	isr := append([]channel.NodeID(nil), followerEnv.replica.meta.ISR...)
	followerEnv.replica.mu.RUnlock()
	require.NotContains(t, isr, channel.NodeID(2))
}

func TestReplicaFetchBelowRetentionFloorReturnsRetentionResetUnlessSnapshotDominates(t *testing.T) {
	env := newRetentionRecoveredEnv(t, 10, 10, 10)
	env.log.records = retentionFetchRecords(10)
	env.replica = newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(7, 1, 1)
	meta.RetentionThroughSeq = 5
	require.NoError(t, env.replica.BecomeLeader(meta))

	result, err := env.replica.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey: env.replica.state.ChannelKey,
		Epoch:      env.replica.state.Epoch,
		ReplicaID:  2,
		// Offset 3 is below RetentionThroughSeq=5 while LogStartOffset=0.
		FetchOffset: 3,
		MaxBytes:    1024,
	})
	require.NoError(t, err)
	require.NotNil(t, result.RetentionReset)
	require.Equal(t, uint64(5), result.RetentionReset.RetentionThroughSeq)
	require.Equal(t, uint64(6), result.RetentionReset.MinAvailableSeq)

	env.replica.mu.Lock()
	env.replica.state.LogStartOffset = 8
	env.replica.publishStateLocked()
	env.replica.mu.Unlock()
	_, err = env.replica.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey:  env.replica.state.ChannelKey,
		Epoch:       env.replica.state.Epoch,
		ReplicaID:   2,
		FetchOffset: 4,
		MaxBytes:    1024,
	})
	require.ErrorIs(t, err, channel.ErrSnapshotRequired)
}

func TestReplicaFetchAtRetentionBoundaryReturnsNextVisibleRecord(t *testing.T) {
	env := newRetentionRecoveredEnv(t, 10, 10, 10)
	env.log.records = retentionFetchRecords(10)
	env.replica = newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(7, 1, 1)
	meta.RetentionThroughSeq = 5
	require.NoError(t, env.replica.BecomeLeader(meta))

	env.replica.mu.Lock()
	env.replica.state.HW = 10
	env.replica.state.CheckpointHW = 10
	env.replica.publishStateLocked()
	env.replica.mu.Unlock()

	result, err := env.replica.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey:  env.replica.state.ChannelKey,
		Epoch:       env.replica.state.Epoch,
		ReplicaID:   2,
		FetchOffset: 5,
		OffsetEpoch: env.replica.state.OffsetEpoch,
		MaxBytes:    1024,
	})
	require.NoError(t, err)
	require.Nil(t, result.RetentionReset)
	require.NotEmpty(t, result.Records)
	require.Equal(t, uint64(6), result.Records[0].Index)
}

func TestReplicaFetchInFlightReadRechecksRetentionFloor(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	env.log.readStarted = make(chan struct{}, 1)
	env.log.readContinue = make(chan struct{})
	status := env.replica.Status()

	type fetchOutcome struct {
		result channel.ReplicaFetchResult
		err    error
	}
	done := make(chan fetchOutcome, 1)
	go func() {
		result, err := env.replica.Fetch(context.Background(), channel.ReplicaFetchRequest{
			ChannelKey:  status.ChannelKey,
			Epoch:       status.Epoch,
			ReplicaID:   2,
			FetchOffset: 4,
			OffsetEpoch: status.OffsetEpoch,
			MaxBytes:    1024,
		})
		done <- fetchOutcome{result: result, err: err}
	}()

	select {
	case <-env.log.readStarted:
	case <-time.After(time.Second):
		t.Fatal("fetch did not start reading the log")
	}
	require.NoError(t, env.replica.ApplyRetentionBoundary(context.Background(), 5))
	close(env.log.readContinue)

	select {
	case outcome := <-done:
		require.NoError(t, outcome.err)
		require.Empty(t, outcome.result.Records)
		require.NotNil(t, outcome.result.RetentionReset)
		require.Equal(t, uint64(5), outcome.result.RetentionReset.RetentionThroughSeq)
		require.Equal(t, uint64(6), outcome.result.RetentionReset.MinAvailableSeq)
	case <-time.After(time.Second):
		t.Fatal("fetch did not return after read was released")
	}
}

func TestReplicaRetentionViewResetsFollowerProgressOnLeaderMetaRefresh(t *testing.T) {
	env := newRetentionRecoveredEnv(t, 10, 4, 4)
	env.replica = newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(7, 1, 2)
	meta.LeaderEpoch = 1
	meta.Replicas = []channel.NodeID{1, 2}
	meta.ISR = []channel.NodeID{1, 2}
	meta.RetentionThroughSeq = 4
	require.NoError(t, env.replica.BecomeLeader(meta))
	require.NoError(t, env.replica.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		ReplicaID:   2,
		MatchOffset: 9,
		OffsetEpoch: meta.Epoch,
	}))
	view, err := env.replica.RetentionView()
	require.NoError(t, err)
	require.Equal(t, uint64(9), view.MinISRMatchOffset)

	refreshed := meta
	refreshed.LeaderEpoch = 2
	require.NoError(t, env.replica.ApplyMeta(refreshed))
	view, err = env.replica.RetentionView()
	require.NoError(t, err)
	require.Equal(t, uint64(4), view.MinISRMatchOffset)

	require.NoError(t, env.replica.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  meta.Key,
		Epoch:       meta.Epoch,
		ReplicaID:   2,
		MatchOffset: 9,
		OffsetEpoch: meta.Epoch,
	}))
	view, err = env.replica.RetentionView()
	require.NoError(t, err)
	require.Equal(t, uint64(9), view.MinISRMatchOffset)
}

func TestReplicaRetentionAdoptStaleFailureAfterNewerSuccessIsIgnored(t *testing.T) {
	env := newTestEnv(t)
	r := newReplicaFromEnv(t, env)

	oldResult := r.applyLoopEvent(machineApplyRetentionCommand{ThroughSeq: 5})
	require.Len(t, oldResult.Effects, 1)
	oldEffect, ok := oldResult.Effects[0].(retentionAdoptEffect)
	require.True(t, ok)

	newResult := r.applyLoopEvent(machineApplyRetentionCommand{ThroughSeq: 6})
	require.Len(t, newResult.Effects, 1)
	newEffect, ok := newResult.Effects[0].(retentionAdoptEffect)
	require.True(t, ok)
	require.ErrorIs(t, r.validateRetentionAdoptEffectFence(oldEffect), channel.ErrStaleMeta)
	require.NoError(t, r.validateRetentionAdoptEffectFence(newEffect))

	result := r.applyLoopEvent(machineRetentionAdoptedEvent{
		EffectID:       newEffect.EffectID,
		ChannelKey:     newEffect.ChannelKey,
		Epoch:          newEffect.Epoch,
		RoleGeneration: newEffect.RoleGeneration,
		ThroughSeq:     newEffect.ThroughSeq,
		StoredLEO:      newEffect.ThroughSeq,
	})
	require.NoError(t, result.Err)
	require.Equal(t, uint64(6), r.Status().LocalRetentionThroughSeq)

	result = r.applyLoopEvent(machineRetentionAdoptedEvent{
		EffectID:       oldEffect.EffectID,
		ChannelKey:     oldEffect.ChannelKey,
		Epoch:          oldEffect.Epoch,
		RoleGeneration: oldEffect.RoleGeneration,
		ThroughSeq:     oldEffect.ThroughSeq,
		Err:            errors.New("old adopt failed"),
	})
	require.NoError(t, result.Err)
	require.Equal(t, uint64(6), r.Status().LocalRetentionThroughSeq)
}

func TestReplicaRetentionTrimStaleFailureAfterNewerSuccessIsIgnored(t *testing.T) {
	env := newTestEnv(t)
	r := newReplicaFromEnv(t, env)
	r.mu.Lock()
	r.state.ChannelKey = "group-10"
	r.state.Epoch = 7
	r.state.LEO = 10
	r.state.HW = 10
	r.state.CheckpointHW = 10
	r.state.CommitReady = true
	r.state.LocalRetentionThroughSeq = 10
	r.publishStateLocked()
	r.mu.Unlock()

	oldResult := r.applyLoopEvent(machineApplyRetentionCommand{ThroughSeq: 5})
	require.Len(t, oldResult.Effects, 1)
	oldEffect, ok := oldResult.Effects[0].(retentionTrimEffect)
	require.True(t, ok)

	newResult := r.applyLoopEvent(machineApplyRetentionCommand{ThroughSeq: 6})
	require.Len(t, newResult.Effects, 1)
	newEffect, ok := newResult.Effects[0].(retentionTrimEffect)
	require.True(t, ok)
	require.ErrorIs(t, r.validateRetentionTrimEffectFence(oldEffect), channel.ErrStaleMeta)
	require.NoError(t, r.validateRetentionTrimEffectFence(newEffect))

	result := r.applyLoopEvent(machineRetentionTrimmedEvent{
		EffectID:           newEffect.EffectID,
		ChannelKey:         newEffect.ChannelKey,
		Epoch:              newEffect.Epoch,
		RoleGeneration:     newEffect.RoleGeneration,
		ThroughSeq:         newEffect.ThroughSeq,
		PhysicalThroughSeq: newEffect.ThroughSeq,
	})
	require.NoError(t, result.Err)
	require.Equal(t, uint64(6), r.Status().PhysicalRetentionThroughSeq)

	result = r.applyLoopEvent(machineRetentionTrimmedEvent{
		EffectID:       oldEffect.EffectID,
		ChannelKey:     oldEffect.ChannelKey,
		Epoch:          oldEffect.Epoch,
		RoleGeneration: oldEffect.RoleGeneration,
		ThroughSeq:     oldEffect.ThroughSeq,
		Err:            errors.New("old trim failed"),
	})
	require.NoError(t, result.Err)
	require.Equal(t, uint64(6), r.Status().PhysicalRetentionThroughSeq)
}

func TestReplicaDurableAdapterRejectsInvalidRetentionState(t *testing.T) {
	env := newTestEnv(t)
	env.log.retention = channel.RetentionState{
		LocalRetentionThroughSeq:    2,
		PhysicalRetentionThroughSeq: 3,
		RetainedMaxSeq:              3,
	}
	store := newDurableReplicaStore(env.log, env.checkpoints, env.applyFetch, env.history, env.snapshots)

	_, err := store.LoadRetentionState()

	require.ErrorIs(t, err, channel.ErrCorruptValue)
}

func newRetentionRecoveredEnv(t testing.TB, leo, hw, checkpointHW uint64) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	env.log.leo = leo
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: checkpointHW}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	_ = hw
	return env
}

func retentionFetchRecords(count uint64) []channel.Record {
	records := make([]channel.Record, 0, count)
	for seq := uint64(1); seq <= count; seq++ {
		records = append(records, channel.Record{Index: seq, SizeBytes: 1})
	}
	return records
}
