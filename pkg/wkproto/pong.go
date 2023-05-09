package wkproto

// PongPacket pong包对ping的回应
type PongPacket struct {
	Framer
}

// GetFrameType 包类型
func (p *PongPacket) GetFrameType() FrameType {
	return PONG
}
