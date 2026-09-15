package meta

import (
	"bytes"
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// ChannelRuntimeMetaBatchMaxReads bounds one request-local metadata read view.
const ChannelRuntimeMetaBatchMaxReads = 4096

// ChannelRuntimeMetaReadKey identifies a row in its caller-resolved hash slot.
// The distributed caller must establish Slot authority before using this key.
type ChannelRuntimeMetaReadKey struct {
	HashSlot    HashSlot
	ChannelID   string
	ChannelType int64
}

// GetChannelRuntimeMetaBatch reads exact runtime rows from one pinned iterator.
// Missing rows are omitted, found rows retain request order, and any read or
// decode error fails the batch. Returned rows own their data after view closure.
func (db *MetaDB) GetChannelRuntimeMetaBatch(ctx context.Context, keys []ChannelRuntimeMetaReadKey) ([]ChannelRuntimeMeta, error) {
	if err := (&Shard{db: db}).check(ctx); err != nil {
		return nil, err
	}
	if len(keys) > ChannelRuntimeMetaBatchMaxReads {
		return nil, dberrors.ErrInvalidArgument
	}
	if len(keys) == 0 {
		return []ChannelRuntimeMeta{}, nil
	}
	encoded := make([][]byte, len(keys))
	var lower, upper []byte
	for i, key := range keys {
		if err := validateKeyString(key.ChannelID); err != nil {
			return nil, err
		}
		raw := encodeChannelRuntimeMetaRowKey(key.HashSlot, key.ChannelID, key.ChannelType, channelRuntimeMetaPrimaryFamilyID)
		encoded[i] = raw
		if lower == nil || bytes.Compare(raw, lower) < 0 {
			lower = raw
		}
		if upper == nil || bytes.Compare(raw, upper) > 0 {
			upper = raw
		}
	}
	// Seek only requested exact keys; the bounds do not cause a range scan.
	end := append(append([]byte(nil), upper...), 0)
	it, err := db.engine.NewIter(engine.Span{Start: lower, End: end}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer it.Close()
	out := make([]ChannelRuntimeMeta, 0, len(keys))
	for i, key := range keys {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if !it.SeekGE(encoded[i]) {
			if err := it.Error(); err != nil {
				return nil, err
			}
			continue
		}
		if !it.KeyEquals(encoded[i]) {
			continue
		}
		value, err := it.Value()
		if err != nil {
			return nil, err
		}
		row, err := decodeChannelRuntimeMetaRow(encoded[i], key.ChannelID, key.ChannelType, value)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, it.Error()
}

// GetChannelRuntimeMetaBatch reads caller-resolved rows without caching authority.
func (db *DB) GetChannelRuntimeMetaBatch(ctx context.Context, keys []ChannelRuntimeMetaReadKey) ([]ChannelRuntimeMeta, error) {
	if db == nil || db.meta == nil {
		return nil, dberrors.ErrClosed
	}
	return db.meta.GetChannelRuntimeMetaBatch(ctx, keys)
}
