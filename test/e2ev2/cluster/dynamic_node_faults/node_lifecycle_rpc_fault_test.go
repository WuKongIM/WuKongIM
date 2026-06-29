//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

const (
	clusterNetCallShardFault      = "wkClusterNetCallShardFault"
	clusterNetCallShardOwnedFault = "wkClusterNetCallShardOwnedFault"
)

func TestSeedJoinRetriesThroughInjectedNodeLifecycleRPCFault(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-node-lifecycle-token"
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
		suite.WithNodeEnv(4,
			node4Fail.Env(),
			`GOFAIL_FAILPOINTS=wkClusterNetCallShardFault=return("node_lifecycle:temporary join rpc fault")`,
		),
	)
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer readyCancel()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	node4 := cluster.StartSeedJoinNodeNoWait(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     []string{cluster.NodeAddr(1)},
		JoinToken: joinToken,
	})

	waitGofailListed(t, cluster, node4Fail, clusterNetCallShardFault)
	defer disableGofail(t, node4Fail, clusterNetCallShardFault)
	requireNodeNotReadyFor(t, cluster, node4, 750*time.Millisecond)

	disableGofail(t, node4Fail, clusterNetCallShardFault)

	node4ReadyCtx, node4ReadyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer node4ReadyCancel()
	require.NoError(t, suite.WaitHTTPReady(node4ReadyCtx, node4.APIAddr(), "/readyz"), cluster.DumpDiagnostics())
	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
}

func TestOnboardingControlWriteRetriesThroughInjectedRPCFault(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-control-write-token"
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
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer readyCancel()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	activeReadyCtx, activeReadyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer activeReadyCancel()
	require.NoError(t, cluster.WaitClusterReady(activeReadyCtx), cluster.DumpDiagnostics())

	plan := manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, plan.Candidates, 1, "plan=%#v\n%s", plan, cluster.DumpDiagnostics())

	failpoints := []suite.GofailEndpoint{node1Fail, node2Fail, node3Fail}
	for _, endpoint := range failpoints {
		waitGofailListed(t, cluster, endpoint, clusterNetCallShardOwnedFault)
	}
	for _, endpoint := range failpoints {
		enableGofail(t, cluster, endpoint, clusterNetCallShardOwnedFault, `return("control_write:temporary control write fault")`)
		defer disableGofail(t, endpoint, clusterNetCallShardOwnedFault)
	}

	remoteManager := cluster.ManagerClient(t, controlWriteForwardingNodeID(t, cluster))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	faulted, status, body, err := remoteManager.StartOnboarding(ctx, 4, 1)
	cancel()
	requireBoundedFaultedOnboardingStart(t, cluster, faulted, status, body, err)

	for _, endpoint := range failpoints {
		disableGofail(t, endpoint, clusterNetCallShardOwnedFault)
	}

	start := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created, cluster.DumpDiagnostics())
	manager.EventuallyOnboardingSafe(t, 4, 90*time.Second)

	statusCtx, statusCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer statusCancel()
	onboardingStatus, err := manager.NodeOnboardingStatus(statusCtx, 4)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, 0, onboardingStatus.Summary.Failed, "status=%#v\n%s", onboardingStatus, cluster.DumpDiagnostics())
}

func waitGofailListed(t testing.TB, cluster *suite.StartedCluster, endpoint suite.GofailEndpoint, failpoint string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body, err := endpoint.WaitListed(ctx, failpoint)
	require.NoError(t, err, "gofail body:\n%s\n%s", body, cluster.DumpDiagnostics())
	require.Contains(t, body, failpoint, cluster.DumpDiagnostics())
}

func controlWriteForwardingNodeID(t testing.TB, cluster *suite.StartedCluster) uint64 {
	t.Helper()

	leaderID := eventuallyControllerLeaderID(t, cluster, 10*time.Second)
	for _, node := range cluster.Nodes {
		if node.Spec.ID != leaderID {
			return node.Spec.ID
		}
	}
	t.Fatalf("no non-leader manager node found for controller leader %d\n%s", leaderID, cluster.DumpDiagnostics())
	return 0
}

func eventuallyControllerLeaderID(t testing.TB, cluster *suite.StartedCluster, timeout time.Duration) uint64 {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var (
		lastResp nodeFaultManagerNodesResponse
		lastErr  error
	)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resp, err := fetchManagerNodes(ctx, cluster.MustNode(1).ManagerAddr())
		cancel()
		if err == nil {
			lastResp = resp
			if resp.ControllerLeaderID != 0 {
				return resp.ControllerLeaderID
			}
			lastErr = fmt.Errorf("controller_leader_id is zero")
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("manager did not report controller leader: last=%#v lastErr=%v\n%s", lastResp, lastErr, cluster.DumpDiagnostics())
	return 0
}

func fetchManagerNodes(ctx context.Context, managerAddr string) (nodeFaultManagerNodesResponse, error) {
	var out nodeFaultManagerNodesResponse
	_, err := suite.GetJSON(ctx, "http://"+managerAddr+"/manager/nodes", &out)
	return out, err
}

func enableGofail(t testing.TB, cluster *suite.StartedCluster, endpoint suite.GofailEndpoint, failpoint string, expression string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, endpoint.Enable(ctx, failpoint, expression), cluster.DumpDiagnostics())
}

func disableGofail(t testing.TB, endpoint suite.GofailEndpoint, failpoint string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = endpoint.Disable(ctx, failpoint)
}

func requireNodeNotReadyFor(t testing.TB, cluster *suite.StartedCluster, node *suite.StartedNode, window time.Duration) {
	t.Helper()

	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		err := suite.WaitHTTPReady(ctx, node.APIAddr(), "/readyz")
		cancel()
		require.Error(t, err, "node %d became ready while join RPC fault was enabled\n%s", node.Spec.ID, cluster.DumpDiagnostics())
		time.Sleep(50 * time.Millisecond)
	}
}

func requireBoundedFaultedOnboardingStart(
	t testing.TB,
	cluster *suite.StartedCluster,
	response suite.NodeOnboardingStartDTO,
	status int,
	body []byte,
	err error,
) {
	t.Helper()

	if err != nil {
		return
	}
	if status/100 != 2 {
		return
	}
	if response.Created == 0 {
		return
	}
	t.Fatalf("onboarding start created tasks while control_write RPC fault was enabled: status=%d body=%s response=%s\n%s",
		status,
		strings.TrimSpace(string(body)),
		formatOnboardingStart(response),
		cluster.DumpDiagnostics(),
	)
}

func formatOnboardingStart(response suite.NodeOnboardingStartDTO) string {
	return fmt.Sprintf("target=%d max_slot_moves=%d created=%d results=%#v skipped=%#v",
		response.TargetNodeID,
		response.MaxSlotMoves,
		response.Created,
		response.Results,
		response.Skipped,
	)
}

type nodeFaultManagerNodesResponse struct {
	ControllerLeaderID uint64 `json:"controller_leader_id"`
}
