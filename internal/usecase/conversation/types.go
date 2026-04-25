package conversation

import "github.com/WuKongIM/WuKongIM/pkg/channel"

type ConversationKey struct {
	ChannelID   string
	ChannelType uint8
}

type SyncQuery struct {
	UID                 string
	Version             int64
	LastMsgSeqs         map[ConversationKey]uint64
	MsgCount            int
	OnlyUnread          bool
	ExcludeChannelTypes []uint8
	Limit               int
}

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

type SyncResult struct {
	Conversations []SyncConversation
}
