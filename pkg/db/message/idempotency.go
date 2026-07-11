package message

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// LookupIdempotency returns the indexed message selected by a sender/client pair.
func (l *ChannelLog) LookupIdempotency(ctx context.Context, key IdempotencyKey) (IdempotencyHit, bool, error) {
	if err := l.beginUse(); err != nil {
		return IdempotencyHit{}, false, err
	}
	defer l.endUse()
	return l.lookupIdempotency(ctx, key)
}

func (l *ChannelLog) lookupIdempotency(ctx context.Context, key IdempotencyKey) (IdempotencyHit, bool, error) {
	if err := ctx.Err(); err != nil {
		return IdempotencyHit{}, false, err
	}
	if key.FromUID == "" || key.ClientMsgNo == "" {
		return IdempotencyHit{}, false, dberrors.ErrInvalidArgument
	}
	cache := l.appendKeyCache
	return l.lookupIdempotencyByKey(ctx, key, cache.idempotencyIndexKey(key.FromUID, key.ClientMsgNo))
}

func (l *ChannelLog) lookupIdempotencyByKey(ctx context.Context, key IdempotencyKey, storageKey []byte) (IdempotencyHit, bool, error) {
	if err := ctx.Err(); err != nil {
		return IdempotencyHit{}, false, err
	}
	value, ok, err := l.db.engine.Get(storageKey)
	if err != nil || !ok {
		return IdempotencyHit{}, ok, err
	}
	hit, err := decodeIdempotencyIndexValue(value)
	if err != nil {
		return IdempotencyHit{}, false, err
	}
	row, ok, err := l.getRowBySeq(ctx, hit.MessageSeq)
	if err != nil {
		return IdempotencyHit{}, false, err
	}
	if !ok || row.MessageID != hit.MessageID || row.PayloadHash != hit.PayloadHash || row.FromUID != key.FromUID || row.ClientMsgNo != key.ClientMsgNo {
		return IdempotencyHit{}, false, fmt.Errorf("%w: stale idempotency index", dberrors.ErrCorruptState)
	}
	return hit, true, nil
}

const idempotencyIndexValueLen = 24

func encodeIdempotencyIndexValue(row messageRow) ([]byte, error) {
	value := make([]byte, idempotencyIndexValueLen)
	if err := writeIdempotencyIndexValue(value, row); err != nil {
		return nil, err
	}
	return value, nil
}

func writeIdempotencyIndexValue(dst []byte, row messageRow) error {
	row = normalizeMessageRow(row)
	if err := row.validate(); err != nil {
		return err
	}
	if len(dst) != idempotencyIndexValueLen {
		return dberrors.ErrInvalidArgument
	}
	binary.BigEndian.PutUint64(dst[0:8], row.MessageSeq)
	binary.BigEndian.PutUint64(dst[8:16], row.MessageID)
	binary.BigEndian.PutUint64(dst[16:24], row.PayloadHash)
	return nil
}

func decodeIdempotencyIndexValue(value []byte) (IdempotencyHit, error) {
	if len(value) != idempotencyIndexValueLen {
		return IdempotencyHit{}, dberrors.ErrCorruptValue
	}
	seq := binary.BigEndian.Uint64(value[0:8])
	var offset uint64
	if seq > 0 {
		offset = seq - 1
	}
	return IdempotencyHit{
		MessageSeq:  seq,
		MessageID:   binary.BigEndian.Uint64(value[8:16]),
		Offset:      offset,
		PayloadHash: binary.BigEndian.Uint64(value[16:24]),
	}, nil
}
