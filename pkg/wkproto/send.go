package wkproto

import (
	"fmt"
	"sync"

	"github.com/pkg/errors"
)

type Setting uint8

const (
	SettingUnknown        Setting = 0
	SettingReceiptEnabled Setting = 1 << 7 // 是否开启回执
	SettingSignal         Setting = 1 << 5 // 是否开启signal加密
	SettingNoEncrypt      Setting = 1 << 4 // 是否不加密
	SettingTopic          Setting = 1 << 3 // 是否有topic
)

func (s Setting) IsSet(v Setting) bool {
	return s&v != 0
}

func (s *Setting) Clear(v Setting) {
	*s &= ^v
}

func (s *Setting) Set(v Setting) {
	*s |= v
}

func (s Setting) Uint8() uint8 {
	return uint8(s)
}

// SendPacket 发送包
type SendPacket struct {
	Framer
	Setting     Setting
	MsgKey      string // 用于验证此消息是否合法（仿中间人篡改）
	ClientSeq   uint64 // 客户端提供的序列号，在客户端内唯一
	ClientMsgNo string // 客户端消息唯一编号一般是uuid，为了去重
	ChannelID   string // 频道ID（如果是个人频道ChannelId为个人的UID）
	ChannelType uint8  // 频道类型（1.个人 2.群组）
	Topic       string // 消息topic
	Payload     []byte // 消息内容

}

func (s *SendPacket) UniqueKey() string {
	return fmt.Sprintf("%s-%d-%s-%d", s.ChannelID, s.ChannelType, s.ClientMsgNo, s.ClientSeq)
}

// GetPacketType 包类型
func (s *SendPacket) GetFrameType() FrameType {
	return SEND
}

func (s *SendPacket) String() string {
	return fmt.Sprintf("Setting:%v MsgKey:%s ClientSeq:%d ClientMsgNo:%s ChannelId:%s ChannelType:%d Topic:%s Payload:%s", s.Setting, s.MsgKey, s.ClientSeq, s.ClientMsgNo, s.ChannelID, s.ChannelType, s.Topic, string(s.Payload))
}

func (s *SendPacket) reset() {
	s.Framer.RedDot = false
	s.Framer.DUP = false
	s.Framer.NoPersist = false
	s.Framer.SyncOnce = false
	s.Framer.FrameSize = 0
	s.Framer.RemainingLength = 0
	s.Setting = 0
	s.MsgKey = ""
	s.ClientSeq = 0
	s.ClientMsgNo = ""
	s.ChannelID = ""
	s.ChannelType = 0
	s.Topic = ""
	s.Payload = nil
}

// VerityString 验证字符串
func (s *SendPacket) VerityString() string {
	return fmt.Sprintf("%d%s%s%d%s", s.ClientSeq, s.ClientMsgNo, s.ChannelID, s.ChannelType, string(s.Payload))
}

var sendPacketPool = sync.Pool{
	New: func() any {
		return &SendPacket{}
	},
}

func decodeSend(frame Frame, data []byte, version uint8) (Frame, error) {
	dec := decoderPool.Get().(*Decoder)
	dec.p = data
	dec.offset = 0

	sendPacket := &SendPacket{}
	// sendPacket.reset()
	sendPacket.Framer = frame.(Framer)

	var err error
	if version > 3 {
		setting, err := dec.Uint8()
		if err != nil {
			return nil, errors.Wrap(err, "解码消息设置失败！")
		}
		sendPacket.Setting = Setting(setting)
	}

	if version > 2 {
		// msg key
		if sendPacket.MsgKey, err = dec.String(); err != nil {
			return nil, errors.Wrap(err, "解码MsgKey失败！")
		}
	}

	// 消息序列号(客户端维护)
	var clientSeq uint32
	if clientSeq, err = dec.Uint32(); err != nil {
		return nil, errors.Wrap(err, "解码ClientSeq失败！")
	}
	sendPacket.ClientSeq = uint64(clientSeq)
	if version > 1 {
		// // 客户端唯一标示
		if sendPacket.ClientMsgNo, err = dec.String(); err != nil {
			return nil, errors.Wrap(err, "解码ClientMsgNo失败！")
		}
	}
	// 频道ID
	if sendPacket.ChannelID, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码ChannelId失败！")
	}
	// 频道类型
	if sendPacket.ChannelType, err = dec.Uint8(); err != nil {

		return nil, errors.Wrap(err, "解码ChannelType失败！")
	}
	if version > 4 {
		if sendPacket.Setting.IsSet(SettingTopic) {
			// topic
			if sendPacket.Topic, err = dec.String(); err != nil {
				return nil, errors.Wrap(err, "解密topic消息失败！")
			}
		}
	}
	if sendPacket.Payload, err = dec.BinaryAll(); err != nil {
		return nil, errors.Wrap(err, "解码payload失败！")
	}
	return sendPacket, err
}

func encodeSend(frame Frame, enc *Encoder, version uint8) error {
	sendPacket := frame.(*SendPacket)

	if version > 3 {
		enc.WriteByte(sendPacket.Setting.Uint8())
	}
	if version > 2 {
		enc.WriteString(sendPacket.MsgKey)
	}

	// 消息序列号(客户端维护)
	enc.WriteUint32(uint32(sendPacket.ClientSeq))
	if version > 1 {
		// 客户端唯一标示
		enc.WriteString(sendPacket.ClientMsgNo)
	}
	// 频道ID
	enc.WriteString(sendPacket.ChannelID)
	// 频道类型
	enc.WriteUint8(sendPacket.ChannelType)

	if version > 4 {
		if sendPacket.Setting.IsSet(SettingTopic) {
			enc.WriteString(sendPacket.Topic)
		}
	}
	// 消息内容
	enc.WriteBytes(sendPacket.Payload)

	return nil
}

func encodeSendSize(frame Frame, version uint8) int {
	sendPacket := frame.(*SendPacket)
	size := 0
	if version > 3 {
		size += SettingByteSize
	}
	if version > 2 {
		size += (len(sendPacket.MsgKey) + StringFixLenByteSize)
	}
	size += ClientSeqByteSize

	if version > 1 {
		size += (len(sendPacket.ClientMsgNo) + StringFixLenByteSize)
	}
	size += (len(sendPacket.ChannelID) + StringFixLenByteSize)

	size += ChannelTypeByteSize

	if version > 4 {
		if sendPacket.Setting.IsSet(SettingTopic) {
			size += (len(sendPacket.Topic) + StringFixLenByteSize)
		}
	}
	size += len(sendPacket.Payload)

	return size
}
