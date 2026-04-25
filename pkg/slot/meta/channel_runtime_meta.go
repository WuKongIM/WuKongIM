package meta

import (
	"context"
	"encoding/binary"
	"sort"

	"github.com/cockroachdb/pebble/v2"
)

type ChannelRuntimeMeta struct {
	ChannelID    string
	ChannelType  int64
	ChannelEpoch uint64
	LeaderEpoch  uint64
	Replicas     []uint64
	ISR          []uint64
	Leader       uint64
	MinISR       int64
	Status       uint8
	Features     uint64
	LeaseUntilMS int64
}

func (s *ShardStore) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (ChannelRuntimeMeta, error) {
	if err := s.validate(); err != nil {
		return ChannelRuntimeMeta{}, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return ChannelRuntimeMeta{}, err
	}
	if err := validateChannelRuntimeMetaChannelID(channelID); err != nil {
		return ChannelRuntimeMeta{}, ErrInvalidArgument
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	return s.getChannelRuntimeMetaLocked(channelID, channelType)
}

func (s *ShardStore) getChannelRuntimeMetaLocked(channelID string, channelType int64) (ChannelRuntimeMeta, error) {
	key := encodeChannelRuntimeMetaPrimaryKey(s.slot, channelID, channelType, channelRuntimeMetaPrimaryFamilyID)
	value, err := s.db.getValue(key)
	if err != nil {
		return ChannelRuntimeMeta{}, err
	}

	meta, err := decodeChannelRuntimeMetaFamilyValue(key, value)
	if err != nil {
		return ChannelRuntimeMeta{}, err
	}
	meta.ChannelID = channelID
	meta.ChannelType = channelType
	return meta, nil
}

func (s *ShardStore) UpsertChannelRuntimeMeta(ctx context.Context, meta ChannelRuntimeMeta) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateChannelRuntimeMeta(meta); err != nil {
		return err
	}

	meta = normalizeChannelRuntimeMeta(meta)

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	key := encodeChannelRuntimeMetaPrimaryKey(s.slot, meta.ChannelID, meta.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	value := encodeChannelRuntimeMetaFamilyValue(meta, key)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Set(key, value, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) DeleteChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateChannelRuntimeMetaChannelID(channelID); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	key := encodeChannelRuntimeMetaPrimaryKey(s.slot, channelID, channelType, channelRuntimeMetaPrimaryFamilyID)
	exists, err := s.db.hasKey(key)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Delete(key, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func validateChannelRuntimeMeta(meta ChannelRuntimeMeta) error {
	if err := validateChannelRuntimeMetaChannelID(meta.ChannelID); err != nil {
		return err
	}
	meta = normalizeChannelRuntimeMeta(meta)
	if len(meta.Replicas) == 0 {
		return ErrInvalidArgument
	}
	if meta.MinISR <= 0 || meta.MinISR > int64(len(meta.Replicas)) {
		return ErrInvalidArgument
	}
	replicas := make(map[uint64]struct{}, len(meta.Replicas))
	for _, replica := range meta.Replicas {
		replicas[replica] = struct{}{}
	}
	for _, member := range meta.ISR {
		if _, ok := replicas[member]; !ok {
			return ErrInvalidArgument
		}
	}
	if meta.Leader != 0 {
		if _, ok := replicas[meta.Leader]; !ok {
			return ErrInvalidArgument
		}
		if !containsUint64(meta.ISR, meta.Leader) {
			return ErrInvalidArgument
		}
	}
	return nil
}

func validateChannelRuntimeMetaChannelID(channelID string) error {
	if channelID == "" || len(channelID) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func normalizeChannelRuntimeMeta(meta ChannelRuntimeMeta) ChannelRuntimeMeta {
	meta.Replicas = normalizeUint64Set(meta.Replicas)
	meta.ISR = normalizeUint64Set(meta.ISR)
	return meta
}

func normalizeUint64Set(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}

	sorted := append([]uint64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	n := 1
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[n-1] {
			continue
		}
		sorted[n] = sorted[i]
		n++
	}
	return sorted[:n]
}

func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (db *DB) ListChannelRuntimeMeta(ctx context.Context) ([]ChannelRuntimeMeta, error) {
	const statePrefixLen = 1 + 2 + 4

	if err := db.checkContext(ctx); err != nil {
		return nil, err
	}

	db.mu.RLock()
	defer db.mu.RUnlock()

	iter, err := db.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte{keyspaceState},
		UpperBound: []byte{keyspaceState + 1},
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	metas := make([]ChannelRuntimeMeta, 0, 16)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := db.checkContext(ctx); err != nil {
			return nil, err
		}

		key := iter.Key()
		if len(key) < statePrefixLen || key[0] != keyspaceState {
			continue
		}
		if binary.BigEndian.Uint32(key[1+2:statePrefixLen]) != TableIDChannelRuntimeMeta {
			continue
		}

		value, err := iter.ValueAndErr()
		if err != nil {
			return nil, err
		}

		meta, familyID, err := decodeChannelRuntimeMetaRecord(key, value)
		if err != nil {
			return nil, err
		}
		if familyID != channelRuntimeMetaPrimaryFamilyID {
			continue
		}
		metas = append(metas, meta)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return metas, nil
}

func decodeChannelRuntimeMetaRecord(key, value []byte) (ChannelRuntimeMeta, uint16, error) {
	rest := key[1+2+4:]
	channelID, rest, err := decodeKeyString(rest)
	if err != nil {
		return ChannelRuntimeMeta{}, 0, err
	}
	channelType, rest, err := decodeOrderedInt64(rest)
	if err != nil {
		return ChannelRuntimeMeta{}, 0, err
	}
	familyID, n := binary.Uvarint(rest)
	if n <= 0 {
		return ChannelRuntimeMeta{}, 0, ErrCorruptValue
	}
	if len(rest[n:]) != 0 {
		return ChannelRuntimeMeta{}, 0, ErrCorruptValue
	}

	meta, err := decodeChannelRuntimeMetaFamilyValue(key, value)
	if err != nil {
		return ChannelRuntimeMeta{}, 0, err
	}
	meta.ChannelID = channelID
	meta.ChannelType = channelType
	return meta, uint16(familyID), nil
}
