package raftgroup

import "time"

type Options struct {
	// TickInterval tick间隔,多久触发一次tick
	TickInterval time.Duration
	// GoPoolSize 协程池大小
	GoPoolSize int
	// Storage 存储接口
	Storage IStorage
	// Transport 传输接口
	Transport ITransport
	// ReceiveQueueLength 处理者接收队列的长度。
	ReceiveQueueLength uint64
	// MaxLogSizePerBatch 每次同步的最大日志大小 单位byte
	MaxLogSizePerBatch uint64
	// ProposeTimeout 提案超时时间
	ProposeTimeout time.Duration
	// LogPrefix 日志前缀
	LogPrefix string
	// NotNeedApplied 是否不需要应用日志,如果为true，表示不需要应用日志，只需要存储日志，也就是不会调用storage.Apply方法
	NotNeedApplied bool

	// 事件
	Event IEvent
}

func NewOptions(opt ...Option) *Options {
	os := &Options{
		TickInterval:       100 * time.Millisecond,
		GoPoolSize:         6000,
		ReceiveQueueLength: 1024,
		MaxLogSizePerBatch: 1024 * 1024 * 10,
		ProposeTimeout:     5 * time.Second,
		NotNeedApplied:     false,
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

func WithMaxLogSizePerBatch(size uint64) Option {
	return func(o *Options) {
		o.MaxLogSizePerBatch = size
	}
}

func WithTransport(transport ITransport) Option {
	return func(o *Options) {
		o.Transport = transport
	}
}

func WithProposeTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.ProposeTimeout = timeout
	}
}

func WithLogPrefix(prefix string) Option {
	return func(o *Options) {
		o.LogPrefix = prefix
	}
}

func WithNotNeedApplied(notNeedApplied bool) Option {
	return func(o *Options) {
		o.NotNeedApplied = notNeedApplied
	}
}

func WithEvent(event IEvent) Option {
	return func(o *Options) {
		o.Event = event
	}
}
