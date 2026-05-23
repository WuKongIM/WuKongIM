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

	state.AbortAppendBatchProposal(100)

	require.Nil(t, state.InflightAppend)
	require.NotContains(t, state.PendingAppends, ch.OpID(1))
	require.Equal(t, uint64(0), state.LEO)
	require.Equal(t, uint64(0), state.HW)
}
