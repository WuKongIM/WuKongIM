//go:build e2e

package channel_failover

import (
	"encoding/json"
	"testing"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestChannelRuntimeMetaItemDecodesMinISR(t *testing.T) {
	var meta channelRuntimeMetaItem
	require.NoError(t, json.Unmarshal([]byte(`{"min_isr":2}`), &meta))
	require.Equal(t, int64(2), meta.MinISR)
}

func TestReplicaRepairTopologyPreconditionRequiresMinISRQuorum(t *testing.T) {
	candidate := followerRepairCandidate{
		Leader:                1,
		StoppedFollower:       2,
		SecondStoppedFollower: 3,
		SpareNode:             4,
	}
	meta := channelRuntimeMetaItem{
		Leader:     1,
		SlotLeader: 1,
		Replicas:   []uint64{1, 3, 4},
		ISR:        []uint64{1, 3, 4},
		MinISR:     1,
		Status:     "active",
	}

	err := replicaRepairTopologyPrecondition(meta, candidate)

	require.ErrorContains(t, err, "min_isr=1, want 2")
}

func TestReplicaRepairTopologyPreconditionAcceptsRepairedQuorum(t *testing.T) {
	candidate := followerRepairCandidate{
		Leader:                1,
		StoppedFollower:       2,
		SecondStoppedFollower: 3,
		SpareNode:             4,
	}
	meta := channelRuntimeMetaItem{
		Leader:     1,
		SlotLeader: 1,
		Replicas:   []uint64{1, 3, 4},
		ISR:        []uint64{1, 3, 4},
		MinISR:     2,
		Status:     "active",
	}

	require.NoError(t, replicaRepairTopologyPrecondition(meta, candidate))
}

func TestControllerQuorumPreconditionRequiresHealthySchedulableSpare(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*suite.NodeDTO)
	}{
		{
			name: "not schedulable",
			mutate: func(node *suite.NodeDTO) {
				node.Membership.Schedulable = false
			},
		},
		{
			name: "stale",
			mutate: func(node *suite.NodeDTO) {
				node.Health.Fresh = false
			},
		},
		{
			name: "runtime not ready",
			mutate: func(node *suite.NodeDTO) {
				node.Health.RuntimeReady = false
			},
		},
		{
			name: "not alive",
			mutate: func(node *suite.NodeDTO) {
				node.Health.Status = "stale"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes := healthyControllerQuorumNodes()
			tc.mutate(&nodes.Items[3])

			err := controllerQuorumPrecondition(nodes, []uint64{1, 2, 3}, 4)

			require.ErrorContains(t, err, "repaired spare node 4 is not healthy/schedulable")
		})
	}
}

func TestControllerQuorumPreconditionAcceptsHealthyDataOnlySpare(t *testing.T) {
	require.NoError(t, controllerQuorumPrecondition(healthyControllerQuorumNodes(), []uint64{1, 2, 3}, 4))
}

func healthyControllerQuorumNodes() suite.NodeListDTO {
	items := make([]suite.NodeDTO, 0, 4)
	for nodeID := uint64(1); nodeID <= 4; nodeID++ {
		items = append(items, suite.NodeDTO{
			NodeID: nodeID,
			Membership: suite.NodeMembershipDTO{
				Schedulable: true,
			},
			Health: suite.NodeHealthDTO{
				Fresh:        true,
				RuntimeReady: true,
				Status:       "alive",
			},
			Controller: suite.NodeControllerDTO{
				Voter: nodeID != 4,
			},
		})
	}
	return suite.NodeListDTO{Total: len(items), Items: items}
}
