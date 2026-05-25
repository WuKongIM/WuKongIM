package message

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// Read returns messages in ascending sequence order starting at fromSeq.
func (l *ChannelLog) Read(ctx context.Context, fromSeq uint64, opts ReadOptions) ([]Message, error) {
	if fromSeq == 0 {
		fromSeq = 1
	}
	return l.readForward(ctx, fromSeq, 0, opts)
}

// ReadReverse returns messages in descending sequence order starting at fromSeq.
func (l *ChannelLog) ReadReverse(ctx context.Context, fromSeq uint64, opts ReadOptions) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fromSeq == 0 {
		leo, err := l.LEO(ctx)
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

func (l *ChannelLog) readForward(ctx context.Context, fromSeq uint64, maxSeq uint64, opts ReadOptions) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return nil, dberrors.ErrClosed
	}
	prefix := encodeMessageRowPrefix(l.key)
	span := keycodec.NewPrefixSpan(prefix)
	start := encodeMessageRowKey(l.key, fromSeq, messageHeaderFamilyID)
	iter, err := l.db.engine.NewIter(engine.Span{Start: start, End: span.End}, engine.IterOptions{})
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
		key := iter.Key()
		seq, familyID, ok := decodeMessageRowKey(l.key, key)
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
			if err := decodeMessageHeader(key, value, &current); err != nil {
				return nil, err
			}
			haveHeader = true
		case messagePayloadFamilyID:
			if err := decodeMessagePayload(key, value, &current); err != nil {
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
		MessageSeq: row.MessageSeq,
		MessageID:  row.MessageID,
		Payload:    append([]byte(nil), row.Payload...),
	}
}

func boundedCapacity(defaultCapacity int, limit int) int {
	if limit > 0 && limit < defaultCapacity {
		return limit
	}
	return defaultCapacity
}
