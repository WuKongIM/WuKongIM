package message

import (
	"context"
	"errors"
	"strings"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

const (
	defaultSyncMessagesLimit = 100
	maxSyncMessagesLimit     = 10000
)

// PullMode selects the compatible /channel/messagesync direction.
type PullMode uint8

const (
	// PullModeDown pulls older messages at or before start_message_seq.
	PullModeDown PullMode = iota
	// PullModeUp pulls newer messages at or after start_message_seq.
	PullModeUp
)

// MessageFlags carries legacy message header flags for compatible HTTP responses.
type MessageFlags struct {
	// NoPersist reports whether the message was marked as non-durable.
	NoPersist bool
	// RedDot reports whether the message should affect unread red-dot state.
	RedDot bool
	// SyncOnce reports whether the message was sent as a one-shot sync command.
	SyncOnce bool
}

// SyncedMessage is a channel message returned by the compatible sync usecase.
type SyncedMessage struct {
	// Flags contains the legacy framer flags exposed in HTTP responses.
	Flags MessageFlags
	// Setting is the legacy message setting bitset.
	Setting uint8
	// MessageID is the durable message id.
	MessageID uint64
	// ClientMsgNo is the client idempotency key.
	ClientMsgNo string
	// MessageSeq is the committed channel sequence.
	MessageSeq uint64
	// FromUID is the sender user id.
	FromUID string
	// ChannelID is the normalized channel id.
	ChannelID string
	// ChannelType is the protocol channel category.
	ChannelType uint8
	// Topic is the optional message topic.
	Topic string
	// Expire is the legacy expiration value.
	Expire uint32
	// Timestamp is the legacy message timestamp.
	Timestamp int32
	// Payload is the immutable message payload.
	Payload []byte
}

// SyncChannelMessagesQuery describes a compatible channel message sync request.
type SyncChannelMessagesQuery struct {
	// LoginUID is the current logged-in user, used for person-channel normalization.
	LoginUID string
	// ChannelID is the client-facing channel identifier.
	ChannelID string
	// ChannelType is the client-facing channel type.
	ChannelType uint8
	// StartMessageSeq is the inclusive starting sequence boundary.
	StartMessageSeq uint64
	// EndMessageSeq is the exclusive ending sequence boundary.
	EndMessageSeq uint64
	// Limit is the maximum number of messages to return.
	Limit int
	// PullMode selects whether to pull older or newer messages.
	PullMode PullMode
	// EventSummaryMode is accepted for compatibility with older stream-message clients.
	EventSummaryMode string
}

// SyncChannelMessagesResult contains one compatible channel message sync page.
type SyncChannelMessagesResult struct {
	// Messages contains synced messages ordered by ascending message sequence.
	Messages []SyncedMessage
	// More reports whether another page exists inside the requested bounds.
	More bool
}

// ChannelMessageQuery is the storage-facing channel message sync request.
type ChannelMessageQuery struct {
	// ChannelID identifies the normalized channel to scan.
	ChannelID ChannelID
	// StartSeq is the inclusive starting sequence boundary.
	StartSeq uint64
	// EndSeq is the exclusive ending sequence boundary.
	EndSeq uint64
	// Limit is the maximum number of messages to return.
	Limit int
	// PullMode selects whether to pull older or newer messages.
	PullMode PullMode
}

// ChannelMessagePage is one authoritative channel message sync page.
type ChannelMessagePage struct {
	// Messages contains synced messages ordered by ascending message sequence.
	Messages []SyncedMessage
	// HasMore reports whether another page exists inside the requested bounds.
	HasMore bool
}

// SyncChannelMessages returns a compatible message page for a channel.
func (a *App) SyncChannelMessages(ctx context.Context, query SyncChannelMessagesQuery) (SyncChannelMessagesResult, error) {
	loginUID := strings.TrimSpace(query.LoginUID)
	if loginUID == "" {
		return SyncChannelMessagesResult{}, ErrSyncLoginUIDRequired
	}
	channelID := strings.TrimSpace(query.ChannelID)
	if channelID == "" {
		return SyncChannelMessagesResult{}, ErrSyncChannelIDRequired
	}
	if query.ChannelType == 0 {
		return SyncChannelMessagesResult{}, ErrSyncChannelTypeRequired
	}
	if query.ChannelType == channelTypePerson {
		normalized, err := runtimechannelid.NormalizePersonChannel(loginUID, channelID)
		if err != nil {
			return SyncChannelMessagesResult{}, err
		}
		channelID = normalized
	}
	if a == nil || a.reader == nil {
		return SyncChannelMessagesResult{}, ErrMessageReaderRequired
	}
	page, err := a.reader.SyncMessages(ctx, ChannelMessageQuery{
		ChannelID: ChannelID{ID: channelID, Type: query.ChannelType},
		StartSeq:  query.StartMessageSeq,
		EndSeq:    query.EndMessageSeq,
		Limit:     normalizeSyncMessagesLimit(query.Limit),
		PullMode:  query.PullMode,
	})
	if errors.Is(err, metadb.ErrNotFound) {
		return SyncChannelMessagesResult{Messages: []SyncedMessage{}}, nil
	}
	if err != nil {
		return SyncChannelMessagesResult{}, err
	}
	return SyncChannelMessagesResult{
		Messages: cloneSyncedMessages(page.Messages),
		More:     page.HasMore,
	}, nil
}

func normalizeSyncMessagesLimit(limit int) int {
	if limit <= 0 {
		return defaultSyncMessagesLimit
	}
	if limit > maxSyncMessagesLimit {
		return maxSyncMessagesLimit
	}
	return limit
}

func cloneSyncedMessages(in []SyncedMessage) []SyncedMessage {
	out := make([]SyncedMessage, len(in))
	copy(out, in)
	for i := range out {
		out[i].Payload = cloneBytes(out[i].Payload)
	}
	return out
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
