//go:build e2e

package bootstrap_task

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeBootstrapTasksCompleteAndClearFromManagerSlots(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(suite.WithManagerHTTP())

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	node := cluster.MustNode(1)
	slotsCtx, cancelSlots := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelSlots()
	slots := requireBootstrapTasksCleared(t, slotsCtx, cluster, node)

	require.Equal(t, 3, slots.Total)
	require.Len(t, slots.Items, 3)
	for _, item := range slots.Items {
		require.Nil(t, item.Task, "slot %d still has active task: %#v", item.SlotID, item.Task)
		require.ElementsMatch(t, []uint64{1, 2, 3}, item.Assignment.DesiredPeers, "slot %d desired peers", item.SlotID)
		require.NotNil(t, item.NodeLog, "slot %d missing node-local raft status", item.SlotID)
		require.NotZero(t, item.NodeLog.LeaderID, "slot %d missing slot raft leader", item.SlotID)
		require.NotEqual(t, "unknown", item.NodeLog.Role, "slot %d unknown local slot raft role", item.SlotID)
		require.NotZero(t, item.NodeLog.CommitIndex, "slot %d missing committed bootstrap log", item.SlotID)
		require.NotZero(t, item.NodeLog.AppliedIndex, "slot %d missing applied bootstrap log", item.SlotID)
	}
}

func requireBootstrapTasksCleared(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, node *suite.StartedNode) managerSlotsResponse {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last managerSlotsResponse
	var lastErr error
	for {
		var out managerSlotsResponse
		_, err := suite.GetJSON(ctx, fmt.Sprintf("http://%s/manager/slots?node_id=%d", node.ManagerAddr(), node.Spec.ID), &out)
		if err == nil {
			if checkErr := checkBootstrapSlots(out); checkErr == nil {
				return out
			} else {
				last = out
				lastErr = checkErr
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("bootstrap slot tasks did not clear: last=%#v lastErr=%v\n%s", last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func checkBootstrapSlots(resp managerSlotsResponse) error {
	if resp.Total != 3 || len(resp.Items) != 3 {
		return fmt.Errorf("manager slots total=%d items=%d, want 3", resp.Total, len(resp.Items))
	}
	for _, item := range resp.Items {
		if item.Task != nil {
			return fmt.Errorf("slot %d active task=%#v", item.SlotID, item.Task)
		}
		if !sameUint64Set(item.Assignment.DesiredPeers, []uint64{1, 2, 3}) {
			return fmt.Errorf("slot %d desired peers=%v, want [1 2 3]", item.SlotID, item.Assignment.DesiredPeers)
		}
		if item.NodeLog == nil {
			return fmt.Errorf("slot %d node log status missing", item.SlotID)
		}
		if item.NodeLog.LeaderID == 0 || item.NodeLog.Role == "" || item.NodeLog.Role == "unknown" {
			return fmt.Errorf("slot %d node log not ready: %#v", item.SlotID, item.NodeLog)
		}
		if item.NodeLog.CommitIndex == 0 || item.NodeLog.AppliedIndex == 0 {
			return fmt.Errorf("slot %d log watermarks not ready: %#v", item.SlotID, item.NodeLog)
		}
	}
	return nil
}

func sameUint64Set(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]uint64(nil), a...)
	right := append([]uint64(nil), b...)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
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
	TaskID string `json:"task_id"`
	Kind   string `json:"kind"`
	Step   string `json:"step"`
	Status string `json:"status"`
}

type managerSlotNodeStatus struct {
	LeaderID     uint64 `json:"leader_id"`
	Role         string `json:"role"`
	CommitIndex  uint64 `json:"commit_index"`
	AppliedIndex uint64 `json:"applied_index"`
}
