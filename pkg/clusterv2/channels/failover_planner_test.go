package channels

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestFailoverPlannerSelectsHealthyISRTargetWithHighestCompatibleHW(t *testing.T) {
	id := ch.ChannelID{ID: "failover-a", Type: 1}
	meta := failoverPlannerMeta(id)

	decision := NewFailoverPlanner().Plan(FailoverPlanInput{
		Meta:       meta,
		Nodes:      failoverHealthyNodes(1, 2, 3),
		RequiredHW: 9,
		Probes: []FailoverCandidateProbe{
			failoverProbe(id, 2, meta.ChannelEpoch, meta.LeaderEpoch, 12, 12),
			failoverProbe(id, 3, meta.ChannelEpoch, meta.LeaderEpoch, 10, 10),
		},
	})

	require.Equal(t, FailoverActionCreateLeaderTransfer, decision.Action)
	require.Equal(t, uint64(2), decision.TargetNode)
	require.Equal(t, uint64(12), decision.ObservedHW)
	require.Equal(t, meta.LeaderEpoch, decision.ObservedEpoch)
}

func TestFailoverPlannerRejectsCandidatesNotInISR(t *testing.T) {
	id := ch.ChannelID{ID: "failover-not-isr", Type: 1}
	meta := failoverPlannerMeta(id)
	meta.ISR = []uint64{1}

	decision := NewFailoverPlanner().Plan(FailoverPlanInput{
		Meta:       meta,
		Nodes:      failoverHealthyNodes(1, 2),
		RequiredHW: 1,
		Probes: []FailoverCandidateProbe{
			failoverProbe(id, 2, meta.ChannelEpoch, meta.LeaderEpoch, 5, 5),
		},
	})

	require.Equal(t, FailoverActionBlocked, decision.Action)
	require.Equal(t, "no_safe_candidate", decision.BlockReason)
}

func TestFailoverPlannerRejectsMismatchedEpochs(t *testing.T) {
	id := ch.ChannelID{ID: "failover-epoch", Type: 1}
	meta := failoverPlannerMeta(id)

	decision := NewFailoverPlanner().Plan(FailoverPlanInput{
		Meta:       meta,
		Nodes:      failoverHealthyNodes(1, 2, 3),
		RequiredHW: 1,
		Probes: []FailoverCandidateProbe{
			failoverProbe(id, 2, meta.ChannelEpoch+1, meta.LeaderEpoch, 5, 5),
			failoverProbe(id, 3, meta.ChannelEpoch, meta.LeaderEpoch+2, 6, 6),
		},
	})

	require.Equal(t, FailoverActionBlocked, decision.Action)
	require.Equal(t, "no_safe_candidate", decision.BlockReason)
}

func TestFailoverPlannerBlocksWhenNoCandidateProvesSafePrefix(t *testing.T) {
	id := ch.ChannelID{ID: "failover-lag", Type: 1}
	meta := failoverPlannerMeta(id)

	decision := NewFailoverPlanner().Plan(FailoverPlanInput{
		Meta:       meta,
		Nodes:      failoverHealthyNodes(1, 2, 3),
		RequiredHW: 10,
		Probes: []FailoverCandidateProbe{
			failoverProbe(id, 2, meta.ChannelEpoch, meta.LeaderEpoch, 7, 7),
			failoverProbe(id, 3, meta.ChannelEpoch, meta.LeaderEpoch, 9, 9),
		},
	})

	require.Equal(t, FailoverActionBlocked, decision.Action)
	require.Equal(t, "target_lagging", decision.BlockReason)
	require.Equal(t, uint64(9), decision.ObservedHW)
}

func TestFailoverPlannerBlocksWhenActiveMigrationTaskExists(t *testing.T) {
	id := ch.ChannelID{ID: "failover-active", Type: 1}
	meta := failoverPlannerMeta(id)

	decision := NewFailoverPlanner().Plan(FailoverPlanInput{
		Meta:          meta,
		Nodes:         failoverHealthyNodes(1, 2),
		RequiredHW:    1,
		ActiveTask:    true,
		Probes:        []FailoverCandidateProbe{failoverProbe(id, 2, meta.ChannelEpoch, meta.LeaderEpoch, 5, 5)},
		LeaderSuspect: true,
	})

	require.Equal(t, FailoverActionBlocked, decision.Action)
	require.Equal(t, "active_migration", decision.BlockReason)
}

func failoverPlannerMeta(id ch.ChannelID) metadb.ChannelRuntimeMeta {
	return metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 11,
		LeaderEpoch:  20,
		Leader:       1,
		Replicas:     []uint64{1, 2, 3},
		ISR:          []uint64{1, 2, 3},
		MinISR:       2,
		Status:       uint8(ch.StatusActive),
	})
}

func failoverHealthyNodes(ids ...uint64) []control.Node {
	nodes := make([]control.Node, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, control.Node{
			NodeID:    id,
			Roles:     []control.Role{control.RoleData},
			JoinState: control.NodeJoinStateActive,
			Health: control.NodeHealth{
				Freshness:    control.NodeHealthFresh,
				Status:       control.NodeAlive,
				RuntimeReady: true,
			},
		})
	}
	return nodes
}

func failoverProbe(id ch.ChannelID, nodeID uint64, channelEpoch uint64, leaderEpoch uint64, hw uint64, checkpointHW uint64) FailoverCandidateProbe {
	return FailoverCandidateProbe{
		NodeID: nodeID,
		Probe: ch.RuntimeProbeChannel{
			ChannelID:      id,
			ChannelEpoch:   channelEpoch,
			LeaderEpoch:    leaderEpoch,
			Role:           ch.RoleFollower,
			Status:         ch.StatusActive,
			HW:             hw,
			LEO:            hw,
			CheckpointHW:   checkpointHW,
			WriteFence:     ch.WriteFence{},
			InflightAppend: false,
		},
	}
}
