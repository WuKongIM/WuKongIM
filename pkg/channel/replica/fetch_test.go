package replica

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestFetchRejectsInvalidBudget(t *testing.T) {
	r := newLeaderReplica(t)
	_, err := r.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		FetchOffset: 0,
		OffsetEpoch: 3,
		MaxBytes:    0,
	})
	require.ErrorIs(t, err, channel.ErrInvalidFetchBudget)
}

func TestFetchRejectsMismatchedChannelKey(t *testing.T) {
	r := newLeaderReplica(t)
	_, err := r.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey:  "group-other",
		Epoch:       7,
		ReplicaID:   2,
		FetchOffset: 0,
		OffsetEpoch: 3,
		MaxBytes:    1024,
	})
	require.ErrorIs(t, err, channel.ErrStaleMeta)
}

func TestFetchReturnsTruncateToWhenOffsetEpochDiverges(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	result, err := env.replica.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		FetchOffset: 5,
		OffsetEpoch: 4,
		MaxBytes:    1024,
	})
	require.NoError(t, err)
	require.NotNil(t, result.TruncateTo)
	require.Equal(t, uint64(4), *result.TruncateTo)
}

func TestFetchRejectsFutureOffsetEpoch(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	r := env.replica

	r.mu.Lock()
	r.progress[2] = 4
	r.mu.Unlock()

	_, err := r.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		FetchOffset: 5,
		OffsetEpoch: 99,
		MaxBytes:    1024,
	})
	require.ErrorIs(t, err, channel.ErrStaleMeta)

	r.mu.RLock()
	require.Equal(t, uint64(4), r.progress[2])
	r.mu.RUnlock()
}

func TestFetchZeroEpochWithHistoryCapsProgressToCurrentHW(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	r := env.replica

	r.mu.Lock()
	r.state.HW = 4
	r.state.CheckpointHW = 4
	r.progress[2] = 4
	r.publishStateLocked()
	r.mu.Unlock()

	result, err := r.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		FetchOffset: 5,
		OffsetEpoch: 0,
		MaxBytes:    1024,
	})
	require.NoError(t, err)
	require.NotNil(t, result.TruncateTo)
	require.Equal(t, uint64(4), *result.TruncateTo)

	r.mu.RLock()
	require.Equal(t, uint64(4), r.progress[2])
	r.mu.RUnlock()
}

func TestFetchReturnsSnapshotRequiredWhenLineageCapFallsBelowLogStart(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	r := env.replica

	r.mu.Lock()
	r.state.LogStartOffset = 5
	r.state.HW = 5
	r.state.CheckpointHW = 5
	r.state.LEO = 8
	r.epochHistory = []channel.EpochPoint{
		{Epoch: 3, StartOffset: 0},
		{Epoch: 4, StartOffset: 4},
		{Epoch: 7, StartOffset: 6},
	}
	r.progress[2] = 5
	r.publishStateLocked()
	r.mu.Unlock()

	_, err := r.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		FetchOffset: 5,
		OffsetEpoch: 3,
		MaxBytes:    1024,
	})
	require.ErrorIs(t, err, channel.ErrSnapshotRequired)

	r.mu.RLock()
	require.Equal(t, uint64(5), r.progress[2])
	r.mu.RUnlock()
}

func TestFetchReturnsSnapshotRequiredWhenFollowerFallsBehindLogStart(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	env.replica.state.LogStartOffset = 4
	env.replica.publishStateLocked()
	_, err := env.replica.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		FetchOffset: 3,
		OffsetEpoch: 3,
		MaxBytes:    1024,
	})
	require.ErrorIs(t, err, channel.ErrSnapshotRequired)
}

func TestFetchReturnsProvisionalCommittedHWWhenCommitNotReady(t *testing.T) {
	cases := []struct {
		name         string
		commitReady  bool
		checkpointHW uint64
		commitHW     uint64
		wantLeaderHW uint64
	}{
		{
			name:         "not ready",
			commitReady:  false,
			checkpointHW: 3,
			commitHW:     5,
			wantLeaderHW: 3,
		},
		{
			name:         "ready",
			commitReady:  true,
			checkpointHW: 5,
			commitHW:     5,
			wantLeaderHW: 5,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestReplica(t)

			r.mu.Lock()
			r.state = channel.ReplicaState{
				ChannelKey: "group-10",
				Role:       channel.ReplicaRoleLeader,
				Epoch:      7,
				HW:         tc.commitHW,
				LEO:        tc.commitHW,
			}
			setReplicaStateOptionalUint64Field(t, &r.state, "CheckpointHW", tc.checkpointHW)
			setReplicaStateOptionalBoolField(t, &r.state, "CommitReady", tc.commitReady)
			r.publishStateLocked()
			r.mu.Unlock()

			result, err := r.Fetch(context.Background(), channel.ReplicaFetchRequest{
				ChannelKey:  "group-10",
				Epoch:       7,
				ReplicaID:   2,
				FetchOffset: 0,
				OffsetEpoch: 3,
				MaxBytes:    1024,
			})
			require.NoError(t, err)
			require.Equal(t, tc.wantLeaderHW, result.HW)
		})
	}
}

func TestFetchDoesNotExposeRecordsBeyondPublishedLeaderLEO(t *testing.T) {
	leaderEnv := newFetchEnvWithHistory(t)

	// Simulate synced records already visible in the store before the leader has
	// published a matching runtime LEO snapshot.
	leaderEnv.log.records = append(
		leaderEnv.log.records,
		channel.Record{Payload: []byte("r6"), SizeBytes: 1},
		channel.Record{Payload: []byte("r7"), SizeBytes: 1},
	)
	leaderEnv.log.leo = 8
	leaderEnv.replica.state.LEO = 6
	leaderEnv.replica.publishStateLocked()

	result, err := leaderEnv.replica.Fetch(context.Background(), channel.ReplicaFetchRequest{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		FetchOffset: 5,
		OffsetEpoch: leaderEnv.replica.state.OffsetEpoch,
		MaxBytes:    1024,
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)

	matchOffset := uint64(5 + len(result.Records))
	require.Equal(t, uint64(6), matchOffset)
	require.NoError(t, leaderEnv.replica.ApplyProgressAck(context.Background(), channel.ReplicaProgressAckRequest{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		MatchOffset: matchOffset,
	}))
}

func TestFetchReadLogResultAfterLeadershipLossIsFenced(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	env.log.readStarted = make(chan struct{}, 1)
	env.log.readContinue = make(chan struct{})

	type fetchOutcome struct {
		result channel.ReplicaFetchResult
		err    error
	}
	done := make(chan fetchOutcome, 1)
	go func() {
		result, err := env.replica.Fetch(context.Background(), channel.ReplicaFetchRequest{
			ChannelKey:  "group-10",
			Epoch:       7,
			ReplicaID:   2,
			FetchOffset: 5,
			OffsetEpoch: env.replica.state.OffsetEpoch,
			MaxBytes:    1024,
		})
		done <- fetchOutcome{result: result, err: err}
	}()

	select {
	case <-env.log.readStarted:
	case <-time.After(time.Second):
		t.Fatal("fetch did not start reading the log")
	}
	require.NoError(t, env.replica.BecomeFollower(activeMetaWithMinISR(8, 2, 1)))
	close(env.log.readContinue)

	select {
	case outcome := <-done:
		require.ErrorIs(t, outcome.err, channel.ErrNotLeader)
		require.Empty(t, outcome.result.Records)
	case <-time.After(time.Second):
		t.Fatal("fetch did not return after read was released")
	}
}

func TestFetchReadLogResultAfterSameGenerationLEORegressionIsFenced(t *testing.T) {
	env := newFetchEnvWithHistory(t)
	env.log.readStarted = make(chan struct{}, 1)
	env.log.readContinue = make(chan struct{})

	type fetchOutcome struct {
		result channel.ReplicaFetchResult
		err    error
	}
	done := make(chan fetchOutcome, 1)
	go func() {
		result, err := env.replica.Fetch(context.Background(), channel.ReplicaFetchRequest{
			ChannelKey:  "group-10",
			Epoch:       7,
			ReplicaID:   2,
			FetchOffset: 5,
			OffsetEpoch: env.replica.state.OffsetEpoch,
			MaxBytes:    1024,
		})
		done <- fetchOutcome{result: result, err: err}
	}()

	select {
	case <-env.log.readStarted:
	case <-time.After(time.Second):
		t.Fatal("fetch did not start reading the log")
	}
	env.replica.mu.Lock()
	env.replica.state.LEO = 5
	env.replica.state.OffsetEpoch = offsetEpochForLEO(env.replica.epochHistory, 5)
	env.replica.setReplicaProgressLocked(env.replica.localNode, 5)
	env.replica.publishStateLocked()
	env.replica.mu.Unlock()
	close(env.log.readContinue)

	select {
	case outcome := <-done:
		require.ErrorIs(t, outcome.err, channel.ErrStaleMeta)
		require.Empty(t, outcome.result.Records)
	case <-time.After(time.Second):
		t.Fatal("fetch did not return after read was released")
	}
}
