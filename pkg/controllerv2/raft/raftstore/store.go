package raftstore

import (
	"context"
	"path/filepath"
	"sync"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// Store persists ControllerV2 Raft WAL, snapshots, and applied metadata.
type Store struct {
	mu sync.RWMutex

	cfg      Config
	wal      *wal
	metaPath string
	snapDir  string

	meta     metadata
	snapshot raftpb.Snapshot
	entries  []raftpb.Entry
}

// Open opens or creates a ControllerV2 Raft store rooted at cfg.Dir.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	walDir := filepath.Join(cfg.Dir, "wal")
	snapDir := filepath.Join(cfg.Dir, "snap")
	metaPath := filepath.Join(cfg.Dir, "meta.json")
	meta, err := loadMetadata(metaPath)
	if err != nil {
		return nil, err
	}
	if meta.Version == 0 {
		meta = metadata{Version: metadataVersion, NodeID: cfg.NodeID}
	}
	if meta.NodeID == 0 {
		meta.NodeID = cfg.NodeID
	}
	if meta.ConfState.Voters == nil {
		meta.ConfState = cloneConfState(meta.ConfState)
	}
	w, err := openWAL(walConfig{Dir: walDir, NodeID: cfg.NodeID, SegmentSize: cfg.SegmentSize})
	if err != nil {
		return nil, err
	}
	replayed, err := w.replay()
	if err != nil {
		_ = w.close()
		return nil, err
	}
	snapshot, err := loadSnapshotFile(meta.Snapshot.Path)
	if err != nil {
		_ = w.close()
		return nil, err
	}
	if replayed.Snapshot.Index > snapshot.Metadata.Index {
		path := filepath.Join(snapDir, snapshotFileName(replayed.Snapshot.Index, replayed.Snapshot.Term))
		snapshot, err = loadSnapshotFile(path)
		if err != nil {
			_ = w.close()
			return nil, err
		}
		if snapshot.Metadata.Index == 0 {
			snapshot = raftpb.Snapshot{Metadata: cloneSnapshotMetadata(replayed.Snapshot)}
		}
		meta.Snapshot = snapshotMeta{Index: replayed.Snapshot.Index, Term: replayed.Snapshot.Term, Path: path}
	}
	if !raft.IsEmptyHardState(replayed.HardState) {
		meta.HardState = replayed.HardState
	}
	if replayed.AppliedIndex > meta.AppliedIndex {
		meta.AppliedIndex = replayed.AppliedIndex
	}
	if replayed.ConfState.Voters != nil || len(replayed.ConfState.Learners) > 0 {
		meta.ConfState = cloneConfState(replayed.ConfState)
	} else if snapshot.Metadata.Index > 0 {
		meta.ConfState = cloneConfState(snapshot.Metadata.ConfState)
	}
	entries := trimEntriesAfter(cloneEntries(replayed.Entries), snapshot.Metadata.Index)
	store := &Store{cfg: cfg, wal: w, metaPath: metaPath, snapDir: snapDir, meta: meta, snapshot: snapshot, entries: entries}
	if err := saveMetadata(ctx, metaPath, meta); err != nil {
		_ = w.close()
		return nil, err
	}
	return store, nil
}

// Close closes the underlying WAL segment.
func (s *Store) Close() error {
	if s == nil || s.wal == nil {
		return nil
	}
	return s.wal.close()
}

// SaveReady persists a Raft Ready's durable state and updates the in-memory storage view.
func (s *Store) SaveReady(ctx context.Context, hs raftpb.HardState, entries []raftpb.Entry, snap raftpb.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta := raftpb.SnapshotMetadata{}
	if !raft.IsEmptySnap(snap) {
		meta = cloneSnapshotMetadata(snap.Metadata)
	}
	if err := s.wal.appendReady(ctx, hs, entries, meta); err != nil {
		return err
	}
	if !raft.IsEmptyHardState(hs) {
		s.meta.HardState = hs
	}
	if !raft.IsEmptySnap(snap) {
		path, err := saveSnapshotFile(ctx, s.snapDir, snap)
		if err != nil {
			return err
		}
		s.snapshot = cloneSnapshot(snap)
		s.entries = trimEntriesAfter(s.entries, snap.Metadata.Index)
		s.meta.Snapshot = snapshotMeta{Index: snap.Metadata.Index, Term: snap.Metadata.Term, Path: path}
		s.meta.ConfState = cloneConfState(snap.Metadata.ConfState)
	}
	if len(entries) > 0 {
		first := entries[0].Index
		kept := s.entries[:0]
		for _, entry := range s.entries {
			if entry.Index < first {
				kept = append(kept, entry)
			}
		}
		s.entries = append(kept, cloneEntries(entries)...)
		for _, entry := range entries {
			if err := applyConfEntry(&s.meta.ConfState, entry); err != nil {
				return err
			}
		}
	}
	return saveMetadata(ctx, s.metaPath, s.meta)
}

// MarkAppliedBatch durably advances the local applied index.
func (s *Store) MarkAppliedBatch(ctx context.Context, index uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index <= s.meta.AppliedIndex {
		return nil
	}
	if err := s.wal.appendAppliedIndex(ctx, index); err != nil {
		return err
	}
	s.meta.AppliedIndex = index
	return saveMetadata(ctx, s.metaPath, s.meta)
}

// SaveSnapshot persists a ControllerV2 snapshot and records it in the WAL.
func (s *Store) SaveSnapshot(ctx context.Context, snap raftpb.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := saveSnapshotFile(ctx, s.snapDir, snap)
	if err != nil {
		return err
	}
	if err := s.wal.appendReady(ctx, raftpb.HardState{}, nil, snap.Metadata); err != nil {
		return err
	}
	s.snapshot = cloneSnapshot(snap)
	s.entries = trimEntriesAfter(s.entries, snap.Metadata.Index)
	s.meta.Snapshot = snapshotMeta{Index: snap.Metadata.Index, Term: snap.Metadata.Term, Path: path}
	s.meta.ConfState = cloneConfState(snap.Metadata.ConfState)
	if s.meta.HardState.Commit < snap.Metadata.Index {
		s.meta.HardState.Commit = snap.Metadata.Index
	}
	return saveMetadata(ctx, s.metaPath, s.meta)
}

// Compact removes entries at or below compactTo from the in-memory suffix and old WAL segments.
func (s *Store) Compact(ctx context.Context, compactTo uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = trimEntriesAfter(s.entries, compactTo)
	return s.wal.releaseBefore(compactTo + 1)
}

// AppliedIndex returns the durable local applied Raft index.
func (s *Store) AppliedIndex() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.meta.AppliedIndex
}

// InitialState returns the durable Raft hard state and latest known conf state.
func (s *Store) InitialState() (raftpb.HardState, raftpb.ConfState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.meta.HardState, cloneConfState(s.meta.ConfState), nil
}

// Entries returns a range of Raft log entries.
func (s *Store) Entries(lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if lo <= s.snapshot.Metadata.Index {
		return nil, raft.ErrCompacted
	}
	if hi <= lo {
		return nil, nil
	}
	out := make([]raftpb.Entry, 0, hi-lo)
	var size uint64
	for _, entry := range s.entries {
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	if index == s.snapshot.Metadata.Index {
		return s.snapshot.Metadata.Term, nil
	}
	if index < s.snapshot.Metadata.Index {
		return 0, raft.ErrCompacted
	}
	for _, entry := range s.entries {
		if entry.Index == index {
			return entry.Term, nil
		}
	}
	return 0, raft.ErrUnavailable
}

// FirstIndex returns the first available log index after the snapshot.
func (s *Store) FirstIndex() (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapshot.Metadata.Index > 0 {
		return s.snapshot.Metadata.Index + 1, nil
	}
	if len(s.entries) > 0 {
		return s.entries[0].Index, nil
	}
	return 1, nil
}

// LastIndex returns the last available log index or snapshot index.
func (s *Store) LastIndex() (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) > 0 {
		return s.entries[len(s.entries)-1].Index, nil
	}
	return s.snapshot.Metadata.Index, nil
}

// Snapshot returns the latest durable snapshot.
func (s *Store) Snapshot() (raftpb.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSnapshot(s.snapshot), nil
}
