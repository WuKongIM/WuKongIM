package raftstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// Store persists Controller Raft WAL, snapshots, and applied metadata.
type Store struct {
	mu sync.RWMutex
	// readState publishes a complete successful durable cut independently of
	// writer I/O. Readers never retain file ownership or mutable writer headers.
	readState atomic.Pointer[storageReadView]
	// entriesDirty is writer-owned and survives failed persistence. Empty
	// operations never publish it or retire the previously durable read cut.
	entriesDirty bool

	cfg      Config
	wal      *wal
	metaPath string
	snapDir  string

	meta     metadata
	snapshot raftpb.Snapshot
	entries  []raftpb.Entry
}

// Open opens or creates a Controller Raft store rooted at cfg.Dir.
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
	wc := walConfig{Dir: walDir, NodeID: cfg.NodeID, SegmentSize: cfg.SegmentSize}
	w, err := openWAL(wc)
	if errors.Is(err, ErrCRCMismatch) {
		w, err = recoverLegacyPrefix(ctx, cfg, wc, meta, err)
	}
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
	store := &Store{cfg: cfg, wal: w, metaPath: metaPath, snapDir: snapDir, meta: meta, snapshot: snapshot, entries: entries, entriesDirty: true}
	if err := saveMetadata(ctx, metaPath, meta); err != nil {
		_ = w.close()
		return nil, err
	}
	store.publishReadViewLocked()
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
	// Heartbeat/read-index Ready can contain no durable state. It must not
	// wait behind unrelated applied metadata I/O, or publish a failed writer cut.
	if raft.IsEmptyHardState(hs) && len(entries) == 0 && raft.IsEmptySnap(snap) {
		return nil
	}
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
		s.entriesDirty = true
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
		s.entriesDirty = true
		for _, entry := range entries {
			if err := applyConfEntry(&s.meta.ConfState, entry); err != nil {
				return err
			}
		}
	}
	if err := saveMetadata(ctx, s.metaPath, s.meta); err != nil {
		return err
	}
	s.publishReadViewLocked()
	return nil
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
	if err := saveMetadata(ctx, s.metaPath, s.meta); err != nil {
		return err
	}
	s.publishReadViewLocked()
	return nil
}

// SaveSnapshot persists a Controller snapshot and records it in the WAL.
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
	s.entriesDirty = true
	s.meta.Snapshot = snapshotMeta{Index: snap.Metadata.Index, Term: snap.Metadata.Term, Path: path}
	s.meta.ConfState = cloneConfState(snap.Metadata.ConfState)
	if s.meta.HardState.Commit < snap.Metadata.Index {
		s.meta.HardState.Commit = snap.Metadata.Index
	}
	if err := saveMetadata(ctx, s.metaPath, s.meta); err != nil {
		return err
	}
	s.publishReadViewLocked()
	return nil
}

// Compact removes entries at or below compactTo from the in-memory suffix and old WAL segments.
func (s *Store) Compact(ctx context.Context, compactTo uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if compactTo > s.snapshot.Metadata.Index {
		return fmt.Errorf("controller/raftstore: compaction exceeds durable snapshot")
	}
	s.entries = trimEntriesAfter(s.entries, compactTo)
	s.entriesDirty = true
	if err := s.wal.releaseBefore(compactTo + 1); err != nil {
		return err
	}
	// SaveSnapshot already published the logical compaction boundary. Physical
	// prefix removal cannot publish metadata left dirty by an earlier failure.
	return nil
}
