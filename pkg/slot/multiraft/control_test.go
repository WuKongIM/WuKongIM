package multiraft

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/quorum"
	"go.etcd.io/raft/v3/raftpb"
	"go.etcd.io/raft/v3/tracker"
)

func TestEnsurePendingConfigCapacityPreservesTrackedFuture(t *testing.T) {
	fut := newFuture(nil)
	g := &slot{
		pendingConfigs: map[uint64]trackedFuture{
			9: {future: fut, term: 4},
		},
	}

	g.ensurePendingConfigCapacity(3)

	tracked, ok := g.pendingConfigs[9]
	if !ok || tracked.future != fut || tracked.term != 4 {
		t.Fatalf("tracked future was lost: %+v ok=%v", tracked, ok)
	}
}

func TestChangeConfigAppliesAddLearner(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 30)

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 2,
	})
	if err != nil {
		t.Fatalf("ChangeConfig() error = %v", err)
	}
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestTransferLeadershipRejectsUnknownSlot(t *testing.T) {
	rt := newStartedRuntime(t)
	err := rt.TransferLeadership(context.Background(), 999, 2)
	if !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("expected ErrSlotNotFound, got %v", err)
	}
}

func TestExpectLeaderTransferRejectsUnknownSlot(t *testing.T) {
	rt := newStartedRuntime(t)
	err := rt.ExpectLeaderTransfer(context.Background(), 999, 2)
	if !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("expected ErrSlotNotFound, got %v", err)
	}
}

func TestSelectLeaderTransferTransfereePrefersEligiblePreferred(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			SoftState: raft.SoftState{Lead: 1},
			HardState: raftpb.HardState{Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 10},
			2: {Match: 10},
			3: {Match: 10},
		},
	}

	if got := selectLeaderTransferTransferee(st, 2); got != 2 {
		t.Fatalf("selectLeaderTransferTransferee() = %d, want preferred 2", got)
	}
}

func TestSelectLeaderTransferTransfereeFallsBackWhenPreferredBehind(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			SoftState: raft.SoftState{Lead: 1},
			HardState: raftpb.HardState{Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}, 4: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 10},
			2: {Match: 9},
			3: {Match: 10},
			4: {Match: 10},
		},
	}

	if got := selectLeaderTransferTransferee(st, 2); got != 3 {
		t.Fatalf("selectLeaderTransferTransferee() = %d, want eligible voter 3", got)
	}
}

func TestSelectLeaderTransferTransfereePrefersCommittedPreferredWithActiveLeaderTail(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			SoftState: raft.SoftState{Lead: 1},
			HardState: raftpb.HardState{Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 12},
			2: {Match: 10},
			3: {Match: 12},
		},
	}

	if got := selectLeaderTransferTransferee(st, 2); got != 2 {
		t.Fatalf("selectLeaderTransferTransferee() = %d, want committed preferred voter 2", got)
	}
}

func TestSelectLeaderTransferTransfereeAllowsCommittedPreferredWithoutFullyCaughtUpPeer(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			SoftState: raft.SoftState{Lead: 1},
			HardState: raftpb.HardState{Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 12},
			2: {Match: 10},
			3: {Match: 9},
		},
	}

	if got := selectLeaderTransferTransferee(st, 2); got != 2 {
		t.Fatalf("selectLeaderTransferTransferee() = %d, want committed preferred voter 2", got)
	}
}

func TestSelectLeaderTransferTransfereeReturnsZeroWithoutEligibleVoter(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			SoftState: raft.SoftState{Lead: 1},
			HardState: raftpb.HardState{Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 10},
			2: {Match: 9},
			3: {Match: 8},
		},
	}

	if got := selectLeaderTransferTransferee(st, 2); got != 0 {
		t.Fatalf("selectLeaderTransferTransferee() = %d, want 0", got)
	}
}

func TestChangeConfigCorrelatesFutureByCommittedIndex(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 31)

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 2,
	})
	if err != nil {
		t.Fatalf("ChangeConfig() error = %v", err)
	}

	res, err := fut.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if res.Index == 0 {
		t.Fatalf("Wait().Index = 0")
	}

	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if res.Index != st.AppliedIndex {
		t.Fatalf("Wait().Index = %d, want applied index %d", res.Index, st.AppliedIndex)
	}
}

func TestChangeConfigRejectsWhileAnotherConfigChangeIsPending(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := SlotID(34)
	store := newBlockingMarkAppliedStorage()

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: &internalFakeStateMachine{},
		},
		Voters: []NodeID{1},
	})
	if err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	store.internalFakeStorage.mu.Lock()
	baselineApplied := store.internalFakeStorage.lastApplied
	store.internalFakeStorage.mu.Unlock()
	store.armAfter(baselineApplied + 1)

	first := mustChangeConfig(t, rt, slotID, 2)

	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("first MarkApplied() did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := first.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Wait() error = %v, want %v", err, context.DeadlineExceeded)
	}

	second, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 3,
	})
	if !errors.Is(err, ErrConfigChangePending) {
		t.Fatalf("expected ErrConfigChangePending, got future=%v err=%v", second, err)
	}

	store.unblock()

	if _, err := first.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait() after unblock error = %v", err)
	}
}

func TestChangeConfigAllowsNextConfigChangeAfterPreviousApplied(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 35)

	first := waitForFutureResult(t, mustChangeConfig(t, rt, slotID, 2))
	second := waitForFutureResult(t, mustChangeConfig(t, rt, slotID, 3))

	if second.Index <= first.Index {
		t.Fatalf("second.Index = %d, want > first.Index %d", second.Index, first.Index)
	}
}

func TestRemoteConfigChangeDoesNotResolveLocalFuture(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: 5 * time.Millisecond,
		Seed:     14,
	})
	slotID := SlotID(32)

	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	cluster.waitForBootstrapApplied(t, slotID, 3)

	oldLeader := cluster.waitForLeader(t, slotID)
	cluster.partitionNode(oldLeader)

	stale, err := cluster.runtime(oldLeader).ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 4,
	})
	if err != nil {
		t.Fatalf("ChangeConfig(stale) error = %v", err)
	}

	newLeader := cluster.waitForLeaderAmong(t, slotID, cluster.otherNodes(oldLeader))
	fresh, err := cluster.runtime(newLeader).ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 5,
	})
	if err != nil {
		t.Fatalf("ChangeConfig(fresh) error = %v", err)
	}

	freshRes, err := fresh.Wait(context.Background())
	if err != nil {
		t.Fatalf("fresh Wait() error = %v", err)
	}

	cluster.healNode(oldLeader)
	cluster.waitForNodeCommitIndex(t, oldLeader, slotID, freshRes.Index)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	res, err := stale.Wait(ctx)
	if err == nil {
		t.Fatalf("stale config future resolved unexpectedly: result=%+v err=%v", res, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrNotLeader) {
		t.Fatalf("stale config future error = %v", err)
	}
}

func TestChangeConfigWaitBlocksUntilReadyBatchFullyCompletes(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := SlotID(33)
	store := newBlockingMarkAppliedStorage()

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: &internalFakeStateMachine{},
		},
		Voters: []NodeID{1},
	})
	if err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})
	store.internalFakeStorage.mu.Lock()
	baselineApplied := store.internalFakeStorage.lastApplied
	store.internalFakeStorage.mu.Unlock()
	store.armAfter(baselineApplied + 1)

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 2,
	})
	if err != nil {
		t.Fatalf("ChangeConfig() error = %v", err)
	}

	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("MarkApplied() did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := fut.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() error = %v, want %v", err, context.DeadlineExceeded)
	}

	store.unblock()

	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() after unblock error = %v", err)
	}
}

func mustChangeConfig(t *testing.T, rt *Runtime, slotID SlotID, nodeID NodeID) Future {
	t.Helper()

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: nodeID,
	})
	if err != nil {
		t.Fatalf("ChangeConfig(node=%d) error = %v", nodeID, err)
	}
	return fut
}
