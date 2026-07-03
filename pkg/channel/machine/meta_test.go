package machine

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestApplyMetaAssignsLeaderRole(t *testing.T) {
	state := NewChannelState(ch.ChannelKey("1:a"), ch.NodeID(1), 1)
	decision := state.ApplyMeta(ch.Meta{Key: state.Key, ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive})
	require.Empty(t, decision.Replies)
	require.Equal(t, ch.RoleLeader, state.Role)
	require.True(t, state.CommitReady)
	require.Equal(t, uint64(0), state.HW)
}

func TestApplyMetaAssignsFollowerRole(t *testing.T) {
	state := NewChannelState(ch.ChannelKey("1:a"), ch.NodeID(2), 1)
	state.ApplyMeta(ch.Meta{Key: state.Key, ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive})
	require.Equal(t, ch.RoleFollower, state.Role)
}

func TestApplyMetaRejectsInvalidMinISR(t *testing.T) {
	state := NewChannelState(ch.ChannelKey("1:a"), ch.NodeID(1), 1)
	decision := state.ApplyMeta(ch.Meta{Key: state.Key, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 2, Status: ch.StatusActive})
	require.ErrorIs(t, decision.Err, ch.ErrInvalidConfig)
}

func TestApplyMetaLeaderEpochChangeClearsAppendStateWithoutReplies(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2)
	proposed := state.ProposeAppendBatch(AppendBatchCommand{
		BatchOpID: 100,
		Waiters: []AppendBatchWaiter{
			{OpID: 1, Records: []ch.Record{{ID: 10, SizeBytes: 1}}},
			{OpID: 2, Records: []ch.Record{{ID: 20, SizeBytes: 1}}},
		},
	})
	require.Len(t, proposed.Tasks, 1)
	require.NotNil(t, state.InflightAppend)
	require.Len(t, state.PendingAppends, 2)
	require.Equal(t, []ch.OpID{1, 2}, state.PendingAppendOrder)

	decision := state.ApplyMeta(ch.Meta{Key: state.Key, ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 2, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive})

	require.NoError(t, decision.Err)
	require.Empty(t, decision.Replies)
	require.Nil(t, state.InflightAppend)
	require.Empty(t, state.PendingAppends)
	require.Empty(t, state.PendingAppendOrder)

	next := state.ProposeAppendBatch(AppendBatchCommand{
		BatchOpID: 101,
		Waiters:   []AppendBatchWaiter{{OpID: 3, Records: []ch.Record{{ID: 30, SizeBytes: 1}}}},
	})
	require.NoError(t, next.Err)
	require.Len(t, next.Tasks, 1)
}

func TestApplyMetaCopiesWriteFenceWithoutClearingInflight(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2)
	state.PendingAppends[1] = &AppendWaiter{OpID: 1}
	state.InflightAppend = &AppendOp{OpID: 2}

	meta := ch.Meta{
		Key:         state.Key,
		ID:          state.ID,
		Epoch:       state.Epoch,
		LeaderEpoch: state.LeaderEpoch,
		Leader:      state.Leader,
		Replicas:    []ch.NodeID{1, 2, 3},
		ISR:         []ch.NodeID{1, 2, 3},
		MinISR:      2,
		Status:      ch.StatusActive,
		WriteFence: ch.WriteFence{
			Token:   "migration-1",
			Version: 7,
			Reason:  ch.WriteFenceReasonLeaderTransfer,
		},
	}

	decision := state.ApplyMeta(meta)

	require.NoError(t, decision.Err)
	require.Equal(t, meta.WriteFence, state.WriteFence)
	require.Len(t, state.PendingAppends, 1)
	require.NotNil(t, state.InflightAppend)
}
