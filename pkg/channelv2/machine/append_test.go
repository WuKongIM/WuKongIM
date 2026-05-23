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
