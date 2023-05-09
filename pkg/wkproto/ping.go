package wkproto

// PingPacket ping包
type PingPacket struct {
	Framer
}

// GetFrameType 包类型
func (p *PingPacket) GetFrameType() FrameType {
	return PING
}
