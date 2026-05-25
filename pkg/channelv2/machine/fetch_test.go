package machine

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestBuildFetchReturnsEmptyAboveHW(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 3
	decision := state.BuildFetch(FetchCommand{OpID: 1, FromSeq: 4, Limit: 10, MaxBytes: 1024})
	require.Len(t, decision.Replies, 1)
	require.Equal(t, uint64(4), decision.Replies[0].Fetch.NextSeq)
}

func TestBuildFetchCreatesReadCommittedTask(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 3
	decision := state.BuildFetch(FetchCommand{OpID: 1, FromSeq: 1, Limit: 10, MaxBytes: 1024})
	require.Len(t, decision.Tasks, 1)
	require.Equal(t, uint64(3), decision.Tasks[0].ReadCommitted.MaxSeq)
}

func TestReadCommittedIgnoresStaleFence(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 3
	decision := state.ApplyReadCommitted(ReadCommittedResult{
		Fence:    ch.Fence{ChannelKey: state.Key, Generation: state.Generation, Epoch: state.Epoch, LeaderEpoch: state.LeaderEpoch + 1, OpID: 10},
		Messages: []ch.Message{{MessageSeq: 1}},
		NextSeq:  2,
	})
	require.Empty(t, decision.Replies)
}

func TestApplyReadCommittedReturnsStoreError(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 3
	fence := ch.Fence{ChannelKey: state.Key, Generation: state.Generation, Epoch: state.Epoch, LeaderEpoch: state.LeaderEpoch, OpID: 7}

	decision := state.ApplyReadCommitted(ReadCommittedResult{Fence: fence, Err: ch.ErrNotReady})

	require.Len(t, decision.Replies, 1)
	require.Equal(t, ReplyKindFetch, decision.Replies[0].Kind)
	require.Equal(t, ch.OpID(7), decision.Replies[0].OpID)
	require.ErrorIs(t, decision.Replies[0].Err, ch.ErrNotReady)
}

func TestApplyReadCommittedTrimsMessagesAboveHW(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 2
	fence := ch.Fence{ChannelKey: state.Key, Generation: state.Generation, Epoch: state.Epoch, LeaderEpoch: state.LeaderEpoch, OpID: 8}

	decision := state.ApplyReadCommitted(ReadCommittedResult{
		Fence: fence,
		Messages: []ch.Message{
			{MessageSeq: 1, Payload: []byte("a")},
			{MessageSeq: 3, Payload: []byte("future")},
		},
		NextSeq: 4,
	})

	require.Len(t, decision.Replies, 1)
	require.Len(t, decision.Replies[0].Fetch.Messages, 1)
	require.Equal(t, uint64(1), decision.Replies[0].Fetch.Messages[0].MessageSeq)
	require.Equal(t, uint64(4), decision.Replies[0].Fetch.NextSeq)
	require.Equal(t, uint64(2), decision.Replies[0].Fetch.CommittedSeq)
}

func TestApplyReadCommittedDefaultsNextSeqWhenStoreOmitsIt(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.HW = 5
	fence := ch.Fence{ChannelKey: state.Key, Generation: state.Generation, Epoch: state.Epoch, LeaderEpoch: state.LeaderEpoch, OpID: 9}

	decision := state.ApplyReadCommitted(ReadCommittedResult{Fence: fence})

	require.Len(t, decision.Replies, 1)
	require.Equal(t, uint64(6), decision.Replies[0].Fetch.NextSeq)
	require.Equal(t, uint64(5), decision.Replies[0].Fetch.CommittedSeq)
}
