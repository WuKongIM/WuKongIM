package api

import (
	"strconv"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

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

func newLegacyMessageResp(uid string, msg messageusecase.SyncedMessage) legacyMessageResp {
	return legacyMessageResp{
		Header: legacyMessageHeader{
			NoPersist: boolToInt(msg.Flags.NoPersist),
			RedDot:    boolToInt(msg.Flags.RedDot),
			SyncOnce:  boolToInt(msg.Flags.SyncOnce),
		},
		Setting:      msg.Setting,
		MessageID:    int64(msg.MessageID),
		MessageIDStr: strconv.FormatUint(msg.MessageID, 10),
		ClientMsgNo:  msg.ClientMsgNo,
		MessageSeq:   msg.MessageSeq,
		FromUID:      msg.FromUID,
		ChannelID:    legacyMessageChannelID(uid, msg.ChannelID, msg.ChannelType),
		ChannelType:  msg.ChannelType,
		Topic:        msg.Topic,
		Expire:       msg.Expire,
		Timestamp:    msg.Timestamp,
		Payload:      append([]byte(nil), msg.Payload...),
	}
}

func legacyMessageChannelID(uid, channelID string, channelType uint8) string {
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
