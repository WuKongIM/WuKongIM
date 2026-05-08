package raftlog

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"

	"github.com/cockroachdb/pebble/v2"
	"go.etcd.io/raft/v3/raftpb"
)

type unmarshaler interface {
	Unmarshal(data []byte) error
}

func (s *pebbleStore) loadHardState() (raftpb.HardState, error) {
	var hs raftpb.HardState
	if err := s.loadProto(encodeHardStateKey(s.scope), &hs); err != nil {
		return raftpb.HardState{}, err
	}
	return hs, nil
}

// loadSnapshotManifest decodes the snapshot manifest stored in Pebble without reading chunk files.
func (s *pebbleStore) loadSnapshotManifest(ctx context.Context) (SnapshotManifest, bool, error) {
	if err := ctx.Err(); err != nil {
		return SnapshotManifest{}, false, err
	}
	value, err := s.getValue(encodeSnapshotKey(s.scope))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return SnapshotManifest{}, false, nil
		}
		return SnapshotManifest{}, false, err
	}
	manifest, err := decodeSnapshotManifest(s.scope, value)
	if err != nil {
		return SnapshotManifest{}, false, err
	}
	return manifest, true, nil
}

// loadSnapshot reads the external snapshot payload described by the Pebble manifest.
func (s *pebbleStore) loadSnapshot(ctx context.Context) (raftpb.Snapshot, error) {
	manifest, ok, err := s.loadConsistentSnapshotManifest(ctx)
	if err != nil {
		return raftpb.Snapshot{}, err
	}
	if !ok {
		return raftpb.Snapshot{}, nil
	}
	finalDir := filepath.Join(s.db.snapshotStore.scopeDir(s.scope), manifest.SnapshotID)
	unregister := s.db.registerActiveSnapshotPath(finalDir)
	defer unregister()
	if s.db.snapshotReadBeforeChunksHook != nil {
		s.db.snapshotReadBeforeChunksHook(s.scope, manifest)
	}
	return s.db.snapshotStore.read(ctx, s.scope, manifest)
}

func (s *pebbleStore) loadAppliedIndex() (uint64, error) {
	value, err := s.getValue(encodeAppliedIndexKey(s.scope))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	if len(value) != appliedIndexSize {
		return 0, errors.New("raftstorage: invalid applied index encoding")
	}
	return binary.BigEndian.Uint64(value), nil
}

func (s *pebbleStore) loadProto(key []byte, msg unmarshaler) error {
	value, err := s.getValue(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil
		}
		return err
	}
	return msg.Unmarshal(value)
}

func (s *pebbleStore) getValue(key []byte) ([]byte, error) {
	value, closer, err := s.db.db.Get(key)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	return append([]byte(nil), value...), nil
}

func (s *pebbleStore) loadMeta() (logMeta, bool, error) {
	value, err := s.getValue(encodeGroupStateKey(s.scope))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return logMeta{}, false, nil
		}
		return logMeta{}, false, err
	}

	var meta logMeta
	if err := meta.Unmarshal(value); err != nil {
		return logMeta{}, false, err
	}
	return meta, true, nil
}

func (s *pebbleStore) currentMeta() (logMeta, bool, error) {
	meta, hasMeta, err := s.loadMeta()
	if err != nil {
		return logMeta{}, false, err
	}
	manifest, hasManifest, err := s.loadSnapshotManifest(context.Background())
	if err != nil {
		return logMeta{}, false, err
	}
	if err := validateManifestMetaConsistency(manifest, hasManifest, meta, hasMeta); err != nil {
		return logMeta{}, false, err
	}
	if hasMeta {
		return meta, true, nil
	}

	hs, err := s.loadHardState()
	if err != nil {
		return logMeta{}, false, err
	}
	appliedIndex, err := s.loadAppliedIndex()
	if err != nil {
		return logMeta{}, false, err
	}
	entries, err := s.loadEntries(0, 0)
	if err != nil {
		return logMeta{}, false, err
	}

	meta = logMeta{AppliedIndex: appliedIndex}
	if err := updateLogMeta(&meta, raftpb.Snapshot{}, entries, hs.Commit); err != nil {
		return logMeta{}, false, err
	}
	return meta, false, nil
}

func (s *pebbleStore) ensureMeta() (logMeta, bool, error) {
	meta, fromDisk, err := s.currentMeta()
	if err != nil {
		return logMeta{}, false, err
	}
	if fromDisk {
		return meta, true, nil
	}
	if err := s.persistMeta(meta); err != nil {
		return logMeta{}, false, err
	}
	return meta, true, nil
}

func (s *pebbleStore) setMeta(batch *pebble.Batch, meta logMeta) error {
	data, err := meta.Marshal()
	if err != nil {
		return err
	}
	return batch.Set(encodeGroupStateKey(s.scope), data, nil)
}

func (s *pebbleStore) persistMeta(meta logMeta) error {
	data, err := meta.Marshal()
	if err != nil {
		return err
	}
	return s.db.db.Set(encodeGroupStateKey(s.scope), data, pebble.Sync)
}

func (s *pebbleStore) loadEntries(lo, hi uint64) ([]raftpb.Entry, error) {
	lower := encodeEntryPrefix(s.scope)
	upper := encodeEntryPrefixEnd(s.scope)
	if lo > 0 {
		lower = encodeEntryKey(s.scope, lo)
	}
	if hi > 0 {
		upper = encodeEntryKey(s.scope, hi)
	}

	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: lower,
		UpperBound: upper,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var entries []raftpb.Entry
	for iter.First(); iter.Valid(); iter.Next() {
		entry, err := decodeEntryValue(iter)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *pebbleStore) loadConsistentSnapshotManifest(ctx context.Context) (SnapshotManifest, bool, error) {
	meta, hasMeta, err := s.loadMeta()
	if err != nil {
		return SnapshotManifest{}, false, err
	}
	manifest, hasManifest, err := s.loadSnapshotManifest(ctx)
	if err != nil {
		return SnapshotManifest{}, false, err
	}
	if err := validateManifestMetaConsistency(manifest, hasManifest, meta, hasMeta); err != nil {
		return SnapshotManifest{}, false, err
	}
	return manifest, hasManifest, nil
}

func validateManifestMetaConsistency(manifest SnapshotManifest, hasManifest bool, meta logMeta, hasMeta bool) error {
	if hasManifest && !hasMeta {
		return errors.New("raftstorage: snapshot manifest missing log metadata")
	}
	if !hasManifest && hasMeta && meta.SnapshotIndex > 0 {
		return errors.New("raftstorage: snapshot metadata missing manifest")
	}
	if !hasManifest {
		return nil
	}
	if meta.SnapshotIndex != manifest.Index || meta.SnapshotTerm != manifest.Term {
		return errors.New("raftstorage: snapshot manifest and metadata mismatch")
	}
	if meta.LastIndex <= meta.SnapshotIndex && !confStateEqual(meta.ConfState, manifest.ConfState) {
		return errors.New("raftstorage: snapshot manifest and metadata mismatch")
	}
	return nil
}

type iterValueReader interface {
	ValueAndErr() ([]byte, error)
}

func decodeEntryValue(iter iterValueReader) (raftpb.Entry, error) {
	value, err := iter.ValueAndErr()
	if err != nil {
		return raftpb.Entry{}, err
	}
	var entry raftpb.Entry
	if err := entry.Unmarshal(value); err != nil {
		return raftpb.Entry{}, err
	}
	return cloneEntry(entry), nil
}
