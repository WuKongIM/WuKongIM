package api

import (
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/types"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
)

type streamv2OpenReq struct {
	Header      types.MessageHeader `json:"header"`        // 消息头
	ClientMsgNo string              `json:"client_msg_no"` // 客户端消息编号（相同编号，客户端只会显示一条）
	FromUid     string              `json:"from_uid"`      // 发送者UID
	ChannelId   string              `json:"channel_id"`    // 频道ID
	ChannelType uint8               `json:"channel_type"`  // 频道类型
	Payload     []byte              `json:"payload"`       // 初始化消息内容
	Force       bool                `json:"force"`         // 是否强制创建(如果为true 则会强制关闭其他在进行中流)
}

func (s streamv2OpenReq) check() error {
	if strings.TrimSpace(s.ChannelId) == "" {
		return errors.New("频道ID不能为空！")
	}
	if s.ChannelType == 0 {
		return errors.New("频道类型不能为0！")
	}

	if s.ChannelType == wkproto.ChannelTypePerson && strings.TrimSpace(s.FromUid) == "" {
		return errors.New("发送者UID不能为空！")
	}

	if s.Payload == nil || len(s.Payload) <= 0 {
		return errors.New("初始化消息内容不能为空！")
	}
	return nil
}

type streamv2WriteReq struct {
	ChannelId   string            `json:"channel_id"`           // 频道ID
	ChannelType uint8             `json:"channel_type"`         // 频道类型
	FromUid     string            `json:"from_uid"`             // 发送者UID(如果是非个人频道此字段可以为空)
	MessageId   int64             `json:"message_id"`           // 消息id
	End         uint8             `json:"end,omitempty"`        // 是否是最后一段
	EndReason   wkproto.EndReason `json:"end_reason,omitempty"` // 结束原因
	Payload     []byte            `json:"payload"`              // 消息内容
}

func (s streamv2WriteReq) check() error {
	if s.MessageId <= 0 {
		return errors.New("消息id不能小于等于0！")
	}

	if strings.TrimSpace(s.ChannelId) == "" {
		return errors.New("频道ID不能为空！")
	}

	if s.ChannelType == 0 {
		return errors.New("频道类型不能为0！")
	}

	if s.ChannelType == wkproto.ChannelTypePerson && strings.TrimSpace(s.FromUid) == "" {
		return errors.New("发送者UID不能为空！")
	}
	if len(s.Payload) <= 0 {
		return errors.New("消息内容不能为空！")
	}
	return nil
}
