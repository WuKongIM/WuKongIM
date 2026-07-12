package message

import (
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

const latestMessageRawScanMultiplier = 16

// LatestMessagePage is one node-local page ordered by global message ID descending.
type LatestMessagePage struct {
	// Messages contains the newest locally persisted messages.
	Messages []Message
	// HasMore reports whether another older local page exists.
	HasMore bool
	// NextBeforeMessageID is the exclusive upper message ID for the next page.
	NextBeforeMessageID uint64
}

// ListLatestMessages returns node-local messages ordered by global message ID descending.
func (db *MessageDB) ListLatestMessages(ctx context.Context, beforeMessageID uint64, limit int) (LatestMessagePage, error) {
	if err := db.beginUse(); err != nil {
		return LatestMessagePage{}, err
	}
	defer db.endUse()
	if err := ctx.Err(); err != nil {
		return LatestMessagePage{}, err
	}
	if limit <= 0 {
		return LatestMessagePage{}, dberrors.ErrInvalidArgument
	}
	ready, err := db.latestIndex.result()
	if err != nil {
		return LatestMessagePage{}, err
	}
	if !ready {
		return LatestMessagePage{}, ErrLatestMessageIndexBuilding
	}

	prefix := encodeGlobalMessageIDIndexPrefix()
	span := keycodec.NewPrefixSpan(prefix)
	if beforeMessageID > 0 {
		span.End = encodeGlobalMessageIDIndexKey(beforeMessageID)
	}
	iter, err := db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return LatestMessagePage{}, err
	}
	defer iter.Close()

	page := LatestMessagePage{Messages: make([]Message, 0, limit)}
	staleMessageIDs := make([]uint64, 0)
	rawScanBudget := max(512, limit*latestMessageRawScanMultiplier)
	rawScanned := 0
	budgetExhausted := false
	for ok := iter.Last(); ok; ok = iter.Prev() {
		if rawScanned >= rawScanBudget {
			budgetExhausted = true
			break
		}
		if err := ctx.Err(); err != nil {
			return LatestMessagePage{}, err
		}
		messageID, ok := decodeGlobalMessageIDIndexKey(iter.Key())
		if !ok {
			continue
		}
		rawScanned++
		value, err := iter.Value()
		if err != nil {
			return LatestMessagePage{}, err
		}
		channelKey, seq, err := decodeGlobalMessageIDIndexValue(value)
		if err != nil {
			return LatestMessagePage{}, err
		}
		row, ok, err := getMessageRowBySeq(ctx, db, channelKey, seq)
		if err != nil {
			return LatestMessagePage{}, err
		}
		if !ok || row.MessageID != messageID {
			staleMessageIDs = append(staleMessageIDs, messageID)
			continue
		}
		if len(page.Messages) == limit {
			page.HasMore = true
			break
		}
		page.Messages = append(page.Messages, messageFromRow(row))
	}
	if err := iter.Error(); err != nil {
		return LatestMessagePage{}, err
	}
	if len(staleMessageIDs) > 0 {
		if err := db.deleteLatestMessageIndexes(staleMessageIDs); err != nil {
			return LatestMessagePage{}, err
		}
	}
	if budgetExhausted {
		return LatestMessagePage{}, ErrLatestMessageIndexMaintenance
	}
	if page.HasMore && len(page.Messages) > 0 {
		page.NextBeforeMessageID = page.Messages[len(page.Messages)-1].MessageID
	}
	return page, nil
}

// DeleteLatestMessageIndexes removes manager-only global projection entries.
func (db *MessageDB) DeleteLatestMessageIndexes(ctx context.Context, messageIDs []uint64) error {
	if err := db.beginUse(); err != nil {
		return err
	}
	defer db.endUse()
	if err := ctx.Err(); err != nil {
		return err
	}
	return db.deleteLatestMessageIndexes(messageIDs)
}

func (db *MessageDB) deleteLatestMessageIndexes(messageIDs []uint64) error {
	if len(messageIDs) == 0 {
		return nil
	}
	batch := db.engine.NewBatch()
	defer batch.Close()
	for _, messageID := range messageIDs {
		if messageID == 0 {
			continue
		}
		if err := batch.Delete(encodeGlobalMessageIDIndexKey(messageID)); err != nil {
			return err
		}
	}
	return batch.Commit(false)
}

func encodeGlobalMessageIDIndexValue(channelKey ChannelKey, seq uint64) []byte {
	value := keycodec.AppendString(nil, string(channelKey))
	return binary.BigEndian.AppendUint64(value, seq)
}

func decodeGlobalMessageIDIndexValue(value []byte) (ChannelKey, uint64, error) {
	channelKey, rest, err := keycodec.ReadString(value)
	if err != nil || channelKey == "" || len(rest) != 8 {
		return "", 0, dberrors.ErrCorruptValue
	}
	seq := binary.BigEndian.Uint64(rest)
	if seq == 0 {
		return "", 0, dberrors.ErrCorruptValue
	}
	return ChannelKey(channelKey), seq, nil
}

func getMessageRowBySeq(ctx context.Context, db *MessageDB, channelKey ChannelKey, seq uint64) (messageRow, bool, error) {
	if err := ctx.Err(); err != nil {
		return messageRow{}, false, err
	}
	if db == nil || db.engine == nil {
		return messageRow{}, false, dberrors.ErrClosed
	}
	headerKey := encodeMessageRowKey(channelKey, seq, messageHeaderFamilyID)
	headerValue, okHeader, err := db.engine.Get(headerKey)
	if err != nil {
		return messageRow{}, false, err
	}
	payloadKey := encodeMessageRowKey(channelKey, seq, messagePayloadFamilyID)
	payloadValue, okPayload, err := db.engine.Get(payloadKey)
	if err != nil {
		return messageRow{}, false, err
	}
	if !okHeader && !okPayload {
		return messageRow{}, false, nil
	}
	if !okHeader || !okPayload {
		return messageRow{}, false, dberrors.ErrCorruptState
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
