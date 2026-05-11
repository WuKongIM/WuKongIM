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
