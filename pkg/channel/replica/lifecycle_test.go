package replica

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestCloseStopsAppendEffectWorker(t *testing.T) {
	r := newLeaderReplica(t)
	require.NoError(t, r.Close())

	select {
	case <-r.appendWorkerDone:
	case <-time.After(time.Second):
		t.Fatal("append effect worker did not stop")
	}

	require.NoError(t, r.Close())
}

func TestCloseReturnsWhileDurableAppendInFlight(t *testing.T) {
	env := newTestEnv(t)
	env.replica = newReplicaFromEnvWithGroupCommit(t, env, time.Millisecond, 1, 1024)
	meta := activeMetaWithMinISR(7, 1, 1)
	env.replica.mustApplyMeta(t, meta)
	require.NoError(t, env.replica.BecomeLeader(meta))
	env.log.syncStarted = make(chan struct{}, 1)
	env.log.syncContinue = make(chan struct{})
	env.log.syncCanceled = make(chan struct{}, 1)
	var unblockOnce sync.Once
	unblock := func() {
		unblockOnce.Do(func() {
			close(env.log.syncContinue)
		})
	}
	defer unblock()

	appendDone := make(chan error, 1)
	go func() {
		_, err := env.replica.Append(channel.WithCommitMode(context.Background(), channel.CommitModeLocal), []channel.Record{{Payload: []byte("x"), SizeBytes: 1}})
		appendDone <- err
	}()
	select {
	case <-env.log.syncStarted:
	case <-time.After(time.Second):
		t.Fatal("durable append did not start")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- env.replica.Close()
	}()
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		unblock()
		select {
		case <-closeDone:
		case <-time.After(time.Second):
		}
		t.Fatal("Close blocked on an in-flight durable append")
	}
	select {
	case <-env.log.syncCanceled:
	case <-time.After(time.Second):
		t.Fatal("in-flight durable append did not observe close cancellation")
	}
	select {
	case err := <-appendDone:
		require.ErrorIs(t, err, channel.ErrNotLeader)
	case <-time.After(time.Second):
		t.Fatal("append did not fail after close")
	}
	unblock()
}

func TestReplicaLoopStartsAndStops(t *testing.T) {
	r := newLeaderReplica(t)
	require.NotNil(t, r.loopDone)

	select {
	case <-r.loopDone:
		t.Fatal("replica loop stopped before Close")
	default:
	}

	closed := make(chan error, 1)
	go func() {
		closed <- r.Close()
	}()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close did not stop replica loop")
	}

	requireClosed(t, r.loopDone, "replica loop did not stop")
}

func TestReplicaLoopSubmitAfterCloseReturnsNotLeader(t *testing.T) {
	r := newLeaderReplica(t)
	require.NoError(t, r.Close())

	result := r.submitLoopCommand(context.Background(), machineCloseCommand{})

	require.ErrorIs(t, result.Err, channel.ErrNotLeader)
}

func TestReplicaLoopSubmitResultAfterCloseReturnsNotLeader(t *testing.T) {
	r := newLeaderReplica(t)
	require.NoError(t, r.Close())

	err := r.submitLoopResult(context.Background(), machineAdvanceHWEvent{})

	require.ErrorIs(t, err, channel.ErrNotLeader)
}

func TestReplicaLoopCommandsQueuedAfterCloseCannotMutate(t *testing.T) {
	r := newLeaderReplica(t)
	closeResult := r.applyCloseCommand()
	require.NoError(t, closeResult.Err)
	before := r.Status()

	tests := []struct {
		name  string
		event machineEvent
	}{
		{name: "apply meta", event: machineApplyMetaCommand{Meta: activeMetaWithMinISR(8, 1, 1)}},
		{name: "become follower", event: machineBecomeFollowerCommand{Meta: activeMetaWithMinISR(8, 2, 2)}},
		{name: "tombstone", event: machineTombstoneCommand{}},
		{name: "install snapshot", event: machineInstallSnapshotCommand{Snapshot: channel.Snapshot{ChannelKey: "group-10", Epoch: 8, EndOffset: before.HW}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.applyLoopEvent(tt.event)

			require.ErrorIs(t, result.Err, channel.ErrNotLeader)
			require.Equal(t, before, r.Status())
		})
	}
}

func TestReplicaLoopStatusDoesNotWaitForStateLock(t *testing.T) {
	r := newLeaderReplica(t)
	defer func() {
		require.NoError(t, r.Close())
	}()

	r.mu.Lock()
	defer r.mu.Unlock()

	done := make(chan channel.ReplicaState, 1)
	go func() {
		done <- r.Status()
	}()

	select {
	case st := <-done:
		require.Equal(t, r.state.ChannelKey, st.ChannelKey)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Status blocked on mutable state lock")
	}
}

func requireClosed[T any](t *testing.T, ch <-chan T, msg string) {
	t.Helper()
	select {
	case <-ch:
	default:
		t.Fatal(msg)
	}
}

func TestBecomeLeaderSucceedsButAppendStaysGatedUntilCommitReady(t *testing.T) {
	env := newRecoveredLeaderEnv(t)
	env.log.leo = 3
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(8, 1, 1)
	r.mustApplyMeta(t, meta)

	require.NoError(t, r.BecomeLeader(meta))
	st := r.Status()
	require.Equal(t, channel.ReplicaRoleLeader, st.Role)

	setReplicaStateOptionalBoolField(t, &r.state, "CommitReady", false)

	_, err := r.Append(context.Background(), []channel.Record{{Payload: []byte("gated"), SizeBytes: 1}})
	require.Error(t, err, "leader append should stay gated until commit-ready")
	require.Zero(t, env.log.appendCount, "gated leader must not append to the log")
}

func TestBecomeLeaderExpiredLeaseDoesNotPersistEpochPoint(t *testing.T) {
	env := newRecoveredLeaderEnv(t)
	env.log.leo = 5
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(8, 1, 1)
	meta.LeaseUntil = env.clock.Now().Add(-time.Millisecond)
	r.mustApplyMeta(t, activeMetaWithMinISR(7, 1, 1))

	err := r.BecomeLeader(meta)

	require.ErrorIs(t, err, channel.ErrLeaseExpired)
	require.Empty(t, env.history.appended, "expired leader lease must not persist a new epoch boundary")
	require.Equal(t, []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}, env.history.points)
}

func TestBecomeLeaderReconcilesLocalTailWithoutPeerProbesWhenMinISROne(t *testing.T) {
	env := newRecoveredLeaderEnv(t)
	env.log.leo = 5
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	r := newReplicaFromEnv(t, env)
	meta := channel.Meta{
		Key:        "group-10",
		Epoch:      8,
		Leader:     1,
		Replicas:   []channel.NodeID{1},
		ISR:        []channel.NodeID{1},
		MinISR:     1,
		LeaseUntil: time.Unix(1_700_000_300, 0).UTC(),
	}
	r.mustApplyMeta(t, meta)

	require.NoError(t, r.BecomeLeader(meta))
	require.Eventually(t, func() bool {
		st := r.Status()
		return st.CommitReady && st.HW == 5 && st.CheckpointHW == 5 && st.LEO == 5
	}, time.Second, 10*time.Millisecond, "single-node cluster should locally recover its durable tail above the checkpoint")
	require.Equal(t, uint64(5), env.checkpoints.lastStored().HW)
}

func TestBecomeLeaderReconcilesFetchedTailWhenFollowerHasRecordsAboveHW(t *testing.T) {
	env := newTestEnv(t)
	env.localNode = 3
	env.log.records = []channel.Record{{Payload: []byte("a"), SizeBytes: 1}}
	env.log.leo = 1
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	env.peerProofs = map[channel.NodeID]channel.ReplicaReconcileProof{
		1: {OffsetEpoch: 7, LogEndOffset: 1, CheckpointHW: 0},
		2: {OffsetEpoch: 7, LogEndOffset: 1, CheckpointHW: 0},
	}

	r := newReplicaFromEnv(t, env)
	followerMeta := activeMetaWithMinISR(7, 1, 2)
	r.mustApplyMeta(t, followerMeta)
	require.NoError(t, r.BecomeFollower(followerMeta))

	r.mu.Lock()
	r.state.LEO = 1
	r.state.HW = 0
	r.state.CheckpointHW = 0
	r.state.CommitReady = true
	r.publishStateLocked()
	r.mu.Unlock()

	leaderMeta := activeMetaWithMinISR(8, 3, 2)
	r.mustApplyMeta(t, leaderMeta)
	require.NoError(t, r.BecomeLeader(leaderMeta))
	require.Eventually(t, func() bool {
		st := r.Status()
		return st.CommitReady && st.HW == 1 && st.CheckpointHW == 1 && st.LEO == 1
	}, time.Second, 10*time.Millisecond, "leader reconcile should commit a quorum-visible fetched tail even if follower state previously looked commit-ready")
}

func TestBecomeLeaderReconcileNotifiesHWAdvance(t *testing.T) {
	env := newTestEnv(t)
	env.localNode = 3
	env.log.records = []channel.Record{{Payload: []byte("a"), SizeBytes: 1}}
	env.log.leo = 1
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	env.peerProofs = map[channel.NodeID]channel.ReplicaReconcileProof{
		2: {OffsetEpoch: 7, LogEndOffset: 1, CheckpointHW: 0},
	}

	r := newReplicaFromEnv(t, env)
	followerMeta := activeMetaWithMinISR(7, 1, 2)
	r.mustApplyMeta(t, followerMeta)
	require.NoError(t, r.BecomeFollower(followerMeta))

	r.mu.Lock()
	r.state.LEO = 1
	r.state.HW = 0
	r.state.CheckpointHW = 0
	r.state.CommitReady = true
	r.publishStateLocked()
	r.mu.Unlock()

	notified := make(chan struct{}, 1)
	r.SetLeaderHWAdvanceNotifier(func() {
		notified <- struct{}{}
	})

	leaderMeta := activeMetaWithMinISR(8, 3, 2)
	r.mustApplyMeta(t, leaderMeta)
	require.NoError(t, r.BecomeLeader(leaderMeta))
	require.Eventually(t, func() bool {
		return r.Status().HW == 1
	}, time.Second, 10*time.Millisecond)

	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("leader reconcile did not notify HW advance")
	}
}

func TestBecomeLeaderReconcileNotifiesHWAdvanceExactlyOnce(t *testing.T) {
	env := newTestEnv(t)
	env.localNode = 3
	env.log.records = []channel.Record{{Payload: []byte("a"), SizeBytes: 1}}
	env.log.leo = 1
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	env.peerProofs = map[channel.NodeID]channel.ReplicaReconcileProof{
		2: {OffsetEpoch: 7, LogEndOffset: 1, CheckpointHW: 0},
	}

	r := newReplicaFromEnv(t, env)
	followerMeta := activeMetaWithMinISR(7, 1, 2)
	r.mustApplyMeta(t, followerMeta)
	require.NoError(t, r.BecomeFollower(followerMeta))

	r.mu.Lock()
	r.state.LEO = 1
	r.state.HW = 0
	r.state.CheckpointHW = 0
	r.state.CommitReady = true
	r.publishStateLocked()
	r.mu.Unlock()

	notified := make(chan struct{}, 4)
	r.SetLeaderHWAdvanceNotifier(func() {
		notified <- struct{}{}
	})

	leaderMeta := activeMetaWithMinISR(8, 3, 2)
	r.mustApplyMeta(t, leaderMeta)
	require.NoError(t, r.BecomeLeader(leaderMeta))
	require.Eventually(t, func() bool {
		return r.Status().HW == 1
	}, time.Second, 10*time.Millisecond)

	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("leader reconcile did not notify HW advance")
	}
	select {
	case <-notified:
		t.Fatal("leader reconcile notified the same HW advance more than once")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBecomeLeaderCommitsFullyProvenTailWithoutWaitingForOfflinePeerProof(t *testing.T) {
	env := newTestEnv(t)
	env.localNode = 3
	env.log.records = []channel.Record{{Payload: []byte("a"), SizeBytes: 1}}
	env.log.leo = 1
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	env.peerProofs = map[channel.NodeID]channel.ReplicaReconcileProof{
		2: {OffsetEpoch: 7, LogEndOffset: 1, CheckpointHW: 0},
	}

	r := newReplicaFromEnv(t, env)
	followerMeta := activeMetaWithMinISR(7, 1, 2)
	r.mustApplyMeta(t, followerMeta)
	require.NoError(t, r.BecomeFollower(followerMeta))

	r.mu.Lock()
	r.state.LEO = 1
	r.state.HW = 0
	r.state.CheckpointHW = 0
	r.state.CommitReady = true
	r.publishStateLocked()
	r.mu.Unlock()

	leaderMeta := activeMetaWithMinISR(8, 3, 2)
	r.mustApplyMeta(t, leaderMeta)
	require.NoError(t, r.BecomeLeader(leaderMeta))
	require.Eventually(t, func() bool {
		st := r.Status()
		return st.CommitReady && st.HW == 1 && st.CheckpointHW == 1 && st.LEO == 1
	}, time.Second, 10*time.Millisecond, "leader should become ready once its local tail is fully proven by a quorum even if another ISR peer stays offline")
}

func TestBecomeLeaderTruncatesToHWWhenOfflineISRDoesNotProveTail(t *testing.T) {
	env := newTestEnv(t)
	env.localNode = 3
	env.log.records = []channel.Record{
		{Payload: []byte("a"), SizeBytes: 1},
		{Payload: []byte("b"), SizeBytes: 1},
		{Payload: []byte("c"), SizeBytes: 1},
		{Payload: []byte("d"), SizeBytes: 1},
		{Payload: []byte("e"), SizeBytes: 1},
	}
	env.log.leo = 5
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	env.peerProofs = map[channel.NodeID]channel.ReplicaReconcileProof{
		1: {OffsetEpoch: 7, LogEndOffset: 5, CheckpointHW: 3},
	}

	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(8, 3, 3)
	r.mustApplyMeta(t, meta)
	require.NoError(t, r.BecomeLeader(meta))

	require.Eventually(t, func() bool {
		st := r.Status()
		return st.CommitReady && st.LEO == 3 && st.HW == 3 && st.CheckpointHW == 3
	}, time.Second, 10*time.Millisecond, "offline ISR peer must count as HW at most, forcing the unsafe tail to be truncated")
	require.Equal(t, []uint64{3}, env.log.truncateCalls)
}

func TestBecomeLeaderReconcileCompleteAfterLeaseExpiryDoesNotCommitTail(t *testing.T) {
	env := newTestEnv(t)
	env.log.records = []channel.Record{
		{Payload: []byte("a"), SizeBytes: 1},
		{Payload: []byte("b"), SizeBytes: 1},
		{Payload: []byte("c"), SizeBytes: 1},
		{Payload: []byte("d"), SizeBytes: 1},
		{Payload: []byte("e"), SizeBytes: 1},
	}
	env.log.leo = 5
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(8, 1, 2)
	r.mustApplyMeta(t, meta)
	require.NoError(t, r.BecomeLeader(meta))
	env.clock.Advance(time.Hour)

	result := r.applyLoopEvent(machineCompleteReconcileCommand{Meta: meta})

	require.ErrorIs(t, result.Err, channel.ErrLeaseExpired)
	require.Empty(t, env.log.truncateCalls)
	st := r.Status()
	require.Equal(t, channel.ReplicaRoleFencedLeader, st.Role)
	require.False(t, st.CommitReady)
	require.Equal(t, uint64(5), st.LEO)
	require.Equal(t, uint64(3), st.HW)
}

func TestBecomeLeaderReconcileCompleteRequiresMetaFence(t *testing.T) {
	env := newTestEnv(t)
	env.log.records = []channel.Record{
		{Payload: []byte("a"), SizeBytes: 1},
		{Payload: []byte("b"), SizeBytes: 1},
		{Payload: []byte("c"), SizeBytes: 1},
	}
	env.log.leo = 3
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 1}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(8, 1, 1)
	meta.LeaderEpoch = 1
	r.mustApplyMeta(t, meta)
	require.NoError(t, r.BecomeLeader(meta))

	tests := []struct {
		name string
		meta channel.Meta
	}{
		{name: "missing channel key", meta: channel.Meta{Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch}},
		{name: "missing channel epoch", meta: channel.Meta{Key: meta.Key, LeaderEpoch: meta.LeaderEpoch}},
		{name: "missing leader epoch", meta: channel.Meta{Key: meta.Key, Epoch: meta.Epoch}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.applyLoopEvent(machineCompleteReconcileCommand{Meta: tt.meta})

			require.ErrorIs(t, result.Err, channel.ErrStaleMeta)
			st := r.Status()
			require.False(t, st.CommitReady)
			require.Equal(t, uint64(1), st.HW)
		})
	}
}

func TestBecomeLeaderReconcileDurableResultAfterMetaChangeIsDiscarded(t *testing.T) {
	env := newTestEnv(t)
	env.log.records = []channel.Record{
		{Payload: []byte("a"), SizeBytes: 1},
		{Payload: []byte("b"), SizeBytes: 1},
		{Payload: []byte("c"), SizeBytes: 1},
		{Payload: []byte("d"), SizeBytes: 1},
		{Payload: []byte("e"), SizeBytes: 1},
	}
	env.log.leo = 5
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(8, 1, 2)
	meta.LeaderEpoch = 1
	r.mustApplyMeta(t, meta)
	require.NoError(t, r.BecomeLeader(meta))

	prepared := r.applyLoopEvent(machineCompleteReconcileCommand{Meta: meta})
	require.NoError(t, prepared.Err)
	require.Len(t, prepared.Effects, 1)
	effect := prepared.Effects[0].(leaderReconcileDurableEffect)

	next := meta
	next.LeaderEpoch = 2
	require.NoError(t, r.ApplyMeta(next))
	result := r.applyLoopEvent(machineLeaderReconcileResultCommand{
		EffectID:       effect.EffectID,
		ChannelKey:     effect.ChannelKey,
		Epoch:          effect.Epoch,
		LeaderEpoch:    effect.LeaderEpoch,
		RoleGeneration: effect.RoleGeneration,
		StoredLEO:      effect.NewLEO,
		HW:             effect.HW,
		Checkpoint:     effect.Checkpoint,
	})

	require.NoError(t, result.Err)
	st := r.Status()
	require.False(t, st.CommitReady)
	require.Equal(t, uint64(5), st.LEO)
	require.Equal(t, uint64(3), st.HW)
	require.Equal(t, uint64(3), st.CheckpointHW)
}

func TestBecomeLeaderReconcileDurableTruncateResultAfterLeaseExpiryIsDiscarded(t *testing.T) {
	env := newTestEnv(t)
	env.log.records = []channel.Record{
		{Payload: []byte("a"), SizeBytes: 1},
		{Payload: []byte("b"), SizeBytes: 1},
		{Payload: []byte("c"), SizeBytes: 1},
		{Payload: []byte("d"), SizeBytes: 1},
		{Payload: []byte("e"), SizeBytes: 1},
	}
	env.log.leo = 5
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 3}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(8, 1, 2)
	meta.LeaderEpoch = 1
	meta.LeaseUntil = env.clock.Now().Add(time.Second)
	r.mustApplyMeta(t, meta)
	require.NoError(t, r.BecomeLeader(meta))

	prepared := r.applyLoopEvent(machineCompleteReconcileCommand{Meta: meta})
	require.NoError(t, prepared.Err)
	require.Len(t, prepared.Effects, 1)
	effect := prepared.Effects[0].(leaderReconcileDurableEffect)
	env.clock.Advance(2 * time.Second)
	require.ErrorIs(t, r.applyLoopEvent(machineCompleteReconcileCommand{Meta: meta}).Err, channel.ErrLeaseExpired)

	result := r.applyLoopEvent(machineLeaderReconcileResultCommand{
		EffectID:       effect.EffectID,
		ChannelKey:     effect.ChannelKey,
		Epoch:          effect.Epoch,
		LeaderEpoch:    effect.LeaderEpoch,
		RoleGeneration: effect.RoleGeneration,
		StoredLEO:      effect.NewLEO,
		HW:             effect.HW,
		Checkpoint:     effect.Checkpoint,
		Truncated:      true,
		TruncateTo:     cloneUint64Pointer(effect.TruncateTo),
	})

	require.NoError(t, result.Err)
	st := r.Status()
	require.Equal(t, channel.ReplicaRoleFencedLeader, st.Role)
	require.False(t, st.CommitReady)
	require.Equal(t, uint64(5), st.LEO)
	require.Equal(t, uint64(3), st.HW)
}
