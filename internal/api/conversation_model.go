package api

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
)

type clearConversationUnreadReq struct {
	UID         string `json:"uid"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	MessageSeq  uint32 `json:"message_seq"` // messageSeq 只有超大群才会传 因为超大群最近会话服务器不会维护，需要客户端传递messageSeq进行主动维护
}

func (req clearConversationUnreadReq) Check() error {
	if req.UID == "" {
		return errors.New("uid cannot be empty")
	}
	if req.ChannelID == "" || req.ChannelType == 0 {
		return errors.New("channel_id or channel_type cannot be empty")
	}
	return nil
}

type deleteChannelReq struct {
	UID         string `json:"uid"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

func (req deleteChannelReq) Check() error {
	if len(req.UID) <= 0 {
		return errors.New("Uid cannot be empty")
	}
	if req.ChannelID == "" || req.ChannelType == 0 {
		return errors.New("channel_id or channel_type cannot be empty")
	}
	return nil
}

type syncUserConversationResp struct {
	ChannelId       string               `json:"channel_id"`         // 频道ID
	ChannelType     uint8                `json:"channel_type"`       // 频道类型
	Unread          int                  `json:"unread"`             // 未读消息
	Timestamp       int64                `json:"timestamp"`          // 最后一次会话时间
	LastMsgSeq      uint32               `json:"last_msg_seq"`       // 最后一条消息seq
	LastClientMsgNo string               `json:"last_client_msg_no"` // 最后一次消息客户端编号
	OffsetMsgSeq    int64                `json:"offset_msg_seq"`     // 偏移位的消息seq
	ReadedToMsgSeq  uint32               `json:"readed_to_msg_seq"`  // 已读至的消息seq
	Version         int64                `json:"version"`            // 数据版本
	Recents         []*types.MessageResp `json:"recents"`            // 最近N条消息
}

func newSyncUserConversationResp(conversation wkdb.Conversation) *syncUserConversationResp {
	realChannelId := conversation.ChannelId
	if conversation.ChannelType == wkproto.ChannelTypePerson {
		from, to := options.GetFromUIDAndToUIDWith(conversation.ChannelId)
		if from == conversation.Uid {
			realChannelId = to
		} else {
			realChannelId = from
		}
	}
	return &syncUserConversationResp{
		ChannelId:      realChannelId,
		ChannelType:    conversation.ChannelType,
		Unread:         int(conversation.UnreadCount),
		ReadedToMsgSeq: uint32(conversation.ReadToMsgSeq),
	}
}

type channelRecentMessageReq struct {
	ChannelId   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	LastMsgSeq  uint64 `json:"last_msg_seq"`
}

type channelRecentMessage struct {
	ChannelId   string               `json:"channel_id"`
	ChannelType uint8                `json:"channel_type"`
	Messages    []*types.MessageResp `json:"messages"`
}
