//go:build e2e

package dynamic_node_join

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestSeedJoinRejectsInvalidJoinTokenWithoutMembership(t *testing.T) {
	s := suite.New(t)
	const goodToken = "e2e-negative-good-token"
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(goodToken),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	beforeSlots := manager.MustSlots(t)
	node4 := cluster.StartSeedJoinNodeNoWait(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: "bad-token",
	})
	require.NotNil(t, node4)

	requireNodeAbsentFor(t, cluster, manager, 4, 3*time.Second)
	afterSlots := manager.MustSlots(t)
	require.True(t, suite.SameSlotAssignments(beforeSlots, afterSlots), "invalid token join must not change Slot assignments\n%s", cluster.DumpDiagnostics())
	manager.EventuallyNodeReadiness(t, 4, false, 2*time.Second)
}

func TestActivateRejectsUnreachableAdvertiseAddr(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2e-negative-unreachable-token"
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	beforeSlots := manager.MustSlots(t)
	unreachableAddr := suite.ReserveLoopbackPorts(t).ClusterAddr
	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinAddr:  unreachableAddr,
		JoinToken: joinToken,
	})
	require.NotNil(t, node4)

	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	status, body, err := manager.ActivateNodeStatus(ctx, 4)
	cancel()
	require.NoError(t, err, cluster.DumpDiagnostics())
	requireScaleInConflict(t, status, body, cluster.DumpDiagnostics())

	stateCtx, cancelState := context.WithTimeout(context.Background(), 5*time.Second)
	state, ok, err := manager.NodeJoinState(stateCtx, 4)
	cancelState()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())
	require.Equal(t, "joining", state, "node 4 should remain joining after failed activation\n%s", cluster.DumpDiagnostics())

	afterSlots := manager.MustSlots(t)
	require.True(t, suite.SameSlotAssignments(beforeSlots, afterSlots), "failed activation must not change Slot assignments\n%s", cluster.DumpDiagnostics())
}

func requireNodeAbsentFor(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, window time.Duration) {
	t.Helper()

	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		state, ok, err := manager.NodeJoinState(ctx, nodeID)
		cancel()
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.False(t, ok, "node %d unexpectedly appeared with join_state=%q\n%s", nodeID, state, cluster.DumpDiagnostics())
		time.Sleep(100 * time.Millisecond)
	}
}
