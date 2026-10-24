//go:build integration

package multiraft

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

// These existing regressions exercise real Runtime workers and blocking ports.
// Snapshot persistence and memory publication both follow the predecessor's
// durable apply; an earlier higher-term Future failure remains immutable.
func TestSnapshotReadyWaitsForAsyncApplyBeforeMemoryApplySnapshot(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	slotID := SlotID(1184)
	store := &snapshotPredecessorStorage{internalFakeStorage: &internalFakeStorage{}}
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{
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

	fut, err := rt.Propose(ctx, slotID, proposalString("snapshot-barrier"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-ctx.Done():
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
	store.target.Store(snapIndex)
	predecessor, err := store.InitialState(ctx)
	if err != nil || predecessor.AppliedIndex >= predecessor.HardState.Commit {
		t.Fatalf("fixture lacks a durable committed but unapplied predecessor: %+v err=%v", predecessor, err)
	}
	store.required.Store(predecessor.HardState.Commit)
	beforeSnapshot, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snap := raftpb.Snapshot{
		Data: []byte("incoming snapshot"),
		Metadata: raftpb.SnapshotMetadata{
			Index:     snapIndex,
			Term:      snapTerm,
			ConfState: raftpb.ConfState{Voters: []uint64{1}},
		},
	}
	if err := rt.Step(ctx, Envelope{
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

	// A resolved leadership failure is a positive public witness that the
	// higher-term snapshot reached the owner while predecessor Apply is held.
	// The proposal's terminal result is separate from subsequent FSM durability.
	if _, err := fut.Wait(ctx); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("old Future before predecessor release = %v, want %v", err, ErrNotLeader)
	}
	if store.snapshotCalls.Load() != 0 {
		t.Fatal("snapshot Save crossed the blocked predecessor")
	}
	firstWhileBlocked, err := g.storageView.memory.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex() while apply blocked error = %v", err)
	}
	if firstWhileBlocked != beforeFirst {
		t.Fatalf("memory FirstIndex while apply blocked = %d, want %d before snapshot memory apply", firstWhileBlocked, beforeFirst)
	}

	fsm.unblock()
	if _, err := fut.Wait(ctx); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("late apply changed the old Future: %v", err)
	}
	waitForCondition(t, func() bool {
		first, err := g.storageView.memory.FirstIndex()
		st, statusErr := rt.Status(slotID)
		return err == nil && statusErr == nil && first == snapIndex+1 && st.AppliedIndex >= snapIndex
	})
	saved, err := store.Snapshot(ctx)
	if err != nil || saved.Metadata.Index != snapIndex || saved.Metadata.Term != snapTerm {
		t.Fatalf("snapshot was not durably installed: before=%+v after=%+v err=%v", beforeSnapshot.Metadata, saved.Metadata, err)
	}
	if store.snapshotCalls.Load() != 1 || store.earlySave.Load() {
		t.Fatalf("snapshot persistence crossed predecessor durability: calls=%d early=%v", store.snapshotCalls.Load(), store.earlySave.Load())
	}
}

func TestSnapshotReadyDoesNotApplyMemoryAfterBarrierPredecessorFails(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	slotID := SlotID(1186)
	store := &snapshotPredecessorStorage{internalFakeStorage: &internalFakeStorage{}}
	fatalErr := errors.New("fatal snapshot predecessor")
	fsm := newFailFirstBlockingStateMachine(fatalErr)
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{
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

	fut, err := rt.Propose(ctx, slotID, proposalString("fatal-before-snapshot"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-ctx.Done():
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
	store.target.Store(snapIndex)
	predecessor, err := store.InitialState(ctx)
	if err != nil || predecessor.AppliedIndex >= predecessor.HardState.Commit {
		t.Fatalf("fixture lacks a durable committed but unapplied predecessor: %+v err=%v", predecessor, err)
	}
	store.required.Store(predecessor.HardState.Commit)
	beforeSnapshot, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snap := raftpb.Snapshot{
		Data: []byte("fatal predecessor snapshot"),
		Metadata: raftpb.SnapshotMetadata{
			Index:     snapIndex,
			Term:      snapTerm,
			ConfState: raftpb.ConfState{Voters: []uint64{1}},
		},
	}
	if err := rt.Step(ctx, Envelope{
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

	// A resolved leadership failure is a positive public witness that the
	// higher-term snapshot reached the owner while predecessor Apply is held.
	// The proposal's terminal result is separate from subsequent FSM durability.
	if _, err := fut.Wait(ctx); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("old Future before predecessor release = %v, want %v", err, ErrNotLeader)
	}
	if store.snapshotCalls.Load() != 0 {
		t.Fatal("snapshot Save crossed the blocked predecessor")
	}
	fsm.unblock()
	if _, err := fut.Wait(ctx); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("predecessor failure rewrote the old Future: %v", err)
	}
	waitForCondition(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return !g.processing && g.applying == 0 && g.storageTasks == 0 && errors.Is(g.fatalErr, fatalErr)
	})

	if store.snapshotCalls.Load() != 0 || store.earlySave.Load() {
		t.Fatal("fatal predecessor allowed snapshot physical Save")
	}
	saved, err := store.Snapshot(ctx)
	if err != nil || saved.Metadata.Index != beforeSnapshot.Metadata.Index || saved.Metadata.Term != beforeSnapshot.Metadata.Term {
		t.Fatalf("fatal predecessor changed durable snapshot: before=%+v after=%+v err=%v", beforeSnapshot.Metadata, saved.Metadata, err)
	}
	if _, err := rt.Propose(ctx, slotID, proposalString("after-fatal-snapshot")); !errors.Is(err, fatalErr) {
		t.Fatalf("Slot did not retain predecessor fatal cause: %v", err)
	}

	firstAfterFatal, err := g.storageView.memory.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex() after fatal error = %v", err)
	}
	if firstAfterFatal != beforeFirst {
		t.Fatalf("memory FirstIndex after predecessor fatal = %d, want %d", firstAfterFatal, beforeFirst)
	}
}

// snapshotPredecessorStorage checks the public Save boundary independently of
// the caller's polling schedule. Only the exact incoming snapshot is counted.
type snapshotPredecessorStorage struct {
	*internalFakeStorage
	target, required atomic.Uint64
	snapshotCalls    atomic.Uint64
	earlySave        atomic.Bool
}

func (s *snapshotPredecessorStorage) Save(ctx context.Context, state PersistentState) error {
	if state.Snapshot != nil && s.target.Load() != 0 && state.Snapshot.Metadata.Index == s.target.Load() {
		before, err := s.internalFakeStorage.InitialState(ctx)
		if err != nil {
			return err
		}
		if before.AppliedIndex < s.required.Load() {
			s.earlySave.Store(true)
		}
		s.snapshotCalls.Add(1)
	}
	return s.internalFakeStorage.Save(ctx, state)
}
