//go:build integration

package cluster

import (
	"context"
	"fmt"
	"testing"
	"time"

	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestThreeNodeSlotElectsAfterControllerAndSlotLeaderStops(t *testing.T) {
	record := recordNodeRestartEvidence(t)
	nodes := newDefaultThreeNodeCluster(t)
	for _, node := range nodes {
		node.cfg.Slots.HashSlotCount = 256
	}
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
	before, err := nodes[controllerLeaderID-1].defaultSlotRuntime.Status(1)
	if err != nil {
		t.Fatal(err)
	}
	record("before-leader-stop", nodes...)
	stoppedNodeID = controllerLeaderID
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
	if err := nodes[controllerLeaderID-1].Stop(stopCtx); err != nil {
		cancelStop()
		t.Fatalf("Stop(node=%d) error = %v", controllerLeaderID, err)
	}
	cancelStop()

	// Raft randomizes election waiting to [ElectionTick, 2*ElectionTick).
	// Include that window and one bounded second for voting, publication and apply.
	started := time.Now()
	recoveryBudget := 2*nodes[0].cfg.Slots.TickInterval*time.Duration(nodes[0].cfg.Slots.ElectionTick) + time.Second
	deadline := started.Add(recoveryBudget)
	var survivors []*Node
	for _, node := range nodes {
		if node.NodeID() != stoppedNodeID {
			survivors = append(survivors, node)
		}
	}
	record("leader-stopped", survivors...)
	for time.Now().Before(deadline) {
		newControllerLeaderID := sharedControllerLeader(nodes, stoppedNodeID)
		newSlotLeaderID, slotsReady := sharedSlotLeader(nodes, stoppedNodeID, 1)
		if newControllerLeaderID != 0 && newSlotLeaderID != 0 && slotsReady {
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			defer cancel()
			if err := nodes[newSlotLeaderID-1].Propose(ctx, ProposeRequest{
				Command: metafsm.EncodeNoopCommand(),
				Target:  ProposeTarget{HashSlot: 0, HasHashSlot: true, SlotID: 1, HasSlotID: true},
			}); err != nil {
				t.Fatalf("post-failover proposal: %v", err)
			}
			// Async apply can resolve the proposal before cached commit status refreshes.
			// Read on the owning worker so the quorum target includes this proposal.
			result, err := nodes[newSlotLeaderID-1].defaultSlotRuntime.FreshStatus(ctx, 1)
			if err != nil || result.CommitIndex <= before.CommitIndex || result.Term <= before.Term {
				t.Fatalf("post-failover commit = %+v, err=%v, before=%+v", result, err, before)
			}
			for time.Now().Before(deadline) {
				applied := true
				for _, node := range survivors {
					status, err := node.defaultSlotRuntime.Status(1)
					if err != nil || uint64(status.LeaderID) != newSlotLeaderID || status.Term != result.Term ||
						status.CommitIndex < result.CommitIndex || status.AppliedIndex < result.CommitIndex {
						applied = false
					}
				}
				if applied {
					record("leaders-recovered-quorum-applied", survivors...)
					t.Logf("failover recovered: budget_ms=%d elapsed_ms=%d controller=%d slot=%d committed_index=%d term=%d", recoveryBudget.Milliseconds(), time.Since(started).Milliseconds(), newControllerLeaderID, newSlotLeaderID, result.CommitIndex, result.Term)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("post-failover quorum did not apply index %d: %s", result.CommitIndex, controllerSlotStatusSummary(nodes, stoppedNodeID, 1))
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Controller/Slot leaders did not recover within %s after node %d stopped: %s", recoveryBudget, stoppedNodeID, controllerSlotStatusSummary(nodes, stoppedNodeID, 1))
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
