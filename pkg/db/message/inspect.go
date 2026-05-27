package message

import (
	"context"
	"fmt"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// InspectMessageRow contains one decoded message-domain inspection row.
type InspectMessageRow map[string]any

// InspectMessageCursor identifies the next row position for an inspection scan.
type InspectMessageCursor struct {
	// AfterSeq resumes a channel message scan after this sequence.
	AfterSeq uint64
	// AfterChannelKey resumes a channel catalog scan after this channel key.
	AfterChannelKey string
}

// InspectMessageRequest describes a bounded read-only message-domain scan.
type InspectMessageRequest struct {
	// ChannelKey optionally selects one channel catalog entry or message log.
	ChannelKey string
	// AfterSeq resumes a channel message scan after this sequence.
	AfterSeq uint64
	// AfterChannelKey resumes a channel catalog scan after this channel key.
	AfterChannelKey string
	// Limit bounds the total returned rows.
	Limit int
}

// InspectMessageResult contains rows and resume metadata from an inspection scan.
type InspectMessageResult struct {
	// Rows contains decoded inspection rows.
	Rows []InspectMessageRow
	// Next is the cursor to continue scanning when Done is false.
	Next *InspectMessageCursor
	// Done reports whether the requested scan range was exhausted.
	Done bool
	// ScannedRows counts matching rows read, including a lookahead row when present.
	ScannedRows int
}

// InspectChannels lists message catalog channels for read-only diagnostics.
func InspectChannels(ctx context.Context, db *MessageDB, req InspectMessageRequest) (InspectMessageResult, error) {
	if err := inspectMessageCheckDB(db); err != nil {
		return InspectMessageResult{}, err
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}
	entries, scannedRows, done, err := inspectChannelCatalogPage(ctx, db, req)
	if err != nil {
		return InspectMessageResult{}, err
	}

	result := InspectMessageResult{
		Rows:        make([]InspectMessageRow, 0, boundedCapacity(len(entries), req.Limit)),
		Done:        done,
		ScannedRows: scannedRows,
	}
	if len(entries) > req.Limit {
		entries = entries[:req.Limit]
	}
	for _, entry := range entries {
		result.Rows = append(result.Rows, inspectChannelRow(entry))
	}
	if !result.Done && len(result.Rows) > 0 {
		result.Next = &InspectMessageCursor{AfterChannelKey: result.Rows[len(result.Rows)-1]["channel_key"].(string)}
	}
	return result, nil
}

func inspectChannelCatalogPage(ctx context.Context, db *MessageDB, req InspectMessageRequest) ([]ChannelCatalogEntry, int, bool, error) {
	if req.ChannelKey != "" {
		return inspectChannelCatalogPoint(ctx, db, req.ChannelKey, req.AfterChannelKey)
	}
	prefix := encodeCatalogPrefix()
	span := keycodec.NewPrefixSpan(prefix)
	start := span.Start
	if req.AfterChannelKey != "" {
		start = encodeCatalogKey(ChannelKey(req.AfterChannelKey))
	}
	iter, err := db.engine.NewIter(engine.Span{Start: start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, 0, false, err
	}
	defer iter.Close()

	entries := make([]ChannelCatalogEntry, 0, req.Limit+1)
	scannedRows := 0
	done := true
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, 0, false, err
		}
		key, entry, err := decodeCatalogIteratorEntry(iter)
		if err != nil {
			return nil, 0, false, err
		}
		if req.AfterChannelKey != "" && string(key) <= req.AfterChannelKey {
			continue
		}
		scannedRows++
		entries = append(entries, entry)
		if len(entries) > req.Limit {
			done = false
			break
		}
	}
	if err := iter.Error(); err != nil {
		return nil, 0, false, err
	}
	return entries, scannedRows, done, nil
}

func inspectChannelCatalogPoint(ctx context.Context, db *MessageDB, channelKey string, afterChannelKey string) ([]ChannelCatalogEntry, int, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, false, err
	}
	if afterChannelKey != "" && channelKey <= afterChannelKey {
		return nil, 0, true, nil
	}
	value, ok, err := db.engine.Get(encodeCatalogKey(ChannelKey(channelKey)))
	if err != nil {
		return nil, 0, false, err
	}
	if !ok {
		return nil, 0, true, nil
	}
	id, err := decodeCatalogValue(value)
	if err != nil {
		return nil, 0, false, err
	}
	return []ChannelCatalogEntry{{Key: ChannelKey(channelKey), ID: id}}, 1, true, nil
}

func decodeCatalogIteratorEntry(iter *engine.Iter) (ChannelKey, ChannelCatalogEntry, error) {
	key, ok := decodeCatalogKey(iter.Key())
	if !ok {
		return "", ChannelCatalogEntry{}, fmt.Errorf("%w: corrupt catalog key", dberrors.ErrCorruptValue)
	}
	value, err := iter.Value()
	if err != nil {
		return "", ChannelCatalogEntry{}, err
	}
	id, err := decodeCatalogValue(value)
	if err != nil {
		return "", ChannelCatalogEntry{}, err
	}
	return key, ChannelCatalogEntry{Key: key, ID: id}, nil
}

// InspectMessages scans one channel's messages for read-only diagnostics.
func InspectMessages(ctx context.Context, db *MessageDB, req InspectMessageRequest) (InspectMessageResult, error) {
	if err := inspectMessageCheckDB(db); err != nil {
		return InspectMessageResult{}, err
	}
	if req.ChannelKey == "" {
		return InspectMessageResult{}, dberrors.ErrInvalidArgument
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.AfterSeq == math.MaxUint64 {
		return InspectMessageResult{
			Rows: make([]InspectMessageRow, 0, req.Limit),
			Done: true,
		}, nil
	}

	log := &ChannelLog{db: db, key: ChannelKey(req.ChannelKey)}
	messages, err := log.Read(ctx, req.AfterSeq+1, ReadOptions{Limit: req.Limit + 1})
	if err != nil {
		return InspectMessageResult{}, err
	}
	result := InspectMessageResult{
		Rows:        make([]InspectMessageRow, 0, boundedCapacity(len(messages), req.Limit)),
		Done:        true,
		ScannedRows: len(messages),
	}
	if len(messages) > req.Limit {
		result.Done = false
		messages = messages[:req.Limit]
	}
	for _, msg := range messages {
		result.Rows = append(result.Rows, inspectMessageRow(msg))
	}
	if !result.Done && len(result.Rows) > 0 {
		result.Next = &InspectMessageCursor{AfterSeq: result.Rows[len(result.Rows)-1]["message_seq"].(uint64)}
	}
	return result, nil
}

func inspectMessageCheckDB(db *MessageDB) error {
	if db == nil || db.engine == nil {
		return dberrors.ErrClosed
	}
	return nil
}

func inspectChannelRow(entry ChannelCatalogEntry) InspectMessageRow {
	return InspectMessageRow{
		"channel_key":  string(entry.Key),
		"channel_id":   entry.ID.ID,
		"channel_type": entry.ID.Type,
	}
}

func inspectMessageRow(msg Message) InspectMessageRow {
	payload := append([]byte(nil), msg.Payload...)
	return InspectMessageRow{
		"message_seq":   msg.MessageSeq,
		"message_id":    msg.MessageID,
		"client_msg_no": msg.ClientMsgNo,
		"from_uid":      msg.FromUID,
		"payload_hash":  msg.PayloadHash,
		"payload_size":  uint64(len(payload)),
		"payload":       payload,
	}
}
