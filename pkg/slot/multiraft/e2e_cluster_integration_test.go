//go:build integration

package multiraft

import (
	"context"
	"testing"
	"time"
)

func TestThreeNodeClusterReplicatesProposalEndToEnd(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: 5 * time.Millisecond,
		Seed:     1,
	})
	slotID := SlotID(100)

	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	cluster.waitForBootstrapApplied(t, slotID, 3)

	leaderID := cluster.waitForLeader(t, slotID)
	fut, err := cluster.runtime(leaderID).Propose(context.Background(), slotID, proposalString("set a=1"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	res := waitForFutureResult(t, fut)
	if string(res.Data) != "ok:set a=1" {
		t.Fatalf("Wait().Data = %q", res.Data)
	}

	cluster.waitForAllApplied(t, slotID, []byte("set a=1"))
}

func TestThreeNodeClusterReplicatesMultipleProposalsInOrder(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: 5 * time.Millisecond,
		Seed:     2,
	})
	slotID := SlotID(101)

	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	cluster.waitForBootstrapApplied(t, slotID, 3)

	leaderID := cluster.waitForLeader(t, slotID)
	commands := [][]byte{
		[]byte("set a=1"),
		[]byte("set b=2"),
		[]byte("set c=3"),
	}

	for _, command := range commands {
		fut, err := cluster.runtime(leaderID).Propose(context.Background(), slotID, proposalPayload(0, command))
		if err != nil {
			t.Fatalf("Propose(%q) error = %v", command, err)
		}

		res := waitForFutureResult(t, fut)
		if string(res.Data) != "ok:"+string(command) {
			t.Fatalf("Wait(%q).Data = %q", command, res.Data)
		}
	}

	cluster.waitForAllAppliedSequence(t, slotID, commands)
}

func TestThreeNodeClusterTransfersLeadershipAndReplicatesAgain(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: 5 * time.Millisecond,
		Seed:     3,
	})
	slotID := SlotID(102)

	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	cluster.waitForBootstrapApplied(t, slotID, 3)

	leaderID := cluster.waitForLeader(t, slotID)
	warmup, err := cluster.runtime(leaderID).Propose(context.Background(), slotID, proposalString("warmup"))
	if err != nil {
		t.Fatalf("Propose(warmup) error = %v", err)
	}
	waitForFutureResult(t, warmup)
	cluster.waitForAllApplied(t, slotID, []byte("warmup"))

	targetLeader := cluster.pickFollower(leaderID)

	if err := cluster.runtime(leaderID).TransferLeadership(context.Background(), slotID, targetLeader); err != nil {
		t.Fatalf("TransferLeadership() error = %v", err)
	}

	cluster.waitForSpecificLeader(t, slotID, targetLeader)

	fut, err := cluster.runtime(targetLeader).Propose(context.Background(), slotID, proposalString("set c=3"))
	if err != nil {
		t.Fatalf("Propose(newLeader=%d) error = %v", targetLeader, err)
	}

	res := waitForFutureResult(t, fut)
	if string(res.Data) != "ok:set c=3" {
		t.Fatalf("Wait().Data = %q", res.Data)
	}

	cluster.waitForAllApplied(t, slotID, []byte("set c=3"))
}

func TestThreeNodeClusterObservesLeaderChangeAfterTransfer(t *testing.T) {
	observer := &slotLeaderChangeObserver{}
	cluster := newAsyncTestClusterWithObserver(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: 5 * time.Millisecond,
		Seed:     31,
	}, observer)
	slotID := SlotID(112)

	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	cluster.waitForBootstrapApplied(t, slotID, 3)

	leaderID := cluster.waitForLeader(t, slotID)
	observer.clear()
	targetLeader := cluster.pickFollower(leaderID)
	if err := cluster.runtime(leaderID).TransferLeadership(context.Background(), slotID, targetLeader); err != nil {
		t.Fatalf("TransferLeadership() error = %v", err)
	}
	cluster.waitForSpecificLeader(t, slotID, targetLeader)

	observer.waitForTarget(t, slotID, targetLeader)
	observer.waitForTargetCause(t, slotID, targetLeader, LeaderChangeCausePlannedTransfer)
}

func TestThreeNodeClusterElectionWindowSurvivesBoundedLeaderIsolation(t *testing.T) {
	const isolation = 1600 * time.Millisecond
	for _, tc := range []struct {
		name             string
		tickInterval     time.Duration
		electionTick     int
		heartbeatTick    int
		wantLeaderChange bool
	}{
		{name: "legacy-500ms-floor", tickInterval: 10 * time.Millisecond, electionTick: 50, heartbeatTick: 1, wantLeaderChange: true},
		{name: "resilient-2s-floor", tickInterval: 50 * time.Millisecond, electionTick: 40, heartbeatTick: 2, wantLeaderChange: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observer := &slotLeaderChangeObserver{}
			cluster := newAsyncTestClusterWithObserver(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
				MaxDelay:      5 * time.Millisecond,
				Seed:          91,
				TickInterval:  tc.tickInterval,
				ElectionTick:  tc.electionTick,
				HeartbeatTick: tc.heartbeatTick,
			}, observer)
			slotID := SlotID(113)
			cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
			cluster.waitForBootstrapApplied(t, slotID, 3)
			leaderID := cluster.waitForLeader(t, slotID)

			observer.clear()
			cluster.partitionNode(leaderID)
			time.Sleep(isolation)

			observer.mu.Lock()
			changes := append([]slotLeaderChangeObservation(nil), observer.changes...)
			observer.mu.Unlock()
			changed := false
			for _, change := range changes {
				if change.slotID == slotID && change.to != 0 && change.to != leaderID {
					changed = true
					break
				}
			}
			if changed != tc.wantLeaderChange {
				t.Fatalf("leader changes after %s isolation = %#v, changed=%t want=%t", isolation, changes, changed, tc.wantLeaderChange)
			}
		})
	}
}

func TestThreeNodeClusterIdleDoesNotRemarkApplied(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: 5 * time.Millisecond,
		Seed:     4,
	})
	slotID := SlotID(103)

	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	cluster.waitForBootstrapApplied(t, slotID, 3)
	cluster.waitForLeader(t, slotID)

	before := cluster.markAppliedCounts(slotID)
	time.Sleep(300 * time.Millisecond)
	after := cluster.markAppliedCounts(slotID)

	for nodeID, count := range after {
		if count != before[nodeID] {
			t.Fatalf("node %d MarkApplied() count = %d, want %d while idle", nodeID, count, before[nodeID])
		}
	}
}
