package cluster

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestRuntimeStateCopiesPeersOnSetAndGet(t *testing.T) {
	state := newRuntimeState()
	peers := []multiraft.NodeID{1, 2, 3}

	state.Set(7, peers)
	peers[0] = 9

	got, ok := state.Get(7)
	if !ok {
		t.Fatal("RuntimeState.Get() missing peers")
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("RuntimeState.Get() = %v", got)
	}

	got[1] = 8
	again, ok := state.Get(7)
	if !ok {
		t.Fatal("RuntimeState.Get() missing peers on second read")
	}
	if again[1] != 2 {
		t.Fatalf("RuntimeState.Get() leaked caller mutation: %v", again)
	}
}

func TestRuntimeStateSnapshotCopiesAllEntries(t *testing.T) {
	state := newRuntimeState()
	state.Set(1, []multiraft.NodeID{1, 2})
	state.Set(2, []multiraft.NodeID{3, 4})

	snapshot := state.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("RuntimeState.Snapshot() len = %d, want 2", len(snapshot))
	}

	snapshot[1][0] = 9
	snapshot[3] = []multiraft.NodeID{5}

	got, ok := state.Get(1)
	if !ok {
		t.Fatal("RuntimeState.Get() missing slot 1")
	}
	if got[0] != 1 {
		t.Fatalf("RuntimeState.Get() mutated through snapshot: %v", got)
	}
	if _, ok := state.Get(3); ok {
		t.Fatal("RuntimeState.Get() unexpectedly saw snapshot mutation")
	}
}

func TestRuntimeStateDeleteRemovesEntries(t *testing.T) {
	state := newRuntimeState()
	state.Set(9, []multiraft.NodeID{1})

	state.Delete(9)

	if _, ok := state.Get(9); ok {
		t.Fatal("RuntimeState.Delete() did not remove entry")
	}
}
