package replica

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestStatusReturnsLatestReplicaSnapshot(t *testing.T) {
	cluster := newThreeReplicaCluster(t)
	done := make(chan channel.CommitResult, 1)
	go func() {
		res, err := cluster.leader.Append(context.Background(), []channel.Record{{Payload: []byte("a"), SizeBytes: 1}})
		if err == nil {
			done <- res
		}
	}()
	waitForLogAppend(t, cluster.leader.log.(*fakeLogStore), 1)

	deadline := time.After(time.Second)
	for {
		state := cluster.leader.Status()
		if state.LEO == 1 {
			require.Equal(t, uint64(0), state.HW)
			break
		}
		select {
		case <-deadline:
			t.Fatal("Status() did not expose latest LEO snapshot while append was waiting for quorum")
		default:
		}
	}
	select {
	case <-done:
		t.Fatal("append returned before MinISR quorum")
	default:
	}

	cluster.replicateOnce(t, cluster.follower2)
	cluster.replicateOnce(t, cluster.follower3)
	<-done
}

func TestStatusIsUpdatedAfterFollowerAckAdvancesHW(t *testing.T) {
	cluster := newThreeReplicaCluster(t)
	done := make(chan channel.CommitResult, 1)
	go func() {
		res, err := cluster.leader.Append(context.Background(), []channel.Record{{Payload: []byte("m"), SizeBytes: 1}})
		if err == nil {
			done <- res
		}
	}()
	waitForLogAppend(t, cluster.leader.log.(*fakeLogStore), 1)

	cluster.replicateOnce(t, cluster.follower2)
	cluster.replicateOnce(t, cluster.follower3)
	<-done

	state := cluster.leader.Status()
	require.Equal(t, uint64(1), state.HW)
	require.Equal(t, uint64(1), state.LEO)
}

func TestProgressCursorUpdateIgnoresRegressingMatchOffset(t *testing.T) {
	cluster := newThreeReplicaCluster(t)
	cluster.leader.mu.Lock()
	cluster.leader.state.LEO = 3
	cluster.leader.state.OffsetEpoch = 7
	cluster.leader.progress[cluster.leader.localNode] = 3
	cluster.leader.mu.Unlock()

	require.NoError(t, cluster.leader.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  cluster.leader.state.ChannelKey,
		Epoch:       cluster.leader.state.Epoch,
		ReplicaID:   cluster.follower2.localNode,
		MatchOffset: 3,
		OffsetEpoch: cluster.leader.state.OffsetEpoch,
	}))
	require.NoError(t, cluster.leader.ApplyFollowerCursor(context.Background(), channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  cluster.leader.state.ChannelKey,
		Epoch:       cluster.leader.state.Epoch,
		ReplicaID:   cluster.follower2.localNode,
		MatchOffset: 2,
		OffsetEpoch: cluster.leader.state.OffsetEpoch,
	}))

	cluster.leader.mu.RLock()
	defer cluster.leader.mu.RUnlock()
	require.Equal(t, uint64(3), cluster.leader.progress[cluster.follower2.localNode])
}

func TestProgressLoopQueuedCursorAfterMetaChangeDoesNotMutate(t *testing.T) {
	r := newLeaderReplica(t)
	st := r.Status()
	cursor := machineCursorCommand{
		ChannelKey:  st.ChannelKey,
		Epoch:       st.Epoch,
		ReplicaID:   2,
		MatchOffset: 3,
		OffsetEpoch: st.OffsetEpoch,
	}
	require.NoError(t, r.ApplyMeta(activeMetaWithMinISR(st.Epoch+1, 1, 1)))

	result := r.applyLoopEvent(cursor)

	require.ErrorIs(t, result.Err, channel.ErrStaleMeta)
	r.mu.RLock()
	require.Zero(t, r.progress[2])
	r.mu.RUnlock()
}

func TestProgressLoopFetchRejectsStaleEpochWithoutMutating(t *testing.T) {
	r := newLeaderReplica(t)
	st := r.Status()
	req := channel.ReplicaFetchRequest{
		ChannelKey:  st.ChannelKey,
		Epoch:       st.Epoch,
		ReplicaID:   2,
		FetchOffset: 0,
		OffsetEpoch: st.OffsetEpoch,
		MaxBytes:    1024,
	}
	require.NoError(t, r.ApplyMeta(activeMetaWithMinISR(st.Epoch+1, 1, 1)))

	result := r.applyLoopEvent(machineFetchProgressCommand{Request: req})

	require.ErrorIs(t, result.Err, channel.ErrStaleMeta)
	r.mu.RLock()
	require.Zero(t, r.progress[2])
	r.mu.RUnlock()
}

func TestProgressLoopFetchDoesNotRegressProgress(t *testing.T) {
	r := newLeaderReplica(t)
	r.mu.Lock()
	r.state.LEO = 5
	r.state.OffsetEpoch = r.state.Epoch
	r.progress[r.localNode] = 5
	r.progress[2] = 4
	r.publishStateLocked()
	r.mu.Unlock()
	st := r.Status()

	result := r.applyLoopEvent(machineFetchProgressCommand{Request: channel.ReplicaFetchRequest{
		ChannelKey:  st.ChannelKey,
		Epoch:       st.Epoch,
		ReplicaID:   2,
		FetchOffset: 2,
		OffsetEpoch: st.OffsetEpoch,
		MaxBytes:    1024,
	}})

	require.NoError(t, result.Err)
	r.mu.RLock()
	require.Equal(t, uint64(4), r.progress[2])
	r.mu.RUnlock()
}

func TestProgressLoopAppendCommitPublishesLocalProgress(t *testing.T) {
	r := newLeaderReplica(t)
	st := r.Status()
	waiter := &appendWaiter{ch: make(chan appendCompletion, 1)}
	req := &appendRequest{
		requestID:  1,
		ctx:        channel.WithCommitMode(context.Background(), channel.CommitModeLocal),
		batch:      []channel.Record{{Payload: []byte("a"), SizeBytes: 1}, {Payload: []byte("b"), SizeBytes: 1}},
		commitMode: channel.CommitModeLocal,
		waiter:     waiter,
		stage:      appendRequestDurable,
	}
	waiter.request = req
	r.mu.Lock()
	r.appendRequests = map[uint64]*appendRequest{req.requestID: req}
	r.appendInFlightIDs = []uint64{req.requestID}
	r.appendInFlightEffectID = 10
	r.mu.Unlock()

	result := r.applyLoopEvent(machineLeaderAppendCommittedEvent{
		EffectID:       10,
		ChannelKey:     st.ChannelKey,
		Epoch:          st.Epoch,
		LeaderEpoch:    r.meta.LeaderEpoch,
		RoleGeneration: r.roleGeneration,
		RequestIDs:     []uint64{req.requestID},
		BaseOffset:     0,
		DoneAt:         time.Now(),
	})

	require.NoError(t, result.Err)
	st = r.Status()
	require.Equal(t, uint64(2), st.LEO)
	r.mu.RLock()
	require.Equal(t, uint64(2), r.progress[r.localNode])
	r.mu.RUnlock()
	select {
	case completion := <-waiter.ch:
		require.NoError(t, completion.err)
		require.Equal(t, uint64(0), completion.result.BaseOffset)
	default:
		t.Fatal("local append waiter was not completed")
	}
}

func TestProgressLoopStaleAppendCommitAfterCloseDoesNotPublishProgress(t *testing.T) {
	r := newLeaderReplica(t)
	st := r.Status()
	waiter := &appendWaiter{ch: make(chan appendCompletion, 1)}
	req := &appendRequest{
		requestID:  1,
		ctx:        channel.WithCommitMode(context.Background(), channel.CommitModeLocal),
		batch:      []channel.Record{{Payload: []byte("a"), SizeBytes: 1}},
		commitMode: channel.CommitModeLocal,
		waiter:     waiter,
		stage:      appendRequestDurable,
	}
	waiter.request = req
	r.mu.Lock()
	r.appendRequests = map[uint64]*appendRequest{req.requestID: req}
	r.appendInFlightIDs = []uint64{req.requestID}
	r.appendInFlightEffectID = 10
	r.mu.Unlock()
	require.NoError(t, r.applyCloseCommand().Err)

	result := r.applyLoopEvent(machineLeaderAppendCommittedEvent{
		EffectID:       10,
		ChannelKey:     st.ChannelKey,
		Epoch:          st.Epoch,
		LeaderEpoch:    r.meta.LeaderEpoch,
		RoleGeneration: r.roleGeneration - 1,
		RequestIDs:     []uint64{req.requestID},
		BaseOffset:     0,
		DoneAt:         time.Now(),
	})

	require.NoError(t, result.Err)
	require.Equal(t, uint64(0), r.Status().LEO)
	r.mu.RLock()
	require.Zero(t, r.progress[r.localNode])
	r.mu.RUnlock()
	select {
	case completion := <-waiter.ch:
		require.ErrorIs(t, completion.err, channel.ErrNotLeader)
	default:
		t.Fatal("stale append waiter was not failed")
	}
}

func TestProgressLoopReconcileProofUpdatesProgress(t *testing.T) {
	r := newLeaderReplica(t)
	r.mu.Lock()
	r.state.LEO = 3
	r.state.HW = 0
	r.state.CheckpointHW = 0
	r.state.OffsetEpoch = r.state.Epoch
	r.progress[r.localNode] = 3
	r.progress[2] = 0
	r.reconcilePending = map[channel.NodeID]struct{}{2: {}}
	r.publishStateLocked()
	r.mu.Unlock()
	st := r.Status()

	result := r.applyLoopEvent(machineReconcileProofCommand{Proof: channel.ReplicaReconcileProof{
		ChannelKey:   st.ChannelKey,
		Epoch:        st.Epoch,
		LeaderEpoch:  r.meta.LeaderEpoch,
		ReplicaID:    2,
		OffsetEpoch:  st.OffsetEpoch,
		LogEndOffset: 3,
		CheckpointHW: 0,
	}})

	require.NoError(t, result.Err)
	r.mu.RLock()
	require.Equal(t, uint64(3), r.progress[2])
	r.mu.RUnlock()
}

func TestProgressLoopReconcileProofRequiresLeaderEpoch(t *testing.T) {
	env := newTestEnv(t)
	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(7, 1, 2)
	meta.LeaderEpoch = 1
	r.mustApplyMeta(t, meta)
	require.NoError(t, r.BecomeLeader(meta))
	r.mu.Lock()
	r.state.LEO = 3
	r.state.HW = 0
	r.state.CheckpointHW = 0
	r.state.OffsetEpoch = r.state.Epoch
	r.progress[r.localNode] = 3
	r.progress[2] = 0
	r.reconcilePending = map[channel.NodeID]struct{}{2: {}}
	r.publishStateLocked()
	r.mu.Unlock()
	st := r.Status()

	result := r.applyLoopEvent(machineReconcileProofCommand{Proof: channel.ReplicaReconcileProof{
		ChannelKey:   st.ChannelKey,
		Epoch:        st.Epoch,
		ReplicaID:    2,
		OffsetEpoch:  st.OffsetEpoch,
		LogEndOffset: 3,
		CheckpointHW: 0,
	}})

	require.ErrorIs(t, result.Err, channel.ErrStaleMeta)
	r.mu.RLock()
	require.Zero(t, r.progress[2])
	r.mu.RUnlock()
}

func TestProgressLoopReconcileProofRequiresChannelFence(t *testing.T) {
	env := newTestEnv(t)
	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(7, 1, 2)
	meta.LeaderEpoch = 1
	r.mustApplyMeta(t, meta)
	require.NoError(t, r.BecomeLeader(meta))

	r.mu.Lock()
	r.state.LEO = 3
	r.state.HW = 0
	r.state.CheckpointHW = 0
	r.state.OffsetEpoch = r.state.Epoch
	r.progress[r.localNode] = 3
	r.progress[2] = 0
	r.reconcilePending = map[channel.NodeID]struct{}{2: {}}
	r.publishStateLocked()
	r.mu.Unlock()

	tests := []struct {
		name  string
		proof channel.ReplicaReconcileProof
	}{
		{
			name: "missing channel key",
			proof: channel.ReplicaReconcileProof{
				Epoch:        meta.Epoch,
				LeaderEpoch:  meta.LeaderEpoch,
				ReplicaID:    2,
				OffsetEpoch:  meta.Epoch,
				LogEndOffset: 3,
			},
		},
		{
			name: "missing channel epoch",
			proof: channel.ReplicaReconcileProof{
				ChannelKey:   meta.Key,
				LeaderEpoch:  meta.LeaderEpoch,
				ReplicaID:    2,
				OffsetEpoch:  meta.Epoch,
				LogEndOffset: 3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.applyLoopEvent(machineReconcileProofCommand{Proof: tt.proof})

			require.ErrorIs(t, result.Err, channel.ErrStaleMeta)
			r.mu.RLock()
			require.Zero(t, r.progress[2])
			r.mu.RUnlock()
		})
	}
}

func TestProgressLoopReconcileProofDoesNotRegressProgress(t *testing.T) {
	r := newLeaderReplica(t)
	r.mu.Lock()
	r.state.LEO = 5
	r.state.OffsetEpoch = r.state.Epoch
	r.progress[r.localNode] = 5
	r.progress[2] = 4
	r.publishStateLocked()
	r.mu.Unlock()
	st := r.Status()

	result := r.applyLoopEvent(machineReconcileProofCommand{Proof: channel.ReplicaReconcileProof{
		ChannelKey:   st.ChannelKey,
		Epoch:        st.Epoch,
		LeaderEpoch:  r.meta.LeaderEpoch,
		ReplicaID:    2,
		OffsetEpoch:  st.OffsetEpoch,
		LogEndOffset: 2,
		CheckpointHW: 0,
	}})

	require.NoError(t, result.Err)
	r.mu.RLock()
	require.Equal(t, uint64(4), r.progress[2])
	r.mu.RUnlock()
}

func TestProgressLoopStaleReconcileProofAfterMetaChangeDoesNotMutate(t *testing.T) {
	r := newLeaderReplica(t)
	r.mu.Lock()
	r.state.LEO = 3
	r.progress[r.localNode] = 3
	r.reconcilePending = map[channel.NodeID]struct{}{2: {}}
	r.publishStateLocked()
	r.mu.Unlock()
	st := r.Status()
	require.NoError(t, r.ApplyMeta(activeMetaWithMinISR(st.Epoch+1, 1, 1)))

	result := r.applyLoopEvent(machineReconcileProofCommand{Proof: channel.ReplicaReconcileProof{
		ChannelKey:   st.ChannelKey,
		Epoch:        st.Epoch,
		LeaderEpoch:  r.meta.LeaderEpoch,
		ReplicaID:    2,
		OffsetEpoch:  st.OffsetEpoch,
		LogEndOffset: 3,
		CheckpointHW: 0,
	}})

	require.ErrorIs(t, result.Err, channel.ErrStaleMeta)
	r.mu.RLock()
	require.Zero(t, r.progress[2])
	r.mu.RUnlock()
}

func TestProgressLoopStaleReconcileProofAfterLeaderEpochChangeDoesNotMutate(t *testing.T) {
	env := newTestEnv(t)
	r := newReplicaFromEnv(t, env)
	meta := activeMetaWithMinISR(7, 1, 2)
	meta.LeaderEpoch = 1
	r.mustApplyMeta(t, meta)
	require.NoError(t, r.BecomeLeader(meta))

	r.mu.Lock()
	r.state.LEO = 3
	r.state.HW = 0
	r.state.CheckpointHW = 0
	r.state.OffsetEpoch = r.state.Epoch
	r.progress[r.localNode] = 3
	r.progress[2] = 0
	r.reconcilePending = map[channel.NodeID]struct{}{2: {}}
	r.publishStateLocked()
	r.mu.Unlock()

	next := meta
	next.LeaderEpoch = 2
	require.NoError(t, r.ApplyMeta(next))

	result := r.applyLoopEvent(machineReconcileProofCommand{Proof: channel.ReplicaReconcileProof{
		ChannelKey:   meta.Key,
		Epoch:        meta.Epoch,
		LeaderEpoch:  meta.LeaderEpoch,
		ReplicaID:    2,
		OffsetEpoch:  meta.Epoch,
		LogEndOffset: 3,
		CheckpointHW: 0,
	}})

	require.ErrorIs(t, result.Err, channel.ErrStaleMeta)
	r.mu.RLock()
	require.Zero(t, r.progress[2])
	r.mu.RUnlock()
}

func TestProgressLoopStaleAdvanceAfterCloseDoesNotMutate(t *testing.T) {
	r := newLeaderReplica(t)
	r.mu.Lock()
	r.state.LEO = 2
	r.state.HW = 0
	r.state.CheckpointHW = 0
	r.progress[r.localNode] = 2
	r.progress[2] = 2
	r.publishStateLocked()
	r.mu.Unlock()
	require.NoError(t, r.applyCloseCommand().Err)

	result := r.applyLoopEvent(machineAdvanceHWEvent{})

	require.NoError(t, result.Err)
	require.Equal(t, uint64(0), r.Status().HW)
}

func TestStatusReportsCommitAndCheckpointWatermarksSeparately(t *testing.T) {
	t.Run("not ready", func(t *testing.T) {
		env := newTestEnv(t)
		r := newReplicaFromEnv(t, env)

		r.mu.Lock()
		r.state.HW = 5
		r.state.LEO = 5
		setReplicaStateOptionalUint64Field(t, &r.state, "CheckpointHW", 3)
		setReplicaStateOptionalBoolField(t, &r.state, "CommitReady", false)
		r.publishStateLocked()
		r.mu.Unlock()

		st := r.Status()
		require.Equal(t, uint64(5), st.HW)
		require.Equal(t, uint64(5), st.LEO)
		requireReplicaStateUint64Field(t, st, "CheckpointHW", 3)
		requireReplicaStateBoolField(t, st, "CommitReady", false)
	})

	t.Run("ready", func(t *testing.T) {
		env := newTestEnv(t)
		r := newReplicaFromEnv(t, env)

		r.mu.Lock()
		r.state.HW = 5
		r.state.LEO = 5
		setReplicaStateOptionalUint64Field(t, &r.state, "CheckpointHW", 5)
		setReplicaStateOptionalBoolField(t, &r.state, "CommitReady", true)
		r.publishStateLocked()
		r.mu.Unlock()

		st := r.Status()
		require.Equal(t, uint64(5), st.HW)
		require.Equal(t, uint64(5), st.LEO)
		requireReplicaStateUint64Field(t, st, "CheckpointHW", 5)
		requireReplicaStateBoolField(t, st, "CommitReady", true)
	})
}
