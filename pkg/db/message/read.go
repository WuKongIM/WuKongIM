package message

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// GetBySeq returns one message by its durable channel sequence.
func (l *ChannelLog) GetBySeq(ctx context.Context, seq uint64) (Message, bool, error) {
	if err := l.beginUse(); err != nil {
		return Message{}, false, err
	}
	defer l.endUse()
	row, ok, err := l.getRowBySeq(ctx, seq)
	if err != nil || !ok {
		return Message{}, ok, err
	}
	return messageFromRow(row), true, nil
}

// Read returns messages in ascending sequence order starting at fromSeq.
func (l *ChannelLog) Read(ctx context.Context, fromSeq uint64, opts ReadOptions) ([]Message, error) {
	if err := l.beginUse(); err != nil {
		return nil, err
	}
	defer l.endUse()
	if fromSeq == 0 {
		fromSeq = 1
	}
	return l.readForward(ctx, fromSeq, 0, opts)
}

// ReadReverse returns messages in descending sequence order starting at fromSeq.
func (l *ChannelLog) ReadReverse(ctx context.Context, fromSeq uint64, opts ReadOptions) ([]Message, error) {
	if err := l.beginUse(); err != nil {
		return nil, err
	}
	defer l.endUse()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fromSeq == 0 {
		l.appendMu.Lock()
		leo, err := l.loadLEOLocked(ctx)
		l.appendMu.Unlock()
		if err != nil {
			return nil, err
		}
		fromSeq = leo
	}
	all, err := l.readForward(ctx, 1, fromSeq, ReadOptions{})
	if err != nil {
		return nil, err
	}
	messages := make([]Message, 0, boundedCapacity(len(all), opts.Limit))
	var totalBytes int
	for i := len(all) - 1; i >= 0; i-- {
		var stop bool
		messages, totalBytes, stop = appendReadMessage(messages, totalBytes, all[i], opts)
		if stop {
			break
		}
	}
	return messages, nil
}

// GetLastVisibleMessage returns the newest message whose sequence is greater than visibleAfterSeq.
func (l *ChannelLog) GetLastVisibleMessage(ctx context.Context, visibleAfterSeq uint64) (Message, bool, error) {
	if err := l.beginUse(); err != nil {
		return Message{}, false, err
	}
	defer l.endUse()
	if err := ctx.Err(); err != nil {
		return Message{}, false, err
	}
	prefix := encodeMessageRowPrefix(l.key)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := l.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return Message{}, false, err
	}
	defer iter.Close()

	for ok := iter.Last(); ok; ok = iter.Prev() {
		if err := ctx.Err(); err != nil {
			return Message{}, false, err
		}
		seq, familyID, ok := decodeMessageRowKey(l.key, iter.Key())
		if !ok {
			continue
		}
		if seq <= visibleAfterSeq {
			return Message{}, false, nil
		}
		if familyID != messageHeaderFamilyID {
			continue
		}
		row, ok, err := l.getRowBySeq(ctx, seq)
		if err != nil {
			return Message{}, false, err
		}
		if ok {
			return messageFromRow(row), true, nil
		}
	}
	if err := iter.Error(); err != nil {
		return Message{}, false, err
	}
	return Message{}, false, nil
}

func (l *ChannelLog) readForward(ctx context.Context, fromSeq uint64, maxSeq uint64, opts ReadOptions) ([]Message, error) {
	return readMessagesRaw(ctx, l.db, l.key, fromSeq, maxSeq, opts)
}

func readMessagesRaw(ctx context.Context, db *MessageDB, channelKey ChannelKey, fromSeq uint64, maxSeq uint64, opts ReadOptions) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if db == nil || db.engine == nil {
		return nil, dberrors.ErrClosed
	}
	prefix := encodeMessageRowPrefix(channelKey)
	span := keycodec.NewPrefixSpan(prefix)
	start := encodeMessageRowKey(channelKey, fromSeq, messageHeaderFamilyID)
	iter, err := db.engine.NewIter(engine.Span{Start: start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	messages := make([]Message, 0, boundedCapacity(16, opts.Limit))
	var totalBytes int
	var current messageRow
	var currentSeq uint64
	var haveRow bool
	var haveHeader bool
	var havePayload bool

	flush := func() (bool, error) {
		if !haveRow {
			return false, nil
		}
		if !haveHeader || !havePayload {
			return false, fmt.Errorf("%w: incomplete message row at seq %d", dberrors.ErrCorruptState, currentSeq)
		}
		if err := validateMaterializedMessageRow(current); err != nil {
			return false, err
		}
		msg := messageFromRow(current)
		var stop bool
		messages, totalBytes, stop = appendReadMessage(messages, totalBytes, msg, opts)
		haveRow = false
		haveHeader = false
		havePayload = false
		current = messageRow{}
		currentSeq = 0
		return stop, nil
	}

	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		storageKey := iter.Key()
		seq, familyID, ok := decodeMessageRowKey(channelKey, storageKey)
		if !ok {
			continue
		}
		if maxSeq > 0 && seq > maxSeq {
			break
		}
		if !haveRow || seq != currentSeq {
			stop, err := flush()
			if err != nil {
				return nil, err
			}
			if stop {
				return messages, nil
			}
			current = messageRow{MessageSeq: seq}
			currentSeq = seq
			haveRow = true
		}
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		switch familyID {
		case messageHeaderFamilyID:
			if err := decodeMessageHeader(storageKey, value, &current); err != nil {
				return nil, err
			}
			haveHeader = true
		case messagePayloadFamilyID:
			if err := decodeMessagePayload(storageKey, value, &current); err != nil {
				return nil, err
			}
			havePayload = true
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	stop, err := flush()
	if err != nil {
		return nil, err
	}
	if stop {
		return messages, nil
	}
	return messages, nil
}

func (l *ChannelLog) getRowBySeq(ctx context.Context, seq uint64) (messageRow, bool, error) {
	if err := ctx.Err(); err != nil {
		return messageRow{}, false, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return messageRow{}, false, dberrors.ErrClosed
	}
	if seq == 0 {
		return messageRow{}, false, dberrors.ErrInvalidArgument
	}
	headerKey := encodeMessageRowKey(l.key, seq, messageHeaderFamilyID)
	headerValue, okHeader, err := l.db.engine.Get(headerKey)
	if err != nil {
		return messageRow{}, false, err
	}
	payloadKey := encodeMessageRowKey(l.key, seq, messagePayloadFamilyID)
	payloadValue, okPayload, err := l.db.engine.Get(payloadKey)
	if err != nil {
		return messageRow{}, false, err
	}
	if !okHeader && !okPayload {
		return messageRow{}, false, nil
	}
	if !okHeader || !okPayload {
		return messageRow{}, false, fmt.Errorf("%w: incomplete message row at seq %d", dberrors.ErrCorruptState, seq)
	}
	row := messageRow{MessageSeq: seq}
	if err := decodeMessageHeader(headerKey, headerValue, &row); err != nil {
		return messageRow{}, false, err
	}
	if err := decodeMessagePayload(payloadKey, payloadValue, &row); err != nil {
		return messageRow{}, false, err
	}
	if err := validateMaterializedMessageRow(row); err != nil {
		return messageRow{}, false, err
	}
	return row, true, nil
}

func appendReadMessage(messages []Message, totalBytes int, msg Message, opts ReadOptions) ([]Message, int, bool) {
	payloadBytes := len(msg.Payload)
	if opts.MaxBytes > 0 && len(messages) > 0 && totalBytes+payloadBytes > opts.MaxBytes {
		return messages, totalBytes, true
	}
	messages = append(messages, msg)
	totalBytes += payloadBytes
	if opts.Limit > 0 && len(messages) >= opts.Limit {
		return messages, totalBytes, true
	}
	return messages, totalBytes, false
}

func messageFromRow(row messageRow) Message {
	return Message{
		MessageSeq:        row.MessageSeq,
		MessageID:         row.MessageID,
		ClientMsgNo:       row.ClientMsgNo,
		FromUID:           row.FromUID,
		PayloadHash:       row.PayloadHash,
		Payload:           append([]byte(nil), row.Payload...),
		ServerTimestampMS: row.ServerTimestampMS,
	}
}

func validateMaterializedMessageRow(row messageRow) error {
	if row.MessageID == 0 {
		return dberrors.ErrCorruptValue
	}
	if row.PayloadHash != hashPayload(row.Payload) {
		return fmt.Errorf("%w: payload hash mismatch at seq %d", dberrors.ErrCorruptState, row.MessageSeq)
	}
	return nil
}

func boundedCapacity(defaultCapacity int, limit int) int {
	if limit > 0 && limit < defaultCapacity {
		return limit
	}
	return defaultCapacity
}
