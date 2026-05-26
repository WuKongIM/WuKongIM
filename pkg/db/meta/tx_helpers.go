package meta

import (
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

const maxKeyStringLen = 1<<16 - 1

func (s *Shard) check(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if s == nil || s.db == nil || s.db.engine == nil {
		return dberrors.ErrClosed
	}
	return nil
}

func (s *Shard) lock() func() {
	return s.db.lockHashSlots([]HashSlot{s.hashSlot})
}

func (db *MetaDB) get(key []byte) ([]byte, bool, error) {
	if db == nil || db.engine == nil {
		return nil, false, dberrors.ErrClosed
	}
	return db.engine.Get(key)
}

func commitSet(batch *engine.Batch, key []byte, value []byte) error {
	if err := batch.Set(key, value); err != nil {
		return err
	}
	return batch.Commit(true)
}

func validateKeyString(value string) error {
	if value == "" || len(value) > maxKeyStringLen {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func appendValueString(dst []byte, value string) []byte {
	return keycodec.AppendString(dst, value)
}

func readValueString(src []byte) (string, []byte, error) {
	value, rest, err := keycodec.ReadString(src)
	if err != nil {
		return "", nil, dberrors.ErrCorruptValue
	}
	return value, rest, nil
}

func appendValueInt64(dst []byte, value int64) []byte {
	return binary.BigEndian.AppendUint64(dst, uint64(value))
}

func readValueInt64(src []byte) (int64, []byte, error) {
	if len(src) < 8 {
		return 0, nil, dberrors.ErrCorruptValue
	}
	return int64(binary.BigEndian.Uint64(src[:8])), src[8:], nil
}

func appendValueUint64(dst []byte, value uint64) []byte {
	return binary.BigEndian.AppendUint64(dst, value)
}

func readValueUint64(src []byte) (uint64, []byte, error) {
	if len(src) < 8 {
		return 0, nil, dberrors.ErrCorruptValue
	}
	return binary.BigEndian.Uint64(src[:8]), src[8:], nil
}
