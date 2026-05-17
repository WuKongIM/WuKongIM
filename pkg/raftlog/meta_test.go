package raftlog

import (
	"testing"

	"go.etcd.io/raft/v3/raftpb"
)

func TestReplaceEntriesFromIndexAvoidsCloningRetainedPrefixPayloads(t *testing.T) {
	existing := make([]raftpb.Entry, 128)
	for i := range existing {
		existing[i] = raftpb.Entry{
			Index: uint64(i + 1),
			Term:  1,
			Data:  []byte("retained payload"),
		}
	}
	incoming := []raftpb.Entry{{
		Index: 129,
		Term:  2,
		Data:  []byte("incoming payload"),
	}}

	allocs := testing.AllocsPerRun(100, func() {
		_ = replaceEntriesFromIndex(existing, 129, incoming)
	})
	if allocs > 4 {
		t.Fatalf("replaceEntriesFromIndex allocations = %.0f, want <= 4", allocs)
	}
}

func TestReplaceEntriesFromIndexReusesTailCapacityForAppend(t *testing.T) {
	existing := make([]raftpb.Entry, 128, 256)
	for i := range existing {
		existing[i] = raftpb.Entry{
			Index: uint64(i + 1),
			Term:  1,
		}
	}
	incoming := []raftpb.Entry{{
		Index: 129,
		Term:  2,
	}}

	allocs := testing.AllocsPerRun(100, func() {
		got := replaceEntriesFromIndex(existing, 129, incoming)
		if len(got) != 129 {
			t.Fatalf("len(replaced) = %d, want 129", len(got))
		}
	})
	if allocs > 0 {
		t.Fatalf("replaceEntriesFromIndex append allocations = %.0f, want 0", allocs)
	}
}

func TestReplaceEntriesFromIndexPreservesAliasedIncomingTail(t *testing.T) {
	existing := []raftpb.Entry{
		{Index: 1, Term: 1, Data: []byte("one")},
		{Index: 2, Term: 2, Data: []byte("two")},
	}
	incoming := existing[1:]

	got := replaceEntriesFromIndex(existing, 2, incoming)
	if len(got) != 2 {
		t.Fatalf("len(replaced) = %d, want 2", len(got))
	}
	if string(got[1].Data) != "two" {
		t.Fatalf("replaced tail payload = %q, want two", string(got[1].Data))
	}
}

func TestReplaceEntriesFromIndexClonesIncomingPayloads(t *testing.T) {
	existing := []raftpb.Entry{{
		Index: 1,
		Term:  1,
		Data:  []byte("existing payload"),
	}}
	incoming := []raftpb.Entry{{
		Index: 2,
		Term:  2,
		Data:  []byte("incoming payload"),
	}}

	got := replaceEntriesFromIndex(existing, 2, incoming)
	incoming[0].Data[0] = 'x'

	if string(got[1].Data) != "incoming payload" {
		t.Fatalf("incoming payload was not cloned: %q", got[1].Data)
	}
}
