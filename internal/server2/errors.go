package server

import "fmt"

var (
	ErrConnNotFound     = fmt.Errorf("conn not found")
	ErrReactorStopped   = fmt.Errorf("reactor stopped")
	ErrChannelIdIsEmpty = fmt.Errorf("channel id is empty")
)

type errCode int32

var (
	// 不是频道领导节点
	errCodeNotIsChannelLeader errCode = 1001
	// 不是用户领导节点
	errCodeNotIsUserLeader errCode = 1002

	// 连接未找到
	errCodeConnNotFound errCode = 1003
)
