//go:build integration

package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestThreeNodeSlotElectsAfterControllerAndSlotLeaderStops(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	stoppedNodeID := uint64(0)
	startNodes(t, nodes...)
	t.Cleanup(func() {
		for i := len(nodes) - 1; i >= 0; i-- {
			if nodes[i].NodeID() == stoppedNodeID {
				continue
			}
			if err := nodes[i].Stop(context.Background()); err != nil {
				t.Errorf("Stop(node=%d) error = %v", nodes[i].NodeID(), err)
			}
		}
	})
	waitClusterReady(t, nodes...)

	controllerLeaderID := waitSharedControllerLeader(t, nodes, 3*time.Second)
	transferSlotLeaderAndWait(t, nodes, 1, controllerLeaderID)
	stoppedNodeID = controllerLeaderID
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	if err := nodes[controllerLeaderID-1].Stop(stopCtx); err != nil {
		cancelStop()
		t.Fatalf("Stop(node=%d) error = %v", controllerLeaderID, err)
	}
	cancelStop()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		newControllerLeaderID := sharedControllerLeader(nodes, stoppedNodeID)
		newSlotLeaderID, slotsReady := sharedSlotLeader(nodes, stoppedNodeID, 1)
		if newControllerLeaderID != 0 && newSlotLeaderID != 0 && slotsReady {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Controller/Slot leaders did not recover after node %d stopped: %s", stoppedNodeID, controllerSlotStatusSummary(nodes, stoppedNodeID, 1))
}

func waitSharedControllerLeader(t *testing.T, nodes []*Node, timeout time.Duration) uint64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if leaderID := sharedControllerLeader(nodes, 0); leaderID != 0 {
			return leaderID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Controller leader did not converge: %s", controllerSlotStatusSummary(nodes, 0, 1))
	return 0
}

func sharedControllerLeader(nodes []*Node, excludedNodeID uint64) uint64 {
	var leaderID uint64
	for _, node := range nodes {
		if node.NodeID() == excludedNodeID {
			continue
		}
		observed := node.control.LeaderID()
		if observed == 0 || observed == excludedNodeID {
			return 0
		}
		if leaderID == 0 {
			leaderID = observed
			continue
		}
		if observed != leaderID {
			return 0
		}
	}
	return leaderID
}

func sharedSlotLeader(nodes []*Node, excludedNodeID uint64, slotID multiraft.SlotID) (uint64, bool) {
	var leaderID uint64
	for _, node := range nodes {
		if node.NodeID() == excludedNodeID {
			continue
		}
		status, err := node.defaultSlotRuntime.Status(slotID)
		if err != nil || status.LeaderID == 0 || uint64(status.LeaderID) == excludedNodeID {
			return 0, false
		}
		if leaderID == 0 {
			leaderID = uint64(status.LeaderID)
			continue
		}
		if uint64(status.LeaderID) != leaderID {
			return 0, false
		}
	}
	return leaderID, leaderID != 0
}

func controllerSlotStatusSummary(nodes []*Node, excludedNodeID uint64, slotID multiraft.SlotID) string {
	var summary string
	for _, node := range nodes {
		if node.NodeID() == excludedNodeID {
			continue
		}
		status, err := node.defaultSlotRuntime.Status(slotID)
		summary += fmt.Sprintf("node=%d controller=%d slot=%+v err=%v; ", node.NodeID(), node.control.LeaderID(), status, err)
	}
	return summary
}
