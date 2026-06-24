package raftlog

import (
	"bytes"
	"context"
	"errors"
	"os"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type pebbleStore struct {
	db    *DB
	scope Scope
}

// snapshotSavePlan describes the metadata and external IO needed for a snapshot save.
type snapshotSavePlan struct {
	// Metadata is the incoming snapshot metadata without payload bytes.
	Metadata raftpb.SnapshotMetadata
	// ExistingManifest is set when a same-index snapshot is already durably stored.
	ExistingManifest *SnapshotManifest
	// NeedsExternalWrite is true when payload chunks must be staged externally.
	NeedsExternalWrite bool
}

func (s *pebbleStore) InitialState(ctx context.Context) (multiraft.BootstrapState, error) {
	_ = ctx

	meta, ok, err := s.ensureMeta()
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	configAppliedIndex, err := s.loadConfigAppliedIndex()
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	if ok {
		hs, err := s.loadHardState()
		if err != nil {
			return multiraft.BootstrapState{}, err
		}
		return multiraft.BootstrapState{
			HardState:          hs,
			ConfState:          cloneConfState(meta.ConfState),
			AppliedIndex:       meta.AppliedIndex,
			ConfigAppliedIndex: configAppliedIndex,
		}, nil
	}

	hs, err := s.loadHardState()
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	appliedIndex, err := s.loadAppliedIndex()
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	entries, err := s.loadEntries(0, 0)
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	confState, err := deriveConfState(raftpb.Snapshot{}, entries, hs.Commit)
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	return multiraft.BootstrapState{
		HardState:          hs,
		ConfState:          confState,
		AppliedIndex:       appliedIndex,
		ConfigAppliedIndex: configAppliedIndex,
	}, nil
}

func (s *pebbleStore) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	_ = ctx

	entries, err := s.loadEntries(lo, hi)
	if err != nil {
		return nil, err
	}

	var (
		out  []raftpb.Entry
		size uint64
	)
	for _, entry := range entries {
		if maxSize > 0 && len(out) > 0 && size+uint64(entry.Size()) > maxSize {
			break
		}
		size += uint64(entry.Size())
		out = append(out, cloneEntry(entry))
	}
	return out, nil
}

func (s *pebbleStore) Term(ctx context.Context, index uint64) (uint64, error) {
	_ = ctx

	var entry raftpb.Entry
	if err := s.loadProto(encodeEntryKey(s.scope, index), &entry); err != nil {
		return 0, err
	}
	if entry.Index == index {
		return entry.Term, nil
	}

	meta, _, err := s.ensureMeta()
	if err != nil {
		return 0, err
	}
	if meta.SnapshotIndex == index {
		return meta.SnapshotTerm, nil
	}
	return 0, nil
}

func (s *pebbleStore) FirstIndex(ctx context.Context) (uint64, error) {
	_ = ctx

	meta, _, err := s.ensureMeta()
	if err != nil {
		return 0, err
	}
	return meta.FirstIndex, nil
}

func (s *pebbleStore) LastIndex(ctx context.Context) (uint64, error) {
	_ = ctx

	meta, _, err := s.ensureMeta()
	if err != nil {
		return 0, err
	}
	return meta.LastIndex, nil
}

func (s *pebbleStore) Snapshot(ctx context.Context) (raftpb.Snapshot, error) {
	_ = ctx
	return s.loadSnapshot(ctx)
}

func (s *pebbleStore) Save(ctx context.Context, st multiraft.PersistentState) error {
	unlock := s.db.lockScopeMutation(s.scope)
	defer unlock()

	writeState := withoutSnapshotData(st)
	var staged *stagedSnapshot
	if st.Snapshot != nil {
		plan, err := s.db.planSnapshotSave(ctx, s.scope, *st.Snapshot)
		if err != nil {
			return err
		}
		writeState.Snapshot = &plan.Metadata
		if len(writeState.Entries) > 0 {
			writeState.Entries = filterEntriesAfterSnapshot(writeState.Entries, plan.Metadata.Index)
		}
		if !plan.NeedsExternalWrite {
			writeState.SnapshotManifest = cloneSnapshotManifestPtr(plan.ExistingManifest)
		} else {
			staged, err = s.db.prepareAndWriteSnapshot(ctx, s.scope, *st.Snapshot)
			if err != nil {
				return err
			}
			writeState.SnapshotManifest = cloneSnapshotManifestPtr(&staged.manifest)
		}
	}

	if staged == nil {
		return s.db.submitWrite(&writeRequest{scope: s.scope, op: saveOp{state: writeState}, done: make(chan error, 1)})
	}

	err := s.db.publishSnapshotAndCommit(staged, &writeRequest{scope: s.scope, op: saveOp{state: writeState}, done: make(chan error, 1)})
	if err == nil {
		s.db.startSnapshotGC()
	}
	return err
}

func (s *pebbleStore) MarkApplied(ctx context.Context, index uint64) error {
	_ = ctx

	unlock := s.db.lockScopeMutation(s.scope)
	defer unlock()

	req := &writeRequest{
		scope: s.scope,
		op:    markAppliedOp{index: index},
		done:  make(chan error, 1),
	}
	return s.db.submitWrite(req)
}

func (s *pebbleStore) MarkConfigApplied(ctx context.Context, index uint64) error {
	_ = ctx

	unlock := s.db.lockScopeMutation(s.scope)
	defer unlock()

	req := &writeRequest{
		scope: s.scope,
		op:    markConfigAppliedOp{index: index},
		done:  make(chan error, 1),
	}
	return s.db.submitWrite(req)
}

// planSnapshotSave validates a snapshot save using only Pebble metadata and manifest bytes.
func (db *DB) planSnapshotSave(ctx context.Context, scope Scope, snap raftpb.Snapshot) (snapshotSavePlan, error) {
	if err := ctx.Err(); err != nil {
		return snapshotSavePlan{}, err
	}
	store := &pebbleStore{db: db, scope: scope}
	view, err := store.loadSnapshotMetaView(ctx)
	if err != nil {
		return snapshotSavePlan{}, err
	}
	manifest := view.manifest

	plan := snapshotSavePlan{Metadata: cloneSnapshotMetadata(snap.Metadata)}
	if !view.hasManifest {
		plan.NeedsExternalWrite = true
		return plan, nil
	}
	if snap.Metadata.Index < manifest.Index {
		return snapshotSavePlan{}, raft.ErrSnapOutOfDate
	}
	if snap.Metadata.Index > manifest.Index {
		plan.NeedsExternalWrite = true
		return plan, nil
	}

	if snap.Metadata.Term != manifest.Term || !confStateEqual(snap.Metadata.ConfState, manifest.ConfState) || uint64(len(snap.Data)) != manifest.TotalSize || !bytes.Equal(snapshotChecksum(snap.Data), manifest.WholeChecksum) {
		return snapshotSavePlan{}, errors.New("raftstorage: same-index snapshot does not match existing manifest")
	}
	plan.ExistingManifest = cloneSnapshotManifestPtr(&manifest)
	return plan, nil
}

func (db *DB) prepareAndWriteSnapshot(ctx context.Context, scope Scope, snap raftpb.Snapshot) (*stagedSnapshot, error) {
	for {
		staged, err := db.snapshotStore.prepare(ctx, scope, snap)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(staged.finalDir); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}

		unregisterTmp := db.registerActiveSnapshotPath(staged.tmpDir)
		keepTmpActive := false
		defer func() {
			if !keepTmpActive {
				unregisterTmp()
			}
		}()
		err = db.snapshotStore.write(ctx, staged, snap.Data)
		if err != nil {
			return nil, cleanupStagedTmpPreservingError(staged, err)
		}
		staged.tmpRegistered = true
		keepTmpActive = true
		return staged, nil
	}
}

func (db *DB) publishSnapshotAndCommit(staged *stagedSnapshot, req *writeRequest) (retErr error) {
	published := false
	cleanupPublishedOnFailure := false
	db.snapshotLifecycleMu.Lock()
	if staged.tmpRegistered {
		db.removeActiveSnapshotPathLocked(staged.tmpDir)
		staged.tmpRegistered = false
	}
	db.addActiveSnapshotPathLocked(staged.finalDir)
	defer func() {
		db.removeActiveSnapshotPathLocked(staged.finalDir)
		db.snapshotLifecycleMu.Unlock()
		// Worker failures leave unreferenced final dirs for retry/GC; pre-worker failures are cleaned.
		if retErr != nil && published && cleanupPublishedOnFailure {
			db.removePublishedSnapshotDir(staged)
		}
	}()

	if err := db.snapshotStore.publishFinal(staged); err != nil {
		db.removePublishedSnapshotDirIfRenamed(staged)
		return cleanupStagedTmpPreservingError(staged, err)
	}
	published = true
	cleanupPublishedOnFailure = true
	if db.snapshotAfterPublishTestHook != nil {
		if err := db.snapshotAfterPublishTestHook(staged); err != nil {
			return err
		}
	}
	err := db.submitWrite(req)
	if err == nil || !errors.Is(err, errWriteNotEnqueued) {
		cleanupPublishedOnFailure = false
	}
	return err
}

func (db *DB) removePublishedSnapshotDir(staged *stagedSnapshot) {
	if staged == nil || staged.finalDir == "" {
		return
	}
	_ = os.RemoveAll(staged.finalDir)
}

func (db *DB) removePublishedSnapshotDirIfRenamed(staged *stagedSnapshot) {
	if staged == nil || staged.tmpDir == "" || staged.finalDir == "" {
		return
	}
	if _, err := os.Stat(staged.tmpDir); !os.IsNotExist(err) {
		return
	}
	if _, err := os.Stat(staged.finalDir); err == nil {
		db.removePublishedSnapshotDir(staged)
	}
}

func (db *DB) addActiveSnapshotPathLocked(path string) {
	path = normalizeSnapshotLifecyclePath(path)
	if path == "" {
		return
	}
	if db.activeSnapshotPaths == nil {
		db.activeSnapshotPaths = make(map[string]int)
	}
	db.activeSnapshotPaths[path]++
}

func (db *DB) removeActiveSnapshotPathLocked(path string) {
	path = normalizeSnapshotLifecyclePath(path)
	if path == "" {
		return
	}
	if count := db.activeSnapshotPaths[path]; count <= 1 {
		delete(db.activeSnapshotPaths, path)
	} else {
		db.activeSnapshotPaths[path] = count - 1
	}
}
