package frame

import "fmt"

// DisconnectPacket 断开连接数据包
type DisconnectPacket struct {
	Framer
	ReasonCode ReasonCode // 断开原因代号
	Reason     string     // 断开原因
}

// GetFrameType 包类型
func (c DisconnectPacket) GetFrameType() FrameType {
	return DISCONNECT
}

func (c DisconnectPacket) String() string {
	return fmt.Sprintf("ReasonCode:%d Reason:%s", c.ReasonCode, c.Reason)
}
