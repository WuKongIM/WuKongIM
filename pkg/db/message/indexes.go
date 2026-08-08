package message

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// GetByMessageID returns one message using the unique message ID index.
func (l *ChannelLog) GetByMessageID(ctx context.Context, messageID uint64) (Message, bool, error) {
	if err := l.beginUse(); err != nil {
		return Message{}, false, err
	}
	defer l.endUse()
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
	if err := l.beginUse(); err != nil {
		return MessagePage{}, err
	}
	defer l.endUse()
	return l.listByClientMsgNo(ctx, clientMsgNo, beforeSeq, limit)
}

func (l *ChannelLog) listByClientMsgNo(ctx context.Context, clientMsgNo string, beforeSeq uint64, limit int) (MessagePage, error) {
	if err := ctx.Err(); err != nil {
		return MessagePage{}, err
	}
	if clientMsgNo == "" || limit <= 0 {
		return MessagePage{}, dberrors.ErrInvalidArgument
	}

	type indexedSeq struct {
		seq             uint64
		allowMissingRow bool
	}
	seqs := make([]indexedSeq, 0, limit)
	canonicalPrefix := encodeMessageClientLookupIndexPrefix(l.key, clientMsgNo)
	canonicalSpan := keycodec.NewPrefixSpan(canonicalPrefix)
	canonicalIter, err := l.db.engine.NewIter(engine.Span{Start: canonicalSpan.Start, End: canonicalSpan.End}, engine.IterOptions{})
	if err != nil {
		return MessagePage{}, err
	}
	for ok := canonicalIter.First(); ok; ok = canonicalIter.Next() {
		if err := ctx.Err(); err != nil {
			_ = canonicalIter.Close()
			return MessagePage{}, err
		}
		value, err := canonicalIter.Value()
		if err != nil {
			_ = canonicalIter.Close()
			return MessagePage{}, err
		}
		hit, err := decodeIdempotencyIndexValue(value)
		if err != nil {
			_ = canonicalIter.Close()
			return MessagePage{}, err
		}
		if beforeSeq == 0 || hit.MessageSeq < beforeSeq {
			// PutIdempotency may intentionally create a durable reservation
			// without a corresponding message row.
			seqs = append(seqs, indexedSeq{seq: hit.MessageSeq, allowMissingRow: true})
		}
	}
	if err := canonicalIter.Error(); err != nil {
		_ = canonicalIter.Close()
		return MessagePage{}, err
	}
	if err := canonicalIter.Close(); err != nil {
		return MessagePage{}, err
	}

	// Sender-less records cannot participate in idempotency, so they retain
	// the sequence-suffixed client index without adding a write to normal sends.
	legacyPrefix := encodeMessageClientMsgNoIndexPrefix(l.key, clientMsgNo)
	legacySpan := keycodec.NewPrefixSpan(legacyPrefix)
	legacyIter, err := l.db.engine.NewIter(engine.Span{Start: legacySpan.Start, End: legacySpan.End}, engine.IterOptions{})
	if err != nil {
		return MessagePage{}, err
	}
	for ok := legacyIter.First(); ok; ok = legacyIter.Next() {
		if err := ctx.Err(); err != nil {
			_ = legacyIter.Close()
			return MessagePage{}, err
		}
		seq, ok := decodeMessageClientMsgNoIndexSeq(l.key, clientMsgNo, legacyIter.Key())
		if !ok {
			_ = legacyIter.Close()
			return MessagePage{}, fmt.Errorf("%w: corrupt client message number index", dberrors.ErrCorruptValue)
		}
		if beforeSeq == 0 || seq < beforeSeq {
			seqs = append(seqs, indexedSeq{seq: seq})
		}
	}
	if err := legacyIter.Error(); err != nil {
		_ = legacyIter.Close()
		return MessagePage{}, err
	}
	if err := legacyIter.Close(); err != nil {
		return MessagePage{}, err
	}

	sort.Slice(seqs, func(i, j int) bool { return seqs[i].seq > seqs[j].seq })
	messages := make([]Message, 0, len(seqs))
	for _, indexed := range seqs {
		row, ok, err := l.getRowBySeq(ctx, indexed.seq)
		if err != nil {
			return MessagePage{}, err
		}
		if !ok && indexed.allowMissingRow {
			continue
		}
		if !ok || row.ClientMsgNo != clientMsgNo {
			return MessagePage{}, fmt.Errorf("%w: stale client message number index", dberrors.ErrCorruptState)
		}
		messages = append(messages, messageFromRow(row))
	}
	page := MessagePage{Messages: messages}
	if len(messages) > limit {
		page.Messages = messages[:limit]
		page.HasMore = true
		page.NextBeforeSeq = page.Messages[len(page.Messages)-1].MessageSeq
	}
	return page, nil
}

// GetLastSenderMessageSeq returns the latest sequence sent by fromUID at or
// before throughSeq. Callers pass the committed channel high-water mark so an
// uncommitted mutable tail cannot suppress badge counts.
func (l *ChannelLog) GetLastSenderMessageSeq(ctx context.Context, fromUID string, throughSeq uint64) (uint64, bool, error) {
	if err := l.beginUse(); err != nil {
		return 0, false, err
	}
	defer l.endUse()
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if fromUID == "" || throughSeq == 0 {
		return 0, false, dberrors.ErrInvalidArgument
	}

	prefix := encodeMessageSenderSeqIndexPrefix(l.key, fromUID)
	span := keycodec.NewPrefixSpan(prefix)
	end := span.End
	if throughSeq != ^uint64(0) {
		end = encodeMessageSenderSeqIndexKey(l.key, fromUID, throughSeq+1)
	}
	iter, err := l.db.engine.NewIter(engine.Span{Start: span.Start, End: end}, engine.IterOptions{})
	if err != nil {
		return 0, false, err
	}
	defer iter.Close()
	if !iter.Last() {
		if err := iter.Error(); err != nil {
			return 0, false, err
		}
		return 0, false, nil
	}
	seq, ok := decodeMessageSenderSeqIndexSeq(l.key, fromUID, iter.Key())
	if !ok {
		return 0, false, fmt.Errorf("%w: corrupt sender sequence index", dberrors.ErrCorruptValue)
	}
	return seq, true, nil
}

func (l *ChannelLog) lookupMessageIDSeq(ctx context.Context, messageID uint64) (uint64, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if messageID == 0 {
		return 0, false, dberrors.ErrInvalidArgument
	}
	channelKey, seq, ok, err := l.lookupGlobalMessageIDByKey(ctx, encodeGlobalMessageIDIndexKey(messageID))
	if err != nil || !ok || channelKey != l.key {
		return 0, ok && channelKey == l.key, err
	}
	return seq, true, nil
}

func (l *ChannelLog) lookupGlobalMessageIDByKey(ctx context.Context, storageKey []byte) (ChannelKey, uint64, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, false, err
	}
	value, ok, err := l.db.engine.Get(storageKey)
	if err != nil || !ok {
		return "", 0, ok, err
	}
	channelKey, seq, err := decodeGlobalMessageIDIndexValue(value)
	if err != nil {
		return "", 0, false, err
	}
	return channelKey, seq, true, nil
}

const messageIDIndexValueLen = 8

func encodeMessageIDIndexValue(seq uint64) []byte {
	value := make([]byte, messageIDIndexValueLen)
	writeMessageIDIndexValue(value, seq)
	return value
}

func writeMessageIDIndexValue(dst []byte, seq uint64) {
	binary.BigEndian.PutUint64(dst, seq)
}

func decodeMessageIDIndexValue(value []byte) (uint64, error) {
	if len(value) != messageIDIndexValueLen {
		return 0, dberrors.ErrCorruptValue
	}
	return binary.BigEndian.Uint64(value), nil
}
