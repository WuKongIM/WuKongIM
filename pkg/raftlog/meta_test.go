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

func TestReplaceEntriesFromIndexClearsDroppedTailPayloads(t *testing.T) {
	existing := make([]raftpb.Entry, 4, 8)
	for i, payload := range []string{"one", "two", "three", "four"} {
		existing[i] = raftpb.Entry{
			Index: uint64(i + 1),
			Term:  1,
			Data:  []byte(payload),
		}
	}
	incoming := []raftpb.Entry{{
		Index: 2,
		Term:  2,
		Data:  []byte("replacement"),
	}}

	got := replaceEntriesFromIndex(existing, 2, incoming)
	if len(got) != 2 {
		t.Fatalf("len(replaced) = %d, want 2", len(got))
	}
	if string(got[1].Data) != "replacement" {
		t.Fatalf("replacement payload = %q, want replacement", got[1].Data)
	}

	backing := existing[:cap(existing)]
	for i := 2; i < 4; i++ {
		if backing[i].Index != 0 || backing[i].Term != 0 || backing[i].Data != nil {
			t.Fatalf("dropped tail entry %d was not cleared: index=%d term=%d data=%q", i, backing[i].Index, backing[i].Term, backing[i].Data)
		}
	}
}

func TestReplaceCachedEntriesFromIndexReusesTailCapacityForPureAppend(t *testing.T) {
	existing := make([]raftpb.Entry, 2, 4)
	existing[0] = raftpb.Entry{Index: 1, Term: 1}
	existing[1] = raftpb.Entry{Index: 2, Term: 1}
	firstEntry := &existing[0]
	incoming := []raftpb.Entry{{
		Index: 3,
		Term:  2,
		Type:  raftpb.EntryNormal,
		Data:  []byte("normal payload is not cached"),
	}}

	got := replaceCachedEntriesFromIndex(existing, 3, incoming)
	if len(got) != 3 {
		t.Fatalf("len(appended) = %d, want 3", len(got))
	}
	if &got[0] != firstEntry {
		t.Fatal("pure append replaced the cached entries backing")
	}
	if got[2].Index != 3 || got[2].Term != 2 || got[2].Data != nil {
		t.Fatalf("appended cached entry = %#v, want index=3 term=2 metadata only", got[2])
	}
}

func TestCloneCachedEntryDropsZeroLengthPayloadBacking(t *testing.T) {
	payload := make([]byte, 0, 1<<20)
	entry := raftpb.Entry{
		Index: 4,
		Term:  2,
		Type:  raftpb.EntryConfChangeV2,
		Data:  payload,
	}

	cached := cloneCachedEntry(entry)
	if cached.Data != nil {
		t.Fatalf("cached zero-length payload = len %d cap %d, want nil", len(cached.Data), cap(cached.Data))
	}
}
