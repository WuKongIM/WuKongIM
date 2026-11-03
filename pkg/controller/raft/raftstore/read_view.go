package raftstore

import (
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// storageReadView is immutable after publication. Payloads belong to the
// writer's already-cloned immutable records; only entry headers are independent.
type storageReadView struct {
	// hardState and appliedIndex describe the last successful durable cut.
	hardState    raftpb.HardState
	appliedIndex uint64
	// confState owns all four slices, independently of mutable writer metadata.
	confState raftpb.ConfState
	// snapshot borrows immutable writer-owned snapshot data and metadata.
	snapshot raftpb.Snapshot
	// entries owns headers; future writer suffix compaction cannot overwrite them.
	entries []raftpb.Entry
}

var emptyStorageReadView storageReadView

// publishReadViewLocked runs during construction or after successful physical
// persistence under mu. MarkApplied and hard-state-only writes reuse the suffix,
// avoiding log/snapshot payload copies on every applied watermark.
func (s *Store) publishReadViewLocked() {
	previous := s.readState.Load()
	var entries []raftpb.Entry
	if !s.entriesDirty && previous != nil {
		entries = previous.entries
	} else {
		entries = append([]raftpb.Entry(nil), s.entries...)
	}
	s.readState.Store(&storageReadView{
		hardState: s.meta.HardState, appliedIndex: s.meta.AppliedIndex,
		confState: cloneConfState(s.meta.ConfState), snapshot: s.snapshot, entries: entries,
	})
	s.entriesDirty = false
}

func (s *Store) currentReadView() *storageReadView {
	if view := s.readState.Load(); view != nil {
		return view
	}
	return &emptyStorageReadView
}

// AppliedIndex returns the durable local applied Raft index.
func (s *Store) AppliedIndex() uint64 {
	view := s.currentReadView()
	return view.appliedIndex
}

// InitialState returns the durable Raft hard state and latest known conf state.
func (s *Store) InitialState() (raftpb.HardState, raftpb.ConfState, error) {
	view := s.currentReadView()
	return view.hardState, cloneConfState(view.confState), nil
}

// Entries returns a range of Raft log entries.
func (s *Store) Entries(lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	view := s.currentReadView()
	if lo <= view.snapshot.Metadata.Index {
		return nil, raft.ErrCompacted
	}
	if hi <= lo {
		return nil, nil
	}
	out := make([]raftpb.Entry, 0, hi-lo)
	var size uint64
	for _, entry := range view.entries {
		if entry.Index < lo {
			continue
		}
		if entry.Index >= hi {
			break
		}
		if maxSize > 0 && len(out) > 0 && size+uint64(entry.Size()) > maxSize {
			break
		}
		size += uint64(entry.Size())
		out = append(out, cloneEntry(entry))
	}
	if len(out) == 0 && hi > lo {
		return nil, raft.ErrUnavailable
	}
	if out[0].Index != lo {
		return nil, raft.ErrUnavailable
	}
	return out, nil
}

// Term returns the term for a Raft log index.
func (s *Store) Term(index uint64) (uint64, error) {
	view := s.currentReadView()
	if index == view.snapshot.Metadata.Index {
		return view.snapshot.Metadata.Term, nil
	}
	if index < view.snapshot.Metadata.Index {
		return 0, raft.ErrCompacted
	}
	for _, entry := range view.entries {
		if entry.Index == index {
			return entry.Term, nil
		}
	}
	return 0, raft.ErrUnavailable
}

// FirstIndex returns the first available log index after the snapshot.
func (s *Store) FirstIndex() (uint64, error) {
	view := s.currentReadView()
	if view.snapshot.Metadata.Index > 0 {
		return view.snapshot.Metadata.Index + 1, nil
	}
	if len(view.entries) > 0 {
		return view.entries[0].Index, nil
	}
	return 1, nil
}

// LastIndex returns the last available log index or snapshot index.
func (s *Store) LastIndex() (uint64, error) {
	view := s.currentReadView()
	if len(view.entries) > 0 {
		return view.entries[len(view.entries)-1].Index, nil
	}
	return view.snapshot.Metadata.Index, nil
}

// Snapshot returns the latest durable snapshot.
func (s *Store) Snapshot() (raftpb.Snapshot, error) {
	view := s.currentReadView()
	return cloneSnapshot(view.snapshot), nil
}
