package frame

type SubPacket struct {
	Framer
	Setting     Setting
	SubNo       string
	ChannelID   string // 频道ID（如果是个人频道ChannelId为个人的UID）
	ChannelType uint8  // 频道类型
	Action      Action // 动作
	Param       string // 参数
}

// GetPacketType 包类型
func (s *SubPacket) GetFrameType() FrameType {
	return SUB
}
