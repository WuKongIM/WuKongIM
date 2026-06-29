//go:build e2e

package dynamic_node_faults

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestSlotReplicaMoveSurvivesDelayedLeaderTransfer(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-slot-move-token"
	node1Fail := suite.ReserveGofailEndpoint(t)
	node2Fail := suite.ReserveGofailEndpoint(t)
	node3Fail := suite.ReserveGofailEndpoint(t)
	node4Fail := suite.ReserveGofailEndpoint(t)

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeEnv(1, node1Fail.Env()),
		suite.WithNodeEnv(2, node2Fail.Env()),
		suite.WithNodeEnv(3, node3Fail.Env()),
		suite.WithNodeEnv(4, node4Fail.Env()),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	listCtx, cancelList := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelList()
	for _, endpoint := range []suite.GofailEndpoint{node1Fail, node2Fail, node3Fail} {
		body, err := endpoint.WaitListed(listCtx, "wkSlotReplicaMoveTransferLeaderDelay")
		require.NoError(t, err, "gofail body:\n%s\n%s", body, cluster.DumpDiagnostics())
	}

	failCtx, cancelFail := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFail()
	for _, endpoint := range []suite.GofailEndpoint{node1Fail, node2Fail, node3Fail} {
		require.NoError(t, endpoint.Enable(failCtx, "wkSlotReplicaMoveTransferLeaderDelay", `return("700ms")`), cluster.DumpDiagnostics())
		defer func(endpoint suite.GofailEndpoint) {
			disableCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = endpoint.Disable(disableCtx, "wkSlotReplicaMoveTransferLeaderDelay")
		}(endpoint)
	}

	plan := manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, plan.Candidates, 1, cluster.DumpDiagnostics())
	start := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created, cluster.DumpDiagnostics())

	manager.EventuallyOnboardingSafe(t, 4, 90*time.Second)
	statusCtx, cancelStatus := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStatus()
	status, err := manager.NodeOnboardingStatus(statusCtx, 4)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, 0, status.Summary.Failed, "status=%#v\n%s", status, cluster.DumpDiagnostics())

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()
	require.NoError(t, sender.Connect(node4.GatewayAddr(), "gofail-slot-move-sender", "gofail-slot-move-device"), cluster.DumpDiagnostics())
}
