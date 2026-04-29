package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"

	"github.com/cockroachdb/pebble/v2"
)

const (
	metaRecordHashSlotMigrationState  byte = 0x01
	metaRecordAppliedHashSlotDelta    byte = 0x02
	metaRecordHashSlotMigrationOutbox byte = 0x03

	hashSlotMigrationStateValueVersion  byte  = 0x01
	hashSlotMigrationStateValueLen            = 1 + 8 + 8 + 1 + 8 + 8 + 8
	hashSlotMigrationPhaseDone          uint8 = 0x03
	hashSlotMigrationOutboxValueVersion byte  = 0x01
)

// HashSlotMigrationState stores durable migration progress for one hash slot.
type HashSlotMigrationState struct {
	// HashSlot identifies the hash slot being moved.
	HashSlot uint16
	// SourceSlot identifies the Raft slot that currently owns the hash slot.
	SourceSlot uint64
	// TargetSlot identifies the Raft slot that will own the hash slot after cutover.
	TargetSlot uint64
	// Phase stores the migration phase using the cluster hash-slot phase values.
	Phase uint8
	// FenceIndex is the highest source log index that must be replayed before cutover.
	FenceIndex uint64
	// LastOutboxIndex is the highest source log index copied into the migration outbox.
	LastOutboxIndex uint64
	// LastAckedIndex is the highest outbox index acknowledged by the target slot.
	LastAckedIndex uint64
}

// AppliedHashSlotDelta identifies an already-applied migration delta record.
type AppliedHashSlotDelta struct {
	// HashSlot identifies the target hash slot that accepted the delta.
	HashSlot uint16
	// SourceSlot identifies the source Raft slot that emitted the delta.
	SourceSlot uint64
	// SourceIndex identifies the source log index that produced the delta.
	SourceIndex uint64
}

// HashSlotMigrationOutboxRow stores a source delta waiting for target replay.
type HashSlotMigrationOutboxRow struct {
	// HashSlot identifies the migrating hash slot for this source delta.
	HashSlot uint16
	// SourceSlot identifies the source Raft slot that emitted the delta.
	SourceSlot uint64
	// TargetSlot identifies the target Raft slot that should accept the delta.
	TargetSlot uint64
	// SourceIndex identifies the source log index that produced the delta.
	SourceIndex uint64
	// Data is the original source command payload to wrap in apply_delta.
	Data []byte
}

// UpsertHashSlotMigrationState stores migration progress for state.HashSlot.
func (db *DB) UpsertHashSlotMigrationState(ctx context.Context, state HashSlotMigrationState) error {
	if err := db.checkContext(ctx); err != nil {
		return err
	}
	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.UpsertHashSlotMigrationState(state); err != nil {
		return err
	}
	return wb.Commit()
}

// LoadHashSlotMigrationState loads migration progress for hashSlot.
func (db *DB) LoadHashSlotMigrationState(ctx context.Context, hashSlot uint16) (HashSlotMigrationState, error) {
	if err := validateHashSlot(hashSlot); err != nil {
		return HashSlotMigrationState{}, err
	}
	if err := db.checkContext(ctx); err != nil {
		return HashSlotMigrationState{}, err
	}

	db.mu.RLock()
	defer db.mu.RUnlock()

	key := encodeHashSlotMigrationStateKey(hashSlot)
	value, err := db.getValue(key)
	if err != nil {
		return HashSlotMigrationState{}, err
	}
	return decodeHashSlotMigrationStateValue(hashSlot, value)
}

// ListHashSlotMigrationStates returns all persisted migration states ordered by hash slot.
func (db *DB) ListHashSlotMigrationStates(ctx context.Context) ([]HashSlotMigrationState, error) {
	if err := db.checkContext(ctx); err != nil {
		return nil, err
	}

	db.mu.RLock()
	defer db.mu.RUnlock()

	iter, err := db.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte{keyspaceMeta},
		UpperBound: []byte{keyspaceMeta + 1},
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	states := make([]HashSlotMigrationState, 0, 8)
	for ok := iter.First(); ok; {
		if err := db.checkContext(ctx); err != nil {
			return nil, err
		}
		key := iter.Key()
		if len(key) < 1 || key[0] != keyspaceMeta {
			ok = iter.Next()
			continue
		}
		if len(key) < 1+2 {
			ok = iter.Next()
			continue
		}
		hashSlot := decodeMetaHashSlot(key)
		stateKey := encodeHashSlotMigrationStateKey(hashSlot)
		if cmp := bytes.Compare(key, stateKey); cmp < 0 {
			ok = iter.SeekGE(stateKey)
			continue
		} else if cmp > 0 {
			ok = seekNextMetaHashSlot(iter, hashSlot)
			continue
		}
		value, err := iter.ValueAndErr()
		if err != nil {
			return nil, err
		}
		state, err := decodeHashSlotMigrationStateValue(hashSlot, value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
		ok = seekNextMetaHashSlot(iter, hashSlot)
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return states, nil
}

// DeleteHashSlotMigrationState removes migration progress for hashSlot.
func (db *DB) DeleteHashSlotMigrationState(ctx context.Context, hashSlot uint16) error {
	if err := db.checkContext(ctx); err != nil {
		return err
	}
	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.DeleteHashSlotMigrationState(hashSlot); err != nil {
		return err
	}
	return wb.Commit()
}

// MarkAppliedHashSlotDelta records a migration delta as applied.
func (db *DB) MarkAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) error {
	if err := db.checkContext(ctx); err != nil {
		return err
	}
	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.MarkAppliedHashSlotDelta(delta); err != nil {
		return err
	}
	return wb.Commit()
}

// HasAppliedHashSlotDelta reports whether a migration delta was already applied.
func (db *DB) HasAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) (bool, error) {
	if err := validateAppliedHashSlotDelta(delta); err != nil {
		return false, err
	}
	if err := db.checkContext(ctx); err != nil {
		return false, err
	}

	db.mu.RLock()
	defer db.mu.RUnlock()

	return db.hasKey(encodeAppliedHashSlotDeltaKey(delta))
}

// ListAppliedHashSlotDeltas returns applied delta records for hashSlot ordered by source slot and source index.
func (db *DB) ListAppliedHashSlotDeltas(ctx context.Context, hashSlot uint16) ([]AppliedHashSlotDelta, error) {
	if err := validateHashSlot(hashSlot); err != nil {
		return nil, err
	}
	if err := db.checkContext(ctx); err != nil {
		return nil, err
	}

	db.mu.RLock()
	defer db.mu.RUnlock()

	prefix := encodeAppliedHashSlotDeltaPrefix(hashSlot)
	iter, err := db.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: nextPrefix(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	deltas := make([]AppliedHashSlotDelta, 0, 8)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := db.checkContext(ctx); err != nil {
			return nil, err
		}
		delta, err := decodeAppliedHashSlotDeltaKey(iter.Key())
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
func (db *DB) DeleteAppliedHashSlotDelta(ctx context.Context, delta AppliedHashSlotDelta) error {
	if err := db.checkContext(ctx); err != nil {
		return err
	}
	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.DeleteAppliedHashSlotDelta(delta); err != nil {
		return err
	}
	return wb.Commit()
}

// UpsertHashSlotMigrationOutbox stores a source delta outbox row.
func (db *DB) UpsertHashSlotMigrationOutbox(ctx context.Context, row HashSlotMigrationOutboxRow) error {
	if err := db.checkContext(ctx); err != nil {
		return err
	}
	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.UpsertHashSlotMigrationOutbox(row); err != nil {
		return err
	}
	return wb.Commit()
}

// LoadHashSlotMigrationOutbox loads one source delta outbox row.
func (db *DB) LoadHashSlotMigrationOutbox(ctx context.Context, hashSlot uint16, sourceSlot, targetSlot, sourceIndex uint64) (HashSlotMigrationOutboxRow, error) {
	if err := validateHashSlotMigrationOutboxIdentity(hashSlot, sourceSlot, targetSlot); err != nil {
		return HashSlotMigrationOutboxRow{}, err
	}
	if sourceIndex == 0 {
		return HashSlotMigrationOutboxRow{}, ErrInvalidArgument
	}
	if err := db.checkContext(ctx); err != nil {
		return HashSlotMigrationOutboxRow{}, err
	}

	db.mu.RLock()
	defer db.mu.RUnlock()

	key := encodeHashSlotMigrationOutboxKey(hashSlot, sourceSlot, targetSlot, sourceIndex)
	value, err := db.getValue(key)
	if err != nil {
		return HashSlotMigrationOutboxRow{}, err
	}
	return decodeHashSlotMigrationOutboxValue(key, value)
}

// ListHashSlotMigrationOutbox returns source delta rows strictly after afterSourceIndex.
func (db *DB) ListHashSlotMigrationOutbox(ctx context.Context, hashSlot uint16, sourceSlot, targetSlot, afterSourceIndex uint64, limit int) ([]HashSlotMigrationOutboxRow, error) {
	if err := validateHashSlotMigrationOutboxIdentity(hashSlot, sourceSlot, targetSlot); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, ErrInvalidArgument
	}
	if err := db.checkContext(ctx); err != nil {
		return nil, err
	}
	if afterSourceIndex == math.MaxUint64 {
		return nil, nil
	}

	db.mu.RLock()
	defer db.mu.RUnlock()

	prefix := encodeHashSlotMigrationOutboxPrefix(hashSlot, sourceSlot, targetSlot)
	lower := prefix
	if afterSourceIndex > 0 {
		lower = encodeHashSlotMigrationOutboxKey(hashSlot, sourceSlot, targetSlot, afterSourceIndex+1)
	}
	iter, err := db.db.NewIter(&pebble.IterOptions{
		LowerBound: lower,
		UpperBound: nextPrefix(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	rows := make([]HashSlotMigrationOutboxRow, 0, limit)
	for ok := iter.First(); ok && len(rows) < limit; ok = iter.Next() {
		if err := db.checkContext(ctx); err != nil {
			return nil, err
		}
		value, err := iter.ValueAndErr()
		if err != nil {
			return nil, err
		}
		row, err := decodeHashSlotMigrationOutboxValue(iter.Key(), value)
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
func (db *DB) DeleteHashSlotMigrationOutbox(ctx context.Context, hashSlot uint16, sourceSlot, targetSlot, sourceIndex uint64) error {
	if err := db.checkContext(ctx); err != nil {
		return err
	}
	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.DeleteHashSlotMigrationOutbox(hashSlot, sourceSlot, targetSlot, sourceIndex); err != nil {
		return err
	}
	return wb.Commit()
}

// DeleteAllHashSlotMigrationOutbox removes all source delta outbox rows for hashSlot.
func (db *DB) DeleteAllHashSlotMigrationOutbox(ctx context.Context, hashSlot uint16) error {
	if err := db.checkContext(ctx); err != nil {
		return err
	}
	wb := db.NewWriteBatch()
	defer wb.Close()
	if err := wb.DeleteAllHashSlotMigrationOutbox(hashSlot); err != nil {
		return err
	}
	return wb.Commit()
}

// UpsertHashSlotMigrationState stages migration progress for state.HashSlot.
func (b *WriteBatch) UpsertHashSlotMigrationState(state HashSlotMigrationState) error {
	if err := validateHashSlotMigrationState(state); err != nil {
		return err
	}
	key := encodeHashSlotMigrationStateKey(state.HashSlot)
	return b.batch.Set(key, encodeHashSlotMigrationStateValue(state), nil)
}

// DeleteHashSlotMigrationState stages removal of migration progress for hashSlot.
func (b *WriteBatch) DeleteHashSlotMigrationState(hashSlot uint16) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	return b.batch.Delete(encodeHashSlotMigrationStateKey(hashSlot), nil)
}

// MarkAppliedHashSlotDelta stages an applied delta dedup record.
func (b *WriteBatch) MarkAppliedHashSlotDelta(delta AppliedHashSlotDelta) error {
	if err := validateAppliedHashSlotDelta(delta); err != nil {
		return err
	}
	return b.batch.Set(encodeAppliedHashSlotDeltaKey(delta), nil, nil)
}

// DeleteAppliedHashSlotDelta stages removal of an applied delta dedup record.
func (b *WriteBatch) DeleteAppliedHashSlotDelta(delta AppliedHashSlotDelta) error {
	if err := validateAppliedHashSlotDelta(delta); err != nil {
		return err
	}
	return b.batch.Delete(encodeAppliedHashSlotDeltaKey(delta), nil)
}

// UpsertHashSlotMigrationOutbox stages a source delta outbox row.
func (b *WriteBatch) UpsertHashSlotMigrationOutbox(row HashSlotMigrationOutboxRow) error {
	if err := validateHashSlotMigrationOutboxRow(row); err != nil {
		return err
	}
	return b.batch.Set(encodeHashSlotMigrationOutboxKey(row.HashSlot, row.SourceSlot, row.TargetSlot, row.SourceIndex), encodeHashSlotMigrationOutboxValue(row), nil)
}

// DeleteHashSlotMigrationOutbox stages removal of a source delta outbox row.
func (b *WriteBatch) DeleteHashSlotMigrationOutbox(hashSlot uint16, sourceSlot, targetSlot, sourceIndex uint64) error {
	if err := validateHashSlotMigrationOutboxIdentity(hashSlot, sourceSlot, targetSlot); err != nil {
		return err
	}
	if sourceIndex == 0 {
		return ErrInvalidArgument
	}
	return b.batch.Delete(encodeHashSlotMigrationOutboxKey(hashSlot, sourceSlot, targetSlot, sourceIndex), nil)
}

// DeleteHashSlotMigrationOutboxForPair stages removal of all outbox rows for one migration pair.
func (b *WriteBatch) DeleteHashSlotMigrationOutboxForPair(hashSlot uint16, sourceSlot, targetSlot uint64) error {
	if err := validateHashSlotMigrationOutboxIdentity(hashSlot, sourceSlot, targetSlot); err != nil {
		return err
	}
	prefix := encodeHashSlotMigrationOutboxPrefix(hashSlot, sourceSlot, targetSlot)
	return b.batch.DeleteRange(prefix, nextPrefix(prefix), nil)
}

// DeleteHashSlotMigrationOutboxThrough stages removal of outbox rows through sourceIndex.
func (b *WriteBatch) DeleteHashSlotMigrationOutboxThrough(hashSlot uint16, sourceSlot, targetSlot, sourceIndex uint64) error {
	if err := validateHashSlotMigrationOutboxIdentity(hashSlot, sourceSlot, targetSlot); err != nil {
		return err
	}
	if sourceIndex == 0 {
		return ErrInvalidArgument
	}
	prefix := encodeHashSlotMigrationOutboxPrefix(hashSlot, sourceSlot, targetSlot)
	upper := nextPrefix(prefix)
	if sourceIndex < math.MaxUint64 {
		upper = encodeHashSlotMigrationOutboxKey(hashSlot, sourceSlot, targetSlot, sourceIndex+1)
	}
	return b.batch.DeleteRange(prefix, upper, nil)
}

// DeleteAllHashSlotMigrationOutbox stages removal of every source delta outbox row for hashSlot.
func (b *WriteBatch) DeleteAllHashSlotMigrationOutbox(hashSlot uint16) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	prefix := encodeHashSlotMigrationOutboxHashSlotPrefix(hashSlot)
	return b.batch.DeleteRange(prefix, nextPrefix(prefix), nil)
}

func validateHashSlotMigrationState(state HashSlotMigrationState) error {
	if err := validateHashSlot(state.HashSlot); err != nil {
		return err
	}
	if state.SourceSlot == 0 || state.TargetSlot == 0 {
		return ErrInvalidArgument
	}
	if state.SourceSlot == state.TargetSlot {
		return ErrInvalidArgument
	}
	if state.Phase > hashSlotMigrationPhaseDone {
		return ErrInvalidArgument
	}
	if state.LastAckedIndex > state.LastOutboxIndex {
		return ErrInvalidArgument
	}
	if state.FenceIndex > state.LastOutboxIndex {
		return ErrInvalidArgument
	}
	return nil
}

func validateAppliedHashSlotDelta(delta AppliedHashSlotDelta) error {
	if err := validateHashSlot(delta.HashSlot); err != nil {
		return err
	}
	if delta.SourceSlot == 0 || delta.SourceIndex == 0 {
		return ErrInvalidArgument
	}
	return nil
}

func validateHashSlotMigrationOutboxRow(row HashSlotMigrationOutboxRow) error {
	if err := validateHashSlotMigrationOutboxIdentity(row.HashSlot, row.SourceSlot, row.TargetSlot); err != nil {
		return err
	}
	if row.SourceIndex == 0 || len(row.Data) == 0 {
		return ErrInvalidArgument
	}
	return nil
}

func validateHashSlotMigrationOutboxIdentity(hashSlot uint16, sourceSlot, targetSlot uint64) error {
	if err := validateHashSlot(hashSlot); err != nil {
		return err
	}
	if sourceSlot == 0 || targetSlot == 0 || sourceSlot == targetSlot {
		return ErrInvalidArgument
	}
	return nil
}

func encodeHashSlotMigrationStateKey(hashSlot uint16) []byte {
	key := encodeMetaPrefix(hashSlot)
	key = append(key, metaRecordHashSlotMigrationState)
	return key
}

func isHashSlotMigrationMetaKey(key []byte) bool {
	if len(key) < 4 || key[0] != keyspaceMeta {
		return false
	}
	switch key[3] {
	case metaRecordHashSlotMigrationState, metaRecordAppliedHashSlotDelta, metaRecordHashSlotMigrationOutbox:
		return true
	default:
		return false
	}
}

func seekNextMetaHashSlot(iter *pebble.Iterator, hashSlot uint16) bool {
	if hashSlot == math.MaxUint16 {
		return false
	}
	return iter.SeekGE(encodeMetaPrefix(hashSlot + 1))
}

func encodeAppliedHashSlotDeltaPrefix(hashSlot uint16) []byte {
	key := encodeMetaPrefix(hashSlot)
	key = append(key, metaRecordAppliedHashSlotDelta)
	return key
}

func encodeAppliedHashSlotDeltaKey(delta AppliedHashSlotDelta) []byte {
	key := encodeAppliedHashSlotDeltaPrefix(delta.HashSlot)
	key = binary.BigEndian.AppendUint64(key, delta.SourceSlot)
	key = binary.BigEndian.AppendUint64(key, delta.SourceIndex)
	return key
}

func decodeAppliedHashSlotDeltaKey(key []byte) (AppliedHashSlotDelta, error) {
	const keyLen = 1 + 2 + 1 + 8 + 8
	if len(key) != keyLen || key[0] != keyspaceMeta || key[3] != metaRecordAppliedHashSlotDelta {
		return AppliedHashSlotDelta{}, ErrCorruptValue
	}
	return AppliedHashSlotDelta{
		HashSlot:    decodeMetaHashSlot(key),
		SourceSlot:  binary.BigEndian.Uint64(key[4:12]),
		SourceIndex: binary.BigEndian.Uint64(key[12:20]),
	}, nil
}

func encodeHashSlotMigrationOutboxPrefix(hashSlot uint16, sourceSlot, targetSlot uint64) []byte {
	key := encodeHashSlotMigrationOutboxHashSlotPrefix(hashSlot)
	key = binary.BigEndian.AppendUint64(key, sourceSlot)
	key = binary.BigEndian.AppendUint64(key, targetSlot)
	return key
}

func encodeHashSlotMigrationOutboxHashSlotPrefix(hashSlot uint16) []byte {
	key := encodeMetaPrefix(hashSlot)
	key = append(key, metaRecordHashSlotMigrationOutbox)
	return key
}

func encodeHashSlotMigrationOutboxKey(hashSlot uint16, sourceSlot, targetSlot, sourceIndex uint64) []byte {
	key := encodeHashSlotMigrationOutboxPrefix(hashSlot, sourceSlot, targetSlot)
	key = binary.BigEndian.AppendUint64(key, sourceIndex)
	return key
}

func encodeHashSlotMigrationOutboxValue(row HashSlotMigrationOutboxRow) []byte {
	value := make([]byte, 0, 1+len(row.Data))
	value = append(value, hashSlotMigrationOutboxValueVersion)
	value = append(value, row.Data...)
	return value
}

func decodeHashSlotMigrationOutboxValue(key, value []byte) (HashSlotMigrationOutboxRow, error) {
	const keyLen = 1 + 2 + 1 + 8 + 8 + 8
	if len(key) != keyLen || key[0] != keyspaceMeta || key[3] != metaRecordHashSlotMigrationOutbox {
		return HashSlotMigrationOutboxRow{}, ErrCorruptValue
	}
	if len(value) < 2 || value[0] != hashSlotMigrationOutboxValueVersion {
		return HashSlotMigrationOutboxRow{}, ErrCorruptValue
	}
	return HashSlotMigrationOutboxRow{
		HashSlot:    decodeMetaHashSlot(key),
		SourceSlot:  binary.BigEndian.Uint64(key[4:12]),
		TargetSlot:  binary.BigEndian.Uint64(key[12:20]),
		SourceIndex: binary.BigEndian.Uint64(key[20:28]),
		Data:        append([]byte(nil), value[1:]...),
	}, nil
}

func decodeMetaHashSlot(key []byte) uint16 {
	return binary.BigEndian.Uint16(key[1:3])
}

func encodeHashSlotMigrationStateValue(state HashSlotMigrationState) []byte {
	value := make([]byte, 0, hashSlotMigrationStateValueLen)
	value = append(value, hashSlotMigrationStateValueVersion)
	value = binary.BigEndian.AppendUint64(value, state.SourceSlot)
	value = binary.BigEndian.AppendUint64(value, state.TargetSlot)
	value = append(value, state.Phase)
	value = binary.BigEndian.AppendUint64(value, state.FenceIndex)
	value = binary.BigEndian.AppendUint64(value, state.LastOutboxIndex)
	value = binary.BigEndian.AppendUint64(value, state.LastAckedIndex)
	return value
}

func decodeHashSlotMigrationStateValue(hashSlot uint16, value []byte) (HashSlotMigrationState, error) {
	if len(value) != hashSlotMigrationStateValueLen || value[0] != hashSlotMigrationStateValueVersion {
		return HashSlotMigrationState{}, ErrCorruptValue
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
