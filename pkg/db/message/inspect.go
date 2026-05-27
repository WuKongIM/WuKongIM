package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
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
	if req.Limit <= 0 {
		req.Limit = 100
	}
	entries, err := db.ListChannels(ctx)
	if err != nil {
		return InspectMessageResult{}, err
	}

	result := InspectMessageResult{
		Rows: make([]InspectMessageRow, 0, req.Limit),
		Done: true,
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return InspectMessageResult{}, err
		}
		key := string(entry.Key)
		if req.ChannelKey != "" && key != req.ChannelKey {
			continue
		}
		if req.AfterChannelKey != "" && key <= req.AfterChannelKey {
			continue
		}

		result.ScannedRows++
		if len(result.Rows) >= req.Limit {
			result.Done = false
			result.Next = &InspectMessageCursor{AfterChannelKey: result.Rows[len(result.Rows)-1]["channel_key"].(string)}
			break
		}
		result.Rows = append(result.Rows, inspectChannelRow(entry))
	}
	return result, nil
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

	log := db.Channel(ChannelKey(req.ChannelKey), ChannelID{})
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
		"payload_size":  len(payload),
		"payload":       payload,
	}
}
