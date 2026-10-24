package multiraft

import (
	"context"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// storageAppendBatchSize selects only a compatible prefix of already-owned
// work. It never skips a barrier or inspects mutable RawNode/durable state.
// Each input is an independently valid accepted Ready; an unknown head stays
// on the original single-work path. The caller freezes at most four pointers.
func storageAppendBatchSize(works []*storageWork) int {
	if len(works) < 2 {
		return len(works)
	}
	head := works[0]
	if head == nil || head.slot == nil || head.acceptedTerm == 0 {
		return 1
	}
	var lastIndex, lastTerm uint64
	var lastState raftpb.HardState
	haveState := false
	accepted := 0
members:
	for _, w := range works[:min(len(works), storageSlotOwners)] {
		if w == nil || w.slot != head.slot || w.acceptedTerm != head.acceptedTerm ||
			!w.hasAppend || w.append.Snapshot != nil {
			break
		}
		for _, e := range w.apply.Entries {
			if e.Type != raftpb.EntryNormal {
				break members
			}
		}
		hs := raftpb.HardState{Term: w.append.Term, Vote: w.append.Vote, Commit: w.append.Commit}
		nonempty := !raft.IsEmptyHardState(hs)
		if len(w.append.Entries) == 0 && !nonempty {
			break // A response-only append still orders its original responses.
		}
		if nonempty && (hs.Term != head.acceptedTerm ||
			(haveState && (hs.Term != lastState.Term || hs.Vote != lastState.Vote || hs.Commit < lastState.Commit))) {
			break
		}
		nextIndex, nextTerm := lastIndex, lastTerm
		for _, e := range w.append.Entries {
			if e.Type != raftpb.EntryNormal || e.Index == 0 || e.Term == 0 || e.Term > head.acceptedTerm || e.Term < nextTerm {
				break members
			}
			if nextIndex != 0 {
				if nextIndex == ^uint64(0) || e.Index != nextIndex+1 {
					break members
				}
			} else if haveState && e.Index <= lastState.Commit {
				// HS-only work may refer to older durable history. The first
				// following entries must not replace its committed prefix.
				break members
			}
			nextIndex, nextTerm = e.Index, e.Term
		}
		if nonempty && nextIndex != 0 && hs.Commit > nextIndex {
			break // Later entries must not conceal an earlier invalid commit.
		}
		lastIndex, lastTerm = nextIndex, nextTerm
		if nonempty {
			lastState, haveState = hs, true
		}
		accepted++
	}
	return max(1, accepted)
}

// storageAppendMemberState publishes only this member's original durable view.
// It borrows the already charged message headers, never the aggregate Save.
func storageAppendMemberState(w *storageWork) PersistentState {
	persist := PersistentState{Entries: w.append.Entries}
	hs := raftpb.HardState{Term: w.append.Term, Vote: w.append.Vote, Commit: w.append.Commit}
	if !raft.IsEmptyHardState(hs) {
		persist.HardState = &hs
	}
	return persist
}

// runStorageAppendBatch replaces only physical persistence for a frozen safe
// prefix. Original responses, apply work and leases remain separate. Its one
// compact header slice uses the per-entry Save reservation; payloads are shared.
func (g *slot) runStorageAppendBatch(ctx context.Context, works []*storageWork) error {
	if err := g.currentErr(); err != nil {
		return err
	}
	release, err := g.acquireSave(ctx, nil)
	if err != nil {
		return err
	}
	defer release()
	if err := g.currentErr(); err != nil {
		return err
	}
	var hs raftpb.HardState
	var stageEntries []raftpb.Entry
	entriesCount := 0
	for _, w := range works {
		entriesCount += len(w.append.Entries)
		if len(w.append.Entries) != 0 {
			stageEntries = w.append.Entries
		}
		state := raftpb.HardState{Term: w.append.Term, Vote: w.append.Vote, Commit: w.append.Commit}
		if !raft.IsEmptyHardState(state) {
			hs = state // Preserve the last complete state, never field-wise maxima.
		}
	}
	stage := readySaveStage(raft.Ready{Entries: stageEntries, HardState: hs})
	if g.readBarrierTracing {
		g.beginReadReadyOverlay(stage, false)
	}
	persist := PersistentState{}
	if entriesCount != 0 {
		persist.Entries = make([]raftpb.Entry, entriesCount)
		offset := 0
		for _, w := range works {
			offset += copy(persist.Entries[offset:], w.append.Entries)
		}
	}
	if !raft.IsEmptyHardState(hs) {
		persist.HardState = &hs
	}
	err = g.storageView.storage.Save(ctx, persist)
	// Drop our aggregate view before returning to completion publication. Save
	// implementations may retain their input: never clear its backing array.
	// Do not pass this slice through another cloning adapter.
	persist = PersistentState{}
	if g.readBarrierTracing {
		g.endReadReadyOverlay(stage, err, false)
	}
	if err == nil {
		for _, w := range works {
			g.observePersistedStorageWork(w)
		}
	}
	return err
}
