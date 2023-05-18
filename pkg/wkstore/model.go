package wkstore

import (
	"fmt"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
)

const conversationVersion = 0x1

// Conversation Conversation
type Conversation struct {
	UID             string // User UID (user who belongs to the most recent session)
	ChannelID       string // Conversation channel
	ChannelType     uint8
	UnreadCount     int    // Number of unread messages
	Timestamp       int64  // Last session timestamp (10 digits)
	LastMsgSeq      uint32 // Sequence number of the last message
	LastClientMsgNo string // Last message client number
	LastMsgID       int64  // Last message ID
	Version         int64  // Data version
}

func (c *Conversation) String() string {
	return fmt.Sprintf("uid:%s channelID:%s channelType:%d unreadCount:%d timestamp: %d lastMsgSeq:%d lastClientMsgNo:%s lastMsgID:%d version:%d", c.UID, c.ChannelID, c.ChannelType, c.UnreadCount, c.Timestamp, c.LastMsgSeq, c.LastClientMsgNo, c.LastMsgID, c.Version)
}

type ConversationSet []*Conversation

func (c ConversationSet) Encode() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	for _, cn := range c {
		enc.WriteUint8(conversationVersion)
		enc.WriteString(cn.UID)
		enc.WriteString(cn.ChannelID)
		enc.WriteUint8(cn.ChannelType)
		enc.WriteInt32(int32(cn.UnreadCount))
		enc.WriteInt64(cn.Timestamp)
		enc.WriteUint32(cn.LastMsgSeq)
		enc.WriteString(cn.LastClientMsgNo)
		enc.WriteInt64(cn.LastMsgID)
		enc.WriteInt64(cn.Version)
	}
	return enc.Bytes()
}

func NewConversationSet(data []byte) ConversationSet {
	conversationSet := ConversationSet{}
	decoder := wkproto.NewDecoder(data)

	for {
		conversation, err := decodeConversation(decoder)
		if err == io.EOF {
			break
		}
		conversationSet = append(conversationSet, conversation)
	}
	return conversationSet
}

func decodeConversation(decoder *wkproto.Decoder) (*Conversation, error) {
	// proto version
	_, err := decoder.Uint8()
	if err != nil {
		return nil, err
	}
	cn := &Conversation{}

	if cn.UID, err = decoder.String(); err != nil {
		return nil, err
	}
	if cn.ChannelID, err = decoder.String(); err != nil {
		return nil, err
	}
	if cn.ChannelType, err = decoder.Uint8(); err != nil {
		return nil, err
	}
	var unreadCount uint32
	if unreadCount, err = decoder.Uint32(); err != nil {
		return nil, err
	}
	cn.UnreadCount = int(unreadCount)

	if cn.Timestamp, err = decoder.Int64(); err != nil {
		return nil, err
	}
	if cn.LastMsgSeq, err = decoder.Uint32(); err != nil {
		return nil, err
	}
	if cn.LastClientMsgNo, err = decoder.String(); err != nil {
		return nil, err
	}
	if cn.LastMsgID, err = decoder.Int64(); err != nil {
		return nil, err
	}
	if cn.Version, err = decoder.Int64(); err != nil {
		return nil, err
	}
	return cn, nil
}
