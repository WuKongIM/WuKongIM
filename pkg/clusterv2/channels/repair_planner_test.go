package channels

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestReplicaRepairPlannerBlocksNewPlacementWhenCandidatesBelowReplicaCount(t *testing.T) {
	_, err := selectChannelReplicas("repair-new-channel", []uint64{1, 2}, 3)

	require.ErrorIs(t, err, ch.ErrInvalidConfig)
}

func TestReplicaRepairPlannerMarksDegradedWritableWithMinISRSurvivors(t *testing.T) {
	id := ch.ChannelID{ID: "repair-degraded", Type: 1}
	meta := replicaRepairMeta(id)
	meta.ISR = []uint64{1, 2}

	decision := NewReplicaRepairPlanner().Plan(ReplicaRepairPlanInput{
		Meta:  meta,
		Nodes: repairPlannerNodes([]uint64{1, 2, 4}, []uint64{3}),
	})

	require.True(t, decision.Degraded)
	require.True(t, decision.Writable)
	require.Equal(t, ReplicaRepairActionCreateReplicaReplace, decision.Action)
}

func TestReplicaRepairPlannerBlocksWithoutHealthyReplacement(t *testing.T) {
	id := ch.ChannelID{ID: "repair-no-target", Type: 1}
	meta := replicaRepairMeta(id)
	meta.ISR = []uint64{1, 2}

	decision := NewReplicaRepairPlanner().Plan(ReplicaRepairPlanInput{
		Meta:  meta,
		Nodes: repairPlannerNodes([]uint64{1, 2}, []uint64{3}),
	})

	require.Equal(t, ReplicaRepairActionBlocked, decision.Action)
	require.Equal(t, uint64(3), decision.SourceNode)
	require.Equal(t, "no_healthy_replacement", decision.BlockReason)
	require.True(t, decision.Degraded)
	require.True(t, decision.Writable)
}

func TestReplicaRepairPlannerCreatesReplicaReplacementWithHealthyTarget(t *testing.T) {
	id := ch.ChannelID{ID: "repair-target", Type: 1}
	meta := replicaRepairMeta(id)
	meta.ISR = []uint64{1, 2}

	decision := NewReplicaRepairPlanner().Plan(ReplicaRepairPlanInput{
		Meta:  meta,
		Nodes: repairPlannerNodes([]uint64{1, 2, 4}, []uint64{3}),
	})

	require.Equal(t, ReplicaRepairActionCreateReplicaReplace, decision.Action)
	require.Equal(t, uint64(3), decision.SourceNode)
	require.Equal(t, uint64(4), decision.TargetNode)
	require.Empty(t, decision.BlockReason)
}

func TestReplicaRepairPlannerWaitsForLeaderFailoverWhenSourceIsLeader(t *testing.T) {
	id := ch.ChannelID{ID: "repair-leader-source", Type: 1}
	meta := replicaRepairMeta(id)
	meta.Leader = 3

	decision := NewReplicaRepairPlanner().Plan(ReplicaRepairPlanInput{
		Meta:  meta,
		Nodes: repairPlannerNodes([]uint64{1, 2, 4}, []uint64{3}),
	})

	require.Equal(t, ReplicaRepairActionWaitForLeaderFailover, decision.Action)
	require.Equal(t, uint64(3), decision.SourceNode)
	require.Zero(t, decision.TargetNode)
}

func replicaRepairMeta(id ch.ChannelID) metadb.ChannelRuntimeMeta {
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

func repairPlannerNodes(healthy []uint64, unhealthy []uint64) []control.Node {
	nodes := failoverHealthyNodes(healthy...)
	for _, id := range unhealthy {
		nodes = append(nodes, control.Node{
			NodeID:    id,
			Roles:     []control.Role{control.RoleData},
			JoinState: control.NodeJoinStateActive,
			Health: control.NodeHealth{
				Freshness:    control.NodeHealthFresh,
				Status:       control.NodeDown,
				RuntimeReady: false,
			},
		})
	}
	return nodes
}
