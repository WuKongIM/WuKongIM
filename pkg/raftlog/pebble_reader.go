package raftlog

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"path/filepath"

	"github.com/cockroachdb/pebble/v2"
	"go.etcd.io/raft/v3/raftpb"
)

type unmarshaler interface {
	Unmarshal(data []byte) error
}

type pebbleGetReader interface {
	Get(key []byte) ([]byte, io.Closer, error)
}

type pebbleIterReader interface {
	NewIter(*pebble.IterOptions) (*pebble.Iterator, error)
}

type snapshotMetaView struct {
	meta        logMeta
	hasMeta     bool
	manifest    SnapshotManifest
	hasManifest bool
}

func (s *pebbleStore) loadHardState() (raftpb.HardState, error) {
	return s.loadHardStateFrom(s.db.db)
}

func (s *pebbleStore) loadHardStateFrom(reader pebbleGetReader) (raftpb.HardState, error) {
	var hs raftpb.HardState
	if err := s.loadProtoFrom(reader, encodeHardStateKey(s.scope), &hs); err != nil {
		return raftpb.HardState{}, err
	}
	return hs, nil
}

// loadSnapshotManifest decodes the snapshot manifest stored in Pebble without reading chunk files.
func (s *pebbleStore) loadSnapshotManifest(ctx context.Context) (SnapshotManifest, bool, error) {
	return s.loadSnapshotManifestFrom(ctx, s.db.db)
}

func (s *pebbleStore) loadSnapshotManifestFrom(ctx context.Context, reader pebbleGetReader) (SnapshotManifest, bool, error) {
	if err := ctx.Err(); err != nil {
		return SnapshotManifest{}, false, err
	}
	value, err := s.getValueFrom(reader, encodeSnapshotKey(s.scope))
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
	manifest, ok, unregister, err := s.loadSnapshotManifestAndRegisterActive(ctx)
	if err != nil {
		return raftpb.Snapshot{}, err
	}
	if !ok {
		return raftpb.Snapshot{}, nil
	}
	defer unregister()
	if s.db.snapshotReadBeforeChunksHook != nil {
		s.db.snapshotReadBeforeChunksHook(s.scope, manifest)
	}
	return s.db.snapshotStore.read(ctx, s.scope, manifest)
}

func (s *pebbleStore) loadSnapshotManifestAndRegisterActive(ctx context.Context) (SnapshotManifest, bool, func(), error) {
	s.db.snapshotLifecycleMu.Lock()
	defer s.db.snapshotLifecycleMu.Unlock()

	manifest, ok, err := s.loadConsistentSnapshotManifest(ctx)
	if err != nil || !ok {
		return manifest, ok, func() {}, err
	}
	if s.db.snapshotReadAfterManifestHook != nil {
		s.db.snapshotReadAfterManifestHook(s.scope, manifest)
	}
	finalDir := filepath.Join(s.db.snapshotStore.scopeDir(s.scope), manifest.SnapshotID)
	s.db.addActiveSnapshotPathLocked(finalDir)
	return manifest, true, func() {
		s.db.snapshotLifecycleMu.Lock()
		s.db.removeActiveSnapshotPathLocked(finalDir)
		s.db.snapshotLifecycleMu.Unlock()
	}, nil
}

func (s *pebbleStore) loadAppliedIndex() (uint64, error) {
	return s.loadAppliedIndexFrom(s.db.db)
}

func (s *pebbleStore) loadAppliedIndexFrom(reader pebbleGetReader) (uint64, error) {
	return s.loadUint64From(reader, encodeAppliedIndexKey(s.scope), "applied index")
}

func (s *pebbleStore) loadConfigAppliedIndex() (uint64, error) {
	return s.loadConfigAppliedIndexFrom(s.db.db)
}

func (s *pebbleStore) loadConfigAppliedIndexFrom(reader pebbleGetReader) (uint64, error) {
	return s.loadUint64From(reader, encodeConfigAppliedIndexKey(s.scope), "config applied index")
}

func (s *pebbleStore) loadUint64From(reader pebbleGetReader, key []byte, name string) (uint64, error) {
	value, err := s.getValueFrom(reader, key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	if len(value) != appliedIndexSize {
		return 0, errors.New("raftstorage: invalid " + name + " encoding")
	}
	return binary.BigEndian.Uint64(value), nil
}

func (s *pebbleStore) loadProto(key []byte, msg unmarshaler) error {
	return s.loadProtoFrom(s.db.db, key, msg)
}

func (s *pebbleStore) loadProtoFrom(reader pebbleGetReader, key []byte, msg unmarshaler) error {
	value, err := s.getValueFrom(reader, key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil
		}
		return err
	}
	return msg.Unmarshal(value)
}

func (s *pebbleStore) getValue(key []byte) ([]byte, error) {
	return s.getValueFrom(s.db.db, key)
}

func (s *pebbleStore) getValueFrom(reader pebbleGetReader, key []byte) ([]byte, error) {
	value, closer, err := reader.Get(key)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	return append([]byte(nil), value...), nil
}

func (s *pebbleStore) loadMeta() (logMeta, bool, error) {
	return s.loadMetaFrom(s.db.db)
}

func (s *pebbleStore) loadMetaFrom(reader pebbleGetReader) (logMeta, bool, error) {
	value, err := s.getValueFrom(reader, encodeGroupStateKey(s.scope))
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
	readState := s.db.db.NewSnapshot()
	defer readState.Close()

	view, err := s.loadSnapshotMetaViewFrom(context.Background(), readState)
	if err != nil {
		return logMeta{}, false, err
	}
	if view.hasMeta {
		return view.meta, true, nil
	}

	hs, err := s.loadHardStateFrom(readState)
	if err != nil {
		return logMeta{}, false, err
	}
	appliedIndex, err := s.loadAppliedIndexFrom(readState)
	if err != nil {
		return logMeta{}, false, err
	}
	entries, err := s.loadEntriesFrom(readState, 0, 0)
	if err != nil {
		return logMeta{}, false, err
	}

	meta := logMeta{AppliedIndex: appliedIndex}
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
	return s.loadEntriesFrom(s.db.db, lo, hi)
}

func (s *pebbleStore) loadEntriesFrom(reader pebbleIterReader, lo, hi uint64) ([]raftpb.Entry, error) {
	lower := encodeEntryPrefix(s.scope)
	upper := encodeEntryPrefixEnd(s.scope)
	if lo > 0 {
		lower = encodeEntryKey(s.scope, lo)
	}
	if hi > 0 {
		upper = encodeEntryKey(s.scope, hi)
	}

	iter, err := reader.NewIter(&pebble.IterOptions{
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
	view, err := s.loadSnapshotMetaView(ctx)
	if err != nil {
		return SnapshotManifest{}, false, err
	}
	return view.manifest, view.hasManifest, nil
}

func (s *pebbleStore) loadSnapshotMetaView(ctx context.Context) (snapshotMetaView, error) {
	if err := ctx.Err(); err != nil {
		return snapshotMetaView{}, err
	}
	readState := s.db.db.NewSnapshot()
	defer readState.Close()
	return s.loadSnapshotMetaViewFrom(ctx, readState)
}

func (s *pebbleStore) loadSnapshotMetaViewFrom(ctx context.Context, reader pebbleGetReader) (snapshotMetaView, error) {
	meta, hasMeta, err := s.loadMetaFrom(reader)
	if err != nil {
		return snapshotMetaView{}, err
	}
	if s.db.currentMetaAfterMetaLoadHook != nil {
		s.db.currentMetaAfterMetaLoadHook(s.scope)
	}
	manifest, hasManifest, err := s.loadSnapshotManifestFrom(ctx, reader)
	if err != nil {
		return snapshotMetaView{}, err
	}
	if err := validateManifestMetaConsistency(manifest, hasManifest, meta, hasMeta); err != nil {
		return snapshotMetaView{}, err
	}
	return snapshotMetaView{meta: meta, hasMeta: hasMeta, manifest: manifest, hasManifest: hasManifest}, nil
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
