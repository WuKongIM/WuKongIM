package frame

import (
	"fmt"
)

// ConnectPacket 连接包
type ConnectPacket struct {
	Framer
	Version         uint8      // 协议版本
	ClientKey       string     // 客户端公钥
	DeviceID        string     // 设备ID
	DeviceFlag      DeviceFlag // 设备标示(同标示同账号互踢)
	ClientTimestamp int64      // 客户端当前时间戳(13位时间戳,到毫秒)
	UID             string     // 用户ID
	Token           string     // token
}

// GetFrameType 包类型
func (c ConnectPacket) GetFrameType() FrameType {
	return CONNECT
}

func (c ConnectPacket) String() string {
	return fmt.Sprintf(" UID:%s DeviceFlag:%d DeviceId:%s ClientTimestamp:%d  Token:%s Version:%d", c.UID, c.DeviceFlag, c.DeviceID, c.ClientTimestamp, c.Token, c.Version)
}
