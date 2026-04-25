package meta

import (
	"context"

	"github.com/cockroachdb/pebble/v2"
)

func (db *DB) ExportSlotSnapshot(ctx context.Context, slotID uint64) (SlotSnapshot, error) {
	hashSlot, err := legacySlotIDToHashSlot(slotID)
	if err != nil {
		return SlotSnapshot{}, err
	}
	snap, err := db.ExportHashSlotSnapshot(ctx, []uint16{hashSlot})
	if err != nil {
		return SlotSnapshot{}, err
	}
	snap.SlotID = slotID
	return snap, nil
}

func (db *DB) ImportSlotSnapshot(ctx context.Context, snap SlotSnapshot) error {
	if len(snap.HashSlots) == 0 {
		hashSlot, err := legacySlotIDToHashSlot(snap.SlotID)
		if err != nil {
			return err
		}
		snap.HashSlots = []uint16{hashSlot}
	}
	return db.ImportHashSlotSnapshot(ctx, snap)
}

func (db *DB) ExportHashSlotSnapshot(ctx context.Context, hashSlots []uint16) (SlotSnapshot, error) {
	if len(hashSlots) == 0 {
		return SlotSnapshot{}, ErrInvalidArgument
	}
	if err := db.checkContext(ctx); err != nil {
		return SlotSnapshot{}, err
	}

	db.mu.RLock()
	defer db.mu.RUnlock()

	view := db.db.NewSnapshot()
	defer view.Close()

	data, countPos := beginSlotSnapshotPayload(hashSlots)
	entryCount := 0
	for _, hashSlot := range hashSlots {
		for _, span := range hashSlotAllDataSpans(hashSlot) {
			iter, err := view.NewIter(&pebble.IterOptions{
				LowerBound: span.Start,
				UpperBound: span.End,
			})
			if err != nil {
				return SlotSnapshot{}, err
			}

			for iter.First(); iter.Valid(); iter.Next() {
				if err := db.checkContext(ctx); err != nil {
					iter.Close()
					return SlotSnapshot{}, err
				}

				value, err := iter.ValueAndErr()
				if err != nil {
					iter.Close()
					return SlotSnapshot{}, err
				}

				data = appendSlotSnapshotEntry(data, iter.Key(), value)
				entryCount++
			}
			if err := iter.Error(); err != nil {
				iter.Close()
				return SlotSnapshot{}, err
			}
			if err := iter.Close(); err != nil {
				return SlotSnapshot{}, err
			}
		}
	}

	data, stats := finishSlotSnapshotPayload(data, countPos, entryCount)
	snap := SlotSnapshot{
		HashSlots: append([]uint16(nil), hashSlots...),
		Data:      data,
		Stats:     stats,
	}
	if len(hashSlots) == 1 {
		snap.SlotID = uint64(hashSlots[0])
	}
	return snap, nil
}

func (db *DB) ImportHashSlotSnapshot(ctx context.Context, snap SlotSnapshot) error {
	if len(snap.HashSlots) == 0 {
		return ErrInvalidArgument
	}
	if err := db.checkContext(ctx); err != nil {
		return err
	}

	decoded, err := decodeSlotSnapshotPayload(snap.Data)
	if err != nil {
		return err
	}
	if len(decoded.HashSlots) != len(snap.HashSlots) {
		return ErrInvalidArgument
	}
	for i := range decoded.HashSlots {
		if decoded.HashSlots[i] != snap.HashSlots[i] {
			return ErrInvalidArgument
		}
	}

	if db.testHooks.beforeImportCommit == nil {
		db.mu.Lock()
		defer db.mu.Unlock()

		batch := db.db.NewBatch()
		defer batch.Close()

		for _, hashSlot := range snap.HashSlots {
			for _, span := range hashSlotAllDataSpans(hashSlot) {
				if err := batch.DeleteRange(span.Start, span.End, nil); err != nil {
					return err
				}
			}
		}
		for _, entry := range decoded.Entries {
			if err := batch.Set(entry.Key, entry.Value, nil); err != nil {
				return err
			}
		}

		return batch.Commit(pebble.Sync)
	}

	for _, hashSlot := range snap.HashSlots {
		if err := db.DeleteHashSlotData(ctx, hashSlot); err != nil {
			return err
		}
	}
	if err := db.checkContext(ctx); err != nil {
		return err
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	batch := db.db.NewBatch()
	defer batch.Close()

	for _, entry := range decoded.Entries {
		if err := batch.Set(entry.Key, entry.Value, nil); err != nil {
			return err
		}
	}

	if db.testHooks.beforeImportCommit != nil {
		if err := db.testHooks.beforeImportCommit(); err != nil {
			return err
		}
	}

	return batch.Commit(pebble.Sync)
}
