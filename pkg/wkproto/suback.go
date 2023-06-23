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
	Setting     Setting
	ChannelID   string // 频道ID（如果是个人频道ChannelId为个人的UID）
	ChannelType uint8  // 频道类型
	Action      Action // 动作
	ReasonCode  uint8  // 原因码
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
	setting, err := dec.Uint8()
	if err != nil {
		return nil, errors.Wrap(err, "解码消息设置失败！")
	}
	subackPacket.Setting = Setting(setting)
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
	if subackPacket.ReasonCode, err = dec.Uint8(); err != nil {

		return nil, errors.Wrap(err, "解码ReasonCode失败！")
	}

	return subackPacket, nil
}

func encodeSuback(frame Frame, enc *Encoder, version uint8) error {
	subackPacket := frame.(*SubackPacket)
	enc.WriteByte(subackPacket.Setting.Uint8())
	// 频道ID
	enc.WriteString(subackPacket.ChannelID)
	// 频道类型
	enc.WriteUint8(subackPacket.ChannelType)
	// 动作
	enc.WriteUint8(subackPacket.Action.Uint8())
	// 原因码
	enc.WriteUint8(subackPacket.ReasonCode)
	return nil
}

func encodeSubackSize(frame Frame, version uint8) int {
	subPacket := frame.(*SubackPacket)
	var size = 0
	size += SettingByteSize
	size += (len(subPacket.ChannelID) + StringFixLenByteSize)
	size += ChannelTypeByteSize
	size += ActionByteSize
	size += ReasonCodeByteSize
	return size
}
