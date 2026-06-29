//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

const (
	clusterNetCallShardFault      = "wkClusterNetCallShardFault"
	clusterNetCallShardOwnedFault = "wkClusterNetCallShardOwnedFault"
	controlWriteFaultMarker       = "temporary control write fault"
	nodeLifecycleFaultMarker      = "temporary join rpc fault"
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
	requireNodeArtifactsContain(t, cluster, node4, nodeLifecycleFaultMarker, 10*time.Second)

	requireDisableGofail(t, node4Fail, clusterNetCallShardFault)

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

	forwardingNodeID := controlWriteForwardingNodeID(t, cluster)
	remoteManager := cluster.ManagerClient(t, forwardingNodeID)
	plan := requireStableFaultableOnboardingPlan(t, cluster, forwardingNodeID, 4, 15*time.Second)
	require.Len(t, plan.Candidates, 1, "plan=%#v\n%s", plan, cluster.DumpDiagnostics())

	failpoints := []suite.GofailEndpoint{node1Fail, node2Fail, node3Fail}
	for _, endpoint := range failpoints {
		waitGofailListed(t, cluster, endpoint, clusterNetCallShardOwnedFault)
	}
	for _, endpoint := range failpoints {
		enableGofail(t, cluster, endpoint, clusterNetCallShardOwnedFault, `return("control_write:temporary control write fault")`)
		requireGofailContains(t, cluster, endpoint, clusterNetCallShardOwnedFault, controlWriteFaultMarker)
		defer disableGofail(t, endpoint, clusterNetCallShardOwnedFault)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	faulted, status, body, err := remoteManager.StartOnboarding(ctx, 4, 1)
	cancel()
	requireBoundedFaultedOnboardingStart(t, cluster, faulted, status, body, err)
	requireNoOnboardingTasksLeaked(t, cluster, remoteManager, 4, status)

	for _, endpoint := range failpoints {
		requireDisableGofail(t, endpoint, clusterNetCallShardOwnedFault)
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

func requireGofailContains(t testing.TB, cluster *suite.StartedCluster, endpoint suite.GofailEndpoint, failpoint string, marker string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	body, err := endpoint.List(ctx)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Contains(t, body, failpoint, "gofail body:\n%s\n%s", body, cluster.DumpDiagnostics())
	require.Contains(t, body, marker, "gofail body:\n%s\n%s", body, cluster.DumpDiagnostics())
}

func controlWriteForwardingNodeID(t testing.TB, cluster *suite.StartedCluster) uint64 {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var last []string
	for time.Now().Before(deadline) {
		last = last[:0]
		for _, node := range cluster.Nodes {
			if node.Spec.ID > 3 {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			resp, err := fetchManagerNodes(ctx, node.ManagerAddr())
			cancel()
			if err != nil {
				last = append(last, fmt.Sprintf("node %d manager read error: %v", node.Spec.ID, err))
				continue
			}
			if resp.ControllerLeaderID != 0 && resp.ControllerLeaderID != node.Spec.ID {
				t.Logf("using manager node %d to forward control_write to controller leader %d", node.Spec.ID, resp.ControllerLeaderID)
				return node.Spec.ID
			}
			last = append(last, fmt.Sprintf("node %d reports controller_leader_id=%d", node.Spec.ID, resp.ControllerLeaderID))
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no failpoint-enabled static manager node reported a remote controller leader: last=%s\n%s", strings.Join(last, "; "), cluster.DumpDiagnostics())
	return 0
}

func requireStableFaultableOnboardingPlan(t testing.TB, cluster *suite.StartedCluster, managerNodeID uint64, targetNodeID uint64, timeout time.Duration) suite.NodeOnboardingPlanDTO {
	t.Helper()

	manager := cluster.ManagerClient(t, managerNodeID)
	deadline := time.Now().Add(timeout)
	var (
		previous *suite.NodeOnboardingPlanDTO
		lastPlan suite.NodeOnboardingPlanDTO
		lastErr  error
	)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		nodes, err := fetchManagerNodes(ctx, cluster.MustNode(managerNodeID).ManagerAddr())
		cancel()
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if nodes.ControllerLeaderID == 0 || nodes.ControllerLeaderID == managerNodeID {
			lastErr = fmt.Errorf("manager node %d is not observing a remote controller leader: %#v", managerNodeID, nodes)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		status, err := manager.NodeOnboardingStatus(ctx, targetNodeID)
		cancel()
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if !onboardingStatusEmpty(status) {
			lastErr = fmt.Errorf("manager node %d reports active onboarding status=%#v", managerNodeID, status)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		leaderManager := cluster.ManagerClient(t, nodes.ControllerLeaderID)
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		leaderStatus, err := leaderManager.NodeOnboardingStatus(ctx, targetNodeID)
		cancel()
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if !onboardingStatusEmpty(leaderStatus) {
			lastErr = fmt.Errorf("controller leader node %d reports active onboarding status=%#v", nodes.ControllerLeaderID, leaderStatus)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		plan, err := fetchOnboardingPlan(ctx, cluster.MustNode(managerNodeID).ManagerAddr(), targetNodeID, 1)
		cancel()
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		lastPlan = plan
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		leaderPlan, err := fetchOnboardingPlan(ctx, cluster.MustNode(nodes.ControllerLeaderID).ManagerAddr(), targetNodeID, 1)
		cancel()
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if len(plan.Candidates) != 1 || plan.Candidates[0].TargetNodeID != targetNodeID {
			lastErr = fmt.Errorf("manager node %d plan is not faultable: %#v", managerNodeID, plan)
			previous = nil
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if !sameOnboardingPlan(plan, leaderPlan) {
			lastErr = fmt.Errorf("manager node %d plan is behind controller leader %d: plan=%#v leaderPlan=%#v", managerNodeID, nodes.ControllerLeaderID, plan, leaderPlan)
			previous = nil
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if previous != nil && sameOnboardingPlan(*previous, plan) {
			return plan
		}
		copied := plan
		previous = &copied
		lastErr = fmt.Errorf("manager node %d observed one candidate plan, waiting for repeat: %#v", managerNodeID, plan)
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("manager node %d did not expose a stable faultable onboarding plan for node %d within %s: lastPlan=%#v lastErr=%v\n%s",
		managerNodeID,
		targetNodeID,
		timeout,
		lastPlan,
		lastErr,
		cluster.DumpDiagnostics(),
	)
	return suite.NodeOnboardingPlanDTO{}
}

func fetchOnboardingPlan(ctx context.Context, managerAddr string, nodeID uint64, maxSlotMoves uint32) (suite.NodeOnboardingPlanDTO, error) {
	var out suite.NodeOnboardingPlanDTO
	_, err := suite.PostJSON(ctx, fmt.Sprintf("http://%s/manager/nodes/%d/onboarding/plan", managerAddr, nodeID), map[string]any{
		"max_slot_moves": maxSlotMoves,
	}, &out)
	return out, err
}

func onboardingStatusEmpty(status suite.NodeOnboardingStatusDTO) bool {
	return status.Summary.TotalActive == 0 &&
		status.Summary.Pending == 0 &&
		status.Summary.Running == 0 &&
		status.Summary.Failed == 0 &&
		len(status.Tasks) == 0
}

func sameOnboardingPlan(left, right suite.NodeOnboardingPlanDTO) bool {
	if left.StateRevision != right.StateRevision || len(left.Candidates) != 1 || len(right.Candidates) != 1 {
		return false
	}
	leftCandidate := left.Candidates[0]
	rightCandidate := right.Candidates[0]
	return leftCandidate.SlotID == rightCandidate.SlotID &&
		leftCandidate.SourceNodeID == rightCandidate.SourceNodeID &&
		leftCandidate.TargetNodeID == rightCandidate.TargetNodeID &&
		leftCandidate.ConfigEpoch == rightCandidate.ConfigEpoch &&
		equalUint64s(leftCandidate.TargetPeers, rightCandidate.TargetPeers)
}

func equalUint64s(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
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

func requireDisableGofail(t testing.TB, endpoint suite.GofailEndpoint, failpoint string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, endpoint.Disable(ctx, failpoint))
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

func requireNodeArtifactsContain(t testing.TB, cluster *suite.StartedCluster, node *suite.StartedNode, marker string, timeout time.Duration) {
	t.Helper()

	paths := nodeArtifactPaths(node)
	deadline := time.Now().Add(timeout)
	var last nodeArtifactSearchResult
	for time.Now().Before(deadline) {
		last = searchNodeArtifacts(paths, marker)
		if last.Found {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("node %d artifacts did not contain %q within %s; paths=%s; last=%s\n%s",
		node.Spec.ID,
		marker,
		timeout,
		strings.Join(paths, ", "),
		last.String(),
		cluster.DumpDiagnostics(),
	)
}

func nodeArtifactPaths(node *suite.StartedNode) []string {
	if node == nil {
		return nil
	}
	paths := []string{node.Spec.StdoutPath, node.Spec.StderrPath}
	if node.Spec.LogDir != "" {
		paths = append(paths,
			filepath.Join(node.Spec.LogDir, "app.log"),
			filepath.Join(node.Spec.LogDir, "error.log"),
		)
	}
	return paths
}

func searchNodeArtifacts(paths []string, marker string) nodeArtifactSearchResult {
	result := nodeArtifactSearchResult{Checked: make(map[string]string, len(paths))}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			result.Checked[path] = err.Error()
			continue
		}
		text := string(data)
		if strings.Contains(text, marker) {
			result.Found = true
			result.Checked[path] = "found"
			return result
		}
		result.Checked[path] = fmt.Sprintf("read %d bytes", len(data))
	}
	return result
}

type nodeArtifactSearchResult struct {
	Found   bool
	Checked map[string]string
}

func (r nodeArtifactSearchResult) String() string {
	if len(r.Checked) == 0 {
		return "<no artifacts checked>"
	}
	paths := make([]string, 0, len(r.Checked))
	for path := range r.Checked {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		state := r.Checked[path]
		parts = append(parts, fmt.Sprintf("%s: %s", path, state))
	}
	return strings.Join(parts, "; ")
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
		text := strings.TrimSpace(string(body))
		if strings.Contains(text, controlWriteFaultMarker) || strings.Contains(err.Error(), controlWriteFaultMarker) {
			t.Fatalf("faulted onboarding start surfaced injected marker through transport/decode error instead of HTTP status/body: status=%d body=%s err=%v\n%s",
				status,
				text,
				err,
				cluster.DumpDiagnostics(),
			)
		}
		require.NoError(t, err, "faulted onboarding start returned transport/decode error without injected marker or HTTP status/body: status=%d body=%s\n%s", status, text, cluster.DumpDiagnostics())
	}
	if status/100 != 2 {
		bodyText := strings.TrimSpace(string(body))
		require.Contains(t, bodyText, controlWriteFaultMarker, "faulted onboarding start failed without injected marker: status=%d body=%s\n%s", status, bodyText, cluster.DumpDiagnostics())
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

func requireNoOnboardingTasksLeaked(t testing.TB, cluster *suite.StartedCluster, manager *suite.ManagerClient, nodeID uint64, startStatus int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := manager.NodeOnboardingStatus(ctx, nodeID)
	require.NoError(t, err, "start_status=%d\n%s", startStatus, cluster.DumpDiagnostics())
	require.Equal(t, 0, status.Summary.TotalActive, "start_status=%d status=%#v\n%s", startStatus, status, cluster.DumpDiagnostics())
	require.Equal(t, 0, status.Summary.Pending, "start_status=%d status=%#v\n%s", startStatus, status, cluster.DumpDiagnostics())
	require.Equal(t, 0, status.Summary.Running, "start_status=%d status=%#v\n%s", startStatus, status, cluster.DumpDiagnostics())
	require.Equal(t, 0, status.Summary.Failed, "start_status=%d status=%#v\n%s", startStatus, status, cluster.DumpDiagnostics())
	require.Empty(t, status.Tasks, "start_status=%d status=%#v\n%s", startStatus, status, cluster.DumpDiagnostics())
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
