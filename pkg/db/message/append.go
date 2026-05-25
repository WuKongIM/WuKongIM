package message

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// Append assigns contiguous sequences and durably writes primary message rows.
func (l *ChannelLog) Append(ctx context.Context, records []Record, opts AppendOptions) (AppendResult, error) {
	if err := ctx.Err(); err != nil {
		return AppendResult{}, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return AppendResult{}, dberrors.ErrClosed
	}
	if opts.Mode != AppendStrict && opts.Mode != AppendTrustedContiguous {
		return AppendResult{}, dberrors.ErrInvalidArgument
	}
	if len(records) == 0 {
		return AppendResult{}, nil
	}

	l.appendMu.Lock()
	defer l.appendMu.Unlock()

	leo, err := l.loadLEOLocked(ctx)
	if err != nil {
		return AppendResult{}, err
	}
	baseSeq := leo + 1

	rows := make([]messageRow, 0, len(records))
	seenMessageIDs := make(map[uint64]struct{}, len(records))
	seenIdempotencyKeys := make(map[IdempotencyKey]struct{}, len(records))
	lastSeq := baseSeq - 1
	for i, record := range records {
		if err := ctx.Err(); err != nil {
			return AppendResult{}, err
		}
		seq := baseSeq + uint64(i)
		row := normalizeMessageRow(l.recordToRow(seq, record))
		if err := l.validateAppendRow(ctx, row, seenMessageIDs, seenIdempotencyKeys, opts.Mode); err != nil {
			return AppendResult{}, err
		}
		rows = append(rows, row)
		lastSeq = seq
	}

	batch := l.db.engine.NewBatch()
	defer batch.Close()
	for _, row := range rows {
		seq := row.MessageSeq
		headerKey := encodeMessageRowKey(l.key, seq, messageHeaderFamilyID)
		headerValue, err := encodeMessageHeader(headerKey, row)
		if err != nil {
			return AppendResult{}, err
		}
		payloadKey := encodeMessageRowKey(l.key, seq, messagePayloadFamilyID)
		payloadValue, err := encodeMessagePayload(payloadKey, row)
		if err != nil {
			return AppendResult{}, err
		}
		if err := batch.Set(headerKey, headerValue); err != nil {
			return AppendResult{}, err
		}
		if err := batch.Set(payloadKey, payloadValue); err != nil {
			return AppendResult{}, err
		}
		if err := batch.Set(encodeMessageIDIndexKey(l.key, row.MessageID), encodeMessageIDIndexValue(row.MessageSeq)); err != nil {
			return AppendResult{}, err
		}
		if row.ClientMsgNo != "" {
			if err := batch.Set(encodeMessageClientMsgNoIndexKey(l.key, row.ClientMsgNo, row.MessageSeq), encodeMessageIDIndexValue(row.MessageSeq)); err != nil {
				return AppendResult{}, err
			}
		}
		if row.FromUID != "" && row.ClientMsgNo != "" {
			value, err := encodeIdempotencyIndexValue(row)
			if err != nil {
				return AppendResult{}, err
			}
			if err := batch.Set(encodeMessageIdempotencyIndexKey(l.key, row.FromUID, row.ClientMsgNo), value); err != nil {
				return AppendResult{}, err
			}
		}
	}
	if err := batch.Commit(true); err != nil {
		return AppendResult{}, err
	}
	l.leo.Store(lastSeq)
	l.loaded.Store(true)
	return AppendResult{
		BaseSeq: baseSeq,
		LastSeq: lastSeq,
		Count:   len(records),
	}, nil
}

func (l *ChannelLog) validateAppendRow(ctx context.Context, row messageRow, seenMessageIDs map[uint64]struct{}, seenIdempotencyKeys map[IdempotencyKey]struct{}, mode AppendMode) error {
	if err := row.validate(); err != nil {
		return err
	}
	if _, ok := seenMessageIDs[row.MessageID]; ok {
		return fmt.Errorf("%w: duplicate message id %d", dberrors.ErrConflict, row.MessageID)
	}
	seenMessageIDs[row.MessageID] = struct{}{}
	if mode == AppendStrict {
		existingSeq, ok, err := l.lookupMessageIDSeq(ctx, row.MessageID)
		if err != nil {
			return err
		}
		if ok && existingSeq != row.MessageSeq {
			return fmt.Errorf("%w: message id %d already stored at seq %d", dberrors.ErrConflict, row.MessageID, existingSeq)
		}
	}
	if row.FromUID == "" || row.ClientMsgNo == "" {
		return nil
	}
	key := IdempotencyKey{FromUID: row.FromUID, ClientMsgNo: row.ClientMsgNo}
	if _, ok := seenIdempotencyKeys[key]; ok {
		return fmt.Errorf("%w: duplicate idempotency key", dberrors.ErrConflict)
	}
	seenIdempotencyKeys[key] = struct{}{}
	if mode == AppendTrustedContiguous {
		return nil
	}
	hit, ok, err := l.lookupIdempotency(ctx, key)
	if err != nil {
		return err
	}
	if ok && hit.MessageSeq != row.MessageSeq {
		return fmt.Errorf("%w: idempotency key already stored at seq %d", dberrors.ErrConflict, hit.MessageSeq)
	}
	return nil
}

func (l *ChannelLog) recordToRow(seq uint64, record Record) messageRow {
	row := messageRow{
		MessageSeq:  seq,
		MessageID:   record.ID,
		ClientMsgNo: record.ClientMsgNo,
		FromUID:     record.FromUID,
		ChannelID:   l.id.ID,
		ChannelType: l.id.Type,
		Payload:     append([]byte(nil), record.Payload...),
	}
	if record.SizeBytes > 0 {
		row.PayloadSize = uint64(record.SizeBytes)
	}
	return row
}
