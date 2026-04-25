package frame

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
