package machine

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestProposeAppendRejectsFollower(t *testing.T) {
	state := followerState(t, 2, 1)
	decision := state.ProposeAppend(AppendCommand{OpID: 1, Records: []ch.Record{{ID: 10, Payload: []byte("a"), SizeBytes: 1}}})
	require.ErrorIs(t, decision.Err, ch.ErrNotLeader)
}

func TestAppendStoredAdvancesLEOAndCompletesSingleNodeQuorum(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	decision := state.ProposeAppend(AppendCommand{OpID: 1, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 10, Payload: []byte("a"), SizeBytes: 1}}})
	require.Len(t, decision.Tasks, 1)
	decision = state.ApplyAppendStored(AppendStoredResult{Fence: decision.Tasks[0].Fence, BaseOffset: 1, LastOffset: 1})
	require.Equal(t, uint64(1), state.LEO)
	require.Equal(t, uint64(1), state.HW)
	require.Len(t, decision.Replies, 1)
	require.Equal(t, uint64(1), decision.Replies[0].Append.MessageSeq)
}

func TestAppendStoredWaitsForQuorumFollowerAck(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2)
	decision := state.ProposeAppend(AppendCommand{OpID: 1, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 10, Payload: []byte("a"), SizeBytes: 1}}})
	decision = state.ApplyAppendStored(AppendStoredResult{Fence: decision.Tasks[0].Fence, BaseOffset: 1, LastOffset: 1})
	require.Empty(t, decision.Replies)
	decision = state.ApplyFollowerAck(FollowerAck{Follower: 2, MatchOffset: 1})
	require.Equal(t, uint64(1), state.HW)
	require.Len(t, decision.Replies, 1)
	require.Empty(t, state.PendingAppendOrder)
}

func TestFollowerAckDoesNotRegressProgressOrHW(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2)
	state.HW = 5
	state.Progress[1] = ReplicaProgress{Match: 5}
	state.Progress[2] = ReplicaProgress{Match: 5}

	decision := state.ApplyFollowerAck(FollowerAck{Follower: 2, MatchOffset: 3})

	require.Empty(t, decision.Replies)
	require.Equal(t, uint64(5), state.Progress[2].Match)
	require.Equal(t, uint64(5), state.HW)
}

func TestFollowerAckCompletesBatchedWaitersInProposalOrder(t *testing.T) {
	for iteration := 0; iteration < 200; iteration++ {
		state := leaderState(t, 1, []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2)
		cmd := AppendBatchCommand{
			BatchOpID: ch.OpID(1000 + iteration),
			Waiters: []AppendBatchWaiter{
				{OpID: 1, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 10, Payload: []byte("a"), SizeBytes: 1}}},
				{OpID: 2, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 20, Payload: []byte("b"), SizeBytes: 1}}},
				{OpID: 3, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 30, Payload: []byte("c"), SizeBytes: 1}}},
			},
		}

		decision := state.ProposeAppendBatch(cmd)
		require.Len(t, decision.Tasks, 1)
		decision = state.ApplyAppendStored(AppendStoredResult{Fence: decision.Tasks[0].Fence, BaseOffset: 1, LastOffset: 3})
		require.Empty(t, decision.Replies)

		decision = state.ApplyFollowerAck(FollowerAck{Follower: 2, MatchOffset: 3})
		require.Equal(t, uint64(3), state.HW)
		require.Len(t, decision.Replies, 3)
		require.Equal(t, []ch.OpID{1, 2, 3}, []ch.OpID{decision.Replies[0].OpID, decision.Replies[1].OpID, decision.Replies[2].OpID}, "iteration %d", iteration)
		require.Equal(t, uint64(1), decision.Replies[0].AppendItems[0].MessageSeq)
		require.Equal(t, uint64(2), decision.Replies[1].AppendItems[0].MessageSeq)
		require.Equal(t, uint64(3), decision.Replies[2].AppendItems[0].MessageSeq)
		require.Empty(t, state.PendingAppendOrder)
	}
}

func TestAppendStoredCompletesMultipleWaitersFromOneBatch(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	cmd := AppendBatchCommand{
		BatchOpID: 100,
		Waiters: []AppendBatchWaiter{
			{OpID: 1, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 10, Payload: []byte("a"), SizeBytes: 1}}},
			{OpID: 2, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 20, Payload: []byte("b"), SizeBytes: 1}}},
		},
	}
	decision := state.ProposeAppendBatch(cmd)
	require.Len(t, decision.Tasks, 1)
	decision = state.ApplyAppendStored(AppendStoredResult{Fence: decision.Tasks[0].Fence, BaseOffset: 1, LastOffset: 2})
	require.Len(t, decision.Replies, 2)
	require.Equal(t, ch.OpID(1), decision.Replies[0].OpID)
	require.Equal(t, ch.OpID(2), decision.Replies[1].OpID)
	require.Equal(t, uint64(1), decision.Replies[0].AppendItems[0].MessageSeq)
	require.Equal(t, uint64(2), decision.Replies[1].AppendItems[0].MessageSeq)
	require.Empty(t, state.PendingAppendOrder)
}

func TestAppendStoredErrorFailsInflightWaitersAndPreservesOffsets(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1, 2}, []ch.NodeID{1, 2}, 2)
	decision := state.ProposeAppendBatch(AppendBatchCommand{
		BatchOpID: 100,
		Waiters: []AppendBatchWaiter{
			{OpID: 1, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 10, Payload: []byte("a"), SizeBytes: 1}}},
			{OpID: 2, CommitMode: ch.CommitModeLocal, Records: []ch.Record{{ID: 11, Payload: []byte("b"), SizeBytes: 1}}},
		},
	})
	require.Len(t, decision.Tasks, 1)

	decision = state.ApplyAppendStored(AppendStoredResult{
		Fence: decision.Tasks[0].Fence,
		Err:   ch.ErrNotReady,
	})

	require.Equal(t, uint64(0), state.LEO)
	require.Equal(t, uint64(0), state.HW)
	require.Nil(t, state.InflightAppend)
	require.Empty(t, state.PendingAppends)
	require.Empty(t, state.PendingAppendOrder)
	require.Len(t, decision.Replies, 2)
	require.ErrorIs(t, decision.Replies[0].Err, ch.ErrNotReady)
	require.ErrorIs(t, decision.Replies[1].Err, ch.ErrNotReady)
}

func TestProposeAppendBatchRejectsDuplicateWaiterOpIDWithoutMutation(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	cmd := AppendBatchCommand{
		BatchOpID: 100,
		Waiters: []AppendBatchWaiter{
			{OpID: 1, Records: []ch.Record{{ID: 10, SizeBytes: 1}}},
			{OpID: 1, Records: []ch.Record{{ID: 20, SizeBytes: 1}}},
		},
	}

	decision := state.ProposeAppendBatch(cmd)

	require.ErrorIs(t, decision.Err, ch.ErrInvalidConfig)
	require.Empty(t, decision.Tasks)
	require.Nil(t, state.InflightAppend)
	require.Empty(t, state.PendingAppends)
	require.Empty(t, state.PendingAppendOrder)
}

func TestProposeAppendBatchRejectsZeroRecordWaiterWithoutMutation(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	cmd := AppendBatchCommand{
		BatchOpID: 100,
		Waiters: []AppendBatchWaiter{
			{OpID: 1},
			{OpID: 2, Records: []ch.Record{{ID: 20, SizeBytes: 1}}},
		},
	}

	decision := state.ProposeAppendBatch(cmd)

	require.ErrorIs(t, decision.Err, ch.ErrInvalidConfig)
	require.Empty(t, decision.Tasks)
	require.Nil(t, state.InflightAppend)
	require.Empty(t, state.PendingAppends)
	require.Empty(t, state.PendingAppendOrder)
	require.Zero(t, state.LEO)
	require.Zero(t, state.HW)
}

func TestProposeAppendBatchRejectsAlreadyPendingWaiterOpIDWithoutMutation(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.PendingAppends[1] = &AppendWaiter{OpID: 1, Target: 1, CommitMode: ch.CommitModeQuorum, Records: []ch.Record{{ID: 10, Index: 1, SizeBytes: 1}}}
	state.PendingAppendOrder = []ch.OpID{1}

	decision := state.ProposeAppendBatch(AppendBatchCommand{
		BatchOpID: 100,
		Waiters:   []AppendBatchWaiter{{OpID: 1, Records: []ch.Record{{ID: 20, SizeBytes: 1}}}},
	})

	require.ErrorIs(t, decision.Err, ch.ErrNotReady)
	require.Empty(t, decision.Tasks)
	require.Nil(t, state.InflightAppend)
	require.Len(t, state.PendingAppends, 1)
	require.Equal(t, []ch.OpID{1}, state.PendingAppendOrder)
}

func TestAppendStoredIgnoresStaleBatchFence(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	decision := state.ProposeAppendBatch(AppendBatchCommand{BatchOpID: 100, Waiters: []AppendBatchWaiter{{OpID: 1, Records: []ch.Record{{ID: 10, SizeBytes: 1}}}}})
	stale := decision.Tasks[0].Fence
	stale.LeaderEpoch++
	applied := state.ApplyAppendStored(AppendStoredResult{Fence: stale, BaseOffset: 1, LastOffset: 1})
	require.Empty(t, applied.Replies)
	require.NotNil(t, state.InflightAppend)
}

func TestAbortAppendBatchProposalClearsInflightWithoutCompletingWaiters(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	decision := state.ProposeAppendBatch(AppendBatchCommand{BatchOpID: 100, Waiters: []AppendBatchWaiter{{OpID: 1, Records: []ch.Record{{ID: 10, SizeBytes: 1}}}}})
	require.Len(t, decision.Tasks, 1)
	require.NotNil(t, state.InflightAppend)
	require.Contains(t, state.PendingAppends, ch.OpID(1))
	require.Equal(t, []ch.OpID{1}, state.PendingAppendOrder)

	state.AbortAppendBatchProposal(100)

	require.Nil(t, state.InflightAppend)
	require.NotContains(t, state.PendingAppends, ch.OpID(1))
	require.Empty(t, state.PendingAppendOrder)
	require.Equal(t, uint64(0), state.LEO)
	require.Equal(t, uint64(0), state.HW)
}

func TestCancelAppendWaiterRemovesPendingStateWithoutMutatingInflightBatch(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	decision := state.ProposeAppendBatch(AppendBatchCommand{
		BatchOpID: 100,
		Waiters: []AppendBatchWaiter{
			{OpID: 1, Records: []ch.Record{{ID: 10, SizeBytes: 1}}},
			{OpID: 2, Records: []ch.Record{{ID: 20, SizeBytes: 1}}},
		},
	})
	require.NoError(t, decision.Err)

	require.True(t, state.CancelAppendWaiter(1))
	require.NotContains(t, state.PendingAppends, ch.OpID(1))
	require.Equal(t, []ch.OpID{2}, state.PendingAppendOrder)
	require.NotNil(t, state.InflightAppend)
	require.Equal(t, []ch.OpID{1, 2}, state.InflightAppend.WaiterOpIDs)

	stored := state.ApplyAppendStored(AppendStoredResult{
		Fence:      ch.Fence{ChannelKey: state.Key, Generation: state.Generation, Epoch: state.Epoch, LeaderEpoch: state.LeaderEpoch, OpID: 100},
		BaseOffset: 1,
		LastOffset: 2,
	})
	require.Len(t, stored.Replies, 1)
	require.Equal(t, ch.OpID(2), stored.Replies[0].OpID)
	require.Equal(t, uint64(2), stored.Replies[0].AppendItems[0].MessageSeq)
	require.Empty(t, state.PendingAppends)
	require.Empty(t, state.PendingAppendOrder)
}
