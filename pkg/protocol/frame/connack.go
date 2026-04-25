package frame

import "fmt"

// ConnackPacket 连接回执包
type ConnackPacket struct {
	Framer
	ServerVersion uint8      // 服务端版本
	ServerKey     string     // 服务端的DH公钥
	Salt          string     // salt
	TimeDiff      int64      // 客户端时间与服务器的差值，单位毫秒。
	ReasonCode    ReasonCode // 原因码
	NodeId        uint64     // 节点Id
}

// GetFrameType 获取包类型
func (c ConnackPacket) GetFrameType() FrameType {
	return CONNACK
}

func (c ConnackPacket) String() string {
	return fmt.Sprintf("TimeDiff: %d ReasonCode:%s", c.TimeDiff, c.ReasonCode.String())
}
