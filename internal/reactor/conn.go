package reactor

import wkproto "github.com/WuKongIM/WuKongIMGoProto"

type Conn interface {
	// ConnId  用户唯一连接id
	ConnId() int64
	// Uid 用户uid
	Uid() string
	// DeviceFlag 设备标记
	DeviceFlag() wkproto.DeviceFlag
	// FromNode 连接属于节点
	FromNode() uint64
	// SetAuth 设置是否认证
	SetAuth(auth bool)
	// IsAuth 是否认证
	IsAuth() bool
}
