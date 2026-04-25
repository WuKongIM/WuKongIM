package multiraft

import (
	"context"
	"errors"
	"testing"

	"go.etcd.io/raft/v3/raftpb"
)

func TestOpenSlotRestoresAppliedIndexFromStorage(t *testing.T) {
	store := &internalFakeStorage{
		state: BootstrapState{
			HardState: raftpb.HardState{
				Commit: 7,
			},
			AppliedIndex: 7,
		},
		snapshot: raftpb.Snapshot{
			Metadata: raftpb.SnapshotMetadata{
				Index: 7,
				Term:  1,
			},
		},
	}
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), SlotOptions{
		ID:           40,
		Storage:      store,
		StateMachine: &internalFakeStateMachine{},
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	st, err := rt.Status(40)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if st.AppliedIndex != 7 {
		t.Fatalf("Status().AppliedIndex = %d", st.AppliedIndex)
	}
}

func TestOpenSlotRestoresSnapshotIntoStateMachine(t *testing.T) {
	fsm := &internalFakeStateMachine{}
	store := &internalFakeStorage{
		state: BootstrapState{
			HardState: raftpb.HardState{
				Commit: 5,
			},
			AppliedIndex: 5,
		},
		snapshot: raftpb.Snapshot{
			Data: []byte("snap"),
			Metadata: raftpb.SnapshotMetadata{
				Index: 5,
				Term:  2,
			},
		},
	}
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), SlotOptions{
		ID:           41,
		Storage:      store,
		StateMachine: fsm,
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	if fsm.restoreCount != 1 {
		t.Fatalf("Restore() count = %d", fsm.restoreCount)
	}
	if fsm.lastSnapshot.Index != 5 {
		t.Fatalf("Restore() snapshot index = %d", fsm.lastSnapshot.Index)
	}
}

func TestRestoreFatalStopsSlot(t *testing.T) {
	rt := newStartedRuntime(t)
	fatalErr := errors.New("fatal restore")
	store := &internalFakeStorage{}
	fsm := &internalFakeStateMachine{restoreErr: fatalErr}

	if err := rt.OpenSlot(context.Background(), SlotOptions{
		ID:           42,
		Storage:      store,
		StateMachine: fsm,
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	err := rt.Step(context.Background(), Envelope{
		SlotID: 42,
		Message: raftpb.Message{
			Type: raftpb.MsgSnap,
			From: 2,
			To:   1,
			Term: 2,
			Snapshot: &raftpb.Snapshot{
				Data: []byte("snap"),
				Metadata: raftpb.SnapshotMetadata{
					Index: 5,
					Term:  2,
					ConfState: raftpb.ConfState{
						Voters: []uint64{1, 2},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Step(MsgSnap) error = %v", err)
	}

	waitForCondition(t, func() bool {
		_, err := rt.Status(42)
		return errors.Is(err, fatalErr)
	})

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.lastApplied != 0 {
		t.Fatalf("MarkApplied() = %d, want 0", store.lastApplied)
	}
}
