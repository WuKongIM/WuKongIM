package raftlog

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/cockroachdb/pebble/v2"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type writeRequest struct {
	scope Scope
	op    writeOp
	done  chan error
}

var errDBClosing = errors.New("raftstorage: db closing")

var errWriteNotEnqueued = errors.New("raftstorage: write request not enqueued")

// writeNotEnqueuedError preserves the user-visible cause while classifying pre-worker failures.
type writeNotEnqueuedError struct {
	cause error
}

func (e writeNotEnqueuedError) Error() string {
	return e.cause.Error()
}

func (e writeNotEnqueuedError) Unwrap() error {
	return e.cause
}

func (e writeNotEnqueuedError) Is(target error) bool {
	return target == errWriteNotEnqueued
}

type writeOp interface {
	apply(batch *pebble.Batch, state *scopeWriteState, store *pebbleStore) error
}

// persistentWriteState is the worker-safe form of a raft write request.
type persistentWriteState struct {
	// HardState is the optional raft hard state to persist.
	HardState *raftpb.HardState
	// Entries are log entries retained after snapshot compaction filtering.
	Entries []raftpb.Entry
	// Snapshot is metadata-only snapshot state; payload bytes are never carried here.
	Snapshot *raftpb.SnapshotMetadata
	// SnapshotManifest is the final external payload manifest written to Pebble.
	SnapshotManifest *SnapshotManifest
}

type saveOp struct {
	state persistentWriteState
}

type markAppliedOp struct {
	index uint64
}

type markConfigAppliedOp struct {
	index uint64
}

type scopeWriteState struct {
	hardState        raftpb.HardState
	snapshot         raftpb.Snapshot
	snapshotManifest *SnapshotManifest
	entries          []raftpb.Entry
	meta             logMeta
}

func (db *DB) submitWrite(req *writeRequest) error {
	db.mu.Lock()
	if db.closing {
		db.mu.Unlock()
		return writeNotEnqueuedError{cause: errDBClosing}
	}
	db.writeCh <- req
	db.mu.Unlock()
	return <-req.done
}

func (db *DB) runWriteWorker() {
	defer db.workerWG.Done()

	for {
		req, ok := <-db.writeCh
		if !ok {
			return
		}

		reqs := []*writeRequest{req}
		closed := false
		for {
			select {
			case next, ok := <-db.writeCh:
				if !ok {
					closed = true
					goto flush
				}
				reqs = append(reqs, next)
			default:
				goto flush
			}
		}

	flush:
		err := db.flushWriteRequests(reqs)
		for _, req := range reqs {
			req.done <- err
			close(req.done)
		}
		if closed {
			return
		}
	}
}

func (db *DB) flushWriteRequests(reqs []*writeRequest) error {
	batch := db.db.NewBatch()
	defer batch.Close()

	stateCache := make(map[Scope]*scopeWriteState, len(reqs))
	for _, req := range reqs {
		state, err := db.loadScopeWriteState(stateCache, req.scope)
		if err != nil {
			return err
		}
		if req.op != nil {
			if err := req.op.apply(batch, state, &pebbleStore{db: db, scope: req.scope}); err != nil {
				return err
			}
		}
	}

	if db.writeCommitTestHook != nil {
		if err := db.writeCommitTestHook(); err != nil {
			return err
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return err
	}
	for scope, state := range stateCache {
		db.stateCache[scope] = cloneScopeWriteState(*state, false)
	}
	return nil
}

func (db *DB) loadScopeWriteState(cache map[Scope]*scopeWriteState, scope Scope) (*scopeWriteState, error) {
	if state, ok := cache[scope]; ok {
		return state, nil
	}

	if cached, ok := db.stateCache[scope]; ok {
		// Save operations replace the entries slice copy-on-write, so cached
		// entry payloads can be retained without deep-copying every append.
		state := cloneScopeWriteState(cached, false)
		cache[scope] = &state
		return &state, nil
	}

	store := &pebbleStore{db: db, scope: scope}
	view, err := store.loadSnapshotMetaView(context.Background())
	if err != nil {
		return nil, err
	}
	meta := view.meta
	manifest := view.manifest
	hasManifest := view.hasManifest
	hardState, err := store.loadHardState()
	if err != nil {
		return nil, err
	}
	entries, err := store.loadEntries(0, 0)
	if err != nil {
		return nil, err
	}

	state := &scopeWriteState{
		hardState: hardState,
		entries:   entries,
		meta:      meta,
	}
	if hasManifest {
		state.snapshot = snapshotFromManifest(manifest)
		state.snapshotManifest = cloneSnapshotManifestPtr(&manifest)
	}
	cache[scope] = state
	return state, nil
}

func cloneScopeWriteState(state scopeWriteState, cloneEntries bool) scopeWriteState {
	cloned := scopeWriteState{
		hardState:        state.hardState,
		snapshot:         cloneSnapshot(state.snapshot),
		snapshotManifest: cloneSnapshotManifestPtr(state.snapshotManifest),
		meta:             state.meta,
	}
	cloned.meta.ConfState = cloneConfState(state.meta.ConfState)
	if cloneEntries {
		cloned.entries = make([]raftpb.Entry, 0, len(state.entries))
		for _, entry := range state.entries {
			cloned.entries = append(cloned.entries, cloneEntry(entry))
		}
	} else {
		cloned.entries = state.entries
	}
	return cloned
}

func (op saveOp) apply(batch *pebble.Batch, state *scopeWriteState, store *pebbleStore) error {
	scope := store.scope
	st := op.state
	hs := state.hardState
	persistHardState := false
	if st.HardState != nil {
		hs = *st.HardState
		persistHardState = true
	} else if st.Snapshot != nil {
		persistHardState = true
	}

	snapshotIndex := uint64(0)
	if st.Snapshot != nil {
		if st.SnapshotManifest == nil {
			return errors.New("raftstorage: snapshot save missing manifest")
		}
		if st.Snapshot.Index < state.snapshot.Metadata.Index {
			return raft.ErrSnapOutOfDate
		}
		if st.Snapshot.Index == state.snapshot.Metadata.Index && state.snapshotManifest != nil && !snapshotManifestEquivalent(*state.snapshotManifest, *st.SnapshotManifest) {
			return errors.New("raftstorage: same-index snapshot manifest mismatch")
		}
		encoded, err := encodeSnapshotManifest(scope, *st.SnapshotManifest)
		if err != nil {
			return err
		}
		if err := batch.Set(encodeSnapshotKey(scope), encoded, nil); err != nil {
			return err
		}
		if st.Snapshot.Index < math.MaxUint64 {
			if err := batch.DeleteRange(encodeEntryPrefix(scope), encodeEntryKey(scope, st.Snapshot.Index+1), nil); err != nil {
				return err
			}
		} else {
			if err := batch.DeleteRange(encodeEntryPrefix(scope), encodeEntryPrefixEnd(scope), nil); err != nil {
				return err
			}
		}
		if hs.Commit < st.Snapshot.Index {
			hs.Commit = st.Snapshot.Index
		}
		state.snapshot = raftpb.Snapshot{Metadata: cloneSnapshotMetadata(*st.Snapshot)}
		state.snapshotManifest = cloneSnapshotManifestPtr(st.SnapshotManifest)
		state.entries = trimEntriesAfterSnapshot(state.entries, st.Snapshot.Index)
		snapshotIndex = st.Snapshot.Index
	}

	entries := st.Entries
	if snapshotIndex > 0 {
		entries = filterEntriesAfterSnapshot(entries, snapshotIndex)
	}
	if len(entries) > 0 {
		first := entries[0].Index
		if err := batch.DeleteRange(encodeEntryKey(scope, first), encodeEntryPrefixEnd(scope), nil); err != nil {
			return err
		}
		for _, entry := range entries {
			data, err := entry.Marshal()
			if err != nil {
				return err
			}
			if err := batch.Set(encodeEntryKey(scope, entry.Index), data, nil); err != nil {
				return err
			}
		}
		state.entries = replaceEntriesFromIndex(state.entries, first, entries)
	}

	if persistHardState {
		data, err := hs.Marshal()
		if err != nil {
			return err
		}
		if err := batch.Set(encodeHardStateKey(scope), data, nil); err != nil {
			return err
		}
	}

	state.hardState = hs
	if err := updateLogMeta(&state.meta, state.snapshot, state.entries, state.hardState.Commit); err != nil {
		return err
	}
	return store.setMeta(batch, state.meta)
}

func (op markAppliedOp) apply(batch *pebble.Batch, state *scopeWriteState, store *pebbleStore) error {
	scope := store.scope
	value := make([]byte, appliedIndexSize)
	binary.BigEndian.PutUint64(value, op.index)
	if err := batch.Set(encodeAppliedIndexKey(scope), value, nil); err != nil {
		return err
	}

	state.meta.AppliedIndex = op.index
	return store.setMeta(batch, state.meta)
}

func (op markConfigAppliedOp) apply(batch *pebble.Batch, state *scopeWriteState, store *pebbleStore) error {
	_ = state
	value := make([]byte, appliedIndexSize)
	binary.BigEndian.PutUint64(value, op.index)
	return batch.Set(encodeConfigAppliedIndexKey(store.scope), value, nil)
}

func withoutSnapshotData(st multiraft.PersistentState) persistentWriteState {
	out := persistentWriteState{}
	if st.HardState != nil {
		hs := *st.HardState
		out.HardState = &hs
	}
	if len(st.Entries) > 0 {
		out.Entries = make([]raftpb.Entry, 0, len(st.Entries))
		for _, entry := range st.Entries {
			out.Entries = append(out.Entries, cloneEntry(entry))
		}
	}
	if st.Snapshot != nil {
		metadata := cloneSnapshotMetadata(st.Snapshot.Metadata)
		out.Snapshot = &metadata
	}
	return out
}

func filterEntriesAfterSnapshot(entries []raftpb.Entry, snapshotIndex uint64) []raftpb.Entry {
	if len(entries) == 0 {
		return nil
	}
	retained := make([]raftpb.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Index <= snapshotIndex {
			continue
		}
		retained = append(retained, cloneEntry(entry))
	}
	return retained
}

func snapshotManifestEquivalent(a, b SnapshotManifest) bool {
	return a.Index == b.Index &&
		a.Term == b.Term &&
		a.TotalSize == b.TotalSize &&
		bytes.Equal(a.WholeChecksum, b.WholeChecksum) &&
		confStateEqual(a.ConfState, b.ConfState)
}

func cloneSnapshotMetadata(metadata raftpb.SnapshotMetadata) raftpb.SnapshotMetadata {
	metadata.ConfState = cloneConfState(metadata.ConfState)
	return metadata
}

func cloneSnapshotManifestPtr(manifest *SnapshotManifest) *SnapshotManifest {
	if manifest == nil {
		return nil
	}
	cloned := *manifest
	cloned.ConfState = cloneConfState(manifest.ConfState)
	cloned.WholeChecksum = append([]byte(nil), manifest.WholeChecksum...)
	if len(manifest.ChunkChecksums) > 0 {
		cloned.ChunkChecksums = make([][]byte, len(manifest.ChunkChecksums))
		for i := range manifest.ChunkChecksums {
			cloned.ChunkChecksums[i] = append([]byte(nil), manifest.ChunkChecksums[i]...)
		}
	}
	return &cloned
}
