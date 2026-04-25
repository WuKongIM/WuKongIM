package raftlog

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/cockroachdb/pebble/v2"
	"go.etcd.io/raft/v3/raftpb"
)

type writeRequest struct {
	scope Scope
	op    writeOp
	done  chan error
}

type writeOp interface {
	apply(batch *pebble.Batch, state *scopeWriteState, store *pebbleStore) error
}

type saveOp struct {
	state multiraft.PersistentState
}

type markAppliedOp struct {
	index uint64
}

type scopeWriteState struct {
	hardState raftpb.HardState
	snapshot  raftpb.Snapshot
	entries   []raftpb.Entry
	meta      logMeta
}

func (db *DB) submitWrite(req *writeRequest) error {
	db.mu.Lock()
	if db.closing {
		db.mu.Unlock()
		return errors.New("raftstorage: db closing")
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

	scopeCloneEntries := make(map[Scope]bool, len(reqs))
	for _, req := range reqs {
		if req == nil || req.op == nil {
			continue
		}
		scopeCloneEntries[req.scope] = scopeCloneEntries[req.scope] || writeOpTouchesEntries(req.op)
	}

	stateCache := make(map[Scope]*scopeWriteState, len(reqs))
	for _, req := range reqs {
		state, err := db.loadScopeWriteState(stateCache, req.scope, scopeCloneEntries[req.scope])
		if err != nil {
			return err
		}
		if req.op != nil {
			if err := req.op.apply(batch, state, &pebbleStore{db: db, scope: req.scope}); err != nil {
				return err
			}
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

func (db *DB) loadScopeWriteState(cache map[Scope]*scopeWriteState, scope Scope, cloneEntries bool) (*scopeWriteState, error) {
	if state, ok := cache[scope]; ok {
		return state, nil
	}

	if cached, ok := db.stateCache[scope]; ok {
		state := cloneScopeWriteState(cached, cloneEntries)
		cache[scope] = &state
		return &state, nil
	}

	store := &pebbleStore{db: db, scope: scope}
	meta, _, err := store.currentMeta()
	if err != nil {
		return nil, err
	}
	snapshot, err := store.loadSnapshot()
	if err != nil {
		return nil, err
	}
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
		snapshot:  snapshot,
		entries:   entries,
		meta:      meta,
	}
	cache[scope] = state
	return state, nil
}

func writeOpTouchesEntries(op writeOp) bool {
	switch typed := op.(type) {
	case saveOp:
		return len(typed.state.Entries) > 0 || typed.state.Snapshot != nil
	default:
		return false
	}
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

	if st.Snapshot != nil {
		data, err := st.Snapshot.Marshal()
		if err != nil {
			return err
		}
		if err := batch.Set(encodeSnapshotKey(scope), data, nil); err != nil {
			return err
		}
		if st.Snapshot.Metadata.Index < math.MaxUint64 {
			if err := batch.DeleteRange(
				encodeEntryPrefix(scope),
				encodeEntryKey(scope, st.Snapshot.Metadata.Index+1),
				nil,
			); err != nil {
				return err
			}
		} else {
			if err := batch.DeleteRange(
				encodeEntryPrefix(scope),
				encodeEntryPrefixEnd(scope),
				nil,
			); err != nil {
				return err
			}
		}
		if hs.Commit < st.Snapshot.Metadata.Index {
			hs.Commit = st.Snapshot.Metadata.Index
		}
		state.snapshot = cloneSnapshot(*st.Snapshot)
		state.entries = trimEntriesAfterSnapshot(state.entries, st.Snapshot.Metadata.Index)
	}

	if len(st.Entries) > 0 {
		first := st.Entries[0].Index
		if err := batch.DeleteRange(
			encodeEntryKey(scope, first),
			encodeEntryPrefixEnd(scope),
			nil,
		); err != nil {
			return err
		}
		for _, entry := range st.Entries {
			data, err := entry.Marshal()
			if err != nil {
				return err
			}
			if err := batch.Set(encodeEntryKey(scope, entry.Index), data, nil); err != nil {
				return err
			}
		}
		state.entries = replaceEntriesFromIndex(state.entries, first, st.Entries)
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

func cloneScopeWriteState(state scopeWriteState, cloneEntries bool) scopeWriteState {
	cloned := scopeWriteState{
		hardState: state.hardState,
		snapshot:  cloneSnapshot(state.snapshot),
		meta: logMeta{
			FirstIndex:    state.meta.FirstIndex,
			LastIndex:     state.meta.LastIndex,
			AppliedIndex:  state.meta.AppliedIndex,
			SnapshotIndex: state.meta.SnapshotIndex,
			SnapshotTerm:  state.meta.SnapshotTerm,
			ConfState:     cloneConfState(state.meta.ConfState),
		},
	}
	if len(state.entries) > 0 {
		if cloneEntries {
			cloned.entries = make([]raftpb.Entry, len(state.entries))
			for i, entry := range state.entries {
				cloned.entries[i] = cloneEntry(entry)
			}
		} else {
			cloned.entries = state.entries
		}
	}
	return cloned
}
