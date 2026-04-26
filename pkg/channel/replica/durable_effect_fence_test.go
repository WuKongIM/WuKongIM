package replica

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestAppendEffectBlockedOnDurableLaneRevalidatesFenceBeforeWrite(t *testing.T) {
	r := newLeaderReplica(t)
	spy := &spyDurableStore{appendBase: 0, appendLEO: 1}
	r.durable = spy
	r.mu.Lock()
	r.nextEffectID++
	effect := appendLeaderBatchEffect{
		EffectID:       r.nextEffectID,
		ChannelKey:     r.state.ChannelKey,
		Epoch:          r.state.Epoch,
		LeaderEpoch:    r.meta.LeaderEpoch,
		RoleGeneration: r.roleGeneration,
		LeaseUntil:     r.meta.LeaseUntil,
		Records:        []channel.Record{{Payload: []byte("x"), SizeBytes: 1}},
	}
	r.appendInFlightEffectID = effect.EffectID
	r.mu.Unlock()

	r.durableMu.Lock()
	done := make(chan struct{})
	go func() {
		r.runAppendEffect(context.Background(), effect)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)

	require.NoError(t, r.BecomeFollower(activeMetaWithMinISR(8, 2, 1)))
	r.durableMu.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("append effect did not return after durable lane release")
	}
	require.Equal(t, 0, spy.appendCalls)
}

func TestBeginLeaderEpochEffectBlockedOnDurableLaneRevalidatesFenceBeforeWrite(t *testing.T) {
	env := newTestEnv(t)
	env.checkpoints.loadErr = nil
	env.checkpoints.checkpoint = channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 0}
	env.history.loadErr = nil
	env.history.points = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}
	r := newReplicaFromEnv(t, env)
	r.mustApplyMeta(t, activeMetaWithMinISR(7, 2, 1))
	spy := &spyDurableStore{}
	r.durable = spy
	meta := activeMetaWithMinISR(8, 1, 1)

	r.durableMu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- r.BecomeLeader(meta)
	}()
	require.Eventually(t, func() bool {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.pendingLeaderEpochEffectID != 0
	}, time.Second, time.Millisecond)

	require.NoError(t, r.ApplyMeta(activeMetaWithMinISR(9, 1, 1)))
	r.durableMu.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("become leader did not return after durable lane release")
	}
	require.Equal(t, 0, spy.beginEpochCalls)
}

func TestFollowerApplyEffectBlockedOnDurableLaneRevalidatesFenceBeforeWrite(t *testing.T) {
	env := newFollowerEnv(t)
	spy := &spyDurableStore{applyLEO: 1}
	env.replica.durable = spy

	env.replica.durableMu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- env.replica.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{
			ChannelKey: "group-10",
			Epoch:      7,
			Leader:     1,
			Records:    []channel.Record{{Index: 1, Payload: []byte("a"), SizeBytes: 1}},
			LeaderHW:   1,
		})
	}()
	require.Eventually(t, func() bool {
		env.replica.mu.RLock()
		defer env.replica.mu.RUnlock()
		return env.replica.pendingFollowerApplyEffectID != 0
	}, time.Second, time.Millisecond)

	require.NoError(t, env.replica.ApplyMeta(activeMetaWithMinISR(8, 1, 1)))
	env.replica.durableMu.Unlock()

	select {
	case err := <-done:
		require.ErrorIs(t, err, channel.ErrStaleMeta)
	case <-time.After(time.Second):
		t.Fatal("apply fetch did not return after durable lane release")
	}
	require.Equal(t, 0, spy.applyCalls)
}

func TestSnapshotEffectBlockedOnDurableLaneRevalidatesFenceBeforeWrite(t *testing.T) {
	env := newFollowerEnv(t)
	env.log.leo = 8
	spy := &spyDurableStore{recoverView: durableView{
		Checkpoint:   channel.Checkpoint{Epoch: 7, LogStartOffset: 8, HW: 8},
		EpochHistory: []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
		LEO:          8,
	}}
	env.replica.durable = spy

	env.replica.durableMu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- env.replica.InstallSnapshot(context.Background(), channel.Snapshot{
			ChannelKey: "group-10",
			Epoch:      7,
			EndOffset:  8,
			Payload:    []byte("snap"),
		})
	}()
	require.Eventually(t, func() bool {
		env.replica.mu.RLock()
		defer env.replica.mu.RUnlock()
		return env.replica.pendingSnapshotEffectID != 0
	}, time.Second, time.Millisecond)

	require.NoError(t, env.replica.ApplyMeta(activeMetaWithMinISR(8, 1, 1)))
	env.replica.durableMu.Unlock()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("install snapshot did not return after durable lane release")
	}
	require.Equal(t, 0, spy.installCalls)
}

func TestLeaderReconcileEffectBlockedOnDurableLaneRevalidatesFenceBeforeWrite(t *testing.T) {
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
	spy := &spyDurableStore{}
	r.durable = spy

	r.durableMu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- r.executeLeaderReconcileDurableEffect(context.Background(), effect)
	}()
	time.Sleep(20 * time.Millisecond)

	next := meta
	next.LeaderEpoch = 2
	require.NoError(t, r.ApplyMeta(next))
	r.durableMu.Unlock()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("leader reconcile did not return after durable lane release")
	}
	require.Equal(t, 0, spy.truncateCalls)
	require.Equal(t, 0, spy.checkpointCalls)
}

func TestApplyMetaSameLeaderRunsLocalCheckpointReconcile(t *testing.T) {
	r := newLeaderReplica(t)
	r.mu.Lock()
	r.state.LEO = 5
	r.state.HW = 5
	r.state.CheckpointHW = 3
	r.state.CommitReady = true
	r.setReplicaProgressLocked(r.localNode, 5)
	meta := r.meta
	r.publishStateLocked()
	r.mu.Unlock()

	next := meta
	next.LeaderEpoch++
	require.NoError(t, r.ApplyMeta(next))

	st := r.Status()
	require.True(t, st.CommitReady)
	require.Equal(t, uint64(5), st.HW)
	require.Equal(t, uint64(5), st.CheckpointHW)
}
