package multiraft

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

func TestEnsurePendingProposalCapacityPreservesTrackedFuture(t *testing.T) {
	fut := newFuture()
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
	rt := newStartedRuntime(t)
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
	rt := newStartedRuntime(t)
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

	time.Sleep(100 * time.Millisecond)

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
