package replica

import (
	"context"
	"testing"

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
		OffsetEpoch: 0,
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
