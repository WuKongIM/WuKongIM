package raftgroup

import "time"

type Options struct {
	TickInterval time.Duration

	// GoPoolSize 协程池大小
	GoPoolSize int

	// Storage 存储接口
	Storage IStorage

	// Transport 传输接口
	Transport ITransport

	// ReceiveQueueLength 处理者接收队列的长度。
	ReceiveQueueLength uint64

	// MaxLogCountPerBatch 每次同步的最大日志数量
	MaxLogCountPerBatch uint64
}

func NewOptions(opt ...Option) *Options {
	os := &Options{
		TickInterval:        100 * time.Millisecond,
		GoPoolSize:          3000,
		ReceiveQueueLength:  1024,
		MaxLogCountPerBatch: 1000,
	}
	for _, o := range opt {
		o(os)
	}

	return os
}

type Option func(*Options)

func WithTickInterval(t time.Duration) Option {
	return func(o *Options) {
		o.TickInterval = t
	}
}

func WithGoPoolSize(size int) Option {
	return func(o *Options) {
		o.GoPoolSize = size
	}
}

func WithStorage(storage IStorage) Option {
	return func(o *Options) {
		o.Storage = storage
	}
}

func WithReceiveQueueLength(length uint64) Option {
	return func(o *Options) {
		o.ReceiveQueueLength = length
	}
}

func WithMaxLogCountPerBatch(count uint64) Option {
	return func(o *Options) {
		o.MaxLogCountPerBatch = count
	}
}
