//go:build integration

package multiraft

import (
	"context"
	"errors"
	"go.etcd.io/raft/v3/raftpb"
	"io"
	"testing"
)

func TestStartupDuplicateOpenCannotRestoreRegisteredSlot(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{EnabledSet: true})
	store := &internalFakeStorage{}
	snap := raftpb.Snapshot{Data: []byte("snapshot"), Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	if err := store.Save(context.Background(), PersistentState{Snapshot: &snap}); err != nil {
		t.Fatal(err)
	}
	sm := &snapshottingStateMachine{}
	opts := SlotOptions{ID: 1, Storage: store, StateMachine: sm}
	if err := rt.OpenSlot(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if err := rt.OpenSlot(context.Background(), opts); !errors.Is(err, ErrSlotExists) {
		t.Fatalf("duplicate: %v", err)
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.restoreCount != 1 {
		t.Fatalf("duplicate mutated live FSM: %d restores", sm.restoreCount)
	}
}

// A live Slot open must not opt into partial-batch installation just because
// both adapters support streaming. Only an explicit startup admission fence
// can select that path.
func TestOpenSlotDoesNotStreamWithoutStartupAdmission(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{EnabledSet: true})
	store := &startupAdmissionStorage{internalFakeStorage: &internalFakeStorage{}}
	snap := raftpb.Snapshot{Data: []byte("snapshot"), Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
	if err := store.Save(context.Background(), PersistentState{Snapshot: &snap}); err != nil {
		t.Fatal(err)
	}
	sm := &startupAdmissionFSM{snapshottingStateMachine: &snapshottingStateMachine{}}
	if err := rt.OpenSlot(context.Background(), SlotOptions{ID: 1, Storage: store, StateMachine: sm}); err != nil {
		t.Fatal(err)
	}
	if store.streamCalls != 0 {
		t.Fatal("streamed a live Slot without the node-startup fence")
	}
}

type startupAdmissionStorage struct {
	*internalFakeStorage
	streamCalls int
}

func (s *startupAdmissionStorage) OpenStartupSnapshot(context.Context, func(RecoveryProgress)) (StartupSnapshot, error) {
	s.streamCalls++
	return StartupSnapshot{}, errors.New("streaming requires explicit startup admission")
}

type startupAdmissionFSM struct{ *snapshottingStateMachine }

func (*startupAdmissionFSM) RestoreStartupSnapshot(context.Context, Snapshot, io.ReadSeeker, int64, func(RecoveryProgress)) error {
	return errors.New("unexpected streaming restore")
}
