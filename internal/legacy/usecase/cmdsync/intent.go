package cmdsync

import (
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// ConversationIntent describes one CMD conversation update before it is flushed.
type ConversationIntent struct {
	// CommandChannelID identifies the durable command-channel log, e.g. source____cmd.
	CommandChannelID string
	// ChannelType is the WuKong channel type for the command-channel log.
	ChannelType uint8
	// MessageSeq is the committed command message sequence.
	MessageSeq uint64
	// ActiveAt is the message activity timestamp in Unix nanoseconds.
	ActiveAt int64
	// SenderUID is the trimmed sender UID from the committed message.
	SenderUID string
	// UserReadSeqs maps each resolved recipient UID to its pending read sequence.
	UserReadSeqs map[string]uint64
}

// PendingConversationUpdate is the owner-local buffered update for one CMD channel.
type PendingConversationUpdate struct {
	// CommandChannelID identifies the durable command-channel log, e.g. source____cmd.
	CommandChannelID string `json:"command_channel_id"`
	// ChannelType is the WuKong channel type for the command-channel log.
	ChannelType uint8 `json:"channel_type"`
	// LastMsgSeq is the highest pending command message sequence for this channel.
	LastMsgSeq uint64 `json:"last_msg_seq"`
	// ActiveAt is the latest pending activity timestamp in Unix nanoseconds.
	ActiveAt int64 `json:"active_at"`
	// UserReadSeqs maps each pending recipient UID to its read sequence.
	UserReadSeqs map[string]uint64 `json:"user_read_seqs"`
	// UserLastMsgSeqs maps each pending recipient UID to its covered message sequence.
	UserLastMsgSeqs map[string]uint64 `json:"user_last_msg_seqs,omitempty"`
	// UserActiveAts maps each pending recipient UID to its activity timestamp.
	UserActiveAts map[string]int64 `json:"user_active_ats,omitempty"`
}

// PendingConversationView is the pending overlay for one UID and CMD channel.
type PendingConversationView struct {
	// CommandChannelID identifies the durable command-channel log, e.g. source____cmd.
	CommandChannelID string
	// ChannelType is the WuKong channel type for the command-channel log.
	ChannelType uint8
	// LastMsgSeq is the highest pending command message sequence for this channel.
	LastMsgSeq uint64
	// ActiveAt is the pending activity timestamp in Unix nanoseconds.
	ActiveAt int64
	// ReadSeq is this UID's pending read sequence for the command channel.
	ReadSeq uint64
}

// BuildConversationIntent converts a durable CMD message and resolved UIDs into a pending update intent.
func BuildConversationIntent(msg channel.Message, uids []string, now func() time.Time) (ConversationIntent, bool) {
	if !isDurableCMDProjectionMessage(msg) {
		return ConversationIntent{}, false
	}

	commandChannelID := msg.ChannelID
	if !runtimechannelid.IsCommandChannel(commandChannelID) {
		commandChannelID = runtimechannelid.ToCommandChannel(commandChannelID)
	}

	normalizedUIDs := uniqueNonEmptyStrings(uids)
	if len(normalizedUIDs) == 0 {
		return ConversationIntent{}, false
	}

	senderUID := strings.TrimSpace(msg.FromUID)
	readSeqs := make(map[string]uint64, len(normalizedUIDs))
	for _, uid := range normalizedUIDs {
		if uid == senderUID {
			readSeqs[uid] = msg.MessageSeq
			continue
		}
		readSeqs[uid] = 0
	}

	return ConversationIntent{
		CommandChannelID: commandChannelID,
		ChannelType:      msg.ChannelType,
		MessageSeq:       msg.MessageSeq,
		ActiveAt:         activeAtFromMessage(msg, now),
		SenderUID:        senderUID,
		UserReadSeqs:     readSeqs,
	}, true
}

func isDurableCMDProjectionMessage(msg channel.Message) bool {
	if msg.MessageSeq == 0 || msg.Framer.NoPersist {
		return false
	}
	return runtimechannelid.IsCommandChannel(msg.ChannelID) || msg.Framer.SyncOnce
}

func activeAtFromMessage(msg channel.Message, now func() time.Time) int64 {
	if msg.Timestamp != 0 {
		return time.Unix(int64(msg.Timestamp), 0).UnixNano()
	}
	if now == nil {
		now = time.Now
	}
	return now().UnixNano()
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
