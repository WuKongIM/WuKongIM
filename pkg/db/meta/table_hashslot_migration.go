package meta

import (
	"bytes"
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	hashSlotMigrationRecordState        byte = 0x01
	hashSlotMigrationRecordAppliedDelta byte = 0x02
	hashSlotMigrationRecordOutbox       byte = 0x03

	hashSlotMigrationStateValueVersion  byte  = 1
	hashSlotMigrationStateValueLen            = 1 + 8 + 8 + 1 + 8 + 8 + 8
	hashSlotMigrationPhaseDone          uint8 = 3
	hashSlotMigrationOutboxValueVersion byte  = 1

	hashSlotMigrationPrimaryFamilyID uint16 = 0
	hashSlotMigrationPrimaryIndexID  uint16 = 1

	hashSlotMigrationColumnRecordType uint16 = 1
	hashSlotMigrationColumnValue      uint16 = 2
)

var hashSlotMigrationTable = registerMetaTable(TableSpec[HashSlotMigrationState]{
	ID:             TableIDHashSlotMigration,
	Name:           "hashslot_migration",
	SnapshotPolicy: SnapshotPolicy{PreserveOnImport: true},
	Columns: []schema.Column{
		{ID: hashSlotMigrationColumnRecordType, Name: "record_type", Type: schema.TypeInt64, Required: true},
		{ID: hashSlotMigrationColumnValue, Name: "value", Type: schema.TypeBytes},
	},
	Families: []schema.Family{{ID: hashSlotMigrationPrimaryFamilyID, Name: "primary", Columns: []uint16{hashSlotMigrationColumnValue}}},
	Primary: PrimarySpec[HashSlotMigrationState]{
		IndexID:          hashSlotMigrationPrimaryIndexID,
		FamilyID:         hashSlotMigrationPrimaryFamilyID,
		Name:             "pk_hashslot_migration",
		Columns:          []uint16{hashSlotMigrationColumnRecordType},
		Layout:           KeyLayout{KeyUint8},
		OmitFamilySuffix: true,
		Key: func(HashSlotMigrationState) KeyParts {
			return hashSlotMigrationStatePrimaryKey()
		},
	},
	EncodeValue: func(state HashSlotMigrationState) ([]byte, error) {
		return encodeHashSlotMigrationStateValue(state), nil
	},
	DecodeValueWithKey: func(primaryKey []byte, _ KeyParts, value []byte) (HashSlotMigrationState, error) {
		hashSlot, ok := isMetaRowKeyForTable(primaryKey, TableIDHashSlotMigration)
		if !ok {
			return HashSlotMigrationState{}, dberrors.ErrCorruptValue
		}
		return decodeHashSlotMigrationStateValue(hashSlot, value)
	},
})

// HashSlotMigrationTable describes the hash-slot migration table schema.
var HashSlotMigrationTable = hashSlotMigrationTable.Schema()

// HashSlotMigrationState stores durable migration progress for one hash slot.
type HashSlotMigrationState struct {
	// HashSlot identifies the hash slot being moved.
	HashSlot HashSlot
	// SourceSlot identifies the Raft slot that currently owns the hash slot.
	SourceSlot uint64
	// TargetSlot identifies the Raft slot that will own the hash slot after cutover.
	TargetSlot uint64
	// Phase stores the migration phase using cluster hash-slot phase values.
	Phase uint8
	// FenceIndex is the highest source log index to replay before cutover.
	FenceIndex uint64
	// LastOutboxIndex is the highest source log index copied to the outbox.
	LastOutboxIndex uint64
	// LastAckedIndex is the highest outbox index acknowledged by the target.
	LastAckedIndex uint64
}

// AppliedHashSlotDelta identifies an already-applied migration delta record.
type AppliedHashSlotDelta struct {
	// HashSlot identifies the target hash slot that accepted the delta.
	HashSlot HashSlot
	// SourceSlot identifies the source Raft slot that emitted the delta.
	SourceSlot uint64
	// SourceIndex identifies the source log index that produced the delta.
	SourceIndex uint64
}

// HashSlotMigrationOutboxRow stores a source delta waiting for target replay.
type HashSlotMigrationOutboxRow struct {
	// HashSlot identifies the migrating hash slot for this source delta.
	HashSlot HashSlot
	// SourceSlot identifies the source Raft slot that emitted the delta.
	SourceSlot uint64
	// TargetSlot identifies the target Raft slot that should accept the delta.
	TargetSlot uint64
	// SourceIndex identifies the source log index that produced the delta.
	SourceIndex uint64
	// Data is the original source command payload to wrap in apply_delta.
	Data []byte
}

// UpsertHashSlotMigrationState stores migration progress for this shard.
func (s *Shard) UpsertHashSlotMigrationState(ctx context.Context, state HashSlotMigrationState) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := s.validateHashSlotMigrationState(state); err != nil {
		return err
	}
	return hashSlotMigrationTable.Upsert(ctx, s, state)
}

// LoadHashSlotMigrationState loads migration progress for this shard.
func (s *Shard) LoadHashSlotMigrationState(ctx context.Context) (HashSlotMigrationState, bool, error) {
	if err := s.check(ctx); err != nil {
		return HashSlotMigrationState{}, false, err
	}
	return hashSlotMigrationTable.Get(ctx, s, hashSlotMigrationStatePrimaryKey())
}

// DeleteHashSlotMigrationState removes migration progress for this shard.
func (s *Shard) DeleteHashSlotMigrationState(ctx context.Context) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	return hashSlotMigrationTable.Delete(ctx, s, hashSlotMigrationStatePrimaryKey())
}

// MarkAppliedHashSlotDelta records a migration delta as applied.
func (s *Shard) MarkAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := s.validateAppliedHashSlotDelta(delta); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeAppliedHashSlotDeltaKey(delta), nil); err != nil {
		return err
	}
	return batch.Commit(true)
}

// HasAppliedHashSlotDelta reports whether a migration delta was already applied.
func (s *Shard) HasAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) (bool, error) {
	if err := s.check(ctx); err != nil {
		return false, err
	}
	if err := s.validateAppliedHashSlotDelta(delta); err != nil {
		return false, err
	}
	_, ok, err := s.db.get(encodeAppliedHashSlotDeltaKey(delta))
	return ok, err
}

// ListAppliedHashSlotDeltas returns applied delta records ordered by source slot and source index.
func (s *Shard) ListAppliedHashSlotDeltas(ctx context.Context) ([]AppliedHashSlotDelta, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	prefix := encodeAppliedHashSlotDeltaPrefix(s.hashSlot)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	deltas := make([]AppliedHashSlotDelta, 0, 8)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		delta, err := decodeAppliedHashSlotDeltaKey(s.hashSlot, prefix, iter.Key())
		if err != nil {
			return nil, err
		}
		deltas = append(deltas, delta)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return deltas, nil
}

// DeleteAppliedHashSlotDelta removes an applied delta dedup record.
func (s *Shard) DeleteAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := s.validateAppliedHashSlotDelta(delta); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Delete(encodeAppliedHashSlotDeltaKey(delta)); err != nil {
		return err
	}
	return batch.Commit(true)
}

// UpsertHashSlotMigrationOutbox stores a source delta outbox row.
func (s *Shard) UpsertHashSlotMigrationOutbox(ctx context.Context, row HashSlotMigrationOutboxRow) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := s.validateHashSlotMigrationOutboxRow(row); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	key := encodeHashSlotMigrationOutboxKey(s.hashSlot, row.SourceSlot, row.TargetSlot, row.SourceIndex)
	if err := batch.Set(key, encodeHashSlotMigrationOutboxValue(row)); err != nil {
		return err
	}
	return batch.Commit(true)
}

// LoadHashSlotMigrationOutbox loads one source delta outbox row.
func (s *Shard) LoadHashSlotMigrationOutbox(ctx context.Context, sourceSlot, targetSlot, sourceIndex uint64) (HashSlotMigrationOutboxRow, bool, error) {
	if err := s.check(ctx); err != nil {
		return HashSlotMigrationOutboxRow{}, false, err
	}
	if err := validateHashSlotMigrationOutboxIdentity(sourceSlot, targetSlot); err != nil {
		return HashSlotMigrationOutboxRow{}, false, err
	}
	if sourceIndex == 0 {
		return HashSlotMigrationOutboxRow{}, false, dberrors.ErrInvalidArgument
	}
	key := encodeHashSlotMigrationOutboxKey(s.hashSlot, sourceSlot, targetSlot, sourceIndex)
	value, ok, err := s.db.get(key)
	if err != nil || !ok {
		return HashSlotMigrationOutboxRow{}, ok, err
	}
	row, err := decodeHashSlotMigrationOutboxValue(s.hashSlot, key, value)
	if err != nil {
		return HashSlotMigrationOutboxRow{}, false, err
	}
	return row, true, nil
}

// ListHashSlotMigrationOutbox returns source delta rows strictly after afterSourceIndex.
func (s *Shard) ListHashSlotMigrationOutbox(ctx context.Context, sourceSlot, targetSlot, afterSourceIndex uint64, limit int) ([]HashSlotMigrationOutboxRow, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if err := validateHashSlotMigrationOutboxIdentity(sourceSlot, targetSlot); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, dberrors.ErrInvalidArgument
	}
	if afterSourceIndex == ^uint64(0) {
		return nil, nil
	}
	prefix := encodeHashSlotMigrationOutboxPrefix(s.hashSlot, sourceSlot, targetSlot)
	span := keycodec.NewPrefixSpan(prefix)
	if afterSourceIndex > 0 {
		span.Start = encodeHashSlotMigrationOutboxKey(s.hashSlot, sourceSlot, targetSlot, afterSourceIndex+1)
	}
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	rows := make([]HashSlotMigrationOutboxRow, 0, limit)
	for ok := iter.First(); ok && len(rows) < limit; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		row, err := decodeHashSlotMigrationOutboxValue(s.hashSlot, iter.Key(), value)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return rows, nil
}

// DeleteHashSlotMigrationOutbox removes one source delta outbox row.
func (s *Shard) DeleteHashSlotMigrationOutbox(ctx context.Context, sourceSlot, targetSlot, sourceIndex uint64) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateHashSlotMigrationOutboxIdentity(sourceSlot, targetSlot); err != nil {
		return err
	}
	if sourceIndex == 0 {
		return dberrors.ErrInvalidArgument
	}
	unlock := s.lock()
	defer unlock()

	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Delete(encodeHashSlotMigrationOutboxKey(s.hashSlot, sourceSlot, targetSlot, sourceIndex)); err != nil {
		return err
	}
	return batch.Commit(true)
}

// DeleteHashSlotMigrationOutboxThrough removes outbox rows through sourceIndex.
func (s *Shard) DeleteHashSlotMigrationOutboxThrough(ctx context.Context, sourceSlot, targetSlot, sourceIndex uint64) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateHashSlotMigrationOutboxIdentity(sourceSlot, targetSlot); err != nil {
		return err
	}
	if sourceIndex == 0 {
		return dberrors.ErrInvalidArgument
	}
	unlock := s.lock()
	defer unlock()

	prefix := encodeHashSlotMigrationOutboxPrefix(s.hashSlot, sourceSlot, targetSlot)
	upper := keycodec.PrefixEnd(prefix)
	if sourceIndex < ^uint64(0) {
		upper = encodeHashSlotMigrationOutboxKey(s.hashSlot, sourceSlot, targetSlot, sourceIndex+1)
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.DeleteRange(engine.Span{Start: prefix, End: upper}); err != nil {
		return err
	}
	return batch.Commit(true)
}

// DeleteAllHashSlotMigrationOutbox removes every source delta outbox row for this shard.
func (s *Shard) DeleteAllHashSlotMigrationOutbox(ctx context.Context) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()

	prefix := encodeHashSlotMigrationOutboxHashSlotPrefix(s.hashSlot)
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.DeleteRange(engine.Span{Start: prefix, End: keycodec.PrefixEnd(prefix)}); err != nil {
		return err
	}
	return batch.Commit(true)
}

func (s *Shard) validateHashSlotMigrationState(state HashSlotMigrationState) error {
	if state.HashSlot != s.hashSlot {
		return dberrors.ErrInvalidArgument
	}
	if state.SourceSlot == 0 || state.TargetSlot == 0 || state.SourceSlot == state.TargetSlot {
		return dberrors.ErrInvalidArgument
	}
	if state.Phase > hashSlotMigrationPhaseDone {
		return dberrors.ErrInvalidArgument
	}
	if state.LastAckedIndex > state.LastOutboxIndex || state.FenceIndex > state.LastOutboxIndex {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func (s *Shard) validateAppliedHashSlotDelta(delta AppliedHashSlotDelta) error {
	if delta.HashSlot != s.hashSlot || delta.SourceSlot == 0 || delta.SourceIndex == 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func (s *Shard) validateHashSlotMigrationOutboxRow(row HashSlotMigrationOutboxRow) error {
	if row.HashSlot != s.hashSlot {
		return dberrors.ErrInvalidArgument
	}
	if err := validateHashSlotMigrationOutboxIdentity(row.SourceSlot, row.TargetSlot); err != nil {
		return err
	}
	if row.SourceIndex == 0 || len(row.Data) == 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func validateHashSlotMigrationOutboxIdentity(sourceSlot, targetSlot uint64) error {
	if sourceSlot == 0 || targetSlot == 0 || sourceSlot == targetSlot {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func hashSlotMigrationStatePrimaryKey() KeyParts {
	return KeyParts{Uint8(hashSlotMigrationRecordState)}
}

func encodeHashSlotMigrationStateValue(state HashSlotMigrationState) []byte {
	value := make([]byte, 0, hashSlotMigrationStateValueLen)
	value = append(value, hashSlotMigrationStateValueVersion)
	value = binary.BigEndian.AppendUint64(value, state.SourceSlot)
	value = binary.BigEndian.AppendUint64(value, state.TargetSlot)
	value = append(value, state.Phase)
	value = binary.BigEndian.AppendUint64(value, state.FenceIndex)
	value = binary.BigEndian.AppendUint64(value, state.LastOutboxIndex)
	return binary.BigEndian.AppendUint64(value, state.LastAckedIndex)
}

func decodeHashSlotMigrationStateValue(hashSlot HashSlot, value []byte) (HashSlotMigrationState, error) {
	if len(value) != hashSlotMigrationStateValueLen || value[0] != hashSlotMigrationStateValueVersion {
		return HashSlotMigrationState{}, dberrors.ErrCorruptValue
	}
	return HashSlotMigrationState{
		HashSlot:        hashSlot,
		SourceSlot:      binary.BigEndian.Uint64(value[1:9]),
		TargetSlot:      binary.BigEndian.Uint64(value[9:17]),
		Phase:           value[17],
		FenceIndex:      binary.BigEndian.Uint64(value[18:26]),
		LastOutboxIndex: binary.BigEndian.Uint64(value[26:34]),
		LastAckedIndex:  binary.BigEndian.Uint64(value[34:42]),
	}, nil
}

func decodeAppliedHashSlotDeltaKey(hashSlot HashSlot, prefix []byte, key []byte) (AppliedHashSlotDelta, error) {
	if !bytes.HasPrefix(key, prefix) {
		return AppliedHashSlotDelta{}, dberrors.ErrCorruptValue
	}
	rest := key[len(prefix):]
	if len(rest) != 16 {
		return AppliedHashSlotDelta{}, dberrors.ErrCorruptValue
	}
	return AppliedHashSlotDelta{
		HashSlot:    hashSlot,
		SourceSlot:  binary.BigEndian.Uint64(rest[:8]),
		SourceIndex: binary.BigEndian.Uint64(rest[8:]),
	}, nil
}

func encodeHashSlotMigrationOutboxValue(row HashSlotMigrationOutboxRow) []byte {
	value := make([]byte, 0, 1+len(row.Data))
	value = append(value, hashSlotMigrationOutboxValueVersion)
	return append(value, row.Data...)
}

func decodeHashSlotMigrationOutboxValue(hashSlot HashSlot, key []byte, value []byte) (HashSlotMigrationOutboxRow, error) {
	prefix := encodeHashSlotMigrationOutboxHashSlotPrefix(hashSlot)
	if !bytes.HasPrefix(key, prefix) {
		return HashSlotMigrationOutboxRow{}, dberrors.ErrCorruptValue
	}
	rest := key[len(prefix):]
	if len(rest) != 24 || len(value) < 1 || value[0] != hashSlotMigrationOutboxValueVersion {
		return HashSlotMigrationOutboxRow{}, dberrors.ErrCorruptValue
	}
	return HashSlotMigrationOutboxRow{
		HashSlot:    hashSlot,
		SourceSlot:  binary.BigEndian.Uint64(rest[:8]),
		TargetSlot:  binary.BigEndian.Uint64(rest[8:16]),
		SourceIndex: binary.BigEndian.Uint64(rest[16:24]),
		Data:        append([]byte(nil), value[1:]...),
	}, nil
}
