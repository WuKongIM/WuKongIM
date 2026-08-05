package multiraft

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	raft "go.etcd.io/raft/v3"
)

func TestRuntimeFreshStatusReadsLeaderProgressAfterBasicOnlyElectionTicks(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{Seed: 41})
	slotID := SlotID(201)
	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	leaderID := cluster.waitForLeader(t, slotID)

	cached, err := cluster.runtime(leaderID).Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(cached.Progress) != 0 {
		t.Fatalf("cached Status().Progress = %#v, want empty after basic-only election refresh", cached.Progress)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	fresh, err := cluster.runtime(leaderID).FreshStatus(ctx, slotID)
	if err != nil {
		t.Fatalf("FreshStatus() error = %v", err)
	}
	if fresh.Role != RoleLeader || fresh.LeaderID != leaderID {
		t.Fatalf("FreshStatus() role/leader = %v/%d, want leader/%d", fresh.Role, fresh.LeaderID, leaderID)
	}
	if len(fresh.Progress) != 3 {
		t.Fatalf("FreshStatus().Progress = %#v, want 3 real voter rows", fresh.Progress)
	}
	for _, nodeID := range []NodeID{1, 2, 3} {
		if _, ok := fresh.Progress[nodeID]; !ok {
			t.Fatalf("FreshStatus().Progress missing node %d: %#v", nodeID, fresh.Progress)
		}
	}
}

func TestRuntimeFreshStatusUpdatesFollowerMatchAfterReplication(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{Seed: 42})
	slotID := SlotID(202)
	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	leaderID := cluster.waitForLeader(t, slotID)
	laggingID := cluster.pickFollower(leaderID)
	cluster.partitionNode(laggingID)

	future, err := cluster.runtime(leaderID).Propose(context.Background(), slotID, proposalString("fresh-progress"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	result := waitForFutureResult(t, future)
	lagging, err := cluster.runtime(leaderID).FreshStatus(context.Background(), slotID)
	if err != nil {
		t.Fatalf("FreshStatus(lagging) error = %v", err)
	}
	if lagging.CommitIndex < result.Index || lagging.Progress[laggingID].Match >= result.Index {
		t.Fatalf("lagging status commit/progress = %d/%#v, want commit >= %d and follower behind", lagging.CommitIndex, lagging.Progress[laggingID], result.Index)
	}

	cluster.healNode(laggingID)
	cluster.waitForAllNodesAppliedIndex(t, slotID, result.Index)
	caughtUp, err := cluster.runtime(leaderID).FreshStatus(context.Background(), slotID)
	if err != nil {
		t.Fatalf("FreshStatus(caught-up) error = %v", err)
	}
	if caughtUp.Progress[laggingID].Match < result.Index {
		t.Fatalf("caught-up progress = %#v, want match >= %d", caughtUp.Progress[laggingID], result.Index)
	}
}

func TestRuntimeFreshStatusClearsLeaderProgressAfterRoleTransition(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{Seed: 43})
	slotID := SlotID(203)
	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	oldLeaderID := cluster.waitForLeader(t, slotID)
	newLeaderID := cluster.pickFollower(oldLeaderID)

	before, err := cluster.runtime(oldLeaderID).FreshStatus(context.Background(), slotID)
	if err != nil || len(before.Progress) != 3 {
		t.Fatalf("FreshStatus(old leader) = %#v, %v, want 3 progress rows", before, err)
	}
	if err := cluster.runtime(oldLeaderID).TransferLeadership(context.Background(), slotID, newLeaderID); err != nil {
		t.Fatalf("TransferLeadership() error = %v", err)
	}
	cluster.waitForSpecificLeader(t, slotID, newLeaderID)

	oldLeader, err := cluster.runtime(oldLeaderID).FreshStatus(context.Background(), slotID)
	if err != nil {
		t.Fatalf("FreshStatus(old follower) error = %v", err)
	}
	if oldLeader.Role != RoleFollower || len(oldLeader.Progress) != 0 {
		t.Fatalf("old leader fresh role/progress = %v/%#v, want follower with no stale leader progress", oldLeader.Role, oldLeader.Progress)
	}
	newLeader, err := cluster.runtime(newLeaderID).FreshStatus(context.Background(), slotID)
	if err != nil {
		t.Fatalf("FreshStatus(new leader) error = %v", err)
	}
	if newLeader.Role != RoleLeader || len(newLeader.Progress) != 3 {
		t.Fatalf("new leader fresh role/progress = %v/%#v, want leader with 3 progress rows", newLeader.Role, newLeader.Progress)
	}
}

func TestRuntimeFreshStatusCancellationDoesNotWaitForOwnerLoop(t *testing.T) {
	slotID := SlotID(204)
	g, err := newSlot(context.Background(), 1, nil, RaftOptions{ElectionTick: 10, HeartbeatTick: 1}, newInternalSlotOptions(slotID), nil, nil)
	if err != nil {
		t.Fatalf("newSlot() error = %v", err)
	}
	if err := g.rawNode.Bootstrap([]raft.Peer{{ID: 1}}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	rt := &Runtime{slots: map[SlotID]*slot{slotID: g}, scheduler: newScheduler(nil)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, statusErr := rt.FreshStatus(ctx, slotID)
		done <- statusErr
	}()
	for {
		g.mu.Lock()
		queued := len(g.controls) == 1
		g.mu.Unlock()
		if queued {
			break
		}
		runtime.Gosched()
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("FreshStatus() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("FreshStatus() did not return after in-flight context cancellation")
	}
}
