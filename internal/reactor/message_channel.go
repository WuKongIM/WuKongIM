package reactor

import (
	"fmt"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type ChannelMsgType uint8

const (
	ChannelMsgUnknown ChannelMsgType = iota
	// ChannelMsgSend 发送消息
	ChannelMsgSend
	// ChannelMsgPermission 权限消息
	ChannelMsgPermission
	// ChannelMsgStorage 存储消息
	ChannelMsgStorage
	// ChannelMsgStorageNotifyQueue 存储消息到通知队列，webhook使用
	ChannelMsgStorageNotifyQueue
	// ChannelMsgSendack 发送消息回执
	ChannelMsgSendack
)

func (c ChannelMsgType) String() string {
	switch c {
	case ChannelMsgSend:
		return "ChannelMsgSend"
	case ChannelMsgPermission:
		return "ChannelMsgPermission"
	case ChannelMsgStorage:
		return "ChannelMsgStorage"
	case ChannelMsgStorageNotifyQueue:
		return "ChannelMsgStorageNotifyQueue"
	case ChannelMsgSendack:
		return "ChannelMsgSendack"
	default:
		return fmt.Sprintf("unknown ChannelMsgType: %d", c)
	}
}

type ChannelMessage struct {
	Conn          *Conn               // 发送消息的连接
	FakeChannelId string              // 频道ID, fakeChannelId
	ChannelType   uint8               // 频道类型
	Index         uint64              // 消息顺序下标
	MsgType       ChannelMsgType      // 消息类型
	SendPacket    *wkproto.SendPacket // 发送包
	ToNode        uint64              // 发送给目标节点
	MessageId     int64               // 消息ID
	MessageSeq    uint64              // 消息序列号
	ReasonCode    wkproto.ReasonCode  // 错误原因码
}

func (c *ChannelMessage) Size() uint64 {
	return 0
}
