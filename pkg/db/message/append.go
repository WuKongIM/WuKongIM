package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// Append assigns contiguous sequences and durably writes primary message rows.
func (l *ChannelLog) Append(ctx context.Context, records []Record, opts AppendOptions) (AppendResult, error) {
	_ = opts
	if err := ctx.Err(); err != nil {
		return AppendResult{}, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return AppendResult{}, dberrors.ErrClosed
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
	batch := l.db.engine.NewBatch()
	defer batch.Close()

	lastSeq := baseSeq - 1
	for i, record := range records {
		if err := ctx.Err(); err != nil {
			return AppendResult{}, err
		}
		seq := baseSeq + uint64(i)
		row := l.recordToRow(seq, record)
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
		lastSeq = seq
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

func (l *ChannelLog) recordToRow(seq uint64, record Record) messageRow {
	row := messageRow{
		MessageSeq:  seq,
		MessageID:   record.ID,
		ChannelID:   l.id.ID,
		ChannelType: l.id.Type,
		Payload:     append([]byte(nil), record.Payload...),
	}
	if record.SizeBytes > 0 {
		row.PayloadSize = uint64(record.SizeBytes)
	}
	return row
}
