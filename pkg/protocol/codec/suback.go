package codec

import (
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/pkg/errors"
)

func decodeSuback(f frame.Frame, data []byte, version uint8) (frame.Frame, error) {
	dec := NewDecoder(data)

	subackPacket := &frame.SubackPacket{}
	subackPacket.Framer = f.(frame.Framer)

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
	subackPacket.Action = frame.Action(action)
	// 原因码
	var reasonCode byte
	if reasonCode, err = dec.Uint8(); err != nil {

		return nil, errors.Wrap(err, "解码ReasonCode失败！")
	}
	subackPacket.ReasonCode = frame.ReasonCode(reasonCode)

	return subackPacket, nil
}

func encodeSuback(subackPacket *frame.SubackPacket, enc *Encoder, _ uint8) error {
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

func encodeSubackSize(subPacket *frame.SubackPacket, _ uint8) int {
	var size = 0
	size += len(subPacket.SubNo) + frame.StringFixLenByteSize
	size += len(subPacket.ChannelID) + frame.StringFixLenByteSize
	size += frame.ChannelTypeByteSize
	size += frame.ActionByteSize
	size += frame.ReasonCodeByteSize
	return size
}
