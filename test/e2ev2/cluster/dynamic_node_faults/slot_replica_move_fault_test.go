//go:build e2e

package dynamic_node_faults

import (
	"context"
	"fmt"
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

	plan := manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, plan.Candidates, 1, cluster.DumpDiagnostics())
	candidate := plan.Candidates[0]
	ensureSlotLeaderForReplicaMoveSource(t, cluster, candidate.SlotID, candidate.SourceNodeID, 45*time.Second)

	plan = manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, plan.Candidates, 1, cluster.DumpDiagnostics())
	require.Equal(t, candidate.SlotID, plan.Candidates[0].SlotID, cluster.DumpDiagnostics())
	require.Equal(t, candidate.SourceNodeID, plan.Candidates[0].SourceNodeID, cluster.DumpDiagnostics())

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

func ensureSlotLeaderForReplicaMoveSource(t *testing.T, cluster *suite.StartedCluster, slotID uint32, sourceNodeID uint64, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	initial, err := fetchSlotRowForNode(ctx, cluster, slotID, sourceNodeID)
	if err == nil && initial.NodeLog != nil && initial.NodeLog.LeaderID == sourceNodeID {
		waitSlotLeaderOnSource(t, ctx, cluster, slotID, sourceNodeID, nil)
		return
	}
	transferResp := postSlotLeaderTransferToSource(t, ctx, cluster, slotID, sourceNodeID)
	waitSlotLeaderOnSource(t, ctx, cluster, slotID, sourceNodeID, &transferResp)
}

func postSlotLeaderTransferToSource(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, slotID uint32, sourceNodeID uint64) managerSlotLeaderTransferResponse {
	t.Helper()

	var out managerSlotLeaderTransferResponse
	_, err := suite.PostJSON(ctx, fmt.Sprintf("http://%s/manager/slots/%d/leader-transfer", cluster.MustNode(sourceNodeID).ManagerAddr(), slotID), map[string]any{
		"target_node": sourceNodeID,
	}, &out)
	require.NoError(t, err, "slot=%d source=%d response=%#v\n%s", slotID, sourceNodeID, out, cluster.DumpDiagnostics())
	return out
}

func waitSlotLeaderOnSource(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, slotID uint32, sourceNodeID uint64, transferResp *managerSlotLeaderTransferResponse) {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		lastRow managerSlotItem
		lastErr error
	)
	for {
		row, err := fetchSlotRowForNode(ctx, cluster, slotID, sourceNodeID)
		if err == nil {
			lastRow = row
			if checkErr := checkSlotLeaderOnSource(row, sourceNodeID); checkErr == nil {
				return
			} else {
				lastErr = checkErr
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("slot %d source %d did not become live leader with no active task: lastRow=%#v transferResp=%#v lastErr=%v\n%s", slotID, sourceNodeID, lastRow, transferResp, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func fetchSlotRowForNode(ctx context.Context, cluster *suite.StartedCluster, slotID uint32, nodeID uint64) (managerSlotItem, error) {
	var out managerSlotsResponse
	_, err := suite.GetJSON(ctx, fmt.Sprintf("http://%s/manager/slots?node_id=%d", cluster.MustNode(nodeID).ManagerAddr(), nodeID), &out)
	if err != nil {
		return managerSlotItem{}, err
	}
	for _, item := range out.Items {
		if item.SlotID == slotID {
			return item, nil
		}
	}
	return managerSlotItem{}, fmt.Errorf("slot %d missing from manager slots response total=%d items=%#v", slotID, out.Total, out.Items)
}

func checkSlotLeaderOnSource(item managerSlotItem, sourceNodeID uint64) error {
	if item.Task != nil {
		return fmt.Errorf("slot %d has active task=%#v", item.SlotID, item.Task)
	}
	if item.NodeLog == nil {
		return fmt.Errorf("slot %d node log status missing", item.SlotID)
	}
	if item.NodeLog.LeaderID != sourceNodeID {
		return fmt.Errorf("slot %d leader=%d, want source=%d", item.SlotID, item.NodeLog.LeaderID, sourceNodeID)
	}
	return nil
}

type managerSlotsResponse struct {
	Total int               `json:"total"`
	Items []managerSlotItem `json:"items"`
}

type managerSlotItem struct {
	SlotID     uint32                 `json:"slot_id"`
	Assignment managerSlotAssignment  `json:"assignment"`
	Task       *managerSlotTask       `json:"task,omitempty"`
	NodeLog    *managerSlotNodeStatus `json:"node_log,omitempty"`
}

type managerSlotAssignment struct {
	DesiredPeers []uint64 `json:"desired_peers"`
}

type managerSlotTask struct {
	TaskID     string `json:"task_id"`
	Kind       string `json:"kind"`
	Step       string `json:"step"`
	Status     string `json:"status"`
	SourceNode uint64 `json:"source_node,omitempty"`
	TargetNode uint64 `json:"target_node,omitempty"`
}

type managerSlotNodeStatus struct {
	NodeID       uint64 `json:"node_id"`
	LeaderID     uint64 `json:"leader_id"`
	Role         string `json:"role"`
	CommitIndex  uint64 `json:"commit_index"`
	AppliedIndex uint64 `json:"applied_index"`
}

type managerSlotLeaderTransferResponse struct {
	SlotID          uint32           `json:"slot_id"`
	TargetNode      uint64           `json:"target_node"`
	PreferredLeader uint64           `json:"preferred_leader"`
	ActualLeader    uint64           `json:"actual_leader"`
	Created         bool             `json:"created"`
	Task            *managerSlotTask `json:"task,omitempty"`
	Message         string           `json:"message"`
}
