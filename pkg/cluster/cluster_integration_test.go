//go:build integration
// +build integration

package cluster_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestTestClusterTimingConfigUsesFastTiming(t *testing.T) {
	cfg := testClusterTimingConfig()

	require.Equal(t, 25*time.Millisecond, cfg.TickInterval)
	require.Equal(t, 6, cfg.ElectionTick)
	require.Equal(t, 1, cfg.HeartbeatTick)
	require.Equal(t, 750*time.Millisecond, cfg.DialTimeout)
	require.Equal(t, 750*time.Millisecond, cfg.ForwardTimeout)
	require.Equal(t, 1, cfg.PoolSize)
}

func TestStableLeaderWithinUsesShortConfirmationWindow(t *testing.T) {
	nodes := startThreeNodes(t, 1)

	start := time.Now()
	waitForStableLeader(t, nodes, 1)

	require.Less(t, time.Since(start), time.Second)
}

func TestWaitForManagedSlotsSettledReturnsQuicklyAfterAssignmentsExist(t *testing.T) {
	nodes := startThreeNodesWithControllerWithSettle(t, 4, 3, false)
	waitForControllerAssignments(t, nodes, 4)

	start := time.Now()
	waitForManagedSlotsSettled(t, nodes, 4)

	require.Less(t, time.Since(start), 3*time.Second)
}

func TestStopNodesReturnsWithoutFixedThreeSecondDelay(t *testing.T) {
	nodes := startThreeNodes(t, 1)

	start := time.Now()
	stopNodes(nodes)

	require.Less(t, time.Since(start), time.Second)
}

func TestTestNodeRestartReopensClusterWithSameListenAddr(t *testing.T) {
	testNodes := startThreeNodes(t, 1)
	defer func() {
		for _, n := range testNodes {
			if n != nil {
				n.stop()
			}
		}
	}()

	old := testNodes[0]
	oldAddr := old.listenAddr
	oldDir := old.dir

	restarted := restartNode(t, testNodes, 0)

	if restarted.listenAddr != oldAddr {
		t.Fatalf("listenAddr = %q, want %q", restarted.listenAddr, oldAddr)
	}
	if restarted.dir != oldDir {
		t.Fatalf("dir = %q, want %q", restarted.dir, oldDir)
	}

	waitForStableLeader(t, testNodes, 1)
}

func TestThreeNodeClusterReelectsAfterLeaderRestart(t *testing.T) {
	testNodes := startThreeNodes(t, 1)
	defer func() {
		for _, n := range testNodes {
			if n != nil {
				n.stop()
			}
		}
	}()

	originalLeader := waitForStableLeader(t, testNodes, 1)
	leaderIdx := int(originalLeader - 1)

	testNodes[leaderIdx].stop()
	newLeader := waitForStableLeader(t, testNodes, 1)
	if newLeader == originalLeader {
		t.Fatalf("leader did not change after restarting node %d", originalLeader)
	}

	var follower *testNode
	for _, node := range testNodes {
		if node == nil || node.store == nil || node.cluster == nil || node.nodeID == newLeader {
			continue
		}
		follower = node
		break
	}
	if follower == nil {
		t.Fatal("no follower available after leader restart")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	duringFailoverID := fmt.Sprintf("reelect-during-failover-%d", time.Now().UnixNano())
	if err := follower.store.CreateChannel(ctx, duringFailoverID, 1); err != nil {
		t.Fatalf("CreateChannel during failover: %v", err)
	}
	waitForChannelVisibleOnNodes(t, testNodes, duringFailoverID, 1)

	restarted := restartNode(t, testNodes, leaderIdx)
	stableLeader := waitForStableLeader(t, testNodes, 1)
	if stableLeader == 0 {
		t.Fatal("missing stable leader after restarting old leader")
	}

	afterRestartID := fmt.Sprintf("reelect-after-restart-%d", time.Now().UnixNano())
	if err := restarted.store.CreateChannel(ctx, afterRestartID, 1); err != nil {
		t.Fatalf("CreateChannel via restarted node: %v", err)
	}
	waitForChannelVisibleOnNodes(t, testNodes, afterRestartID, 1)
}

func TestClusterReportsRuntimeViewsToControllerIncrementally(t *testing.T) {
	nodes := startThreeNodesWithControllerWithSettle(t, 4, 3, false)
	defer stopNodes(nodes)

	require.Eventually(t, func() bool {
		views, err := nodes[0].cluster.ListObservedRuntimeViews(context.Background())
		return err == nil && len(views) == 4
	}, 40*time.Second, 100*time.Millisecond)
}

func TestClusterGroupIDsNoLongerDependOnStaticSlotConfig(t *testing.T) {
	node := startSingleNodeWithController(t, 8, 1)
	defer node.stop()

	require.Equal(t, []multiraft.SlotID{1, 2, 3, 4, 5, 6, 7, 8}, node.cluster.SlotIDs())
}

func TestClusterBootstrapsManagedSlotsFromControllerAssignments(t *testing.T) {
	nodes := startThreeNodesWithController(t, 4, 3)
	defer stopNodes(nodes)

	for slotID := 1; slotID <= 4; slotID++ {
		waitForStableLeader(t, nodes, uint64(slotID))
	}
}

func TestClusterContinuesServingWithOneReplicaNodeDown(t *testing.T) {
	nodes := startThreeNodesWithController(t, 1, 3)
	defer stopNodes(nodes)

	leaderIdx := int(waitForStableLeader(t, nodes, 1) - 1)
	nodes[leaderIdx].stop()

	require.Eventually(t, func() bool {
		_, err := nodes[(leaderIdx+1)%3].cluster.LeaderOf(1)
		return err == nil
	}, 10*time.Second, 100*time.Millisecond)
}

func TestClusterListSlotAssignmentsReflectsControllerState(t *testing.T) {
	nodes := startFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	assignments, err := nodes[0].cluster.ListSlotAssignments(context.Background())
	require.NoError(t, err)
	require.Len(t, assignments, 2)
	require.Len(t, assignments[0].DesiredPeers, 3)
}

func TestClusterTransferSlotLeaderDelegatesToManagedSlot(t *testing.T) {
	nodes := startThreeNodesWithController(t, 1, 3)
	defer stopNodes(nodes)

	currentLeader := waitForStableLeader(t, nodes, 1)
	target := multiraft.NodeID(1)
	if currentLeader == target {
		target = 2
	}

	require.NoError(t, nodes[0].cluster.TransferSlotLeader(context.Background(), 1, target))
	require.Eventually(t, func() bool {
		leader, err := nodes[0].cluster.LeaderOf(1)
		return err == nil && leader == target
	}, 10*time.Second, 100*time.Millisecond)
}

func TestClusterMarkNodeDrainingMovesAssignmentsAway(t *testing.T) {
	nodes := startFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	require.NoError(t, nodes[0].cluster.MarkNodeDraining(context.Background(), 1))
	require.Eventually(t, func() bool {
		assignments, err := nodes[0].cluster.ListSlotAssignments(context.Background())
		if err != nil {
			return false
		}
		for _, assignment := range assignments {
			for _, peer := range assignment.DesiredPeers {
				if peer == 1 {
					return false
				}
			}
		}
		return true
	}, 20*time.Second, 200*time.Millisecond)
}

func TestClusterSurfacesFailedRepairAfterRetryExhaustion(t *testing.T) {
	nodes := startFourNodesWithPermanentRepairFailure(t, 1, 3)
	defer stopNodes(nodes)

	var last string
	require.Eventually(t, func() bool {
		controller, ok := currentControllerLeaderNode(nodes)
		if !ok {
			last = "controller unavailable"
			return false
		}
		task, err := controller.cluster.GetReconcileTask(context.Background(), 1)
		if err != nil {
			last = fmt.Sprintf("controller=%d err=%v", controller.nodeID, err)
			return false
		}
		last = fmt.Sprintf(
			"controller=%d status=%v attempt=%d last_error=%q",
			controller.nodeID,
			task.Status,
			task.Attempt,
			task.LastError,
		)
		return task.Status == raftcluster.TaskStatusFailed &&
			task.Attempt == 3 &&
			task.LastError == "injected repair failure"
	}, 180*time.Second, 200*time.Millisecond, "last observed task: %s", last)
}

func TestClusterRecoverSlotReturnsManualRecoveryErrorWhenQuorumLost(t *testing.T) {
	nodes := startThreeNodesWithController(t, 1, 3)
	defer stopNodes(nodes)

	waitForStableLeader(t, nodes, 1)
	nodes[1].stop()
	nodes[2].stop()

	err := nodes[0].cluster.RecoverSlot(context.Background(), 1, raftcluster.RecoverStrategyLatestLiveReplica)
	require.ErrorIs(t, err, raftcluster.ErrManualRecoveryRequired)
}

func TestClusterRebalancesAfterNewWorkerNodeJoins(t *testing.T) {
	nodes := startThreeOfFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	assignments := snapshotAssignments(t, nodes[:3], 2)
	require.False(t, assignmentsContainPeer(assignments, 4))

	nodes[3] = restartNode(t, nodes, 3)

	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, 2)
		return ok && assignmentsContainPeer(assignments, 4)
	}, 20*time.Second, 200*time.Millisecond)
}

func TestClusterRebalancesAfterRecoveredNodeReturns(t *testing.T) {
	nodes := startFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, 2)
		return ok && assignmentsContainPeer(assignments, 4)
	}, 20*time.Second, 200*time.Millisecond)

	requireControllerCommand(t, nodes, func(cluster *raftcluster.Cluster) error {
		return cluster.MarkNodeDraining(context.Background(), 4)
	})

	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, 2)
		return ok && !assignmentsContainPeer(assignments, 4)
	}, 20*time.Second, 200*time.Millisecond)

	requireControllerCommand(t, nodes, func(cluster *raftcluster.Cluster) error {
		return cluster.ResumeNode(context.Background(), 4)
	})

	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, 2)
		return ok && assignmentsContainPeer(assignments, 4)
	}, 20*time.Second, 200*time.Millisecond)
}

func TestClusterControllerLeaderFailoverResumesInFlightRepair(t *testing.T) {
	nodes := startFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	controllerLeader := waitForControllerLeader(t, nodes)
	assignments := snapshotAssignments(t, nodes, 2)
	slotID, sourceNode := slotForControllerLeader(assignments, controllerLeader)
	require.NotZero(t, slotID)
	require.NotZero(t, sourceNode)

	var failRepair atomic.Bool
	failRepair.Store(true)
	var repairExecCount atomic.Int32
	restore := setManagedSlotExecutionHookOnNodes(t, nodes, func(taskGroupID uint32, task controllermeta.ReconcileTask) error {
		if failRepair.Load() && taskGroupID == slotID && task.Kind == controllermeta.TaskKindRepair {
			repairExecCount.Add(1)
			return errors.New("injected repair failure")
		}
		return nil
	})
	defer restore()

	requireControllerCommand(t, nodes, func(cluster *raftcluster.Cluster) error {
		return cluster.MarkNodeDraining(context.Background(), sourceNode)
	})
	require.Eventually(t, func() bool {
		controller, ok := currentControllerLeaderNode(nodes)
		if !ok {
			return false
		}
		if err := controller.cluster.ForceReconcile(context.Background(), slotID); err != nil {
			return false
		}
		_, err := controller.cluster.GetReconcileTask(context.Background(), slotID)
		return err == nil
	}, 10*time.Second, 200*time.Millisecond)
	require.Eventually(t, func() bool {
		return repairExecCount.Load() >= 1
	}, 30*time.Second, 200*time.Millisecond)

	nodes[int(controllerLeader-1)].stop()
	failRepair.Store(false)

	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, 2)
		if !ok {
			return false
		}
		for _, assignment := range assignments {
			if assignment.SlotID == slotID {
				for _, peer := range assignment.DesiredPeers {
					if peer == sourceNode {
						return false
					}
				}
				return true
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond)
}
