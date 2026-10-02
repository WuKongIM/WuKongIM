package message

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// LookupIdempotency verifies the original row in the selected identity domain.
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
	if !key.valid() {
		return IdempotencyHit{}, false, dberrors.ErrInvalidArgument
	}
	return l.lookupIdempotencyByKey(ctx, key, l.idempotencyStorageKey(key))
}

func (key IdempotencyKey) valid() bool {
	if key.FromUID == "" {
		return false
	}
	if key.ServerWillKey != "" {
		return key.ClientMsgNo == "" && publication.ValidServerWillKey(key.ServerWillKey)
	}
	return key.ClientMsgNo != ""
}

// rowIdempotencyKey derives the domain from immutable publication provenance.
// ClientMsgNo remains stored content but is not part of a server Will identity.
func rowIdempotencyKey(uid, client string, metadata []byte) (IdempotencyKey, error) {
	key := IdempotencyKey{FromUID: uid, ClientMsgNo: client}
	if len(metadata) != 0 {
		m, err := publication.Decode(metadata)
		if err != nil {
			return IdempotencyKey{}, dberrors.ErrInvalidArgument
		}
		if m.ServerWillKey != "" {
			key.ClientMsgNo, key.ServerWillKey = "", m.ServerWillKey
		}
	}
	return key, nil
}

func (l *ChannelLog) idempotencyStorageKey(key IdempotencyKey) []byte {
	if key.ServerWillKey != "" {
		return encodeMessageWillIdempotencyIndexKey(l.key, key.FromUID, key.ServerWillKey)
	}
	return l.appendKeyCache.idempotencyIndexKey(key.FromUID, key.ClientMsgNo)
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
	rowKey, keyErr := rowIdempotencyKey(row.FromUID, row.ClientMsgNo, row.PublicationMetadata)
	if !ok || row.MessageID != hit.MessageID || row.PayloadHash != hit.PayloadHash || keyErr != nil || rowKey != key {
		return IdempotencyHit{}, false, fmt.Errorf("%w: stale idempotency index", dberrors.ErrCorruptState)
	}
	return hit, true, nil
}

// ensureIdempotencyMembershipLoaded rebuilds the bounded negative filter while
// the caller holds the canonical channel append mutex.
func (l *ChannelLog) ensureIdempotencyMembershipLoaded(ctx context.Context) error {
	if l.idempotencyMembershipLoaded {
		return nil
	}
	prefix := l.appendKeyCache.idempotencyIndexPrefix
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := l.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return err
	}
	defer iter.Close()
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		l.idempotencyMembership.add(iter.Key())
	}
	if err := iter.Error(); err != nil {
		return err
	}
	l.idempotencyMembershipLoaded = true
	return nil
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
