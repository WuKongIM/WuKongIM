package meta

import (
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// SetSlotAppliedIndex stages the physical Slot applied watermark in the same
// durable batch as its state-machine mutations.
func (b *Batch) SetSlotAppliedIndex(slotID uint64, index uint64) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	if slotID == 0 || index == 0 {
		return dberrors.ErrInvalidArgument
	}
	key := encodeSlotAppliedIndexKey(slotID)
	value := make([]byte, 8)
	binary.BigEndian.PutUint64(value, index)
	b.ops = append(b.ops, metaBatchOp{apply: func(_ context.Context, _ *batchCommitState, batch *engine.Batch) error {
		return batch.Set(key, value)
	}})
	return nil
}

// SlotAppliedIndex returns the state-machine watermark for one physical Slot.
func (db *MetaDB) SlotAppliedIndex(_ context.Context, slotID uint64) (uint64, error) {
	if db == nil || db.engine == nil {
		return 0, dberrors.ErrClosed
	}
	if slotID == 0 {
		return 0, dberrors.ErrInvalidArgument
	}
	value, found, err := db.get(encodeSlotAppliedIndexKey(slotID))
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	if len(value) != 8 {
		return 0, dberrors.ErrCorruptValue
	}
	return binary.BigEndian.Uint64(value), nil
}
