package frame

import "fmt"

// SendPacket 发送包
type SendPacket struct {
	Framer
	Setting     Setting
	MsgKey      string // 用于验证此消息是否合法（仿中间人篡改）
	Expire      uint32 // 消息过期时间 0 表示永不过期
	ClientSeq   uint64 // 客户端提供的序列号，在客户端内唯一
	ClientMsgNo string // 客户端消息唯一编号一般是uuid，为了去重
	StreamNo    string // 流式编号
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
	return fmt.Sprintf("Setting:%v MsgKey:%s Expire: %d ClientSeq:%d ClientMsgNo:%s ChannelId:%s ChannelType:%d Topic:%s Payload:%s", s.Setting, s.MsgKey, s.Expire, s.ClientSeq, s.ClientMsgNo, s.ChannelID, s.ChannelType, s.Topic, string(s.Payload))
}

// VerityString 验证字符串
func (s *SendPacket) VerityString() string {
	return fmt.Sprintf("%d%s%s%d%s", s.ClientSeq, s.ClientMsgNo, s.ChannelID, s.ChannelType, string(s.Payload))
}
