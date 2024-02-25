package replica

import "time"

type AckMode int

const (
	// AckModeNone AckModeNone
	AckModeNone AckMode = iota
	// AckModeMajority AckModeMajority
	AckModeMajority
	// AckModeAll AckModeAll
	AckModeAll
)

type Options struct {
	NodeID                uint64   // 当前节点ID
	ShardNo               string   // 分区编号
	Replicas              []uint64 // 副本节点ID集合
	Storage               IStorage
	MaxUncommittedLogSize uint64
	AppliedIndex          uint64        // 已应用的日志下标
	SyncLimitSize         uint64        // 每次同步日志数据的最大大小（过小影响吞吐量，过大导致消息阻塞，默认为4M）
	MessageSendInterval   time.Duration // 消息发送间隔
	MaxIdleInterval       time.Duration // 最大空闲时间
	AckMode               AckMode       // AckMode
	// LastSyncInfoMap       map[uint64]*SyncInfo
}

func NewOptions() *Options {
	return &Options{
		MaxUncommittedLogSize: 1024 * 1024 * 1024,
		SyncLimitSize:         1024 * 1024 * 4, // 4M
		// LastSyncInfoMap:       map[uint64]*SyncInfo{},
		MessageSendInterval: time.Millisecond * 100,
		MaxIdleInterval:     time.Second * 1,
		AckMode:             AckModeMajority,
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

func WithSyncLimitSize(size uint64) Option {
	return func(o *Options) {
		o.SyncLimitSize = size
	}
}
