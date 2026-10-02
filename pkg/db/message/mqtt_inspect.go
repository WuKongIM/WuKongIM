package message

import (
	"context"
	"encoding/binary"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// HasMQTTState finds shared replay, source/funding fences and retained Will
// evidence even without a catalog or Session row. Whole ordinary row/index
// keyspaces are skipped by seek, so history length does not increase scan work.
// Offline callers must keep the source immutable for the subsequent transfer.
func (db *MessageDB) HasMQTTState(ctx context.Context) (found bool, err error) {
	if err := db.beginUse(); err != nil {
		return false, err
	}
	defer db.endUse()
	if ctx == nil {
		return false, dberrors.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	span := keycodec.NewPrefixSpan([]byte{byte(keycodec.DomainMessage), byte(keycodec.PartitionChannel)})
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
		_, rest, err := keycodec.ReadString(key[2:])
		if err != nil || len(rest) < 5 {
			return false, dberrors.ErrCorruptValue
		}
		table := binary.BigEndian.Uint32(rest[1:5])
		if table == TableIDMQTTReplay {
			return true, iter.Error()
		}
		prefixLen := len(key) - len(rest) + 5
		space := keycodec.Space(rest[0])
		if table == TableIDMessage && (space == keycodec.SpaceIndex || space == keycodec.SpaceSystem) {
			if len(rest) < 7 {
				return false, dberrors.ErrCorruptValue
			}
			id := binary.BigEndian.Uint16(rest[5:7])
			if space == keycodec.SpaceSystem && id >= messageSystemIDMQTTSource && id <= messageSystemIDMQTTStoragePreparation ||
				space == keycodec.SpaceIndex && id == messageIndexIDServerWill {
				return true, iter.Error()
			}
			prefixLen += 2
		}
		ok = iter.SeekGE(keycodec.PrefixEnd(key[:prefixLen]))
	}
	return false, iter.Error()
}
