package api

import (
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/pkg/errors"
)

// messageSendReq 消息发送请求
type messageSendReq struct {
	Header      types.MessageHeader `json:"header"`        // 消息头
	ClientMsgNo string              `json:"client_msg_no"` // 客户端消息编号（相同编号，客户端只会显示一条）
	StreamNo    string              `json:"stream_no"`     // 消息流编号
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
