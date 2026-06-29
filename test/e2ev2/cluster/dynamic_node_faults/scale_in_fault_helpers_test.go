//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

const (
	slotReplicaMoveTransferLeaderDelay = "wkSlotReplicaMoveTransferLeaderDelay"
	slotReplicaMoveRemoveVoterDelay    = "wkSlotReplicaMoveRemoveVoterDelay"
	scaleInRuntimeSummaryFaultMarker   = "temporary scale-in runtime summary fault"
	scaleInChannelInventoryFault       = "wkScaleInChannelDrainInventoryFault"
	markNodeRemovedPostCommitFault     = "wkMarkNodeRemovedPostCommitFault"
)

type faultableDynamicCluster struct {
	cluster   *suite.StartedCluster
	manager   *suite.ManagerClient
	nodeFails map[uint64]suite.GofailEndpoint
}

func startFaultableDynamicCluster(t testing.TB, joinToken string) faultableDynamicCluster {
	t.Helper()

	nodeFails := map[uint64]suite.GofailEndpoint{
		1: suite.ReserveGofailEndpoint(t),
		2: suite.ReserveGofailEndpoint(t),
		3: suite.ReserveGofailEndpoint(t),
		4: suite.ReserveGofailEndpoint(t),
	}
	s := suite.New(testingT(t))
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeEnv(1, nodeFails[1].Env()),
		suite.WithNodeEnv(2, nodeFails[2].Env()),
		suite.WithNodeEnv(3, nodeFails[3].Env()),
		suite.WithNodeEnv(4, nodeFails[4].Env()),
	)
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer readyCancel()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())
	return faultableDynamicCluster{
		cluster:   cluster,
		manager:   cluster.ManagerClient(t, 1),
		nodeFails: nodeFails,
	}
}

func (f faultableDynamicCluster) staticFailpoints() []suite.GofailEndpoint {
	return []suite.GofailEndpoint{f.nodeFails[1], f.nodeFails[2], f.nodeFails[3]}
}

func (f faultableDynamicCluster) allFailpoints() []suite.GofailEndpoint {
	return []suite.GofailEndpoint{f.nodeFails[1], f.nodeFails[2], f.nodeFails[3], f.nodeFails[4]}
}

func startActiveNode4(t testing.TB, f faultableDynamicCluster, joinToken string) *suite.StartedNode {
	t.Helper()

	node4 := f.cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     f.cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	f.manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	f.manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	f.manager.MustActivateNode(t, 4)
	f.manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	readyCtx, readyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer readyCancel()
	require.NoError(t, f.cluster.WaitClusterReady(readyCtx), f.cluster.DumpDiagnostics())
	return node4
}

func onboardOneSlotToNode4(t testing.TB, f faultableDynamicCluster) suite.NodeOnboardingPlanDTO {
	t.Helper()

	plan := f.manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, plan.Candidates, 1, f.cluster.DumpDiagnostics())
	candidate := plan.Candidates[0]
	ensureSlotLeaderForReplicaMoveSourceTB(t, f.cluster, candidate.SlotID, candidate.SourceNodeID, 45*time.Second)

	start := f.manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created, f.cluster.DumpDiagnostics())
	f.manager.EventuallyOnboardingSafe(t, 4, 90*time.Second)
	return plan
}

func startScaleInDrainWithOneSlotMove(t testing.TB, f faultableDynamicCluster) suite.NodeScaleInPlanDTO {
	t.Helper()

	start := f.manager.MustStartScaleIn(t, 4)
	require.Equal(t, uint64(4), start.NodeID)
	require.Equal(t, "leaving", start.JoinState)
	f.manager.EventuallyNodeJoinState(t, 4, "leaving", 20*time.Second)

	drain := f.manager.MustSetScaleInDrain(t, 4, true)
	require.True(t, drain.Draining)
	require.False(t, drain.AcceptingNewSessions)

	plan := eventuallyFaultableScaleInPlan(t, f, 4, 1, 30*time.Second)
	require.Len(t, plan.Candidates, 1, f.cluster.DumpDiagnostics())
	candidate := plan.Candidates[0]
	require.Equal(t, uint64(4), candidate.SourceNodeID, "scale-in candidate must move away from node 4: %#v\n%s", candidate, f.cluster.DumpDiagnostics())
	ensureSlotLeaderForReplicaMoveSourceTB(t, f.cluster, candidate.SlotID, candidate.SourceNodeID, 45*time.Second)
	return plan
}

func eventuallyFaultableScaleInPlan(t testing.TB, f faultableDynamicCluster, nodeID uint64, maxSlotMoves uint32, timeout time.Duration) suite.NodeScaleInPlanDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastPlan   suite.NodeScaleInPlanDTO
		lastErr    error
	)
	for {
		status, err := f.manager.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			lastStatus = status
			if status.JoinState == "leaving" && status.GatewayDraining && status.BlockedBySlots {
				plan := f.manager.MustPlanScaleIn(t, nodeID, maxSlotMoves)
				lastPlan = plan
				if len(plan.Candidates) > 0 {
					return plan
				}
				lastErr = fmt.Errorf("plan has no candidates: %#v", plan)
			} else {
				lastErr = fmt.Errorf("status not ready for scale-in plan: %#v", status)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in plan did not become ready: status=%#v plan=%#v lastErr=%v\n%s", nodeID, lastStatus, lastPlan, lastErr, f.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func waitScaleInTaskActive(t testing.TB, f faultableDynamicCluster, nodeID uint64, timeout time.Duration) suite.NodeScaleInStatusDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastStatus suite.NodeScaleInStatusDTO
		lastErr    error
	)
	for {
		status, err := f.manager.NodeScaleInStatus(ctx, nodeID)
		if err == nil {
			lastStatus = status
			if status.ActiveTaskCount > 0 || status.BlockedByTasks {
				return status
			}
			lastErr = fmt.Errorf("scale-in task inactive: active=%d blocked_by_tasks=%t failed=%d status=%#v",
				status.ActiveTaskCount,
				status.BlockedByTasks,
				status.FailedTaskCount,
				status,
			)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("node %d scale-in task did not become active: last=%#v lastErr=%v\n%s", nodeID, lastStatus, lastErr, f.cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireScaleInStatus(t testing.TB, f faultableDynamicCluster, nodeID uint64, check func(suite.NodeScaleInStatusDTO) error) suite.NodeScaleInStatusDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := f.manager.NodeScaleInStatus(ctx, nodeID)
	require.NoError(t, err, f.cluster.DumpDiagnostics())
	if err := check(status); err != nil {
		t.Fatalf("scale-in status assertion failed: %v status=%#v\n%s", err, status, f.cluster.DumpDiagnostics())
	}
	return status
}

func requireScaleInRemoveConflict(t testing.TB, status int, body []byte, err error, diagnostics string) {
	t.Helper()

	text := strings.TrimSpace(string(body))
	require.NoError(t, err, "status=%d body=%s\n%s", status, text, diagnostics)
	require.Equal(t, http.StatusConflict, status, "body=%s\n%s", text, diagnostics)
	require.True(t,
		strings.Contains(text, `"conflict"`) || strings.Contains(text, `"error"`),
		"expected bounded conflict body, status=%d body=%s\n%s",
		status,
		text,
		diagnostics,
	)
}

func ensureSlotLeaderForReplicaMoveSourceTB(t testing.TB, cluster *suite.StartedCluster, slotID uint32, sourceNodeID uint64, timeout time.Duration) {
	t.Helper()

	ensureSlotLeaderForReplicaMoveSource(testingT(t), cluster, slotID, sourceNodeID, timeout)
}

func testingT(t testing.TB) *testing.T {
	t.Helper()

	tt, ok := t.(*testing.T)
	require.True(t, ok, "helper requires *testing.T")
	return tt
}
