package api

import (
	"encoding/json"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/pkg/errors"
)

// messageSendReq 消息发送请求
type messageSendReq struct {
	Header      types.MessageHeader `json:"header"`        // 消息头
	ClientMsgNo string              `json:"client_msg_no"` // 客户端消息编号（相同编号，客户端只会显示一条）
	IsStream    int                 `json:"is_stream"`     // 是否为流消息（1=是），设置后可通过 eventAppend 追加事件
	FromUID     string              `json:"from_uid"`      // 发送者UID
	ChannelID   string              `json:"channel_id"`    // 频道ID
	ChannelType uint8               `json:"channel_type"`  // 频道类型
	Expire      uint32              `json:"expire"`        // 消息过期时间
	Subscribers []string            `json:"subscribers"`   // 订阅者 如果此字段有值，表示消息只发给指定的订阅者
	Payload     []byte              `json:"payload"`       // 消息内容
	TagKey      string              `json:"tag_key"`       // tagKey
}

// Check 检查输入
func (m messageSendReq) Check() error {
	if m.Payload == nil || len(m.Payload) <= 0 {
		return errors.New("payload不能为空！")
	}
	return nil
}

type syncReq struct {
	UID        string `json:"uid"`         // 用户uid
	MessageSeq uint64 `json:"message_seq"` // 客户端最大消息序列号
	Limit      int    `json:"limit"`       // 消息数量限制
}

func (r syncReq) Check() error {
	if strings.TrimSpace(r.UID) == "" {
		return errors.New("用户uid不能为空！")
	}
	if r.Limit < 0 {
		return errors.New("limit不能为负数！")
	}
	return nil
}

type syncackReq struct {
	// 用户uid
	UID string `json:"uid"`
	// 最后一次同步的message_seq
	LastMessageSeq uint64 `json:"last_message_seq"`
}

func (s syncackReq) Check() error {
	if strings.TrimSpace(s.UID) == "" {
		return errors.New("用户UID不能为空！")
	}
	if s.LastMessageSeq == 0 {
		return errors.New("最后一次messageSeq不能为0！")
	}
	return nil
}

type eventAppendReq struct {
	ChannelID   string            `json:"channel_id"`
	ChannelType uint8             `json:"channel_type"`
	FromUID     string            `json:"from_uid"`
	MessageID   int64             `json:"message_id"`
	ClientMsgNo string            `json:"client_msg_no"`
	EventID     string            `json:"event_id"`
	EventType   string            `json:"event_type"`
	EventKey      string            `json:"event_key"`
	Visibility  string            `json:"visibility"`
	OccurredAt  int64             `json:"occurred_at"`
	Payload     json.RawMessage   `json:"payload"`
	Headers     map[string]string `json:"headers"`
}

func (r eventAppendReq) Check() error {
	if strings.TrimSpace(r.ChannelID) == "" {
		return errors.New("channel_id不能为空！")
	}
	if strings.TrimSpace(r.ClientMsgNo) == "" {
		return errors.New("client_msg_no不能为空！")
	}
	if strings.TrimSpace(r.EventID) == "" {
		return errors.New("event_id不能为空！")
	}
	if strings.TrimSpace(r.EventType) == "" {
		return errors.New("event_type不能为空！")
	}
	return nil
}

type eventAppendResp struct {
	ClientMsgNo  string `json:"client_msg_no"`
	EventKey       string `json:"event_key"`
	EventID      string `json:"event_id"`
	MsgEventSeq  uint64 `json:"msg_event_seq"`
	StreamStatus string `json:"stream_status"`
	ChannelID    string `json:"channel_id"`
	ChannelType  uint8  `json:"channel_type"`
	FromUID      string `json:"from_uid"`
}

type eventSyncReq struct {
	ChannelID       string `json:"channel_id"`
	ChannelType     uint8  `json:"channel_type"`
	FromUID         string `json:"from_uid"`
	ClientMsgNo     string `json:"client_msg_no"`
	EventKey          string `json:"event_key"`
	FromMsgEventSeq uint64 `json:"from_msg_event_seq"`
	Limit           int    `json:"limit"`
	IncludePrivate  uint8  `json:"include_private"`
}

func (r eventSyncReq) Check() error {
	if strings.TrimSpace(r.ChannelID) == "" {
		return errors.New("channel_id不能为空！")
	}
	if strings.TrimSpace(r.ClientMsgNo) == "" {
		return errors.New("client_msg_no不能为空！")
	}
	if r.Limit < 0 {
		return errors.New("limit不能为负数！")
	}
	return nil
}
