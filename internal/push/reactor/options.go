package reactor

import (
	"time"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
)

type Options struct {
	// WorkerCount 数量
	WorkerCount int
	// 每个reactorSub负责的worker数量
	WorkerPerReactorSub int
	// MaxReceiveQueueSize is the maximum size in bytes of each receive queue.
	// Once the maximum size is reached, further replication messages will be
	// dropped to restrict memory usage. When set to 0, it means the queue size
	// is unlimited.
	MaxReceiveQueueSize uint64

	// ReceiveQueueLength 接收队列的长度。
	ReceiveQueueLength uint64

	// TickInterval 多久发起一次tick
	TickInterval time.Duration

	// Send 发送
	Send func(actions []reactor.PushAction)
}

func NewOptions() *Options {
	return &Options{
		TickInterval:        time.Millisecond * 200,
		WorkerCount:         10,
		WorkerPerReactorSub: 10,
		ReceiveQueueLength:  1024,
	}
}

type Option func(*Options)

func WithWorkerCount(workerCount int) Option {
	return func(o *Options) {
		o.WorkerCount = workerCount
	}
}

func WithWorkerPerReactorSub(workerPerReactorSub int) Option {
	return func(o *Options) {
		o.WorkerPerReactorSub = workerPerReactorSub
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

func WithTickInterval(tickInterval time.Duration) Option {
	return func(o *Options) {
		o.TickInterval = tickInterval
	}
}

func WithSend(send func(actions []reactor.PushAction)) Option {
	return func(o *Options) {
		o.Send = send
	}
}
