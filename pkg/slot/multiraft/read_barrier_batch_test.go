package multiraft

import (
	"context"
	"errors"
	"testing"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func newReadBarrierBatchSlot(t *testing.T) *slot {
	t.Helper()
	memory := raft.NewMemoryStorage()
	if err := memory.ApplySnapshot(raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}); err != nil {
		t.Fatal(err)
	}
	memory.SetHardState(raftpb.HardState{Term: 1, Commit: 1})
	raw, err := raft.NewRawNode(&raft.Config{ID: 1, ElectionTick: 10, HeartbeatTick: 1, Storage: memory, Applied: 1, MaxInflightMsgs: 256, ReadOnlyOption: raft.ReadOnlySafe})
	if err != nil {
		t.Fatal(err)
	}
	if err = raw.Campaign(); err != nil {
		t.Fatal(err)
	}
	for raw.HasReady() {
		ready := raw.Ready()
		if err = memory.Append(ready.Entries); err != nil {
			t.Fatal(err)
		}
		if !raft.IsEmptyHardState(ready.HardState) {
			memory.SetHardState(ready.HardState)
		}
		raw.Advance(ready)
	}
	st := raw.BasicStatus()
	if st.RaftState != raft.StateLeader {
		t.Fatal("fixture is not leader")
	}
	return &slot{rawNode: raw, storageView: &storageAdapter{memory: &loadedMemoryStorage{MemoryStorage: memory}}, status: Status{Role: RoleLeader, Term: st.Term}, durableAppliedIndex: st.Commit}
}
func queueBatchRead(t *testing.T, g *slot, ctx context.Context) *readBarrierRequest {
	t.Helper()
	r := &readBarrierRequest{ctx: ctx, resp: make(chan error, 1)}
	if err := g.enqueueControl(controlAction{kind: controlReadBarrier, readBarrier: r}); err != nil {
		t.Fatal(err)
	}
	return r
}
func assertReadPending(t *testing.T, r *readBarrierRequest) {
	t.Helper()
	select {
	case err := <-r.resp:
		t.Fatalf("read completed early: %v", err)
	default:
	}
}
func TestReadBarrierBatchSharesOnlyAlreadyTakenControls(t *testing.T) {
	g := newReadBarrierBatchSlot(t)
	first := queueBatchRead(t, g, context.Background())
	second := queueBatchRead(t, g, context.Background())
	g.processControls(context.Background())
	if g.readSequence != 1 || len(g.pendingReads) != 1 {
		t.Fatalf("two queued reads issued %d proofs with %d pending keys, want one", g.readSequence, len(g.pendingReads))
	}
	ready := g.rawNode.Ready()
	persistReadBarrierBatchReady(t, g, ready)
	if len(ready.ReadStates) != 1 {
		t.Fatalf("ReadIndex count=%d", len(ready.ReadStates))
	}
	later := queueBatchRead(t, g, context.Background())
	g.processControls(context.Background())
	if g.readSequence != 2 {
		t.Fatalf("late request reused proof: %d", g.readSequence)
	}
	g.acceptReadStates(ready.ReadStates)
	for _, r := range []*readBarrierRequest{first, second} {
		select {
		case err := <-r.resp:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("confirmed read not completed")
		}
	}
	assertReadPending(t, later)
	next := g.rawNode.Ready()
	persistReadBarrierBatchReady(t, g, next)
	g.acceptReadStates(next.ReadStates)
	select {
	case err := <-later.resp:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("late read did not complete on its fresh proof")
	}
}

func TestReadBarrierBatchCancellationKeepsCallerBudgetUntilConfirmation(t *testing.T) {
	g := newReadBarrierBatchSlot(t)
	ctx, cancel := context.WithCancel(context.Background())
	callers := make([]*readBarrierRequest, maxPendingReadBarriers)
	for i := range callers {
		callers[i] = queueBatchRead(t, g, ctx)
	}
	excess := queueBatchRead(t, g, context.Background())
	g.processControls(context.Background())
	if g.pendingReadCount != maxPendingReadBarriers || len(g.pendingReads) != 1 {
		t.Fatalf("callers=%d proofs=%d", g.pendingReadCount, len(g.pendingReads))
	}
	requireReadResult(t, excess, ErrSlotBusy)
	cancel()
	g.acceptReadStates(nil)
	for _, r := range callers {
		requireReadResult(t, r, context.Canceled)
	}
	if g.pendingReadCount != maxPendingReadBarriers {
		t.Fatalf("canceled unconfirmed count=%d", g.pendingReadCount)
	}
	stillFull := queueBatchRead(t, g, context.Background())
	g.processControls(context.Background())
	requireReadResult(t, stillFull, ErrSlotBusy)
	ready := g.rawNode.Ready()
	persistReadBarrierBatchReady(t, g, ready)
	g.acceptReadStates(ready.ReadStates)
	if g.pendingReadCount != 0 || len(g.pendingReads) != 0 {
		t.Fatal("confirmed canceled callers retained")
	}
	fresh := queueBatchRead(t, g, context.Background())
	g.processControls(context.Background())
	assertReadPending(t, fresh)
	if g.readSequence != 2 {
		t.Fatal("fresh caller reused canceled proof")
	}
}

func TestReadBarrierBatchCancelOneStillWaitsForDurableApply(t *testing.T) {
	g := newReadBarrierBatchSlot(t)
	ctx, cancel := context.WithCancel(context.Background())
	canceled := queueBatchRead(t, g, ctx)
	live := queueBatchRead(t, g, context.Background())
	g.processControls(context.Background())
	ready := g.rawNode.Ready()
	persistReadBarrierBatchReady(t, g, ready)
	g.durableAppliedIndex = ready.ReadStates[0].Index - 1
	cancel()
	g.acceptReadStates(ready.ReadStates)
	requireReadResult(t, canceled, context.Canceled)
	assertReadPending(t, live)
	if g.pendingReadCount != 1 || len(g.pendingReads) != 1 {
		t.Fatalf("callers=%d proofs=%d", g.pendingReadCount, len(g.pendingReads))
	}
	g.setDurableAppliedIndex(ready.ReadStates[0].Index)
	requireReadResult(t, live, nil)
	if g.pendingReadCount != 0 || len(g.pendingReads) != 0 {
		t.Fatal("applied proof retained")
	}
}

func TestReadBarrierBatchPreservesControlAndTakenBatchBoundaries(t *testing.T) {
	g := newReadBarrierBatchSlot(t)
	queueBatchRead(t, g, context.Background())
	if err := g.enqueueControl(controlAction{kind: controlPropose, data: []byte("write"), future: newFuture(nil)}); err != nil {
		t.Fatal(err)
	}
	queueBatchRead(t, g, context.Background())
	g.processControls(context.Background())
	if g.readSequence != 2 {
		t.Fatal("coalesced across a write control")
	}
	queueBatchRead(t, g, context.Background())
	taken := g.takeControlBatch()
	later := queueBatchRead(t, g, context.Background())
	g.issueReadBarrierBatch(taken)
	g.releaseControlBatch(taken)
	ready := g.rawNode.Ready()
	persistReadBarrierBatchReady(t, g, ready)
	g.acceptReadStates(ready.ReadStates)
	assertReadPending(t, later)
	if g.readSequence != 3 {
		t.Fatal("taken batch proof count changed")
	}
	g.processControls(context.Background())
	if g.readSequence != 4 {
		t.Fatal("later queued request joined detached batch")
	}
}

func TestReadBarrierBatchTransientFailureAndTermChange(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "confirmed"}[confirmed], func(t *testing.T) {
			g := newReadBarrierBatchSlot(t)
			first := queueBatchRead(t, g, context.Background())
			second := queueBatchRead(t, g, context.Background())
			g.processControls(context.Background())
			if confirmed {
				ready := g.rawNode.Ready()
				persistReadBarrierBatchReady(t, g, ready)
				g.durableAppliedIndex = ready.ReadStates[0].Index - 1
				g.acceptReadStates(ready.ReadStates)
			}
			failure := errors.New("transient Ready failure")
			g.failPending(failure)
			requireReadResult(t, first, failure)
			requireReadResult(t, second, failure)
			want := 2
			if confirmed {
				want = 0
			}
			if g.pendingReadCount != want {
				t.Fatalf("pending=%d", g.pendingReadCount)
			}
			g.status.Term++
			g.acceptReadStates(nil)
			if g.pendingReadCount != 0 || len(g.pendingReads) != 0 {
				t.Fatal("old-term group retained")
			}
		})
	}
	g := newReadBarrierBatchSlot(t)
	first := queueBatchRead(t, g, context.Background())
	second := queueBatchRead(t, g, context.Background())
	g.processControls(context.Background())
	g.status.Role = RoleFollower
	g.acceptReadStates(nil)
	requireReadResult(t, first, ErrNotLeader)
	requireReadResult(t, second, ErrNotLeader)
	if g.pendingReadCount != 0 {
		t.Fatal("follower retained callers")
	}
}

func requireReadResult(t *testing.T, r *readBarrierRequest, want error) {
	t.Helper()
	select {
	case err := <-r.resp:
		if !errors.Is(err, want) {
			t.Fatalf("read error=%v want=%v", err, want)
		}
	default:
		t.Fatal("read did not complete")
	}
}

// Persist Ready before advancing the fixture, including intervening proposals.
func persistReadBarrierBatchReady(t *testing.T, g *slot, ready raft.Ready) {
	t.Helper()
	if err := g.storageView.memory.Append(ready.Entries); err != nil {
		t.Fatal(err)
	}
	if !raft.IsEmptyHardState(ready.HardState) {
		g.storageView.memory.SetHardState(ready.HardState)
	}
	g.rawNode.Advance(ready)
}
func TestReadBarrierBatchCloseReleasesEveryCaller(t *testing.T) {
	g := newReadBarrierBatchSlot(t)
	first := queueBatchRead(t, g, context.Background())
	second := queueBatchRead(t, g, context.Background())
	g.processControls(context.Background())
	g.closed = true
	g.failPending(ErrSlotClosed)
	requireReadResult(t, first, ErrSlotClosed)
	requireReadResult(t, second, ErrSlotClosed)
	if g.pendingReadCount != 0 || len(g.pendingReads) != 0 {
		t.Fatal("closed slot retains read callers")
	}
}
