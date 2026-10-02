package meta

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// HasMQTTState checks all persisted Slot keyspaces, including orphan indexes and
// retained system fences. It seeks past whole non-MQTT tables without decoding
// bodies or scanning their rows; caller-supplied Slot ranges cannot hide state.
// Offline callers must keep the source immutable for the subsequent transfer.
func (db *MetaDB) HasMQTTState(ctx context.Context) (found bool, err error) {
	if db == nil || db.engine == nil {
		return false, dberrors.ErrClosed
	}
	if ctx == nil {
		return false, dberrors.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	mqttTables := make(map[uint32]bool)
	for _, table := range Tables() {
		if strings.HasPrefix(table.Name, "mqtt_") {
			mqttTables[table.ID] = true
		}
	}
	span := keycodec.NewPrefixSpan([]byte{byte(keycodec.DomainMeta), byte(keycodec.PartitionHashSlot)})
	iter, err := db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, iter.Close()) }()
	for ok := iter.First(); ok; {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		key := iter.Key()
		// Domain, partition kind, uint16 Slot, space, uint32 table.
		if len(key) < 9 {
			return false, dberrors.ErrCorruptValue
		}
		if mqttTables[binary.BigEndian.Uint32(key[5:9])] {
			return true, iter.Error()
		}
		ok = iter.SeekGE(keycodec.PrefixEnd(key[:9]))
	}
	return false, iter.Error()
}
