package multiraft

import (
	"reflect"
	"testing"

	"go.etcd.io/raft/v3/raftpb"
)

// This approved pure seam checks which already-owned work may share a Save.
// Public Runtime tests separately prove persistence, responses, and retirement.
func TestStorageAppendBatchSizeSafePrefix(t *testing.T) {
	g := &slot{id: 901}
	reopened := &slot{id: 901}
	const term uint64 = 5
	entry := func(index, entryTerm uint64) raftpb.Entry {
		return raftpb.Entry{Index: index, Term: entryTerm, Type: raftpb.EntryNormal, Data: []byte("entry-payload")}
	}
	work := func(entries ...raftpb.Entry) *storageWork {
		return &storageWork{
			slot: g, acceptedTerm: term, hasAppend: true, commitBefore: 8,
			append: raftpb.Message{Type: raftpb.MsgStorageAppend, Entries: entries,
				Responses: []raftpb.Message{{Type: raftpb.MsgStorageAppendResp, Term: term, Context: []byte("response-context")}}},
			lease: &storageLease{slot: g, bytes: 512, remaining: storageAppendPart},
		}
	}
	hs := func(w *storageWork, hsTerm, vote, commit uint64) *storageWork {
		w.append.Term, w.append.Vote, w.append.Commit = hsTerm, vote, commit
		return w
	}
	actualTerm := func(w *storageWork, captured uint64) *storageWork {
		w.acceptedTerm = captured
		return w
	}
	generation := func(w *storageWork, owner *slot) *storageWork {
		w.slot, w.lease.slot = owner, owner
		return w
	}
	withApply := func(w *storageWork, kind raftpb.EntryType) *storageWork {
		w.hasApply = true
		w.apply = raftpb.Message{Type: raftpb.MsgStorageApply, Entries: []raftpb.Entry{{Index: 8, Term: term, Type: kind, Data: []byte("applied-payload")}}}
		w.lease.remaining |= storageApplyPart
		return w
	}
	applyOnly := func() *storageWork {
		w := withApply(work(), raftpb.EntryNormal)
		w.hasAppend = false
		return w
	}
	config := func(index uint64, kind raftpb.EntryType) raftpb.Entry {
		e := entry(index, term)
		e.Type = kind
		return e
	}
	snapshot := func(w *storageWork) *storageWork {
		w.append.Snapshot = &raftpb.Snapshot{Data: []byte("snapshot-payload"), Metadata: raftpb.SnapshotMetadata{Index: 10, Term: term}}
		return w
	}
	maxIndex := ^uint64(0)
	cases := []struct {
		name  string
		works []*storageWork
		want  int
	}{
		{"empty", nil, 0},
		{"single", []*storageWork{work(entry(10, term))}, 1},
		{"four_owned_records", []*storageWork{work(entry(10, term)), work(entry(11, term)), work(entry(12, term)), work(entry(13, term))}, 4},
		{"fixed_four_cap", []*storageWork{work(entry(10, term)), work(entry(11, term)), work(entry(12, term)), work(entry(13, term)), work(entry(14, term))}, 4},
		{"nil_head_not_skipped", []*storageWork{nil, work(entry(10, term))}, 1},
		{"nil_middle_not_skipped", []*storageWork{work(entry(10, term)), work(entry(11, term)), nil, work(entry(12, term))}, 2},
		{"same_id_new_generation", []*storageWork{work(entry(10, term)), generation(work(entry(11, term)), reopened)}, 1},
		{"nil_generation", []*storageWork{generation(work(entry(10, term)), nil), work(entry(11, term))}, 1},
		{"captured_term_not_empty_hardstate", []*storageWork{work(entry(10, term)), actualTerm(work(entry(11, term)), term+1)}, 1},
		{"unknown_head_term", []*storageWork{actualTerm(work(entry(10, term)), 0), work(entry(11, term))}, 1},
		{"unknown_tail_term", []*storageWork{work(entry(10, term)), actualTerm(work(entry(11, term)), 0)}, 1},
		{"ordinary_apply_keeps_member_fence", []*storageWork{withApply(work(entry(10, term)), raftpb.EntryNormal), work(entry(11, term))}, 2},
		{"snapshot_head", []*storageWork{snapshot(work(entry(10, term))), work(entry(11, term))}, 1},
		{"snapshot_tail", []*storageWork{work(entry(10, term)), snapshot(work(entry(11, term))), work(entry(12, term))}, 1},
		{"config_head", []*storageWork{work(config(10, raftpb.EntryConfChange)), work(entry(11, term))}, 1},
		{"config_v2_tail", []*storageWork{work(entry(10, term)), work(config(11, raftpb.EntryConfChangeV2)), work(entry(12, term))}, 1},
		{"config_apply_boundary", []*storageWork{work(entry(10, term)), withApply(work(entry(11, term)), raftpb.EntryConfChange), work(entry(12, term))}, 1},
		{"config_v2_apply_head", []*storageWork{withApply(work(entry(10, term)), raftpb.EntryConfChangeV2), work(entry(11, term))}, 1},
		{"apply_only_head", []*storageWork{applyOnly(), work(entry(10, term))}, 1},
		{"apply_only_middle", []*storageWork{work(entry(10, term)), applyOnly(), work(entry(11, term))}, 1},
		{"response_only_head", []*storageWork{work(), work(entry(10, term))}, 1},
		{"response_only_middle", []*storageWork{work(entry(10, term)), work(), work(entry(11, term))}, 1},
		{"head_internal_gap", []*storageWork{work(entry(10, term), entry(12, term)), work(entry(13, term))}, 1},
		{"tail_internal_gap", []*storageWork{work(entry(10, term)), work(entry(11, term), entry(13, term))}, 1},
		{"head_internal_overlap", []*storageWork{work(entry(10, term), entry(10, term)), work(entry(11, term))}, 1},
		{"tail_internal_overlap", []*storageWork{work(entry(10, term)), work(entry(11, term), entry(11, term))}, 1},
		{"cross_member_gap", []*storageWork{work(entry(10, term)), work(entry(12, term)), work(entry(13, term))}, 1},
		{"cross_member_rewrite", []*storageWork{work(entry(10, term), entry(11, term)), work(entry(11, term)), work(entry(12, term))}, 1},
		{"legal_head_replacement_and_tail", []*storageWork{work(entry(9, 3), entry(10, 4)), work(entry(11, 4), entry(12, term))}, 2},
		{"zero_entry_index", []*storageWork{work(entry(0, term)), work(entry(1, term))}, 1},
		{"zero_entry_term", []*storageWork{work(entry(10, 0)), work(entry(11, term))}, 1},
		{"future_entry_term", []*storageWork{work(entry(10, term)), work(entry(11, term+1))}, 1},
		{"internal_term_regression", []*storageWork{work(entry(10, term), entry(11, term-1)), work(entry(12, term))}, 1},
		{"cross_member_term_regression", []*storageWork{work(entry(10, term)), work(entry(11, term-1))}, 1},
		{"maximum_index_is_valid_tail", []*storageWork{work(entry(maxIndex-1, term)), work(entry(maxIndex, term))}, 2},
		{"cross_member_index_overflow", []*storageWork{work(entry(maxIndex, term)), work(entry(0, term))}, 1},
		{"internal_index_overflow", []*storageWork{work(entry(maxIndex, term), entry(0, term)), work(entry(1, term))}, 1},
		{"whole_hardstate_monotonic", []*storageWork{hs(work(entry(10, term)), term, 1, 9), hs(work(), term, 1, 10), hs(work(entry(11, term)), term, 1, 11)}, 3},
		{"empty_hardstate_is_neutral", []*storageWork{hs(work(entry(10, term)), term, 1, 9), work(entry(11, term)), hs(work(entry(12, term)), term, 1, 12)}, 3},
		{"same_zero_vote", []*storageWork{hs(work(entry(10, term)), term, 0, 9), hs(work(entry(11, term)), term, 0, 10)}, 2},
		{"vote_change", []*storageWork{hs(work(entry(10, term)), term, 1, 9), hs(work(entry(11, term)), term, 2, 10)}, 1},
		{"zero_to_nonzero_vote_change", []*storageWork{hs(work(entry(10, term)), term, 0, 9), hs(work(entry(11, term)), term, 1, 10)}, 1},
		{"hardstate_term_must_match_capture", []*storageWork{hs(work(entry(10, term)), term-1, 1, 9), work(entry(11, term))}, 1},
		{"hardstate_tail_term_change", []*storageWork{hs(work(entry(10, term)), term, 1, 9), hs(work(entry(11, term)), term+1, 1, 10)}, 1},
		{"partial_hardstate_not_a_whole_value", []*storageWork{work(entry(10, term)), hs(work(entry(11, term)), 0, 0, 10)}, 1},
		{"commit_regression_across_empty_state", []*storageWork{hs(work(entry(10, term)), term, 1, 9), work(entry(11, term)), hs(work(entry(12, term)), term, 1, 8)}, 2},
		{"head_commit_beyond_present_entries", []*storageWork{hs(work(entry(10, term)), term, 1, 11), work(entry(11, term))}, 1},
		{"later_entry_cannot_justify_earlier_commit", []*storageWork{work(entry(10, term)), hs(work(), term, 1, 11), work(entry(11, term))}, 1},
		{"candidate_commit_beyond_its_entries", []*storageWork{work(entry(10, term)), hs(work(entry(11, term)), term, 1, 12), work(entry(12, term))}, 1},
		{"hardstate_only_history_prefix", []*storageWork{hs(work(), term, 1, 8), hs(work(), term, 1, 9), work(entry(10, term))}, 3},
		{"first_entries_after_unknown_durable_tail", []*storageWork{hs(work(), term, 1, 8), work(entry(10, term))}, 2},
		{"first_entries_overlap_known_commit", []*storageWork{hs(work(), term, 1, 10), work(entry(10, term)), work(entry(11, term))}, 1},
		{"first_entries_precede_known_commit", []*storageWork{hs(work(), term, 1, 10), work(entry(9, term), entry(10, term))}, 1},
		{"hardstate_only_does_not_reset_entry_term", []*storageWork{work(entry(10, term)), hs(work(), term, 1, 10), work(entry(11, term-1))}, 2},
		{"hardstate_only_does_not_reset_contiguity", []*storageWork{work(entry(10, term)), hs(work(), term, 1, 10), work(entry(12, term))}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pointers := append([]*storageWork(nil), tc.works...)
			before := make([]*storageWork, len(tc.works))
			leases := make([]*storageLease, len(tc.works))
			for i, w := range tc.works {
				before[i] = cloneAppendSelectionInput(t, w)
				if w != nil {
					leases[i] = w.lease
				}
			}
			if got := storageAppendBatchSize(tc.works); got != tc.want {
				t.Fatalf("compatible prefix = %d, want %d", got, tc.want)
			}
			for i, w := range tc.works {
				if w != pointers[i] || !reflect.DeepEqual(w, before[i]) {
					t.Fatalf("selector changed owned input %d", i)
				}
				if w != nil && (w.slot != before[i].slot || w.lease != leases[i]) {
					t.Fatalf("selector changed ownership identity %d", i)
				}
			}
		})
	}
}

func TestStorageAppendBatchSizeUsesOnlyFrozenPrefix(t *testing.T) {
	g := &slot{id: 902}
	queue := make([]*storageWork, 3)
	for i := range queue {
		queue[i] = &storageWork{slot: g, acceptedTerm: 5, hasAppend: true,
			append: raftpb.Message{Type: raftpb.MsgStorageAppend, Entries: []raftpb.Entry{{Index: uint64(i + 10), Term: 5, Type: raftpb.EntryNormal}}}}
	}
	// Length, not the backing array's spare capacity, defines the frozen work.
	if got := storageAppendBatchSize(queue[:2]); got != 2 {
		t.Fatalf("frozen two-member prefix = %d, want 2", got)
	}
	if got := storageAppendBatchSize(queue); got != 3 {
		t.Fatalf("next selection with third arrival = %d, want 3", got)
	}
}

// cloneAppendSelectionInput preserves pointer identity for ownership fields and
// independently copies message/lease values to detect a mutating selector.
func cloneAppendSelectionInput(t *testing.T, w *storageWork) *storageWork {
	t.Helper()
	if w == nil {
		return nil
	}
	copied := *w
	copyMessage := func(m raftpb.Message) raftpb.Message {
		data, err := m.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		var result raftpb.Message
		if err := result.Unmarshal(data); err != nil {
			t.Fatal(err)
		}
		return result
	}
	copied.append, copied.apply = copyMessage(w.append), copyMessage(w.apply)
	if w.lease != nil {
		lease := *w.lease
		copied.lease = &lease
	}
	return &copied
}
