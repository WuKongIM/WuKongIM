package cluster_test

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestDynamicJoinAddsDataNodeWithoutControllerVoterChange(t *testing.T) {
	const joinToken = "join-secret"
	const slotCount = 1

	listeners := make([]net.Listener, 4)
	for i := range listeners {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		listeners[i] = ln
	}

	nodes := make([]raftcluster.NodeConfig, 3)
	for i := range nodes {
		nodes[i] = raftcluster.NodeConfig{
			NodeID: multiraft.NodeID(i + 1),
			Addr:   listeners[i].Addr().String(),
		}
		require.NoError(t, listeners[i].Close())
	}
	joinAddr := listeners[3].Addr().String()
	require.NoError(t, listeners[3].Close())

	testNodes := make([]*testNode, 4)
	root := t.TempDir()
	for i := range nodes {
		testNodes[i] = newStartedTestNodeWithHashSlotsMutate(
			t,
			filepath.Join(root, fmt.Sprintf("n%d", i+1)),
			multiraft.NodeID(i+1),
			nodes[i].Addr,
			nodes,
			nil,
			slotCount,
			0,
			3,
			3,
			true,
			func(cfg *raftcluster.Config) {
				cfg.JoinToken = joinToken
			},
		)
	}
	t.Cleanup(func() { stopNodes(testNodes) })
	waitForManagedSlotsSettled(t, testNodes[:3], slotCount)

	seeds := make([]raftcluster.SeedConfig, 0, len(nodes))
	for _, node := range nodes {
		seeds = append(seeds, raftcluster.SeedConfig{ID: node.NodeID, Addr: node.Addr})
	}
	testNodes[3] = newStartedTestNodeWithHashSlotsMutate(
		t,
		filepath.Join(root, "n4"),
		4,
		joinAddr,
		nil,
		nil,
		slotCount,
		0,
		3,
		3,
		true,
		func(cfg *raftcluster.Config) {
			cfg.Seeds = seeds
			cfg.AdvertiseAddr = joinAddr
			cfg.JoinToken = joinToken
		},
	)

	var joined controllermeta.ClusterNode
	require.Eventually(t, func() bool {
		snapshot, err := testNodes[0].cluster.ListNodesStrict(context.Background())
		if err != nil {
			return false
		}
		node, ok := clusterNodeByID(snapshot, 4)
		if !ok {
			return false
		}
		joined = node
		return node.Status == controllermeta.NodeStatusAlive &&
			node.JoinState == controllermeta.NodeJoinStateActive &&
			node.Role == controllermeta.NodeRoleData
	}, 20*time.Second, 200*time.Millisecond)
	require.Equal(t, joinAddr, joined.Addr)

	require.Eventually(t, func() bool {
		addr, err := testNodes[0].cluster.Discovery().Resolve(4)
		return err == nil && addr == joinAddr
	}, 5*time.Second, 100*time.Millisecond)
	for _, node := range nodes {
		node := node
		require.Eventually(t, func() bool {
			addr, err := testNodes[3].cluster.Discovery().Resolve(uint64(node.NodeID))
			return err == nil && addr == node.Addr
		}, 5*time.Second, 100*time.Millisecond)
	}

	_, node4IsController := probeControllerLeader(testNodes[0].cluster, 4)
	require.False(t, node4IsController, "joined data node must not expose a controller voter endpoint")
	controllerLeader, ok := currentControllerLeaderNode(testNodes)
	require.True(t, ok)
	require.NotEqual(t, multiraft.NodeID(4), controllerLeader.nodeID)
}

func clusterNodeByID(nodes []controllermeta.ClusterNode, nodeID uint64) (controllermeta.ClusterNode, bool) {
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return controllermeta.ClusterNode{}, false
}
