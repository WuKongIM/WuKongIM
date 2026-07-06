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

	End           uint8                       `json:"end,omitempty"`
	EndReason     uint8                       `json:"end_reason,omitempty"`
	Error         string                      `json:"error,omitempty"`
	StreamData    []byte                      `json:"stream_data,omitempty"`
	EventMeta     *legacyMessageEventMeta     `json:"event_meta,omitempty"`
	EventSyncHint *legacyMessageEventSyncHint `json:"event_sync_hint,omitempty"`

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
		Setting:       msg.Setting,
		MessageID:     int64(msg.MessageID),
		MessageIDStr:  strconv.FormatUint(msg.MessageID, 10),
		ClientMsgNo:   msg.ClientMsgNo,
		MessageSeq:    msg.MessageSeq,
		FromUID:       msg.FromUID,
		ChannelID:     legacyMessageChannelID(uid, msg.ChannelID, msg.ChannelType),
		ChannelType:   msg.ChannelType,
		Topic:         msg.Topic,
		Expire:        msg.Expire,
		Timestamp:     msg.Timestamp,
		Payload:       append([]byte(nil), msg.Payload...),
		End:           msg.End,
		EndReason:     msg.EndReason,
		Error:         msg.Error,
		StreamData:    append([]byte(nil), msg.StreamData...),
		EventMeta:     newLegacyMessageEventMeta(msg.EventMeta),
		EventSyncHint: newLegacyMessageEventSyncHint(msg.EventHint),
	}
}

type legacyMessageEventMeta struct {
	HasEvents       bool                        `json:"has_events"`
	Completed       bool                        `json:"completed"`
	EventVersion    uint64                      `json:"event_version,omitempty"`
	LastMsgEventSeq uint64                      `json:"last_msg_event_seq,omitempty"`
	EventCount      int                         `json:"event_count,omitempty"`
	OpenEventCount  int                         `json:"open_event_count,omitempty"`
	Events          []legacyMessageEventKeyMeta `json:"events,omitempty"`
}

type legacyMessageEventKeyMeta struct {
	EventKey        string `json:"event_key"`
	Status          string `json:"status"`
	LastMsgEventSeq uint64 `json:"last_msg_event_seq,omitempty"`
	Snapshot        any    `json:"snapshot,omitempty"`
	EndReason       uint8  `json:"end_reason,omitempty"`
	Error           string `json:"error,omitempty"`
}

type legacyMessageEventSyncHint struct {
	ClientMsgNo     string `json:"client_msg_no"`
	FromMsgEventSeq uint64 `json:"from_msg_event_seq"`
}

func newLegacyMessageEventMeta(meta *messageusecase.MessageEventMeta) *legacyMessageEventMeta {
	if meta == nil {
		return nil
	}
	out := &legacyMessageEventMeta{
		HasEvents:       meta.HasEvents,
		Completed:       meta.Completed,
		EventVersion:    meta.EventVersion,
		LastMsgEventSeq: meta.LastMsgEventSeq,
		EventCount:      meta.EventCount,
		OpenEventCount:  meta.OpenEventCount,
		Events:          make([]legacyMessageEventKeyMeta, 0, len(meta.Events)),
	}
	for _, event := range meta.Events {
		out.Events = append(out.Events, legacyMessageEventKeyMeta{
			EventKey:        event.EventKey,
			Status:          event.Status,
			LastMsgEventSeq: event.LastMsgEventSeq,
			Snapshot:        event.Snapshot,
			EndReason:       event.EndReason,
			Error:           event.Error,
		})
	}
	return out
}

func newLegacyMessageEventSyncHint(hint *messageusecase.MessageEventSyncHint) *legacyMessageEventSyncHint {
	if hint == nil {
		return nil
	}
	return &legacyMessageEventSyncHint{
		ClientMsgNo:     hint.ClientMsgNo,
		FromMsgEventSeq: hint.FromMsgEventSeq,
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
