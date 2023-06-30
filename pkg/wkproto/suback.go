package wkproto

import "github.com/pkg/errors"

type Action uint8

const (
	Subscribe   Action = iota // 订阅
	UnSubscribe               // 取消订阅
)

func (a Action) Uint8() uint8 {
	return uint8(a)
}

type SubackPacket struct {
	Framer
	SubNo       string     // 订阅编号
	ChannelID   string     // 频道ID（如果是个人频道ChannelId为个人的UID）
	ChannelType uint8      // 频道类型
	Action      Action     // 动作
	ReasonCode  ReasonCode // 原因码
}

// GetPacketType 包类型
func (s *SubackPacket) GetFrameType() FrameType {
	return SUBACK
}

func decodeSuback(frame Frame, data []byte, version uint8) (Frame, error) {
	dec := NewDecoder(data)

	subackPacket := &SubackPacket{}
	subackPacket.Framer = frame.(Framer)

	var err error
	// 客户端消息编号
	if subackPacket.SubNo, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码SubNo失败！")
	}
	// 频道ID
	if subackPacket.ChannelID, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码ChannelId失败！")
	}
	// 频道类型
	if subackPacket.ChannelType, err = dec.Uint8(); err != nil {
		return nil, errors.Wrap(err, "解码ChannelType失败！")
	}
	// 动作
	var action uint8
	if action, err = dec.Uint8(); err != nil {
		return nil, errors.Wrap(err, "解码Action失败！")
	}
	subackPacket.Action = Action(action)
	// 原因码
	var reasonCode byte
	if reasonCode, err = dec.Uint8(); err != nil {

		return nil, errors.Wrap(err, "解码ReasonCode失败！")
	}
	subackPacket.ReasonCode = ReasonCode(reasonCode)

	return subackPacket, nil
}

func encodeSuback(frame Frame, enc *Encoder, version uint8) error {
	subackPacket := frame.(*SubackPacket)
	// 客户端消息编号
	enc.WriteString(subackPacket.SubNo)
	// 频道ID
	enc.WriteString(subackPacket.ChannelID)
	// 频道类型
	enc.WriteUint8(subackPacket.ChannelType)
	// 动作
	enc.WriteUint8(subackPacket.Action.Uint8())
	// 原因码
	enc.WriteUint8(subackPacket.ReasonCode.Byte())
	return nil
}

func encodeSubackSize(frame Frame, version uint8) int {
	subPacket := frame.(*SubackPacket)
	var size = 0
	size += (len(subPacket.SubNo) + StringFixLenByteSize)
	size += (len(subPacket.ChannelID) + StringFixLenByteSize)
	size += ChannelTypeByteSize
	size += ActionByteSize
	size += ReasonCodeByteSize
	return size
}
