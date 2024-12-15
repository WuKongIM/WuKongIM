package reactor

import "time"

var options *Options // 全局配置

type Options struct {
	// NodeId 当前节点Id
	NodeId   uint64
	SubCount int
	// TickInterval 多久发起一次tick
	TickInterval time.Duration
	// RetryIntervalTick 默认重试间隔
	RetryIntervalTick int

	NodeHeartbeatTick int

	NodeHeartbeatTimeoutTick int
	// LeaderIdleTimeoutTick leader空闲超时, 当leader空闲超过这个时间，会关闭
	LeaderIdleTimeoutTick int
}

func NewOptions() *Options {
	return &Options{
		TickInterval:             time.Millisecond * 200,
		RetryIntervalTick:        10,
		SubCount:                 64,
		NodeHeartbeatTick:        10,
		NodeHeartbeatTimeoutTick: 30,
		LeaderIdleTimeoutTick:    30,
	}
}

type Option func(*Options)
