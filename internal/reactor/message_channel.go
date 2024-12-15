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
	// ChannelMsgTypeDecrypt 解密消息
	ChannelMsgDecrypt
	// ChannelMsgPermission 权限消息
	ChannelMsgPermission
	// ChannelMsgStorage 存储消息
	ChannelMsgStorage
)

func (c ChannelMsgType) String() string {
	switch c {
	case ChannelMsgSend:
		return "send"
	case ChannelMsgDecrypt:
		return "decrypt"
	case ChannelMsgPermission:
		return "permission"
	case ChannelMsgStorage:
		return "storage"
	default:
		return fmt.Sprintf("unknown ChannelMsgType: %d", c)
	}
}

type ChannelMessage struct {
	Conn       *Conn               // 发送消息的连接
	Index      uint64              // 消息顺序下标
	MsgType    ChannelMsgType      // 消息类型
	SendPacket *wkproto.SendPacket // 发送包
	ToNode     uint64              // 发送给目标节点
}

func (c *ChannelMessage) Size() uint64 {
	return 0
}
