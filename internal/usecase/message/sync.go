package message

import (
	"context"
	"errors"
	"strings"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

const (
	defaultSyncMessagesLimit = 100
	maxSyncMessagesLimit     = 10000
)

// PullMode selects the legacy /channel/messagesync direction.
type PullMode uint8

const (
	// PullModeDown pulls older messages at or before start_message_seq.
	PullModeDown PullMode = iota
	// PullModeUp pulls newer messages at or after start_message_seq.
	PullModeUp
)

// SyncChannelMessagesQuery describes a legacy-compatible channel message sync.
type SyncChannelMessagesQuery struct {
	// LoginUID is the current logged-in user, used for authorization-facing channel normalization.
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

// SyncChannelMessagesResult contains one channel message sync page.
type SyncChannelMessagesResult struct {
	// Messages contains synced messages ordered by ascending message sequence.
	Messages []channel.Message
	// More reports whether another page exists inside the requested bounds.
	More bool
}

// ChannelMessageQuery is the storage-facing channel message sync request.
type ChannelMessageQuery struct {
	// ChannelID identifies the normalized channel to scan.
	ChannelID channel.ChannelID
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
	Messages []channel.Message
	// HasMore reports whether another page exists inside the requested bounds.
	HasMore bool
}

// SyncChannelMessages returns a legacy-compatible message page for a channel.
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
	if query.ChannelType == frame.ChannelTypePerson {
		normalized, err := runtimechannelid.NormalizePersonChannel(loginUID, channelID)
		if err != nil {
			return SyncChannelMessagesResult{}, err
		}
		channelID = normalized
	}

	if a == nil || a.messageReader == nil {
		return SyncChannelMessagesResult{}, ErrMessageReaderRequired
	}

	page, err := a.messageReader.SyncMessages(ctx, ChannelMessageQuery{
		ChannelID: channel.ChannelID{
			ID:   channelID,
			Type: query.ChannelType,
		},
		StartSeq: query.StartMessageSeq,
		EndSeq:   query.EndMessageSeq,
		Limit:    normalizeSyncMessagesLimit(query.Limit),
		PullMode: query.PullMode,
	})
	if errors.Is(err, metadb.ErrNotFound) {
		return SyncChannelMessagesResult{Messages: []channel.Message{}}, nil
	}
	if err != nil {
		return SyncChannelMessagesResult{}, err
	}
	return SyncChannelMessagesResult{
		Messages: append([]channel.Message(nil), page.Messages...),
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
