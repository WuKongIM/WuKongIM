package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"slices"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

const (
	channelRuntimeMetaPrimaryFamilyID uint16 = 0

	runtimeMetaColumnChannelEpoch        uint16 = 1
	runtimeMetaColumnLeaderEpoch         uint16 = 2
	runtimeMetaColumnReplicas            uint16 = 3
	runtimeMetaColumnISR                 uint16 = 4
	runtimeMetaColumnLeader              uint16 = 5
	runtimeMetaColumnMinISR              uint16 = 6
	runtimeMetaColumnStatus              uint16 = 7
	runtimeMetaColumnFeatures            uint16 = 8
	runtimeMetaColumnLeaseUntilMS        uint16 = 9
	runtimeMetaColumnRetentionThroughSeq uint16 = 10
	runtimeMetaColumnRetentionUpdatedAt  uint16 = 11
	runtimeMetaColumnWriteFenceToken     uint16 = 12
	runtimeMetaColumnWriteFenceVersion   uint16 = 13
	runtimeMetaColumnWriteFenceReason    uint16 = 14
	runtimeMetaColumnWriteFenceUntilMS   uint16 = 15
	runtimeMetaColumnRouteGeneration     uint16 = 16
)

const runtimeMetaValueVersion byte = 1

// ChannelRuntimeMeta stores authoritative channel routing and liveness metadata.
type ChannelRuntimeMeta struct {
	ChannelID    string
	ChannelType  int64
	ChannelEpoch uint64
	LeaderEpoch  uint64
	// RouteGeneration is the authoritative version of the channel routing record.
	RouteGeneration uint64
	Replicas        []uint64
	ISR             []uint64
	Leader          uint64
	MinISR          int64
	Status          uint8
	Features        uint64
	LeaseUntilMS    int64
	// RetentionThroughSeq is the highest sequence hidden by authoritative retention.
	RetentionThroughSeq uint64
	// RetentionUpdatedAtMS records the last retention update wall-clock time.
	RetentionUpdatedAtMS int64
	// WriteFenceToken identifies the task currently fencing writes.
	WriteFenceToken string
	// WriteFenceVersion is a monotonic per-channel fence generation.
	WriteFenceVersion uint64
	// WriteFenceReason records why writes are fenced.
	WriteFenceReason uint8
	// WriteFenceUntilMS is the fence lease deadline in milliseconds.
	WriteFenceUntilMS int64
}

// MonotonicResult describes how a runtime metadata upsert resolved.
type MonotonicResult uint8

const (
	// MonotonicApplied means the candidate was durably written.
	MonotonicApplied MonotonicResult = iota + 1
	// MonotonicIgnoredStale means the candidate was older than the stored value.
	MonotonicIgnoredStale
	// MonotonicConflict means the candidate conflicted with the stored value.
	MonotonicConflict
)

// ChannelRuntimeMetaCursor is a page cursor for runtime metadata scans.
type ChannelRuntimeMetaCursor struct {
	// ChannelID is the last returned channel ID.
	ChannelID string
	// ChannelType is the last returned channel type.
	ChannelType int64
}

// ChannelRetentionAdvance advances only the authoritative retention boundary.
type ChannelRetentionAdvance struct {
	ChannelID            string
	ChannelType          int64
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
	ExpectedLeader       uint64
	ExpectedLeaseUntilMS int64
	RetentionThroughSeq  uint64
	RetentionUpdatedAtMS int64
}

// UpsertChannelRuntimeMeta stores runtime metadata with monotonic conflict resolution.
func (s *Shard) UpsertChannelRuntimeMeta(ctx context.Context, meta ChannelRuntimeMeta) (MonotonicResult, error) {
	if err := s.check(ctx); err != nil {
		return 0, err
	}
	if err := validateChannelRuntimeMeta(meta); err != nil {
		return 0, err
	}
	unlock := s.lock()
	defer unlock()

	key := encodeChannelRuntimeMetaRowKey(s.hashSlot, meta.ChannelID, meta.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	existing, exists, err := s.getChannelRuntimeMetaByKey(ctx, key, meta.ChannelID, meta.ChannelType)
	if err != nil {
		return 0, err
	}
	next, result := resolveMonotonicChannelRuntimeMeta(existing, exists, meta)
	if result == MonotonicIgnoredStale {
		return result, nil
	}
	if result == MonotonicConflict {
		return result, dberrors.ErrConflict
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(key, encodeChannelRuntimeMetaValue(key, next)); err != nil {
		return 0, err
	}
	if err := batch.Commit(true); err != nil {
		return 0, err
	}
	return MonotonicApplied, nil
}

// GetChannelRuntimeMeta returns one runtime metadata row by channel.
func (s *Shard) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (ChannelRuntimeMeta, bool, error) {
	if err := s.check(ctx); err != nil {
		return ChannelRuntimeMeta{}, false, err
	}
	if err := validateKeyString(channelID); err != nil {
		return ChannelRuntimeMeta{}, false, err
	}
	key := encodeChannelRuntimeMetaRowKey(s.hashSlot, channelID, channelType, channelRuntimeMetaPrimaryFamilyID)
	return s.getChannelRuntimeMetaByKey(ctx, key, channelID, channelType)
}

// DeleteChannelRuntimeMeta removes one runtime metadata row.
func (s *Shard) DeleteChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(channelID); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	key := encodeChannelRuntimeMetaRowKey(s.hashSlot, channelID, channelType, channelRuntimeMetaPrimaryFamilyID)
	if _, ok, err := s.db.get(key); err != nil || !ok {
		if err != nil {
			return err
		}
		return dberrors.ErrNotFound
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Delete(key); err != nil {
		return err
	}
	return batch.Commit(true)
}

// ListChannelRuntimeMetaPage returns runtime metadata in channel ID/type order.
func (s *Shard) ListChannelRuntimeMetaPage(ctx context.Context, cursor ChannelRuntimeMetaCursor, limit int) ([]ChannelRuntimeMeta, ChannelRuntimeMetaCursor, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, ChannelRuntimeMetaCursor{}, false, err
	}
	if err := validateChannelRuntimeMetaCursor(cursor); err != nil {
		return nil, ChannelRuntimeMetaCursor{}, false, err
	}
	if limit <= 0 {
		return nil, ChannelRuntimeMetaCursor{}, false, dberrors.ErrInvalidArgument
	}
	prefix := encodeChannelRuntimeMetaRowPrefix(s.hashSlot)
	span := keycodec.NewPrefixSpan(prefix)
	if cursor != (ChannelRuntimeMetaCursor{}) {
		span.Start = keycodec.PrefixEnd(encodeChannelRuntimeMetaRowKey(s.hashSlot, cursor.ChannelID, cursor.ChannelType, channelRuntimeMetaPrimaryFamilyID))
	}
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, ChannelRuntimeMetaCursor{}, false, err
	}
	defer iter.Close()

	page := make([]ChannelRuntimeMeta, 0, limit)
	nextCursor := cursor
	hasMore := false
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, ChannelRuntimeMetaCursor{}, false, err
		}
		channelID, channelType, familyID, ok := decodeChannelRuntimeMetaRowKey(prefix, iter.Key())
		if !ok {
			return nil, ChannelRuntimeMetaCursor{}, false, dberrors.ErrCorruptValue
		}
		if familyID != channelRuntimeMetaPrimaryFamilyID {
			continue
		}
		if len(page) == limit {
			hasMore = true
			break
		}
		value, err := iter.Value()
		if err != nil {
			return nil, ChannelRuntimeMetaCursor{}, false, err
		}
		meta, err := decodeChannelRuntimeMetaValue(iter.Key(), value)
		if err != nil {
			return nil, ChannelRuntimeMetaCursor{}, false, err
		}
		meta.ChannelID = channelID
		meta.ChannelType = channelType
		page = append(page, meta)
		nextCursor = ChannelRuntimeMetaCursor{ChannelID: channelID, ChannelType: channelType}
	}
	if err := iter.Error(); err != nil {
		return nil, ChannelRuntimeMetaCursor{}, false, err
	}
	return page, nextCursor, !hasMore, nil
}

// AdvanceChannelRetentionThroughSeq advances only retention fields behind an observed fence.
func (s *Shard) AdvanceChannelRetentionThroughSeq(ctx context.Context, req ChannelRetentionAdvance) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(req.ChannelID); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	key := encodeChannelRuntimeMetaRowKey(s.hashSlot, req.ChannelID, req.ChannelType, channelRuntimeMetaPrimaryFamilyID)
	existing, exists, err := s.getChannelRuntimeMetaByKey(ctx, key, req.ChannelID, req.ChannelType)
	if err != nil {
		return err
	}
	if !exists {
		return dberrors.ErrNotFound
	}
	if existing.ChannelEpoch != req.ExpectedChannelEpoch ||
		existing.LeaderEpoch != req.ExpectedLeaderEpoch ||
		existing.Leader != req.ExpectedLeader ||
		existing.LeaseUntilMS != req.ExpectedLeaseUntilMS {
		return dberrors.ErrConflict
	}
	if req.RetentionThroughSeq <= existing.RetentionThroughSeq {
		return nil
	}
	next := existing
	next.RetentionThroughSeq = req.RetentionThroughSeq
	next.RetentionUpdatedAtMS = req.RetentionUpdatedAtMS
	next.RouteGeneration = nextChannelRouteGeneration(existing.RouteGeneration)

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(key, encodeChannelRuntimeMetaValue(key, next)); err != nil {
		return err
	}
	return batch.Commit(true)
}

func (s *Shard) getChannelRuntimeMetaByKey(ctx context.Context, key []byte, channelID string, channelType int64) (ChannelRuntimeMeta, bool, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return ChannelRuntimeMeta{}, false, err
		}
	}
	value, ok, err := s.db.get(key)
	if err != nil || !ok {
		return ChannelRuntimeMeta{}, ok, err
	}
	meta, err := decodeChannelRuntimeMetaValue(key, value)
	if err != nil {
		return ChannelRuntimeMeta{}, false, err
	}
	meta.ChannelID = channelID
	meta.ChannelType = channelType
	return meta, true, nil
}

func validateChannelRuntimeMeta(meta ChannelRuntimeMeta) error {
	if err := validateKeyString(meta.ChannelID); err != nil {
		return err
	}
	meta = normalizeChannelRuntimeMeta(meta)
	if len(meta.Replicas) == 0 {
		return dberrors.ErrInvalidArgument
	}
	if meta.MinISR <= 0 || meta.MinISR > int64(len(meta.Replicas)) {
		return dberrors.ErrInvalidArgument
	}
	replicas := make(map[uint64]struct{}, len(meta.Replicas))
	for _, replica := range meta.Replicas {
		replicas[replica] = struct{}{}
	}
	for _, member := range meta.ISR {
		if _, ok := replicas[member]; !ok {
			return dberrors.ErrInvalidArgument
		}
	}
	if meta.Leader != 0 {
		if _, ok := replicas[meta.Leader]; !ok || !containsUint64(meta.ISR, meta.Leader) {
			return dberrors.ErrInvalidArgument
		}
	}
	if meta.WriteFenceToken == "" {
		if meta.WriteFenceReason != 0 || meta.WriteFenceUntilMS != 0 {
			return dberrors.ErrInvalidArgument
		}
	} else if meta.WriteFenceVersion == 0 || meta.WriteFenceReason == 0 || meta.WriteFenceUntilMS <= 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func normalizeChannelRuntimeMeta(meta ChannelRuntimeMeta) ChannelRuntimeMeta {
	meta.Replicas = normalizeUint64Set(meta.Replicas)
	meta.ISR = normalizeUint64Set(meta.ISR)
	if meta.RouteGeneration == 0 {
		meta.RouteGeneration = maxUint64(meta.ChannelEpoch, meta.LeaderEpoch, meta.WriteFenceVersion, 1)
	}
	return meta
}

// NormalizeChannelRuntimeMeta returns the canonical representation of runtime metadata.
func NormalizeChannelRuntimeMeta(meta ChannelRuntimeMeta) ChannelRuntimeMeta {
	return normalizeChannelRuntimeMeta(meta)
}

func resolveMonotonicChannelRuntimeMeta(existing ChannelRuntimeMeta, exists bool, candidate ChannelRuntimeMeta) (ChannelRuntimeMeta, MonotonicResult) {
	candidateHadRouteGeneration := candidate.RouteGeneration != 0
	candidate = normalizeChannelRuntimeMeta(candidate)
	if !exists {
		return candidate, MonotonicApplied
	}
	existing = normalizeChannelRuntimeMeta(existing)
	switch {
	case candidateHadRouteGeneration && candidate.RouteGeneration < existing.RouteGeneration:
		return existing, MonotonicIgnoredStale
	case candidate.ChannelEpoch < existing.ChannelEpoch:
		return existing, MonotonicIgnoredStale
	case candidate.ChannelEpoch > existing.ChannelEpoch:
		preserveRuntimeMetaState(existing, &candidate)
		return bumpRuntimeRoute(existing, candidate, candidateHadRouteGeneration), MonotonicApplied
	case candidate.LeaderEpoch < existing.LeaderEpoch:
		return existing, MonotonicIgnoredStale
	case candidate.LeaderEpoch > existing.LeaderEpoch:
		preserveRuntimeMetaState(existing, &candidate)
		return bumpRuntimeRoute(existing, candidate, candidateHadRouteGeneration), MonotonicApplied
	case candidate.Leader != existing.Leader:
		return existing, MonotonicConflict
	}
	if candidate.LeaseUntilMS < existing.LeaseUntilMS {
		candidate.LeaseUntilMS = existing.LeaseUntilMS
	}
	preserveRuntimeMetaState(existing, &candidate)
	return bumpRuntimeRoute(existing, candidate, candidateHadRouteGeneration), MonotonicApplied
}

func preserveRuntimeMetaState(existing ChannelRuntimeMeta, candidate *ChannelRuntimeMeta) {
	if candidate.RetentionThroughSeq < existing.RetentionThroughSeq ||
		(candidate.RetentionThroughSeq == existing.RetentionThroughSeq && candidate.RetentionUpdatedAtMS < existing.RetentionUpdatedAtMS) {
		candidate.RetentionThroughSeq = existing.RetentionThroughSeq
		candidate.RetentionUpdatedAtMS = existing.RetentionUpdatedAtMS
	}
	if candidate.WriteFenceVersion <= existing.WriteFenceVersion {
		candidate.WriteFenceToken = existing.WriteFenceToken
		candidate.WriteFenceVersion = existing.WriteFenceVersion
		candidate.WriteFenceReason = existing.WriteFenceReason
		candidate.WriteFenceUntilMS = existing.WriteFenceUntilMS
	}
}

func bumpRuntimeRoute(existing, candidate ChannelRuntimeMeta, candidateHadRouteGeneration bool) ChannelRuntimeMeta {
	if !candidateHadRouteGeneration && candidate.RouteGeneration < existing.RouteGeneration {
		candidate.RouteGeneration = existing.RouteGeneration
	}
	if runtimeRouteChanged(existing, candidate) && candidate.RouteGeneration <= existing.RouteGeneration {
		candidate.RouteGeneration = nextChannelRouteGeneration(existing.RouteGeneration)
	}
	return candidate
}

func runtimeRouteChanged(a, b ChannelRuntimeMeta) bool {
	return a.ChannelEpoch != b.ChannelEpoch ||
		a.LeaderEpoch != b.LeaderEpoch ||
		a.Leader != b.Leader ||
		!slices.Equal(a.Replicas, b.Replicas) ||
		!slices.Equal(a.ISR, b.ISR) ||
		a.MinISR != b.MinISR ||
		a.Status != b.Status ||
		a.LeaseUntilMS != b.LeaseUntilMS ||
		a.RetentionThroughSeq != b.RetentionThroughSeq ||
		a.RetentionUpdatedAtMS != b.RetentionUpdatedAtMS ||
		a.WriteFenceToken != b.WriteFenceToken ||
		a.WriteFenceVersion != b.WriteFenceVersion ||
		a.WriteFenceReason != b.WriteFenceReason ||
		a.WriteFenceUntilMS != b.WriteFenceUntilMS
}

func nextChannelRouteGeneration(current uint64) uint64 {
	if current == ^uint64(0) {
		return current
	}
	return current + 1
}

func encodeChannelRuntimeMetaValue(key []byte, meta ChannelRuntimeMeta) []byte {
	meta = normalizeChannelRuntimeMeta(meta)
	var w rowcodec.Writer
	_ = w.Uint64(runtimeMetaColumnChannelEpoch, meta.ChannelEpoch)
	_ = w.Uint64(runtimeMetaColumnLeaderEpoch, meta.LeaderEpoch)
	_ = w.RawBytes(runtimeMetaColumnReplicas, encodeUint64Slice(meta.Replicas))
	_ = w.RawBytes(runtimeMetaColumnISR, encodeUint64Slice(meta.ISR))
	_ = w.Uint64(runtimeMetaColumnLeader, meta.Leader)
	_ = w.Int64(runtimeMetaColumnMinISR, meta.MinISR)
	_ = w.Uint8(runtimeMetaColumnStatus, meta.Status)
	_ = w.Uint64(runtimeMetaColumnFeatures, meta.Features)
	_ = w.Int64(runtimeMetaColumnLeaseUntilMS, meta.LeaseUntilMS)
	_ = w.Uint64(runtimeMetaColumnRetentionThroughSeq, meta.RetentionThroughSeq)
	_ = w.Int64(runtimeMetaColumnRetentionUpdatedAt, meta.RetentionUpdatedAtMS)
	_ = w.String(runtimeMetaColumnWriteFenceToken, meta.WriteFenceToken)
	_ = w.Uint64(runtimeMetaColumnWriteFenceVersion, meta.WriteFenceVersion)
	_ = w.Uint8(runtimeMetaColumnWriteFenceReason, meta.WriteFenceReason)
	_ = w.Int64(runtimeMetaColumnWriteFenceUntilMS, meta.WriteFenceUntilMS)
	_ = w.Uint64(runtimeMetaColumnRouteGeneration, meta.RouteGeneration)
	return rowcodec.Wrap(key, runtimeMetaValueVersion, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes())
}

func decodeChannelRuntimeMetaValue(key []byte, value []byte) (ChannelRuntimeMeta, error) {
	env, err := rowcodec.Unwrap(key, value)
	if err != nil {
		return ChannelRuntimeMeta{}, err
	}
	if env.Version != runtimeMetaValueVersion || env.Codec != rowcodec.CodecColumns {
		return ChannelRuntimeMeta{}, fmt.Errorf("%w: invalid runtime meta envelope", dberrors.ErrCorruptValue)
	}
	var meta ChannelRuntimeMeta
	scanner := rowcodec.NewScanner(env.Payload)
	for scanner.Next() {
		if err := decodeRuntimeMetaColumn(scanner, &meta); err != nil {
			return ChannelRuntimeMeta{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return ChannelRuntimeMeta{}, err
	}
	return normalizeChannelRuntimeMeta(meta), nil
}

func decodeRuntimeMetaColumn(scanner *rowcodec.Scanner, meta *ChannelRuntimeMeta) error {
	switch scanner.ColumnID() {
	case runtimeMetaColumnChannelEpoch:
		value, err := scanner.Uint64()
		meta.ChannelEpoch = value
		return err
	case runtimeMetaColumnLeaderEpoch:
		value, err := scanner.Uint64()
		meta.LeaderEpoch = value
		return err
	case runtimeMetaColumnReplicas:
		value, err := scanner.Bytes()
		if err != nil {
			return err
		}
		meta.Replicas, err = decodeUint64Slice(value)
		return err
	case runtimeMetaColumnISR:
		value, err := scanner.Bytes()
		if err != nil {
			return err
		}
		meta.ISR, err = decodeUint64Slice(value)
		return err
	case runtimeMetaColumnLeader:
		value, err := scanner.Uint64()
		meta.Leader = value
		return err
	case runtimeMetaColumnMinISR:
		value, err := scanner.Int64()
		meta.MinISR = value
		return err
	case runtimeMetaColumnStatus:
		value, err := scanner.Uint8()
		meta.Status = value
		return err
	case runtimeMetaColumnFeatures:
		value, err := scanner.Uint64()
		meta.Features = value
		return err
	case runtimeMetaColumnLeaseUntilMS:
		value, err := scanner.Int64()
		meta.LeaseUntilMS = value
		return err
	case runtimeMetaColumnRetentionThroughSeq:
		value, err := scanner.Uint64()
		meta.RetentionThroughSeq = value
		return err
	case runtimeMetaColumnRetentionUpdatedAt:
		value, err := scanner.Int64()
		meta.RetentionUpdatedAtMS = value
		return err
	case runtimeMetaColumnWriteFenceToken:
		value, err := scanner.String()
		meta.WriteFenceToken = value
		return err
	case runtimeMetaColumnWriteFenceVersion:
		value, err := scanner.Uint64()
		meta.WriteFenceVersion = value
		return err
	case runtimeMetaColumnWriteFenceReason:
		value, err := scanner.Uint8()
		meta.WriteFenceReason = value
		return err
	case runtimeMetaColumnWriteFenceUntilMS:
		value, err := scanner.Int64()
		meta.WriteFenceUntilMS = value
		return err
	case runtimeMetaColumnRouteGeneration:
		value, err := scanner.Uint64()
		meta.RouteGeneration = value
		return err
	default:
		return nil
	}
}

func decodeChannelRuntimeMetaRowKey(prefix []byte, key []byte) (string, int64, uint16, bool) {
	if !bytes.HasPrefix(key, prefix) {
		return "", 0, 0, false
	}
	channelID, rest, err := keycodec.ReadString(key[len(prefix):])
	if err != nil || len(rest) != 10 {
		return "", 0, 0, false
	}
	ordered := binary.BigEndian.Uint64(rest[:8])
	channelType := int64(ordered ^ (uint64(1) << 63))
	familyID := binary.BigEndian.Uint16(rest[8:])
	return channelID, channelType, familyID, true
}

func validateChannelRuntimeMetaCursor(cursor ChannelRuntimeMetaCursor) error {
	if cursor == (ChannelRuntimeMetaCursor{}) {
		return nil
	}
	if cursor.ChannelID == "" {
		return dberrors.ErrInvalidArgument
	}
	return validateKeyString(cursor.ChannelID)
}

func encodeUint64Slice(values []uint64) []byte {
	out := binary.AppendUvarint(nil, uint64(len(values)))
	for _, value := range values {
		out = binary.BigEndian.AppendUint64(out, value)
	}
	return out
}

func decodeUint64Slice(value []byte) ([]uint64, error) {
	count, n := binary.Uvarint(value)
	if n <= 0 {
		return nil, dberrors.ErrCorruptValue
	}
	value = value[n:]
	if uint64(len(value)) != count*8 {
		return nil, dberrors.ErrCorruptValue
	}
	out := make([]uint64, 0, count)
	for len(value) > 0 {
		out = append(out, binary.BigEndian.Uint64(value[:8]))
		value = value[8:]
	}
	return out, nil
}

func normalizeUint64Set(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	out := append([]uint64(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	n := 1
	for i := 1; i < len(out); i++ {
		if out[i] == out[n-1] {
			continue
		}
		out[n] = out[i]
		n++
	}
	return out[:n]
}

func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func maxUint64(values ...uint64) uint64 {
	var out uint64
	for _, value := range values {
		if value > out {
			out = value
		}
	}
	return out
}
