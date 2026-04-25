package api

import (
	"strconv"
	"strings"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type syncConversationRequest struct {
	UID                 string  `json:"uid"`
	Version             int64   `json:"version"`
	LastMsgSeqs         string  `json:"last_msg_seqs"`
	MsgCount            int     `json:"msg_count"`
	OnlyUnread          uint8   `json:"only_unread"`
	ExcludeChannelTypes []uint8 `json:"exclude_channel_types"`
	Limit               int     `json:"limit"`
}

type legacyConversationResponse struct {
	ChannelID       string              `json:"channel_id"`
	ChannelType     uint8               `json:"channel_type"`
	Unread          int                 `json:"unread"`
	Timestamp       int64               `json:"timestamp"`
	LastMsgSeq      uint32              `json:"last_msg_seq"`
	LastClientMsgNo string              `json:"last_client_msg_no"`
	OffsetMsgSeq    int64               `json:"offset_msg_seq"`
	ReadedToMsgSeq  uint32              `json:"readed_to_msg_seq"`
	Version         int64               `json:"version"`
	Recents         []legacyMessageResp `json:"recents,omitempty"`
}

type legacyMessageHeader struct {
	NoPersist int `json:"no_persist"`
	RedDot    int `json:"red_dot"`
	SyncOnce  int `json:"sync_once"`
}

type legacyMessageResp struct {
	Header       legacyMessageHeader `json:"header"`
	Setting      uint8               `json:"setting"`
	MessageID    int64               `json:"message_id"`
	MessageIDStr string              `json:"message_idstr"`
	ClientMsgNo  string              `json:"client_msg_no"`

	End           uint8  `json:"end,omitempty"`
	EndReason     uint8  `json:"end_reason,omitempty"`
	Error         string `json:"error,omitempty"`
	StreamData    []byte `json:"stream_data,omitempty"`
	EventMeta     any    `json:"event_meta,omitempty"`
	EventSyncHint any    `json:"event_sync_hint,omitempty"`

	MessageSeq  uint64 `json:"message_seq"`
	FromUID     string `json:"from_uid"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	Topic       string `json:"topic,omitempty"`
	Expire      uint32 `json:"expire"`
	Timestamp   int32  `json:"timestamp"`
	Payload     []byte `json:"payload"`
}

func parseLegacyLastMsgSeqs(uid, raw string) (map[conversationusecase.ConversationKey]uint64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	items := strings.Split(raw, "|")
	out := make(map[conversationusecase.ConversationKey]uint64, len(items))
	for _, item := range items {
		parts := strings.Split(item, ":")
		if len(parts) != 3 || parts[0] == "" {
			return nil, strconv.ErrSyntax
		}

		channelType, err := strconv.ParseUint(parts[1], 10, 8)
		if err != nil {
			return nil, err
		}
		lastMsgSeq, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil {
			return nil, err
		}

		channelID := parts[0]
		if uint8(channelType) == frame.ChannelTypePerson {
			channelID, err = runtimechannelid.NormalizePersonChannel(uid, channelID)
			if err != nil {
				return nil, err
			}
		}
		out[conversationusecase.ConversationKey{
			ChannelID:   channelID,
			ChannelType: uint8(channelType),
		}] = lastMsgSeq
	}
	return out, nil
}

func newLegacyConversationResponse(uid string, item conversationusecase.SyncConversation) legacyConversationResponse {
	resp := legacyConversationResponse{
		ChannelID:       item.ChannelID,
		ChannelType:     item.ChannelType,
		Unread:          item.Unread,
		Timestamp:       item.Timestamp,
		LastMsgSeq:      item.LastMsgSeq,
		LastClientMsgNo: item.LastClientMsgNo,
		OffsetMsgSeq:    0,
		ReadedToMsgSeq:  item.ReadToMsgSeq,
		Version:         item.Version,
	}
	if len(item.Recents) > 0 {
		resp.Recents = make([]legacyMessageResp, 0, len(item.Recents))
		for _, msg := range item.Recents {
			resp.Recents = append(resp.Recents, newLegacyMessageResp(uid, msg))
		}
	}
	return resp
}

func newLegacyMessageResp(uid string, msg channel.Message) legacyMessageResp {
	return legacyMessageResp{
		Header: legacyMessageHeader{
			NoPersist: boolToInt(msg.Framer.NoPersist),
			RedDot:    boolToInt(msg.Framer.RedDot),
			SyncOnce:  boolToInt(msg.Framer.SyncOnce),
		},
		Setting:      uint8(msg.Setting),
		MessageID:    int64(msg.MessageID),
		MessageIDStr: strconv.FormatUint(msg.MessageID, 10),
		ClientMsgNo:  msg.ClientMsgNo,
		MessageSeq:   msg.MessageSeq,
		FromUID:      msg.FromUID,
		ChannelID:    legacyConversationChannelID(uid, msg.ChannelID, msg.ChannelType),
		ChannelType:  msg.ChannelType,
		Topic:        msg.Topic,
		Expire:       msg.Expire,
		Timestamp:    msg.Timestamp,
		Payload:      append([]byte(nil), msg.Payload...),
	}
}

func legacyConversationChannelID(uid, channelID string, channelType uint8) string {
	if channelType != frame.ChannelTypePerson {
		return channelID
	}
	left, right, err := runtimechannelid.DecodePersonChannel(channelID)
	if err != nil {
		return channelID
	}
	if left == uid {
		return right
	}
	if right == uid {
		return left
	}
	return channelID
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
