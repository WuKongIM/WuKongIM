package reactor

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/track"
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
	// ChannelMsgMakeTag 制作tag
	ChannelMsgMakeTag
	// ChannelMsgConversationUpdate 更新最近会话
	ChannelMsgConversationUpdate
	// ChannelMsgDiffuse 消息扩散
	ChannelMsgDiffuse
	// ChannelMsgPushOnline 推送在线消息
	ChannelMsgPushOnline
	// ChannelMsgPushOffline 推送离线消息
	ChannelMsgPushOffline
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
	case ChannelMsgMakeTag:
		return "ChannelMsgMakeTag"
	case ChannelMsgConversationUpdate:
		return "ChannelMsgConversationUpdate"
	case ChannelMsgDiffuse:
		return "ChannelMsgDiffuse"
	case ChannelMsgPushOnline:
		return "ChannelMsgPushOnline"
	case ChannelMsgPushOffline:
		return "ChannelMsgPushOffline"
	default:
		return fmt.Sprintf("unknown ChannelMsgType: %d", c)
	}
}

type ChannelMessage struct {
	Conn          *Conn               // 发送消息的连接
	SendPacket    *wkproto.SendPacket // 发送包
	FakeChannelId string              // 频道ID, fakeChannelId
	ChannelType   uint8               // 频道类型
	Index         uint64              // 消息顺序下标
	MsgType       ChannelMsgType      // 消息类型
	ToNode        uint64              // 发送给目标节点
	MessageId     int64               // 消息ID
	MessageSeq    uint64              // 消息序列号
	ReasonCode    wkproto.ReasonCode  // 错误原因码
	TagKey        string              // 标签key
	ToUid         string              // 接收者
	Track         track.Message       // 消息轨迹信息
	// 不需要编码的字段
	OfflineUsers []string // 离线用户的数组（投递离线的时候需要）

}

func (c *ChannelMessage) Clone() *ChannelMessage {
	return &ChannelMessage{
		Conn:          c.Conn,
		SendPacket:    c.SendPacket,
		FakeChannelId: c.FakeChannelId,
		ChannelType:   c.ChannelType,
		Index:         c.Index,
		MsgType:       c.MsgType,
		ToNode:        c.ToNode,
		MessageId:     c.MessageId,
		MessageSeq:    c.MessageSeq,
		ReasonCode:    c.ReasonCode,
		TagKey:        c.TagKey,
		ToUid:         c.ToUid,
		Track:         c.Track.Clone(),
	}
}

func (c *ChannelMessage) Size() uint64 {
	size := uint64(1)
	if c.hasConn() == 1 {
		size += c.Conn.Size()
	}
	if c.hasSendPacket() == 1 {
		size += uint64(c.SendPacket.GetFrameSize())
	}
	size += uint64(len(c.FakeChannelId)) + 1
	size += 1
	size += 8
	size += 1
	size += 8
	size += 8
	size += 1
	size += uint64(len(c.TagKey)) + 1
	size += uint64(len(c.ToUid)) + 1

	if c.Track.HasData() {
		size += c.Track.Size()
	}
	return size
}

func (c *ChannelMessage) Encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	flag := c.hasConn()<<7 | c.hasSendPacket()<<6 | c.hasTrack()<<5
	enc.WriteUint8(flag)
	if c.hasConn() == 1 {
		data, err := c.Conn.Encode()
		if err != nil {
			return nil, err
		}
		enc.WriteUint16(uint16(len(data)))
		enc.WriteBytes(data)
	}
	if c.hasSendPacket() == 1 {
		data, err := Proto.EncodeFrame(c.SendPacket, wkproto.LatestVersion)
		if err != nil {
			return nil, err
		}
		enc.WriteUint32(uint32(len(data)))
		enc.WriteBytes(data)
	}
	enc.WriteString(c.FakeChannelId)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint64(c.Index)
	enc.WriteUint8(uint8(c.MsgType))
	enc.WriteUint64(c.ToNode)
	enc.WriteInt64(c.MessageId)
	enc.WriteUint64(c.MessageSeq)
	enc.WriteUint8(uint8(c.ReasonCode))
	enc.WriteString(c.TagKey)
	enc.WriteString(c.ToUid)
	if c.hasTrack() == 1 {
		data := c.Track.Encode()
		enc.WriteBytes(data)
	}
	return enc.Bytes(), nil

}

func (c *ChannelMessage) Decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var flag uint8
	if flag, err = dec.Uint8(); err != nil {
		return err
	}
	hasConn := flag >> 7 & 0x01
	hasSendPacket := flag >> 6 & 0x01
	hasTrack := flag >> 5 & 0x01
	if hasConn == 1 {
		connLen, err := dec.Uint16()
		if err != nil {
			return err
		}
		connData, err := dec.Bytes(int(connLen))
		if err != nil {
			return err
		}
		c.Conn = &Conn{}
		if err = c.Conn.Decode(connData); err != nil {
			return err
		}
	}
	if hasSendPacket == 1 {
		sendPacketLen, err := dec.Uint32()
		if err != nil {
			return err
		}
		sendPacketData, err := dec.Bytes(int(sendPacketLen))
		if err != nil {
			return err
		}
		frame, _, err := Proto.DecodeFrame(sendPacketData, wkproto.LatestVersion)
		if err != nil {
			return err
		}
		c.SendPacket = frame.(*wkproto.SendPacket)
	}
	if c.FakeChannelId, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	if c.Index, err = dec.Uint64(); err != nil {
		return err
	}
	var msgType uint8
	if msgType, err = dec.Uint8(); err != nil {
		return err
	}
	c.MsgType = ChannelMsgType(msgType)

	if c.ToNode, err = dec.Uint64(); err != nil {
		return err
	}
	if c.MessageId, err = dec.Int64(); err != nil {
		return err
	}
	if c.MessageSeq, err = dec.Uint64(); err != nil {
		return err
	}

	var reasonCode uint8
	if reasonCode, err = dec.Uint8(); err != nil {
		return err
	}
	c.ReasonCode = wkproto.ReasonCode(reasonCode)

	if c.TagKey, err = dec.String(); err != nil {
		return err
	}
	if c.ToUid, err = dec.String(); err != nil {
		return err
	}
	if hasTrack == 1 {
		recordBytes, err := dec.BinaryAll()
		if err != nil {
			return err
		}
		record := track.Message{}
		err = record.Decode(recordBytes)
		if err != nil {
			return err
		}
		c.Track = record
	}

	return nil
}

func (c *ChannelMessage) hasConn() uint8 {
	if c.Conn != nil {
		return 1
	}
	return 0
}

func (c *ChannelMessage) hasSendPacket() uint8 {
	if c.SendPacket != nil {
		return 1
	}
	return 0
}
func (c *ChannelMessage) hasTrack() uint8 {
	if c.Track.HasData() {
		return 1
	}
	return 0
}

type ChannelMessageBatch []*ChannelMessage

func (c ChannelMessageBatch) Size() uint64 {
	var size uint64
	for _, v := range c {
		size += v.Size()
	}
	return size
}

func (c ChannelMessageBatch) Encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(uint32(len(c)))
	for _, v := range c {
		data, err := v.Encode()
		if err != nil {
			return nil, err
		}
		enc.WriteUint32(uint32(len(data)))
		enc.WriteBytes(data)
	}
	return enc.Bytes(), nil
}

func (c *ChannelMessageBatch) Decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var length uint32
	if length, err = dec.Uint32(); err != nil {
		return err
	}
	for i := 0; i < int(length); i++ {
		var dataLen uint32
		if dataLen, err = dec.Uint32(); err != nil {
			return err
		}
		data, err := dec.Bytes(int(dataLen))
		if err != nil {
			return err
		}
		msg := &ChannelMessage{}
		if err = msg.Decode(data); err != nil {
			return err
		}
		*c = append(*c, msg)
	}
	return nil
}
