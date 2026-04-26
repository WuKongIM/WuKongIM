package conversation

import "github.com/WuKongIM/WuKongIM/pkg/channel"

// ConversationKey identifies one channel conversation in usecase APIs.
type ConversationKey struct {
	ChannelID   string
	ChannelType uint8
}

// SyncQuery describes a legacy conversation sync request after adapter mapping.
type SyncQuery struct {
	UID                 string
	Version             int64
	LastMsgSeqs         map[ConversationKey]uint64
	MsgCount            int
	OnlyUnread          bool
	ExcludeChannelTypes []uint8
	Limit               int
}

// ClearUnreadCommand advances a user's read cursor to the channel latest sequence.
type ClearUnreadCommand struct {
	UID         string
	ChannelID   string
	ChannelType uint8
	// MessageSeq is a legacy client-supplied latest sequence fallback for channels
	// whose latest message cannot be loaded from the server-side facts store.
	MessageSeq uint64
}

// SetUnreadCommand advances a user's read cursor so at most Unread messages remain unread.
type SetUnreadCommand struct {
	UID         string
	ChannelID   string
	ChannelType uint8
	Unread      int
}

// SyncConversation is the entry returned by conversation sync APIs.
type SyncConversation struct {
	ChannelID       string
	ChannelType     uint8
	Unread          int
	Timestamp       int64
	LastMsgSeq      uint32
	LastClientMsgNo string
	ReadToMsgSeq    uint32
	Version         int64
	Recents         []channel.Message
}

// SyncResult contains the conversations selected for a sync response.
type SyncResult struct {
	Conversations []SyncConversation
}
