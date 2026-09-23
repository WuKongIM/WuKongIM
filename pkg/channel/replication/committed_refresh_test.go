package replication

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type committedRefreshSink struct{ repairs []followerRepair }

func (s *committedRefreshSink) RecordFollowerRepair(r followerRepair) {
	s.repairs = append(s.repairs, r)
}
func (s *committedRefreshSink) InstallAuthority(Authority) {}

func TestCommittedReplicaRefreshUsesOnlyInstalledQuorumFrontier(t *testing.T) {
	for _, mode := range []string{"ok", "empty", "missing", "unready", "released", "changed_epoch", "changed_voters", "changed_quorum", "fenced", "cancel", "invalid_tail"} {
		t.Run(mode, func(t *testing.T) {
			h := newReplicaHarness(t, 1, 2, 3)
			sink := &committedRefreshSink{}
			log, err := newQuorumLog(quorumLogConfig{Local: 1, Store: h.stores[1], Recovery: h, Durability: h, RepairAuthorities: sink, RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10, MaxChannels: 8, MaxVoters: 3, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16})
			require.NoError(t, err)
			a := Authority{Key: "1:refresh", ChannelID: ch.ChannelID{ID: "refresh", Type: 1}, ID: AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
			_, err = log.Install(context.Background(), a)
			require.NoError(t, err)
			if mode != "empty" {
				_, err = log.Commit(context.Background(), Proposal{Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{9}, Records: []ch.Record{{ID: 9, Epoch: 1, Payload: []byte("last"), SizeBytes: 4, ServerTimestampMS: 1}}})
				require.NoError(t, err)
			}
			state := log.channels[a.Key]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "missing":
				delete(log.channels, a.Key)
			case "unready":
				state.ready = false
			case "released":
				state.released = true
			case "changed_epoch":
				a.ID.LeaderTerm++
			case "changed_voters":
				a.Voters = []ch.NodeID{1, 2}
			case "changed_quorum":
				a.WriteQuorum = 3
			case "fenced":
				a.WriteFence = ch.WriteFence{Token: "transfer", Version: 1}
			case "cancel":
				cancel()
			case "invalid_tail":
				state.hw++
			}
			err = log.RequestCommittedReplicaRefresh(ctx, a)
			if mode == "ok" {
				require.NoError(t, err)
				require.Len(t, sink.repairs, 2)
				for i, r := range sink.repairs {
					require.Equal(t, ch.NodeID(i+2), r.follower)
					require.Equal(t, uint64(1), r.committed)
					require.Equal(t, uint64(1), r.needFrom)
					require.Equal(t, state.frontier.Manifest, r.manifest)
				}
			} else if mode == "empty" {
				require.NoError(t, err)
				require.Empty(t, sink.repairs)
			} else {
				require.Error(t, err)
				require.Empty(t, sink.repairs)
			}
		})
	}
}
