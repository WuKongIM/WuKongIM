package reactor

import (
	"time"

	goption "github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
)

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

	// MaxReceiveQueueSize is the maximum size in bytes of each receive queue.
	// Once the maximum size is reached, further replication messages will be
	// dropped to restrict memory usage. When set to 0, it means the queue size
	// is unlimited.
	MaxReceiveQueueSize uint64

	// ReceiveQueueLength 接收队列的长度。
	ReceiveQueueLength uint64
	// Send 发送
	Send func(actions []reactor.ChannelAction)
}

func NewOptions() *Options {
	opts := &Options{
		TickInterval:             time.Millisecond * 200,
		RetryIntervalTick:        10,
		SubCount:                 16,
		NodeHeartbeatTick:        10,
		NodeHeartbeatTimeoutTick: 30,
		LeaderIdleTimeoutTick:    30,
		ReceiveQueueLength:       1024,
	}

	// 如果开启了压测模式，接收队列加大长度
	if goption.G.Stress {
		opts.ReceiveQueueLength = 1024 * 10
	}
	return opts
}

type Option func(*Options)

func WithNodeId(nodeId uint64) Option {
	return func(o *Options) {
		o.NodeId = nodeId
	}
}

func WithSubCount(subCount int) Option {
	return func(o *Options) {
		o.SubCount = subCount
	}
}

func WithTickInterval(tickInterval time.Duration) Option {
	return func(o *Options) {
		o.TickInterval = tickInterval
	}
}

func WithRetryIntervalTick(retryIntervalTick int) Option {
	return func(o *Options) {
		o.RetryIntervalTick = retryIntervalTick
	}
}

func WithNodeHeartbeatTick(nodeHeartbeatTick int) Option {
	return func(o *Options) {
		o.NodeHeartbeatTick = nodeHeartbeatTick
	}
}

func WithNodeHeartbeatTimeoutTick(nodeHeartbeatTimeoutTick int) Option {
	return func(o *Options) {
		o.NodeHeartbeatTimeoutTick = nodeHeartbeatTimeoutTick
	}
}

func WithLeaderIdleTimeoutTick(leaderIdleTimeoutTick int) Option {
	return func(o *Options) {
		o.LeaderIdleTimeoutTick = leaderIdleTimeoutTick
	}
}

func WithMaxReceiveQueueSize(maxReceiveQueueSize uint64) Option {
	return func(o *Options) {
		o.MaxReceiveQueueSize = maxReceiveQueueSize
	}
}

func WithReceiveQueueLength(receiveQueueLength uint64) Option {
	return func(o *Options) {
		o.ReceiveQueueLength = receiveQueueLength
	}
}

func WithSend(send func(actions []reactor.ChannelAction)) Option {
	return func(o *Options) {
		o.Send = send
	}
}
