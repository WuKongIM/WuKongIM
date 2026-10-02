package meta

import (
	"context"
	"encoding/binary"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

// subscriberSequenceKey retains the allocation high water after any channel
// deletion. Hash-Slot snapshots preserve this table-owned System-1 record.
func subscriberSequenceKey(slot HashSlot) []byte {
	var b keycodec.Builder
	return b.Reset().Domain(keycodec.DomainMeta).Partition(keycodec.PartitionHashSlot, hashSlotPartitionID(slot)).System(TableIDSubscriber, 1).Key()
}

func decodeSubscriberSequence(key, value []byte, exists bool) (uint64, error) {
	if !exists {
		return 1, nil
	}
	e, err := rowcodec.Unwrap(key, value)
	if err != nil {
		return 0, err
	}
	if e.Version != 1 || e.Codec != rowcodec.CodecFixed || e.Flags != rowcodec.FlagChecksum || len(e.Payload) != 8 {
		return 0, dberrors.ErrCorruptValue
	}
	n := binary.BigEndian.Uint64(e.Payload)
	if n < 2 {
		return 0, dberrors.ErrCorruptValue
	}
	return n, nil
}

func stageSubscriberSequence(b *engine.Batch, key []byte, n uint64) ([]byte, error) {
	value := rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, binary.BigEndian.AppendUint64(nil, n))
	return value, b.Set(key, value)
}

// Allocation uses a per-commit overlay; callers persist only the final high
// water of each subscriber command, avoiding one duplicate WAL write per UID.
func allocateSubscriberIncarnation(st *batchCommitState, slot HashSlot) (uint64, error) {
	if st.subscriberSequences == nil {
		st.subscriberSequences = make(map[HashSlot]uint64)
	}
	previous, ok := st.subscriberSequences[slot]
	if !ok {
		key := subscriberSequenceKey(slot)
		value, exists, err := st.db.get(key)
		if err != nil {
			return 0, err
		}
		previous, err = decodeSubscriberSequence(key, value, exists)
		if err != nil {
			return 0, err
		}
	}
	if previous == math.MaxUint64 {
		return 0, dberrors.ErrConflict
	}
	next := previous + 1
	st.subscriberSequences[slot] = next
	return next, nil
}

func flushSubscriberSequence(st *batchCommitState, b *engine.Batch, slot HashSlot) error {
	if n, ok := st.subscriberSequences[slot]; ok {
		_, err := stageSubscriberSequence(b, subscriberSequenceKey(slot), n)
		return err
	}
	return nil
}

func encodeSubscriberValue(key []byte, r Subscriber) ([]byte, error) {
	// Incarnation 1 (or an unspecified legacy import) retains the old empty value.
	if r.Incarnation <= 1 {
		return nil, nil
	}
	var w rowcodec.Writer
	if err := w.Uint64(subscriberColumnIncarnation, r.Incarnation); err != nil {
		return nil, err
	}
	return rowcodec.Wrap(key, 1, rowcodec.CodecColumns, rowcodec.FlagChecksum, w.Bytes()), nil
}

func decodeSubscriberValue(key []byte, pk KeyParts, value []byte) (Subscriber, error) {
	r := Subscriber{ChannelID: pk[0].S, ChannelType: pk[1].I64, UID: pk[2].S, Incarnation: 1}
	if len(value) == 0 {
		return r, nil
	}
	e, err := rowcodec.Unwrap(key, value)
	if err != nil {
		return Subscriber{}, err
	}
	if e.Version != 1 || e.Codec != rowcodec.CodecColumns || e.Flags != rowcodec.FlagChecksum {
		return Subscriber{}, dberrors.ErrCorruptValue
	}
	scanner := rowcodec.NewBorrowedScanner(e.Payload)
	for scanner.Next() {
		if scanner.ColumnID() == subscriberColumnIncarnation {
			r.Incarnation, err = scanner.Uint64()
			if err != nil || r.Incarnation == 0 {
				return Subscriber{}, dberrors.ErrCorruptValue
			}
		}
	}
	if err = scanner.Err(); err != nil {
		return Subscriber{}, err
	}
	return r, nil
}

// GetSubscriber reads one current membership with its stable join incarnation.
// A missing row is distinct from a legacy member with incarnation 1.
func (s *Shard) GetSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (Subscriber, bool, error) {
	if err := s.check(ctx); err != nil {
		return Subscriber{}, false, err
	}
	if err := validateSubscriber(Subscriber{ChannelID: channelID, ChannelType: channelType, UID: uid}); err != nil {
		return Subscriber{}, false, err
	}
	return subscriberTable.Get(ctx, s, subscriberPrimaryKey(channelID, channelType, uid))
}

// SubscriberSequence reads this hash Slot's durable incarnation high water.
// One is the implicit legacy floor; ordinary mutations never decrease it.
func (s *Shard) SubscriberSequence(ctx context.Context) (uint64, error) {
	if err := s.check(ctx); err != nil {
		return 0, err
	}
	key := subscriberSequenceKey(s.hashSlot)
	var value []byte
	var exists bool
	var err error
	if s.readSnapshot != nil {
		value, exists, err = s.readSnapshot.Get(key)
	} else {
		value, exists, err = s.db.get(key)
	}
	if err != nil {
		return 0, err
	}
	return decodeSubscriberSequence(key, value, exists)
}

// HasSubscriberSequences checks the finite hash-Slot key domain for offline
// empty-target validation, including Slots outside the requested import layout.
// It never scans member rows or materializes canonical Shard handles.
func (db *MetaDB) HasSubscriberSequences(ctx context.Context) (bool, error) {
	if err := checkSnapshotDB(ctx, db); err != nil {
		return false, err
	}
	for slot := uint32(0); slot <= math.MaxUint16; slot++ {
		if err := contextErr(ctx); err != nil {
			return false, err
		}
		key := subscriberSequenceKey(uint16(slot))
		value, exists, err := db.get(key)
		if err != nil {
			return false, err
		}
		if exists {
			_, err = decodeSubscriberSequence(key, value, true)
			return true, err
		}
	}
	return false, nil
}
