package reactor

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestAppendRouteFenceRequiresExactDurableAuthority(t *testing.T) {
	for _, mode := range []string{"exact", "stale", "future", "missing-epoch", "missing-leader-epoch", "local-commit", "no-quorum", "unfenced"} {
		t.Run(mode, func(t *testing.T) {
			log := &reactorCaptureQuorumLog{}
			cfg := Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: store.NewMemoryFactory(), QuorumLog: log, AppendBatchMaxRecords: 1}
			if mode == "no-quorum" {
				cfg.QuorumLog = nil
			}
			g, err := NewGroup(cfg)
			require.NoError(t, err)
			defer g.Close()
			m := testMeta("route-proof", 1, 1)
			m.RouteGeneration = 7
			require.NoError(t, awaitSubmit(g, m.Key, Event{Kind: EventApplyMeta, Key: m.Key, Meta: m}))
			e := appendEvent(m, 11, "body")
			e.Append.ExpectedChannelEpoch = m.Epoch
			e.Append.ExpectedLeaderEpoch = m.LeaderEpoch
			e.Append.ExpectedRouteGeneration = 7
			e.Append.CommitMode = ch.CommitModeQuorum
			want := error(nil)
			switch mode {
			case "stale":
				e.Append.ExpectedRouteGeneration = 6
				want = ch.ErrStaleMeta
			case "future":
				e.Append.ExpectedRouteGeneration = 8
				want = ch.ErrStaleMeta
			case "missing-epoch":
				e.Append.ExpectedChannelEpoch = 0
				want = ch.ErrInvalidConfig
			case "missing-leader-epoch":
				e.Append.ExpectedLeaderEpoch = 0
				want = ch.ErrInvalidConfig
			case "local-commit":
				e.Append.CommitMode = ch.CommitModeLocal
				want = ch.ErrInvalidConfig
			case "no-quorum":
				want = ch.ErrInvalidConfig
			case "unfenced":
				e.Append.ExpectedRouteGeneration = 0
			}
			f, err := g.Submit(context.Background(), m.Key, e)
			require.NoError(t, err)
			result, err := f.Await(context.Background())
			require.ErrorIs(t, err, want)
			if want != nil {
				require.Empty(t, log.proposals())
				return
			}
			require.Len(t, result.AppendBatch.Items, 1)
			require.Equal(t, uint64(1), result.AppendBatch.Items[0].MessageSeq)
			require.Equal(t, uint64(7), log.proposals()[0].Expected.FenceVersion)
		})
	}
}
