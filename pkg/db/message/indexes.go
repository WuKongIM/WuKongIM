package message

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// GetByMessageID returns one message using the unique message ID index.
func (l *ChannelLog) GetByMessageID(ctx context.Context, messageID uint64) (Message, bool, error) {
	seq, ok, err := l.lookupMessageIDSeq(ctx, messageID)
	if err != nil || !ok {
		return Message{}, ok, err
	}
	row, ok, err := l.getRowBySeq(ctx, seq)
	if err != nil {
		return Message{}, false, err
	}
	if !ok || row.MessageID != messageID {
		return Message{}, false, fmt.Errorf("%w: stale message id index", dberrors.ErrCorruptState)
	}
	return messageFromRow(row), true, nil
}

// ListByClientMsgNo returns messages for one client message number newest first.
func (l *ChannelLog) ListByClientMsgNo(ctx context.Context, clientMsgNo string, beforeSeq uint64, limit int) (MessagePage, error) {
	if err := ctx.Err(); err != nil {
		return MessagePage{}, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return MessagePage{}, dberrors.ErrClosed
	}
	if clientMsgNo == "" || limit <= 0 {
		return MessagePage{}, dberrors.ErrInvalidArgument
	}

	prefix := encodeMessageClientMsgNoIndexPrefix(l.key, clientMsgNo)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := l.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return MessagePage{}, err
	}
	defer iter.Close()

	seqs := make([]uint64, 0, limit)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return MessagePage{}, err
		}
		seq, ok := decodeMessageClientMsgNoIndexSeq(l.key, clientMsgNo, iter.Key())
		if !ok {
			return MessagePage{}, fmt.Errorf("%w: corrupt client message number index", dberrors.ErrCorruptValue)
		}
		if beforeSeq == 0 || seq < beforeSeq {
			seqs = append(seqs, seq)
		}
	}
	if err := iter.Error(); err != nil {
		return MessagePage{}, err
	}

	page := MessagePage{Messages: make([]Message, 0, boundedCapacity(len(seqs), limit))}
	for i := len(seqs) - 1; i >= 0; i-- {
		if len(page.Messages) == limit {
			page.HasMore = true
			page.NextBeforeSeq = page.Messages[len(page.Messages)-1].MessageSeq
			break
		}
		row, ok, err := l.getRowBySeq(ctx, seqs[i])
		if err != nil {
			return MessagePage{}, err
		}
		if !ok || row.ClientMsgNo != clientMsgNo {
			return MessagePage{}, fmt.Errorf("%w: stale client message number index", dberrors.ErrCorruptState)
		}
		page.Messages = append(page.Messages, messageFromRow(row))
	}
	return page, nil
}

func (l *ChannelLog) lookupMessageIDSeq(ctx context.Context, messageID uint64) (uint64, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if messageID == 0 {
		return 0, false, dberrors.ErrInvalidArgument
	}
	value, ok, err := l.db.engine.Get(encodeMessageIDIndexKey(l.key, messageID))
	if err != nil || !ok {
		return 0, ok, err
	}
	seq, err := decodeMessageIDIndexValue(value)
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func encodeMessageIDIndexValue(seq uint64) []byte {
	return binary.BigEndian.AppendUint64(nil, seq)
}

func decodeMessageIDIndexValue(value []byte) (uint64, error) {
	if len(value) != 8 {
		return 0, dberrors.ErrCorruptValue
	}
	return binary.BigEndian.Uint64(value), nil
}
