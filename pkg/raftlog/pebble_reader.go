package raftlog

import (
	"encoding/binary"
	"errors"

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

func (s *pebbleStore) loadSnapshot() (raftpb.Snapshot, error) {
	var snap raftpb.Snapshot
	if err := s.loadProto(encodeSnapshotKey(s.scope), &snap); err != nil {
		return raftpb.Snapshot{}, err
	}
	return cloneSnapshot(snap), nil
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
	meta, ok, err := s.loadMeta()
	if err != nil {
		return logMeta{}, false, err
	}
	if ok {
		return meta, true, nil
	}

	snap, err := s.loadSnapshot()
	if err != nil {
		return logMeta{}, false, err
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
	if err := updateLogMeta(&meta, snap, entries, hs.Commit); err != nil {
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
