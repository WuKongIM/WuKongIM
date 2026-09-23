package machine

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestQuorumControlCompletionHasNoFabricatedMessage(t *testing.T) {
	for _, mode := range []string{"new", "old", "canceled", "stale", "error", "zero"} {
		t.Run(mode, func(t *testing.T) {
			s := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
			s.LEO = 5
			s.HW = 5
			d := s.ProposeAppend(AppendCommand{OpID: 1, Records: []ch.Record{{ID: 999, Payload: []byte("request-only"), SizeBytes: 12}}})
			require.Len(t, d.Tasks, 1)
			result := QuorumControlCommittedResult{Fence: d.Tasks[0].Fence, CommittedThrough: 6}
			switch mode {
			case "old":
				result.CommittedThrough = 3
			case "canceled":
				require.True(t, s.CancelAppendWaiter(1))
			case "stale":
				result.Fence.Generation++
			case "error":
				result.Err = ch.ErrClosed
			case "zero":
				result.CommittedThrough = 0
			}
			done := s.ApplyQuorumControlCommitted(result)
			if mode == "stale" {
				require.NotNil(t, s.InflightAppend)
				require.Empty(t, done.Replies)
				require.Equal(t, uint64(5), s.HW)
				return
			}
			require.Nil(t, s.InflightAppend)
			require.Empty(t, s.PendingAppends)
			require.Empty(t, s.PendingAppendOrder)
			if mode == "new" || mode == "canceled" {
				require.Equal(t, uint64(6), s.HW)
			} else {
				require.Equal(t, uint64(5), s.HW)
			}
			require.Equal(t, s.HW, s.LEO)
			if mode == "canceled" {
				require.Empty(t, done.Replies)
				return
			}
			require.Len(t, done.Replies, 1)
			require.Empty(t, done.Replies[0].AppendItems)
			require.Zero(t, done.Replies[0].Append.MessageID)
			require.Equal(t, mode == "error" || mode == "zero", done.Replies[0].Err != nil)
		})
	}
}
