package reactor

import (
	"time"

	goption "github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
)

var options *Options // 全局配置

type Options struct {
	// NodeId 当前节点Id
	NodeId uint64
	// TickInterval 多久发起一次tick
	TickInterval time.Duration
	// RetryIntervalTick 默认重试间隔
	RetryIntervalTick int

	// NodeHeartbeatTick 节点之间协调间隔
	NodeHeartbeatTick int
	// NodeHeartbeatTimeoutTick 节点心跳超时，当超过这个时间没有收到心跳时，认为节点已经下线
	// 一般为NodeHeartbeatTick的 2 - 3 倍
	NodeHeartbeatTimeoutTick int

	// LeaderIdleTimeoutTick leader空闲超时, 当leader空闲超过这个时间，会关闭
	LeaderIdleTimeoutTick int

	// OutboundForwardIntervalTick 发件箱转发间隔tick
	OutboundForwardIntervalTick int

	// OutboundForwardMaxMessageCount 每次最大转发数量
	OutboundForwardMaxMessageCount uint64

	// NodeVersion 获取节点的数据版本
	NodeVersion func() uint64

	// reactorSub数量
	SubCount int

	// MaxReceiveQueueSize is the maximum size in bytes of each receive queue.
	// Once the maximum size is reached, further replication messages will be
	// dropped to restrict memory usage. When set to 0, it means the queue size
	// is unlimited.
	MaxReceiveQueueSize uint64

	// ReceiveQueueLength 接收队列的长度。
	ReceiveQueueLength uint64

	// Send 发送
	Send func(actions []reactor.UserAction)
}

func NewOptions() *Options {

	opts := &Options{
		TickInterval:                   time.Millisecond * 200,
		RetryIntervalTick:              10,
		NodeHeartbeatTick:              10,
		NodeHeartbeatTimeoutTick:       30,
		LeaderIdleTimeoutTick:          30,
		SubCount:                       16,
		ReceiveQueueLength:             1024,
		OutboundForwardIntervalTick:    2,
		OutboundForwardMaxMessageCount: 100,
	}
	if goption.G.Stress {
		opts.ReceiveQueueLength = 1024 * 10
	}
	return opts
}

type Option func(opts *Options)

func WithNodeId(nodeId uint64) Option {

	return func(opts *Options) {
		opts.NodeId = nodeId
	}
}

func WithSubCount(subCount int) Option {

	return func(opts *Options) {
		opts.SubCount = subCount
	}
}

func WithSend(f func([]reactor.UserAction)) Option {
	return func(opts *Options) {
		opts.Send = f
	}
}

func WithNodeVersion(f func() uint64) Option {
	return func(opts *Options) {
		opts.NodeVersion = f
	}
}

func WithReceiveQueueLength(length uint64) Option {
	return func(opts *Options) {
		opts.ReceiveQueueLength = length
	}
}

func WithMaxReceiveQueueSize(size uint64) Option {
	return func(opts *Options) {
		opts.MaxReceiveQueueSize = size
	}
}

func WithOutboundForwardIntervalTick(tick int) Option {
	return func(opts *Options) {
		opts.OutboundForwardIntervalTick = tick
	}
}

func WithOutboundForwardMaxMessageCount(count uint64) Option {
	return func(opts *Options) {
		opts.OutboundForwardMaxMessageCount = count
	}
}

func WithLeaderIdleTimeoutTick(tick int) Option {
	return func(opts *Options) {
		opts.LeaderIdleTimeoutTick = tick
	}
}

func WithNodeHeartbeatTick(tick int) Option {
	return func(opts *Options) {
		opts.NodeHeartbeatTick = tick
	}
}

func WithNodeHeartbeatTimeoutTick(tick int) Option {
	return func(opts *Options) {
		opts.NodeHeartbeatTimeoutTick = tick
	}
}

func WithTickInterval(interval time.Duration) Option {
	return func(opts *Options) {
		opts.TickInterval = interval
	}
}

func WithRetryIntervalTick(tick int) Option {
	return func(opts *Options) {
		opts.RetryIntervalTick = tick
	}
}
