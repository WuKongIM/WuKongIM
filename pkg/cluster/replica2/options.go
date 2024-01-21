package replica

import "time"

type Options struct {
	NodeID                uint64   // 当前节点ID
	ShardNo               string   // 分区编号
	Replicas              []uint64 // 副本节点ID集合
	Storage               IStorage
	MaxUncommittedLogSize uint64
	AppliedIndex          uint64        // 已应用的日志下标
	SyncLimit             uint32        // 同步日志最大数量
	PutMsgInterval        time.Duration // 放入消息的间隔时间
}

func NewOptions() *Options {
	return &Options{
		MaxUncommittedLogSize: 1024 * 1024 * 1024,
		SyncLimit:             100,
		PutMsgInterval:        time.Millisecond * 100,
	}
}

type Option func(o *Options)

func WithReplicas(replicas []uint64) Option {
	return func(o *Options) {
		o.Replicas = replicas
	}
}

func WithStorage(storage IStorage) Option {
	return func(o *Options) {
		o.Storage = storage
	}
}

func WithMaxUncommittedLogSize(size uint64) Option {
	return func(o *Options) {
		o.MaxUncommittedLogSize = size
	}
}

func WithAppliedIndex(index uint64) Option {
	return func(o *Options) {
		o.AppliedIndex = index
	}
}
