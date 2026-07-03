//go:build e2e

package dynamic_node_join

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestLeavingNodeCanBeRemovedAfterDrain(t *testing.T) {
	s := suite.New(t)
	const joinToken = "stage5-remove-token"
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	before := manager.MustSlots(t)

	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	require.NotNil(t, node4)

	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	afterActivate := manager.MustSlots(t)
	require.True(t, suite.SameSlotAssignments(before, afterActivate), "activation without onboarding must not move Slots")

	start := manager.MustStartScaleIn(t, 4)
	require.Equal(t, uint64(4), start.NodeID)
	require.Equal(t, "leaving", start.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "leaving", 20*time.Second)

	drain := manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)
	require.False(t, drain.AcceptingNewSessions)

	status := manager.EventuallyScaleInSafeToRemove(t, 4, 30*time.Second)
	require.True(t, status.SafeToRemove)
	require.False(t, status.BlockedBySlots)
	require.False(t, status.BlockedByTasks)
	require.False(t, status.BlockedByChannels)
	require.False(t, status.BlockedByRuntimeDrain)
	require.True(t, status.GatewayDraining)
	require.False(t, status.AcceptingNewSessions)
	require.Zero(t, status.GatewaySessions)
	require.Zero(t, status.ActiveOnline)
	require.Zero(t, status.ClosingOnline)
	require.Zero(t, status.PendingActivations)

	removed := manager.MustRemoveScaleInNode(t, 4)
	require.Equal(t, uint64(4), removed.NodeID)
	require.Equal(t, "removed", removed.JoinState)
	manager.EventuallyNodeJoinState(t, 4, "removed", 20*time.Second)
}
