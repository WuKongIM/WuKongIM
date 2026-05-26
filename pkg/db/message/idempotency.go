package message

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// LookupIdempotency returns the indexed message selected by a sender/client pair.
func (l *ChannelLog) LookupIdempotency(ctx context.Context, key IdempotencyKey) (IdempotencyHit, bool, error) {
	return l.lookupIdempotency(ctx, key)
}

func (l *ChannelLog) lookupIdempotency(ctx context.Context, key IdempotencyKey) (IdempotencyHit, bool, error) {
	if err := ctx.Err(); err != nil {
		return IdempotencyHit{}, false, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return IdempotencyHit{}, false, dberrors.ErrClosed
	}
	if key.FromUID == "" || key.ClientMsgNo == "" {
		return IdempotencyHit{}, false, dberrors.ErrInvalidArgument
	}
	value, ok, err := l.db.engine.Get(encodeMessageIdempotencyIndexKey(l.key, key.FromUID, key.ClientMsgNo))
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

func encodeIdempotencyIndexValue(row messageRow) ([]byte, error) {
	row = normalizeMessageRow(row)
	if err := row.validate(); err != nil {
		return nil, err
	}
	value := make([]byte, 0, 24)
	value = binary.BigEndian.AppendUint64(value, row.MessageSeq)
	value = binary.BigEndian.AppendUint64(value, row.MessageID)
	value = binary.BigEndian.AppendUint64(value, row.PayloadHash)
	return value, nil
}

func decodeIdempotencyIndexValue(value []byte) (IdempotencyHit, error) {
	if len(value) != 24 {
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
