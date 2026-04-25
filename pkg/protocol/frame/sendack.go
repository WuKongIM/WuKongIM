package frame

import "fmt"

// SendackPacket 发送回执包
type SendackPacket struct {
	Framer
	MessageID   int64      // 消息ID（全局唯一）
	MessageSeq  uint64     // 消息序列号（用户唯一，有序）
	ClientSeq   uint64     // 客户端序列号 (客户端提供，服务端原样返回)
	ClientMsgNo string     // 客户端消息编号(目前只有mos协议有效)
	ReasonCode  ReasonCode // 原因代码
}

// GetPacketType 包类型
func (s *SendackPacket) GetFrameType() FrameType {
	return SENDACK
}

func (s *SendackPacket) String() string {
	return fmt.Sprintf("MessageSeq:%d MessageId:%d ReasonCode:%s", s.MessageSeq, s.MessageID, s.ReasonCode)
}
