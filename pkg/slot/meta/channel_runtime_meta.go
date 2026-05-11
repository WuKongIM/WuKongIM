package meta

import (
	"context"
	"encoding/binary"
	"errors"
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
	// RetentionThroughSeq is the highest channel message sequence that the
	// authoritative cluster metadata declares unavailable for future reads.
	RetentionThroughSeq uint64
	// RetentionUpdatedAtMS is the wall-clock time in milliseconds when the
	// authoritative retention boundary was last advanced.
	RetentionUpdatedAtMS int64
	// WriteFenceToken identifies the migration task currently fencing writes for this channel.
	WriteFenceToken string
	// WriteFenceVersion is a monotonic per-channel fence generation changed by set, renew, reset, or clear commands.
	WriteFenceVersion uint64
	// WriteFenceReason describes why writes are fenced for diagnostics and API responses.
	WriteFenceReason uint8
	// WriteFenceUntilMS is the wall-clock deadline for the current fence lease in milliseconds.
	WriteFenceUntilMS int64
}

// ChannelRetentionAdvance describes a fenced request to advance only the
// authoritative channel retention boundary.
type ChannelRetentionAdvance struct {
	// ChannelID identifies the channel whose retention boundary is advanced.
	ChannelID string
	// ChannelType identifies the channel namespace for ChannelID.
	ChannelType int64
	// ExpectedChannelEpoch fences the advance to a known channel epoch.
	ExpectedChannelEpoch uint64
	// ExpectedLeaderEpoch fences the advance to a known leader epoch.
	ExpectedLeaderEpoch uint64
	// ExpectedLeader fences the advance to the current authoritative leader.
	ExpectedLeader uint64
	// ExpectedLeaseUntilMS fences the advance to the leader lease observed by the caller.
	ExpectedLeaseUntilMS int64
	// RetentionThroughSeq is the proposed highest unavailable message sequence.
	RetentionThroughSeq uint64
	// RetentionUpdatedAtMS is the wall-clock time in milliseconds for this advance.
	RetentionUpdatedAtMS int64
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
	existing, exists, err := s.getChannelRuntimeMetaForPrimaryKeyLocked(key, meta.ChannelID, meta.ChannelType)
	if err != nil {
		return err
	}
	meta, shouldWrite := resolveMonotonicChannelRuntimeMeta(existing, exists, meta)
	if !shouldWrite {
		return nil
	}
	value := encodeChannelRuntimeMetaFamilyValue(meta, key)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Set(key, value, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

// AdvanceChannelRetentionThroughSeq advances only the authoritative retention
// boundary when the request matches the current runtime metadata fence.
func (s *ShardStore) AdvanceChannelRetentionThroughSeq(ctx context.Context, req ChannelRetentionAdvance) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateChannelRetentionAdvance(req); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	key := encodeChannelRuntimeMetaPrimaryKey(s.slot, req.ChannelID, req.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	existing, exists, err := s.getChannelRuntimeMetaForPrimaryKeyLocked(key, req.ChannelID, req.ChannelType)
	if err != nil {
		return err
	}
	next, shouldWrite, err := advanceChannelRetentionThroughSeq(existing, exists, req)
	if err != nil || !shouldWrite {
		return err
	}

	value := encodeChannelRuntimeMetaFamilyValue(next, key)
	batch := s.db.db.NewBatch()
	defer batch.Close()
	if err := batch.Set(key, value, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) getChannelRuntimeMetaForPrimaryKeyLocked(key []byte, channelID string, channelType int64) (ChannelRuntimeMeta, bool, error) {
	value, err := s.db.getValue(key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ChannelRuntimeMeta{}, false, nil
		}
		return ChannelRuntimeMeta{}, false, err
	}
	meta, err := decodeChannelRuntimeMetaFamilyValue(key, value)
	if err != nil {
		return ChannelRuntimeMeta{}, false, err
	}
	meta.ChannelID = channelID
	meta.ChannelType = channelType
	return meta, true, nil
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
	if err := validateChannelRuntimeMetaWriteFence(meta); err != nil {
		return err
	}
	return nil
}

func validateChannelRuntimeMetaWriteFence(meta ChannelRuntimeMeta) error {
	if meta.WriteFenceVersion == 0 &&
		(meta.WriteFenceToken != "" || meta.WriteFenceReason != 0 || meta.WriteFenceUntilMS != 0) {
		return ErrInvalidArgument
	}
	return nil
}

func validateChannelRetentionAdvance(req ChannelRetentionAdvance) error {
	if err := validateChannelRuntimeMetaChannelID(req.ChannelID); err != nil {
		return err
	}
	return nil
}

func advanceChannelRetentionThroughSeq(existing ChannelRuntimeMeta, exists bool, req ChannelRetentionAdvance) (ChannelRuntimeMeta, bool, error) {
	if !exists {
		return ChannelRuntimeMeta{}, false, ErrNotFound
	}
	if existing.ChannelEpoch != req.ExpectedChannelEpoch ||
		existing.LeaderEpoch != req.ExpectedLeaderEpoch ||
		existing.Leader != req.ExpectedLeader ||
		existing.LeaseUntilMS != req.ExpectedLeaseUntilMS {
		return ChannelRuntimeMeta{}, false, ErrStaleMeta
	}
	if req.RetentionThroughSeq <= existing.RetentionThroughSeq {
		return existing, false, nil
	}
	next := existing
	next.RetentionThroughSeq = req.RetentionThroughSeq
	next.RetentionUpdatedAtMS = req.RetentionUpdatedAtMS
	return next, true, nil
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

// resolveMonotonicChannelRuntimeMeta prevents stale runtime metadata from
// regressing leadership epochs or shortening a live lease in the same epoch.
func resolveMonotonicChannelRuntimeMeta(existing ChannelRuntimeMeta, exists bool, candidate ChannelRuntimeMeta) (ChannelRuntimeMeta, bool) {
	if !exists {
		return candidate, true
	}
	switch {
	case candidate.ChannelEpoch < existing.ChannelEpoch:
		return existing, false
	case candidate.ChannelEpoch > existing.ChannelEpoch:
		preserveRetentionBoundary(existing, &candidate)
		preserveWriteFence(existing, &candidate)
		return candidate, true
	case candidate.LeaderEpoch < existing.LeaderEpoch:
		return existing, false
	case candidate.LeaderEpoch > existing.LeaderEpoch:
		preserveRetentionBoundary(existing, &candidate)
		preserveWriteFence(existing, &candidate)
		return candidate, true
	case candidate.Leader != existing.Leader:
		return existing, false
	}
	if candidate.LeaseUntilMS < existing.LeaseUntilMS {
		candidate.LeaseUntilMS = existing.LeaseUntilMS
	}
	preserveRetentionBoundary(existing, &candidate)
	preserveWriteFence(existing, &candidate)
	return candidate, true
}

func preserveRetentionBoundary(existing ChannelRuntimeMeta, candidate *ChannelRuntimeMeta) {
	if candidate.RetentionThroughSeq < existing.RetentionThroughSeq {
		candidate.RetentionThroughSeq = existing.RetentionThroughSeq
		candidate.RetentionUpdatedAtMS = existing.RetentionUpdatedAtMS
		return
	}
	if candidate.RetentionThroughSeq == existing.RetentionThroughSeq && candidate.RetentionUpdatedAtMS < existing.RetentionUpdatedAtMS {
		candidate.RetentionUpdatedAtMS = existing.RetentionUpdatedAtMS
	}
}

func preserveWriteFence(existing ChannelRuntimeMeta, candidate *ChannelRuntimeMeta) {
	if candidate.WriteFenceVersion <= existing.WriteFenceVersion {
		restoreWriteFence(existing, candidate)
	}
}

func restoreWriteFence(existing ChannelRuntimeMeta, candidate *ChannelRuntimeMeta) {
	candidate.WriteFenceToken = existing.WriteFenceToken
	candidate.WriteFenceVersion = existing.WriteFenceVersion
	candidate.WriteFenceReason = existing.WriteFenceReason
	candidate.WriteFenceUntilMS = existing.WriteFenceUntilMS
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
