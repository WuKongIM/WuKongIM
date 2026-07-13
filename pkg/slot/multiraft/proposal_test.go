package multiraft

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestCountTrackedReadyEntriesIgnoresEmptyNormalEntries(t *testing.T) {
	proposals, configs := countTrackedReadyEntries([]raftpb.Entry{
		{Type: raftpb.EntryNormal},
		{Type: raftpb.EntryNormal, Data: []byte("p")},
		{Type: raftpb.EntryConfChange, Data: []byte("c")},
	})

	if proposals != 1 || configs != 1 {
		t.Fatalf("counts = (%d, %d), want (1, 1)", proposals, configs)
	}
}

func TestReadyRequiresSynchronousApplyTreatsConfChangeV2AsBarrier(t *testing.T) {
	ready := raft.Ready{CommittedEntries: []raftpb.Entry{
		{Type: raftpb.EntryConfChangeV2},
	}}

	if !readyRequiresSynchronousApply(ready) {
		t.Fatal("readyRequiresSynchronousApply() = false, want true for EntryConfChangeV2")
	}
}

func TestApplyCommittedEntriesPassesCommandDataWithoutExtraCopy(t *testing.T) {
	sm := &commandDataPointerStateMachine{}
	payload := proposalString("owned-command-data")
	g := &slot{
		id:           7,
		stateMachine: sm,
		pendingProposals: map[uint64]trackedFuture{
			1: {term: 1, future: newFuture(nil)},
		},
	}
	lastApplied := uint64(0)

	resolutions, _ := g.applyCommittedEntries(context.Background(), []raftpb.Entry{{
		Index: 1,
		Term:  1,
		Type:  raftpb.EntryNormal,
		Data:  payload,
	}}, &lastApplied, nil, sm, true)

	if len(resolutions) != 1 {
		t.Fatalf("len(resolutions) = %d, want 1", len(resolutions))
	}
	if len(sm.data) == 0 {
		t.Fatal("state machine did not receive command data")
	}
	if &sm.data[0] != &payload[proposalEnvelopeSize] {
		t.Fatal("command data was copied instead of reusing the owned entry payload")
	}
}

func TestTrackReadyEntriesTracksConfChangeV2Future(t *testing.T) {
	fut := newFuture(nil)
	g := &slot{submittedConfigs: []*future{fut}}

	g.trackReadyEntries([]raftpb.Entry{
		{Type: raftpb.EntryConfChangeV2, Index: 9, Term: 3},
	})

	pending, ok := g.pendingConfigs[9]
	if !ok {
		t.Fatal("EntryConfChangeV2 future was not tracked")
	}
	if pending.future != fut || pending.term != 3 {
		t.Fatalf("tracked future = %+v, want future=%p term=3", pending, fut)
	}
	if len(g.submittedConfigs) != 0 {
		t.Fatalf("submitted configs = %d, want 0", len(g.submittedConfigs))
	}
}

func TestEnsurePendingProposalCapacityPreservesTrackedFuture(t *testing.T) {
	fut := newFuture(nil)
	g := &slot{
		pendingProposals: map[uint64]trackedFuture{
			7: {future: fut, term: 3},
		},
	}

	g.ensurePendingProposalCapacity(4)

	tracked, ok := g.pendingProposals[7]
	if !ok || tracked.future != fut || tracked.term != 3 {
		t.Fatalf("tracked future was lost: %+v ok=%v", tracked, ok)
	}
}

func TestProposeWaitReturnsAfterLocalApply(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 10)

	fut, err := rt.Propose(context.Background(), slotID, proposalString("set a=1"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	res, err := fut.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if string(res.Data) != "ok:set a=1" {
		t.Fatalf("Wait().Data = %q", res.Data)
	}
}

func TestProposeObservesWaitSubStages(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 1010)
	observer := &proposalStageObserver{}

	fut, err := rt.Propose(WithProposalStageObserver(context.Background(), observer), slotID, proposalString("set observed=1"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	requireProposalStage(t, observer.events, "meta_create_slot_control_wait", "ok")
	requireProposalStage(t, observer.events, "meta_create_slot_raft_commit_wait", "ok")
	requireProposalStage(t, observer.events, "meta_create_slot_fsm_apply", "ok")
	requireProposalStage(t, observer.events, "meta_create_slot_mark_applied", "ok")
}

func TestProposeReportsCompletedProposalToObserver(t *testing.T) {
	observer := &slotProposalObserver{}
	rt := newStartedRuntimeWithObserver(t, observer)
	slotID := openSingleNodeLeader(t, rt, 1011)

	fut, err := rt.Propose(context.Background(), slotID, proposalString("set observed-proposal=1"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.proposals) != 1 {
		t.Fatalf("proposal observations = %#v, want one", observer.proposals)
	}
	if got := observer.proposals[0].slotID; got != slotID {
		t.Fatalf("proposal slot = %d, want %d", got, slotID)
	}
	if observer.proposals[0].duration <= 0 {
		t.Fatalf("proposal duration = %v, want > 0", observer.proposals[0].duration)
	}
}

func TestReadyPipelinePersistsBeforeApply(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 11)

	_, err := rt.Propose(context.Background(), slotID, proposalString("cmd"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	waitForCondition(t, func() bool {
		store := fakeStorageFor(rt, slotID)
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.saveCount > 0 && store.lastApplied >= store.lastSavedIndex
	})
}

func TestProposeDeliversHashSlotEnvelopeToStateMachine(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 111)

	data := []byte("set hash-slot")
	payload := proposalPayload(23, data)

	fut, err := rt.Propose(context.Background(), slotID, payload)
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	fsm := fakeStateMachineFor(rt, slotID)
	if fsm == nil {
		t.Fatal("fakeStateMachineFor() = nil")
	}

	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	if len(fsm.commands) != 1 {
		t.Fatalf("len(commands) = %d, want 1", len(fsm.commands))
	}
	if got := fsm.commands[0].HashSlot; got != 23 {
		t.Fatalf("Command.HashSlot = %d, want 23", got)
	}
	if got := string(fsm.commands[0].Data); got != string(data) {
		t.Fatalf("Command.Data = %q, want %q", got, data)
	}
}

func TestProposeRejectsFollower(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: 5 * time.Millisecond,
		Seed:     11,
	})
	slotID := SlotID(12)

	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	cluster.waitForBootstrapApplied(t, slotID, 3)

	leaderID := cluster.waitForLeader(t, slotID)
	followerID := cluster.pickFollower(leaderID)

	fut, err := cluster.runtime(followerID).Propose(context.Background(), slotID, proposalString("set follower=1"))
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("expected ErrNotLeader, got future=%v err=%v", fut, err)
	}
}

func TestChangeConfigRejectsFollower(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: 5 * time.Millisecond,
		Seed:     12,
	})
	slotID := SlotID(13)

	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	cluster.waitForBootstrapApplied(t, slotID, 3)

	leaderID := cluster.waitForLeader(t, slotID)
	followerID := cluster.pickFollower(leaderID)

	fut, err := cluster.runtime(followerID).ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 4,
	})
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("expected ErrNotLeader, got future=%v err=%v", fut, err)
	}
}

func TestProposeRejectsStaleLeaderAfterHigherTermMessageQueued(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := openSingleNodeLeader(t, rt, 131)

	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	if err := g.enqueueRequest(raftpb.Message{
		Type: raftpb.MsgHeartbeat,
		From: 2,
		To:   1,
		Term: st.Term + 1,
	}); err != nil {
		t.Fatalf("enqueueRequest() error = %v", err)
	}

	fut, err := rt.Propose(context.Background(), slotID, proposalString("stale"))
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("expected ErrNotLeader, got future=%v err=%v", fut, err)
	}
}

func TestChangeConfigRejectsStaleLeaderAfterHigherTermMessageQueued(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := openSingleNodeLeader(t, rt, 132)

	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	if err := g.enqueueRequest(raftpb.Message{
		Type: raftpb.MsgHeartbeat,
		From: 2,
		To:   1,
		Term: st.Term + 1,
	}); err != nil {
		t.Fatalf("enqueueRequest() error = %v", err)
	}

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 4,
	})
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("expected ErrNotLeader, got future=%v err=%v", fut, err)
	}
}

func TestFatalSlotRejectsFutureOperations(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := SlotID(14)
	fatalErr := errors.New("fatal apply")
	fsm := &internalFakeStateMachine{applyErr: fatalErr}

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
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

	fut, err := rt.Propose(context.Background(), slotID, proposalString("boom"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := fut.Wait(ctx); !errors.Is(err, fatalErr) {
		t.Fatalf("Wait() error = %v, want %v", err, fatalErr)
	}

	if err := rt.Step(context.Background(), Envelope{
		SlotID:  slotID,
		Message: raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: 1},
	}); !errors.Is(err, fatalErr) {
		t.Fatalf("Step() error = %v, want %v", err, fatalErr)
	}

	if fut, err := rt.Propose(context.Background(), slotID, proposalString("again")); !errors.Is(err, fatalErr) {
		t.Fatalf("Propose() after fatal = future=%v err=%v, want %v", fut, err, fatalErr)
	}

	if fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 2,
	}); !errors.Is(err, fatalErr) {
		t.Fatalf("ChangeConfig() after fatal = future=%v err=%v, want %v", fut, err, fatalErr)
	}

	if err := rt.TransferLeadership(context.Background(), slotID, 2); !errors.Is(err, fatalErr) {
		t.Fatalf("TransferLeadership() error = %v, want %v", err, fatalErr)
	}

	if _, err := rt.Status(slotID); !errors.Is(err, fatalErr) {
		t.Fatalf("Status() error = %v, want %v", err, fatalErr)
	}
}

func TestProposeCorrelatesFutureByCommittedIndex(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 15)

	fut, err := rt.Propose(context.Background(), slotID, proposalString("set idx=1"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	res := waitForFutureResult(t, fut)
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

func TestProposeCorrelatesBurstOfInflightFutures(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 183)

	futures := []Future{
		mustPropose(t, rt, slotID, "p1"),
		mustPropose(t, rt, slotID, "p2"),
		mustPropose(t, rt, slotID, "p3"),
	}

	results := waitForFutureResults(t, futures...)
	assertStrictlyIncreasingIndexes(t, results)

	want := []string{"ok:p1", "ok:p2", "ok:p3"}
	for i, result := range results {
		if string(result.Data) != want[i] {
			t.Fatalf("results[%d].Data = %q, want %q", i, result.Data, want[i])
		}
	}
}

func TestRemoteCommitDoesNotResolveLocalFuture(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: 5 * time.Millisecond,
		Seed:     13,
	})
	slotID := SlotID(16)

	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	cluster.waitForBootstrapApplied(t, slotID, 3)

	oldLeader := cluster.waitForLeader(t, slotID)
	cluster.partitionNode(oldLeader)

	stale, err := cluster.runtime(oldLeader).Propose(context.Background(), slotID, proposalString("stale"))
	if err != nil {
		t.Fatalf("Propose(stale) error = %v", err)
	}

	newLeader := cluster.waitForLeaderAmong(t, slotID, cluster.otherNodes(oldLeader))
	fresh, err := cluster.runtime(newLeader).Propose(context.Background(), slotID, proposalString("fresh"))
	if err != nil {
		t.Fatalf("Propose(fresh) error = %v", err)
	}

	freshRes := waitForFutureResult(t, fresh)
	if string(freshRes.Data) != "ok:fresh" {
		t.Fatalf("fresh Wait().Data = %q", freshRes.Data)
	}

	cluster.healNode(oldLeader)
	cluster.waitForNodeCommitIndex(t, oldLeader, slotID, freshRes.Index)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	res, err := stale.Wait(ctx)
	if err == nil {
		t.Fatalf("stale future resolved unexpectedly: result=%+v err=%v", res, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrNotLeader) {
		t.Fatalf("stale future error = %v", err)
	}
}

func TestRemoteCommitDoesNotApplyStaleCommandAfterHeal(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: 5 * time.Millisecond,
		Seed:     18,
	})
	slotID := SlotID(161)

	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	cluster.waitForBootstrapApplied(t, slotID, 3)

	oldLeader := cluster.waitForLeader(t, slotID)
	cluster.partitionNode(oldLeader)

	stale, err := cluster.runtime(oldLeader).Propose(context.Background(), slotID, proposalString("stale"))
	if err != nil {
		t.Fatalf("Propose(stale) error = %v", err)
	}

	newLeader := cluster.waitForLeaderAmong(t, slotID, cluster.otherNodes(oldLeader))
	fresh, err := cluster.runtime(newLeader).Propose(context.Background(), slotID, proposalString("fresh"))
	if err != nil {
		t.Fatalf("Propose(fresh) error = %v", err)
	}
	freshRes := waitForFutureResult(t, fresh)

	cluster.healNode(oldLeader)
	cluster.waitForNodeCommitIndex(t, oldLeader, slotID, freshRes.Index)
	cluster.waitForAllApplied(t, slotID, []byte("fresh"))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := stale.Wait(ctx); err == nil {
		t.Fatal("stale future resolved unexpectedly")
	}

	for nodeID := range cluster.fsms {
		fsm := cluster.fsms[nodeID][slotID]
		fsm.mu.Lock()
		for _, applied := range fsm.applied {
			if string(applied) == "stale" {
				fsm.mu.Unlock()
				t.Fatalf("node %d applied stale command", nodeID)
			}
		}
		fsm.mu.Unlock()
	}
}

func TestReadyPersistenceFailureDoesNotAdvance(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 17)
	store := fakeStorageFor(rt, slotID)
	if store == nil {
		t.Fatal("fakeStorageFor() = nil")
	}
	fsm := fakeStateMachineFor(rt, slotID)
	if fsm == nil {
		t.Fatal("fakeStateMachineFor() = nil")
	}

	saveErr := errors.New("save failed")
	store.mu.Lock()
	baselineSaves := store.saveCount
	baselineApplied := store.lastApplied
	store.saveErr = saveErr
	store.mu.Unlock()

	fut, err := rt.Propose(context.Background(), slotID, proposalString("persist-fail"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := fut.Wait(ctx); !errors.Is(err, saveErr) {
		t.Fatalf("Wait() error = %v, want %v", err, saveErr)
	}

	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	waitForCondition(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return !g.processing && g.applying == 0
	})

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.saveCount != baselineSaves {
		t.Fatalf("Save() count = %d, want %d", store.saveCount, baselineSaves)
	}
	if store.lastApplied != baselineApplied {
		t.Fatalf("MarkApplied() = %d, want %d", store.lastApplied, baselineApplied)
	}

	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	if len(fsm.applied) != 0 {
		t.Fatalf("Apply() count = %d, want 0", len(fsm.applied))
	}
}

func TestProposeWaitBlocksUntilReadyBatchFullyCompletes(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := SlotID(181)
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

	fut, err := rt.Propose(context.Background(), slotID, proposalString("slow-mark-applied"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
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

func TestNormalEntryApplyDoesNotBlockTickProcessing(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(184)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
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

	beforeTicks := slotTickCount(rt, slotID)
	fut, err := rt.Propose(context.Background(), slotID, proposalString("slow-apply"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	g.markTickPending()
	rt.scheduler.enqueue(slotID)

	waitForCondition(t, func() bool {
		return slotTickCount(rt, slotID) > beforeTicks
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := fut.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() while Apply blocked = %v, want %v", err, context.DeadlineExceeded)
	}

	fsm.unblock()
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() after unblock error = %v", err)
	}
}

func TestNormalEntryAsyncApplyKeepsStatusAppliedAtDurableIndex(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(185)
	store := &internalFakeStorage{}
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: fsm,
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

	store.mu.Lock()
	baselineApplied := store.lastApplied
	store.mu.Unlock()

	fut, err := rt.Propose(context.Background(), slotID, proposalString("durable-status"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	var committed uint64
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		if err != nil {
			return false
		}
		committed = st.CommitIndex
		return st.CommitIndex > baselineApplied
	})

	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if st.AppliedIndex != baselineApplied {
		t.Fatalf("Status().AppliedIndex = %d while apply blocked, want durable %d; committed=%d", st.AppliedIndex, baselineApplied, committed)
	}

	fsm.unblock()
	res, err := fut.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() after unblock error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.AppliedIndex == res.Index
	})
}

func TestSnapshotReadyWaitsForAsyncApplyBeforeMemoryApplySnapshot(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(1184)
	store := &internalFakeStorage{}
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	fut, err := rt.Propose(context.Background(), slotID, proposalString("snapshot-barrier"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	beforeFirst, err := g.storageView.memory.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex() before snapshot error = %v", err)
	}
	status, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	snapIndex := status.CommitIndex + 100
	snapTerm := status.Term + 1
	snap := raftpb.Snapshot{
		Data: []byte("incoming snapshot"),
		Metadata: raftpb.SnapshotMetadata{
			Index:     snapIndex,
			Term:      snapTerm,
			ConfState: raftpb.ConfState{Voters: []uint64{1}},
		},
	}
	if err := rt.Step(context.Background(), Envelope{
		SlotID: slotID,
		Message: raftpb.Message{
			Type:     raftpb.MsgSnap,
			From:     2,
			To:       1,
			Term:     snapTerm,
			Snapshot: &snap,
		},
	}); err != nil {
		t.Fatalf("Step(snapshot) error = %v", err)
	}

	waitForCondition(t, func() bool {
		saved, err := store.Snapshot(context.Background())
		return err == nil && saved.Metadata.Index == snapIndex
	})
	firstWhileBlocked, err := g.storageView.memory.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex() while apply blocked error = %v", err)
	}
	if firstWhileBlocked != beforeFirst {
		t.Fatalf("memory FirstIndex while apply blocked = %d, want %d before snapshot memory apply", firstWhileBlocked, beforeFirst)
	}

	fsm.unblock()
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() after unblock error = %v", err)
	}
	waitForCondition(t, func() bool {
		first, err := g.storageView.memory.FirstIndex()
		return err == nil && first == snapIndex+1
	})
}

func TestSnapshotReadyDoesNotApplyMemoryAfterBarrierPredecessorFails(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(1186)
	store := &internalFakeStorage{}
	fatalErr := errors.New("fatal snapshot predecessor")
	fsm := newFailFirstBlockingStateMachine(fatalErr)
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	fut, err := rt.Propose(context.Background(), slotID, proposalString("fatal-before-snapshot"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	beforeFirst, err := g.storageView.memory.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex() before snapshot error = %v", err)
	}
	status, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	snapIndex := status.CommitIndex + 100
	snapTerm := status.Term + 1
	snap := raftpb.Snapshot{
		Data: []byte("fatal predecessor snapshot"),
		Metadata: raftpb.SnapshotMetadata{
			Index:     snapIndex,
			Term:      snapTerm,
			ConfState: raftpb.ConfState{Voters: []uint64{1}},
		},
	}
	if err := rt.Step(context.Background(), Envelope{
		SlotID: slotID,
		Message: raftpb.Message{
			Type:     raftpb.MsgSnap,
			From:     2,
			To:       1,
			Term:     snapTerm,
			Snapshot: &snap,
		},
	}); err != nil {
		t.Fatalf("Step(snapshot) error = %v", err)
	}

	waitForCondition(t, func() bool {
		saved, err := store.Snapshot(context.Background())
		return err == nil && saved.Metadata.Index == snapIndex
	})
	fsm.unblock()
	if _, err := fut.Wait(context.Background()); !errors.Is(err, fatalErr) {
		t.Fatalf("Wait() error = %v, want %v", err, fatalErr)
	}
	waitForCondition(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return !g.processing && g.applying == 0 && errors.Is(g.fatalErr, fatalErr)
	})

	firstAfterFatal, err := g.storageView.memory.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex() after fatal error = %v", err)
	}
	if firstAfterFatal != beforeFirst {
		t.Fatalf("memory FirstIndex after predecessor fatal = %d, want %d", firstAfterFatal, beforeFirst)
	}
}

func TestAsyncApplyObserverReportsTaskDurationAndQueueDepth(t *testing.T) {
	observer := &slotApplyTaskObserver{}
	rt := newStartedRuntimeWithTickAndObserver(t, time.Hour, observer)
	slotID := SlotID(1185)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	first, err := rt.Propose(context.Background(), slotID, proposalString("observe-blocked"))
	if err != nil {
		t.Fatalf("first Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("first Apply() did not start")
	}

	observer.mu.Lock()
	observedBeforeQueued := len(observer.applyQueues)
	observer.mu.Unlock()
	second, err := rt.Propose(context.Background(), slotID, proposalString("observe-queued"))
	if err != nil {
		t.Fatalf("second Propose() error = %v", err)
	}

	waitForCondition(t, func() bool {
		observer.mu.Lock()
		defer observer.mu.Unlock()
		return len(observer.applyQueues) > observedBeforeQueued
	})

	observer.mu.Lock()
	queueEvent := observer.applyQueues[len(observer.applyQueues)-1]
	observer.mu.Unlock()
	if queueEvent.slotID != slotID {
		t.Fatalf("apply queue slot = %d, want %d", queueEvent.slotID, slotID)
	}
	if queueEvent.queueDepth <= 0 {
		t.Fatalf("apply queue depth = %d, want > 0", queueEvent.queueDepth)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := second.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Wait() while first apply blocked = %v, want %v", err, context.DeadlineExceeded)
	}
	fsm.unblock()
	if _, err := first.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait() after unblock error = %v", err)
	}
	if _, err := second.Wait(context.Background()); err != nil {
		t.Fatalf("second Wait() after unblock error = %v", err)
	}
	waitForCondition(t, func() bool {
		observer.mu.Lock()
		defer observer.mu.Unlock()
		for _, event := range observer.applyTasks {
			if event.slotID == slotID && event.duration > 0 {
				return true
			}
		}
		return false
	})
	observer.mu.Lock()
	defer observer.mu.Unlock()
	for _, event := range observer.applyQueues {
		if event.queueDepth < 0 {
			t.Fatalf("apply queue depth = %d, want >= 0", event.queueDepth)
		}
	}
}

func TestAsyncApplyStopsQueuedTasksAfterApplyFatal(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(186)
	store := &internalFakeStorage{}
	fatalErr := errors.New("fatal first async apply")
	fsm := newFailFirstBlockingStateMachine(fatalErr)
	t.Cleanup(func() {
		fsm.unblock()
	})

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: fsm,
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

	first, err := rt.Propose(context.Background(), slotID, proposalString("fatal-first"))
	if err != nil {
		t.Fatalf("first Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("first Apply() did not start")
	}

	second, err := rt.Propose(context.Background(), slotID, proposalString("must-not-apply"))
	if err != nil {
		t.Fatalf("second Propose() error = %v", err)
	}
	waitForCondition(t, func() bool {
		g := slotFor(rt, slotID)
		if g == nil {
			return false
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		return len(g.pendingProposals) >= 2
	})

	fsm.unblock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := first.Wait(ctx); !errors.Is(err, fatalErr) {
		t.Fatalf("first Wait() error = %v, want %v", err, fatalErr)
	}
	if _, err := second.Wait(ctx); !errors.Is(err, fatalErr) {
		t.Fatalf("second Wait() error = %v, want %v", err, fatalErr)
	}

	select {
	case <-fsm.secondStarted:
		t.Fatal("second queued Apply() started after first fatal error")
	default:
	}
	if got := fsm.applyCount(); got != 1 {
		t.Fatalf("Apply() calls = %d, want 1", got)
	}
	if fut, err := rt.Propose(context.Background(), slotID, proposalString("after-fatal")); !errors.Is(err, fatalErr) {
		t.Fatalf("Propose() after fatal = future=%v err=%v, want %v", fut, err, fatalErr)
	}
}

func TestCloseSlotAllowsReopenSameSlotIDForAsyncApply(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(187)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   newInternalSlotOptions(slotID),
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})
	if err := rt.CloseSlot(context.Background(), slotID); err != nil {
		t.Fatalf("CloseSlot() error = %v", err)
	}

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   newInternalSlotOptions(slotID),
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() after reopen error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	fut, err := rt.Propose(context.Background(), slotID, proposalString("after-reopen"))
	if err != nil {
		t.Fatalf("Propose() after reopen error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := fut.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait() after reopen error = %v", err)
	}
	if string(res.Data) != "ok:after-reopen" {
		t.Fatalf("Wait().Data = %q, want ok:after-reopen", res.Data)
	}
}

func TestCloseSlotWithInflightAsyncApplyRetiresQueueBeforeReopen(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(189)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	if _, err := rt.Propose(context.Background(), slotID, proposalString("close-reopen-race")); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}
	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	retireStarted := make(chan struct{})
	releaseRetire := make(chan struct{})
	var retireOnce sync.Once
	var releaseRetireOnce sync.Once
	restoreRetireHook := setApplyPipelineBeforeRetireQueueHook(func(id SlotID) {
		if id != slotID {
			return
		}
		retireOnce.Do(func() { close(retireStarted) })
		<-releaseRetire
	})
	defer restoreRetireHook()
	defer releaseRetireOnce.Do(func() { close(releaseRetire) })

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- rt.CloseSlot(context.Background(), slotID)
	}()
	waitForCondition(t, func() bool {
		rt.apply.mu.Lock()
		defer rt.apply.mu.Unlock()
		q := rt.apply.queues[slotID]
		return q != nil && q.closed
	})

	fsm.unblock()
	select {
	case <-retireStarted:
	case <-time.After(time.Second):
		t.Fatal("apply queue retire hook did not run")
	}
	select {
	case err := <-closeDone:
		t.Fatalf("CloseSlot() returned before apply queue retired: %v", err)
	default:
	}
	releaseRetireOnce.Do(func() { close(releaseRetire) })

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("CloseSlot() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseSlot() did not return after apply queue retired")
	}

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   newInternalSlotOptions(slotID),
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() after reopen error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})
	fut, err := rt.Propose(context.Background(), slotID, proposalString("after-inflight-reopen"))
	if err != nil {
		t.Fatalf("Propose() after reopen error = %v", err)
	}
	res, err := fut.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() after reopen error = %v", err)
	}
	if string(res.Data) != "ok:after-inflight-reopen" {
		t.Fatalf("Wait().Data = %q, want ok:after-inflight-reopen", res.Data)
	}
}

func TestCloseSlotDuringApplyEnqueueHandoffRejectsTaskAndRetiresQueue(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(190)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   newInternalSlotOptions(slotID),
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	hookStarted := make(chan struct{})
	releaseHook := make(chan struct{})
	var hookOnce sync.Once
	var releaseOnce sync.Once
	restoreHook := setApplyPipelineAfterBeginApplyHook(func(id SlotID) {
		if id != slotID {
			return
		}
		hookOnce.Do(func() { close(hookStarted) })
		<-releaseHook
	})
	defer restoreHook()
	defer releaseOnce.Do(func() { close(releaseHook) })

	proposeDone := make(chan error, 1)
	go func() {
		fut, err := rt.Propose(context.Background(), slotID, proposalString("handoff-race"))
		if err != nil {
			proposeDone <- err
			return
		}
		_, err = fut.Wait(context.Background())
		proposeDone <- err
	}()

	select {
	case <-hookStarted:
	case <-time.After(time.Second):
		t.Fatal("apply enqueue handoff hook did not run")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- rt.CloseSlot(context.Background(), slotID)
	}()
	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	waitForCondition(t, func() bool {
		rt.mu.RLock()
		_, exists := rt.slots[slotID]
		rt.mu.RUnlock()
		g.mu.Lock()
		closed := g.closed
		g.mu.Unlock()
		return closed && !exists
	})
	releaseOnce.Do(func() { close(releaseHook) })

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("CloseSlot() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseSlot() did not return")
	}
	if err := <-proposeDone; !errors.Is(err, ErrSlotClosed) {
		t.Fatalf("in-flight proposal error = %v, want %v", err, ErrSlotClosed)
	}
	rt.apply.mu.Lock()
	leakedQueue := rt.apply.queues[slotID] != nil
	rt.apply.mu.Unlock()
	if leakedQueue {
		t.Fatal("apply queue leaked after CloseSlot during enqueue handoff")
	}

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   newInternalSlotOptions(slotID),
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() after handoff close error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})
	fut, err := rt.Propose(context.Background(), slotID, proposalString("after-handoff-reopen"))
	if err != nil {
		t.Fatalf("Propose() after reopen error = %v", err)
	}
	res, err := fut.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() after reopen error = %v", err)
	}
	if string(res.Data) != "ok:after-handoff-reopen" {
		t.Fatalf("Wait().Data = %q, want ok:after-handoff-reopen", res.Data)
	}
}

func TestRuntimeCloseWaitsForAsyncApply(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	slotID := SlotID(188)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	if _, err := rt.Propose(context.Background(), slotID, proposalString("close waits")); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	done := make(chan error, 1)
	go func() {
		done <- rt.Close()
	}()
	waitForCondition(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.closed && g.applying > 0
	})
	select {
	case err := <-done:
		t.Fatalf("Close() returned before async apply finished: %v", err)
	default:
	}

	fsm.unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not return after async apply unblocked")
	}
}

func TestMarkAppliedFailureFailsFutureAndStopsAdvance(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 182)
	store := fakeStorageFor(rt, slotID)
	if store == nil {
		t.Fatal("fakeStorageFor() = nil")
	}

	markAppliedErr := errors.New("mark applied failed")
	store.mu.Lock()
	baselineApplied := store.lastApplied
	store.markAppliedErr = markAppliedErr
	store.mu.Unlock()

	fut, err := rt.Propose(context.Background(), slotID, proposalString("mark-applied-fail"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := fut.Wait(ctx); !errors.Is(err, markAppliedErr) {
		t.Fatalf("Wait() error = %v, want %v", err, markAppliedErr)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.lastApplied != baselineApplied {
		t.Fatalf("MarkApplied() = %d, want %d", store.lastApplied, baselineApplied)
	}
}

func TestApplyFatalStopsSlot(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := SlotID(18)
	fatalErr := errors.New("fatal apply")
	store := &internalFakeStorage{}
	fsm := &internalFakeStateMachine{applyErr: fatalErr}

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: fsm,
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

	store.mu.Lock()
	baselineApplied := store.lastApplied
	store.mu.Unlock()

	fut, err := rt.Propose(context.Background(), slotID, proposalString("boom"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := fut.Wait(ctx); !errors.Is(err, fatalErr) {
		t.Fatalf("Wait() error = %v, want %v", err, fatalErr)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.lastApplied != baselineApplied {
		t.Fatalf("MarkApplied() advanced to %d, want %d", store.lastApplied, baselineApplied)
	}
}

func openSingleNodeLeader(t *testing.T, rt *Runtime, id SlotID) SlotID {
	t.Helper()

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           id,
			Storage:      &internalFakeStorage{},
			StateMachine: &internalFakeStateMachine{},
		},
		Voters: []NodeID{1},
	})
	if err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(id)
		return err == nil && st.Role == RoleLeader
	})
	return id
}

func fakeStorageFor(rt *Runtime, id SlotID) *internalFakeStorage {
	g := slotFor(rt, id)
	if g == nil {
		return nil
	}
	store, _ := g.storage.(*internalFakeStorage)
	return store
}

func fakeStateMachineFor(rt *Runtime, id SlotID) *internalFakeStateMachine {
	g := slotFor(rt, id)
	if g == nil {
		return nil
	}
	fsm, _ := g.stateMachine.(*internalFakeStateMachine)
	return fsm
}

func slotFor(rt *Runtime, id SlotID) *slot {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	g := rt.slots[id]
	return g
}

type blockingMarkAppliedStorage struct {
	*internalFakeStorage
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
	mu       sync.Mutex
	armed    bool
	minIndex uint64
}

func newBlockingMarkAppliedStorage() *blockingMarkAppliedStorage {
	return &blockingMarkAppliedStorage{
		internalFakeStorage: &internalFakeStorage{},
		started:             make(chan struct{}, 1),
		release:             make(chan struct{}),
	}
}

func (f *blockingMarkAppliedStorage) MarkApplied(ctx context.Context, index uint64) error {
	f.mu.Lock()
	armed := f.armed
	minIndex := f.minIndex
	f.mu.Unlock()
	if !armed || index < minIndex {
		return f.internalFakeStorage.MarkApplied(ctx, index)
	}

	select {
	case f.started <- struct{}{}:
	default:
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.release:
	}

	return f.internalFakeStorage.MarkApplied(ctx, index)
}

func (f *blockingMarkAppliedStorage) armAfter(index uint64) {
	f.mu.Lock()
	f.armed = true
	f.minIndex = index
	f.mu.Unlock()
}

func (f *blockingMarkAppliedStorage) unblock() {
	f.once.Do(func() {
		close(f.release)
	})
}

type failFirstBlockingStateMachine struct {
	mu            sync.Mutex
	started       chan struct{}
	release       chan struct{}
	secondStarted chan struct{}
	once          sync.Once
	firstErr      error
	calls         int
}

func newFailFirstBlockingStateMachine(firstErr error) *failFirstBlockingStateMachine {
	return &failFirstBlockingStateMachine{
		started:       make(chan struct{}, 1),
		release:       make(chan struct{}),
		secondStarted: make(chan struct{}, 1),
		firstErr:      firstErr,
	}
}

func (f *failFirstBlockingStateMachine) Apply(ctx context.Context, cmd Command) ([]byte, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()

	if call == 1 {
		select {
		case f.started <- struct{}{}:
		default:
		}
		<-f.release
		return nil, f.firstErr
	}
	select {
	case f.secondStarted <- struct{}{}:
	default:
	}
	return append([]byte("ok:"), cmd.Data...), nil
}

func (f *failFirstBlockingStateMachine) Restore(ctx context.Context, snap Snapshot) error {
	return nil
}

func (f *failFirstBlockingStateMachine) Snapshot(ctx context.Context) (Snapshot, error) {
	return Snapshot{}, nil
}

func (f *failFirstBlockingStateMachine) unblock() {
	f.once.Do(func() {
		close(f.release)
	})
}

func (f *failFirstBlockingStateMachine) applyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type commandDataPointerStateMachine struct {
	data []byte
}

func (s *commandDataPointerStateMachine) Apply(ctx context.Context, cmd Command) ([]byte, error) {
	s.data = cmd.Data
	return nil, nil
}

func (s *commandDataPointerStateMachine) ApplyBatch(ctx context.Context, cmds []Command) ([][]byte, error) {
	if len(cmds) > 0 {
		s.data = cmds[0].Data
	}
	return make([][]byte, len(cmds)), nil
}

func (s *commandDataPointerStateMachine) Restore(ctx context.Context, snap Snapshot) error {
	return nil
}

func (s *commandDataPointerStateMachine) Snapshot(ctx context.Context) (Snapshot, error) {
	return Snapshot{}, nil
}

func mustPropose(t *testing.T, rt *Runtime, slotID SlotID, data string) Future {
	t.Helper()

	fut, err := rt.Propose(context.Background(), slotID, proposalString(data))
	if err != nil {
		t.Fatalf("Propose(%q) error = %v", data, err)
	}
	return fut
}

func waitForFutureResults(t *testing.T, futures ...Future) []Result {
	t.Helper()

	results := make([]Result, len(futures))
	for i, fut := range futures {
		results[i] = waitForFutureResult(t, fut)
	}
	return results
}

func assertStrictlyIncreasingIndexes(t *testing.T, results []Result) {
	t.Helper()

	for i := 1; i < len(results); i++ {
		if results[i-1].Index >= results[i].Index {
			t.Fatalf("results[%d].Index = %d, results[%d].Index = %d, want strictly increasing",
				i-1, results[i-1].Index, i, results[i].Index)
		}
	}
}

type proposalStageObserver struct {
	events []proposalStageEvent
}

func (o *proposalStageObserver) ObserveProposalStage(stage string, result string, _ time.Duration) {
	o.events = append(o.events, proposalStageEvent{stage: stage, result: result})
}

type proposalStageEvent struct {
	stage  string
	result string
}

func requireProposalStage(t *testing.T, events []proposalStageEvent, stage string, result string) {
	t.Helper()
	for _, event := range events {
		if event.stage == stage && event.result == result {
			return
		}
	}
	t.Fatalf("proposal stage %s/%s not observed in %#v", stage, result, events)
}

type slotProposalObserver struct {
	mu        sync.Mutex
	proposals []slotProposalObservation
}

type slotProposalObservation struct {
	slotID   SlotID
	duration time.Duration
}

func (o *slotProposalObserver) SetSchedulerWorkers(int) {}

func (o *slotProposalObserver) SetSchedulerInflight(int) {}

func (o *slotProposalObserver) SetSchedulerState(SchedulerStateEvent) {}

func (o *slotProposalObserver) ObserveSchedulerAdmission(string) {}

func (o *slotProposalObserver) ObserveSchedulerTask(string, time.Duration) {}

func (o *slotProposalObserver) ObserveSlotProposal(slotID SlotID, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.proposals = append(o.proposals, slotProposalObservation{slotID: slotID, duration: d})
}

type slotApplyTaskObserver struct {
	mu          sync.Mutex
	applyQueues []slotApplyQueueObservation
	applyTasks  []slotApplyTaskObservation
}

type slotApplyQueueObservation struct {
	slotID     SlotID
	queueDepth int
}

type slotApplyTaskObservation struct {
	slotID   SlotID
	duration time.Duration
}

func (o *slotApplyTaskObserver) SetSchedulerWorkers(int) {}

func (o *slotApplyTaskObserver) SetSchedulerInflight(int) {}

func (o *slotApplyTaskObserver) SetSchedulerState(SchedulerStateEvent) {}

func (o *slotApplyTaskObserver) ObserveSchedulerAdmission(string) {}

func (o *slotApplyTaskObserver) ObserveSchedulerTask(string, time.Duration) {}

func (o *slotApplyTaskObserver) ObserveSlotApplyQueue(slotID SlotID, queueDepth int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.applyQueues = append(o.applyQueues, slotApplyQueueObservation{slotID: slotID, queueDepth: queueDepth})
}

func (o *slotApplyTaskObserver) ObserveSlotApplyTask(slotID SlotID, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.applyTasks = append(o.applyTasks, slotApplyTaskObservation{slotID: slotID, duration: d})
}
