package message

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// Append assigns contiguous sequences and durably writes primary message rows.
func (l *ChannelLog) Append(ctx context.Context, records []Record, opts AppendOptions) (AppendResult, error) {
	if err := ctx.Err(); err != nil {
		return AppendResult{}, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return AppendResult{}, dberrors.ErrClosed
	}

	l.appendMu.Lock()
	defer l.appendMu.Unlock()

	batch := l.db.engine.NewBatch()
	defer batch.Close()

	result, err := l.prepareAndStageAppendLocked(ctx, batch, records, opts)
	if err != nil || result.Count == 0 {
		return AppendResult{}, err
	}
	if err := l.stageCatalog(batch); err != nil {
		return AppendResult{}, err
	}
	if err := batch.Commit(true); err != nil {
		return AppendResult{}, err
	}
	l.publishAppendLocked(result)
	return result, nil
}

func (l *ChannelLog) prepareAndStageAppendLocked(ctx context.Context, batch *engine.Batch, records []Record, opts AppendOptions) (AppendResult, error) {
	return l.walkAppendRowsLocked(ctx, records, opts, func(row messageRow, cache appendKeyCache) error {
		return l.stageMessageRow(batch, row, cache)
	})
}

func (l *ChannelLog) prepareAppendRowsLocked(ctx context.Context, records []Record, opts AppendOptions) ([]messageRow, AppendResult, error) {
	var rows []messageRow
	result, err := l.walkAppendRowsLocked(ctx, records, opts, func(row messageRow, _ appendKeyCache) error {
		if rows == nil {
			rows = make([]messageRow, 0, len(records))
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, AppendResult{}, err
	}
	return rows, result, nil
}

func (l *ChannelLog) walkAppendRowsLocked(ctx context.Context, records []Record, opts AppendOptions, onRow func(messageRow, appendKeyCache) error) (AppendResult, error) {
	if opts.Mode != AppendStrict && opts.Mode != AppendTrustedContiguous {
		return AppendResult{}, dberrors.ErrInvalidArgument
	}
	leo, err := l.loadLEOLocked(ctx)
	if err != nil {
		return AppendResult{}, err
	}
	expectedBaseSeq := leo + 1
	if opts.BaseSeq != 0 && opts.BaseSeq != expectedBaseSeq {
		return AppendResult{}, fmt.Errorf("%w: base seq %d does not match leo %d", dberrors.ErrConflict, opts.BaseSeq, leo)
	}
	if len(records) == 0 {
		return AppendResult{}, nil
	}
	baseSeq := expectedBaseSeq
	if opts.BaseSeq != 0 {
		baseSeq = opts.BaseSeq
	}
	seen := newAppendValidationSeen(len(records))
	cache := l.ensureAppendKeyCache()
	scratch := appendValidationScratch{}
	lastSeq := baseSeq - 1
	defaultServerTimestampMS := time.Now().UnixMilli()
	for i, record := range records {
		if err := ctx.Err(); err != nil {
			return AppendResult{}, err
		}
		seq := baseSeq + uint64(i)
		row := normalizeMessageRow(l.recordToRow(seq, record, defaultServerTimestampMS))
		if err := l.validateAppendRow(ctx, row, &seen, opts.Mode, cache, &scratch); err != nil {
			return AppendResult{}, err
		}
		if onRow != nil {
			if err := onRow(row, cache); err != nil {
				return AppendResult{}, err
			}
		}
		lastSeq = seq
	}
	return AppendResult{
		BaseSeq: baseSeq,
		LastSeq: lastSeq,
		Count:   len(records),
	}, nil
}

func (l *ChannelLog) publishAppendLocked(result AppendResult) {
	if result.Count == 0 {
		return
	}
	l.leo.Store(result.LastSeq)
	l.loaded.Store(true)
}

func (l *ChannelLog) stageMessageRows(batch *engine.Batch, rows []messageRow) error {
	cache := l.ensureAppendKeyCache()
	for _, row := range rows {
		if err := l.stageMessageRow(batch, row, cache); err != nil {
			return err
		}
	}
	return nil
}

func (l *ChannelLog) stageMessageRow(batch *engine.Batch, row messageRow, cache appendKeyCache) error {
	if err := l.stageMessageHeaderRow(batch, row, cache); err != nil {
		return err
	}
	if err := l.stageMessagePayloadRow(batch, row, cache); err != nil {
		return err
	}
	if err := l.stageMessageIDIndexRow(batch, row, cache); err != nil {
		return err
	}
	if row.ClientMsgNo != "" {
		if err := l.stageClientMsgNoIndexRow(batch, row, cache); err != nil {
			return err
		}
	}
	if row.FromUID != "" && row.ClientMsgNo != "" {
		if err := l.stageIdempotencyIndexRow(batch, row, cache); err != nil {
			return err
		}
	}
	return nil
}

func (l *ChannelLog) stageMessageHeaderRow(batch *engine.Batch, row messageRow, cache appendKeyCache) error {
	return batch.SetDeferred(cache.messageRowKeyLen(), encodedMessageHeaderLen(row), func(key, value []byte) error {
		cache.writeMessageRowKey(key, row.MessageSeq, messageHeaderFamilyID)
		return encodeMessageHeaderTo(value, key, row)
	})
}

func (l *ChannelLog) stageMessagePayloadRow(batch *engine.Batch, row messageRow, cache appendKeyCache) error {
	return batch.SetDeferred(cache.messageRowKeyLen(), encodedMessagePayloadLen(row), func(key, value []byte) error {
		cache.writeMessageRowKey(key, row.MessageSeq, messagePayloadFamilyID)
		return encodeMessagePayloadTo(value, key, row)
	})
}

func (l *ChannelLog) stageMessageIDIndexRow(batch *engine.Batch, row messageRow, cache appendKeyCache) error {
	return batch.SetDeferred(cache.messageIDIndexKeyLen(), messageIDIndexValueLen, func(key, value []byte) error {
		cache.writeMessageIDIndexKey(key, row.MessageID)
		writeMessageIDIndexValue(value, row.MessageSeq)
		return nil
	})
}

func (l *ChannelLog) stageClientMsgNoIndexRow(batch *engine.Batch, row messageRow, cache appendKeyCache) error {
	return batch.SetDeferred(cache.clientMsgNoIndexKeyLen(row.ClientMsgNo), messageIDIndexValueLen, func(key, value []byte) error {
		cache.writeClientMsgNoIndexKey(key, row.ClientMsgNo, row.MessageSeq)
		writeMessageIDIndexValue(value, row.MessageSeq)
		return nil
	})
}

func (l *ChannelLog) stageIdempotencyIndexRow(batch *engine.Batch, row messageRow, cache appendKeyCache) error {
	return batch.SetDeferred(cache.idempotencyIndexKeyLen(row.FromUID, row.ClientMsgNo), idempotencyIndexValueLen, func(key, value []byte) error {
		cache.writeIdempotencyIndexKey(key, row.FromUID, row.ClientMsgNo)
		return writeIdempotencyIndexValue(value, row)
	})
}

// appendValidationSeen tracks in-batch duplicate keys during one append.
type appendValidationSeen struct {
	messageIDs          map[uint64]struct{}
	idempotencyKeys     map[IdempotencyKey]struct{}
	firstIdempotencyKey IdempotencyKey
	hasIdempotencyKey   bool
	idempotencyCapacity int
}

func newAppendValidationSeen(recordCount int) appendValidationSeen {
	return appendValidationSeen{
		messageIDs:          make(map[uint64]struct{}, recordCount),
		idempotencyCapacity: recordCount,
	}
}

func (s *appendValidationSeen) rememberMessageID(messageID uint64) bool {
	if _, ok := s.messageIDs[messageID]; ok {
		return true
	}
	s.messageIDs[messageID] = struct{}{}
	return false
}

func (s *appendValidationSeen) rememberIdempotencyKey(key IdempotencyKey) bool {
	if !s.hasIdempotencyKey {
		s.firstIdempotencyKey = key
		s.hasIdempotencyKey = true
		return false
	}
	if s.firstIdempotencyKey == key {
		return true
	}
	if s.idempotencyKeys == nil {
		s.idempotencyKeys = make(map[IdempotencyKey]struct{}, s.idempotencyCapacity)
		s.idempotencyKeys[s.firstIdempotencyKey] = struct{}{}
	}
	if _, ok := s.idempotencyKeys[key]; ok {
		return true
	}
	s.idempotencyKeys[key] = struct{}{}
	return false
}

type appendValidationScratch struct {
	messageIDIndexKey   []byte
	idempotencyIndexKey []byte
}

func (l *ChannelLog) validateAppendRow(ctx context.Context, row messageRow, seen *appendValidationSeen, mode AppendMode, cache appendKeyCache, scratch *appendValidationScratch) error {
	if err := row.validate(); err != nil {
		return err
	}
	if seen.rememberMessageID(row.MessageID) {
		return fmt.Errorf("%w: duplicate message id %d", dberrors.ErrConflict, row.MessageID)
	}
	if mode == AppendStrict {
		scratch.messageIDIndexKey = cache.messageIDIndexKeyTo(scratch.messageIDIndexKey, row.MessageID)
		existingSeq, ok, err := l.lookupMessageIDSeqByKey(ctx, scratch.messageIDIndexKey)
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
	if seen.rememberIdempotencyKey(key) {
		return fmt.Errorf("%w: duplicate idempotency key", dberrors.ErrConflict)
	}
	if mode == AppendTrustedContiguous {
		return nil
	}
	scratch.idempotencyIndexKey = cache.idempotencyIndexKeyTo(scratch.idempotencyIndexKey, key.FromUID, key.ClientMsgNo)
	hit, ok, err := l.lookupIdempotencyByKey(ctx, key, scratch.idempotencyIndexKey)
	if err != nil {
		return err
	}
	if ok && hit.MessageSeq != row.MessageSeq {
		return fmt.Errorf("%w: idempotency key already stored at seq %d", dberrors.ErrConflict, hit.MessageSeq)
	}
	return nil
}

func (l *ChannelLog) recordToRow(seq uint64, record Record, defaultServerTimestampMS int64) messageRow {
	serverTimestampMS := record.ServerTimestampMS
	if serverTimestampMS == 0 {
		serverTimestampMS = defaultServerTimestampMS
	}
	row := messageRow{
		MessageSeq:        seq,
		MessageID:         record.ID,
		ClientMsgNo:       record.ClientMsgNo,
		FromUID:           record.FromUID,
		ChannelID:         l.id.ID,
		ChannelType:       l.id.Type,
		Payload:           record.Payload,
		ServerTimestampMS: serverTimestampMS,
	}
	if record.SizeBytes > 0 {
		row.PayloadSize = uint64(record.SizeBytes)
	}
	return row
}
