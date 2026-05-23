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
