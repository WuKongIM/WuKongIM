package frame

import "fmt"

// RecvackPacket 对收取包回执
type RecvackPacket struct {
	Framer
	MessageID  int64  // 服务端的消息ID(全局唯一)
	MessageSeq uint64 // 消息序列号
}

// GetPacketType 包类型
func (s *RecvackPacket) GetFrameType() FrameType {
	return RECVACK
}

func (s *RecvackPacket) String() string {
	return fmt.Sprintf("Framer:%s MessageId:%d MessageSeq:%d", s.Framer.String(), s.MessageID, s.MessageSeq)
}
