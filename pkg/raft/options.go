package raft

import (
	"time"

	"github.com/panjf2000/ants/v2"
)

type Options struct {
	// NodeId 节点ID
	NodeId uint64
	// TickInterval tick间隔,多久触发一次tick
	TickInterval time.Duration
	// SyncInterval 同步间隔, 单位: tick, 表示多少个tick发起一次同步
	SyncInterval int
	// ElectionOn 是否开启选举， 如果开启选举，那么节点之间会自己选举出一个领导者，默认为false
	ElectionOn bool

	// HeartbeatInterval 心跳间隔tick次数, 就是tick触发几次算一次心跳，一般为1 一次tick算一次心跳
	HeartbeatInterval int
	// ElectionInterval 选举间隔tick次数，超过此tick数则发起选举
	ElectionInterval int
	// Replicas 副本的节点id，不包含节点自己
	Replicas []uint64

	// Transport 传输层
	Transport Transport
	//	 Storage 存储层
	Storage Storage
	// Advance 用于推进状态机
	Advance func()
	// MaxLogCountPerBatch 每次同步的最大日志数量
	MaxLogCountPerBatch uint64

	// Submit 提交给协程执行
	Submit func(f func()) error

	// GoPoolSize 协程池大小, 如果设置了Submit, 那么这个参数无效
	GoPoolSize int
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		SyncInterval:        2,
		ElectionOn:          false,
		HeartbeatInterval:   1,
		ElectionInterval:    10,
		TickInterval:        time.Millisecond * 100,
		MaxLogCountPerBatch: 1000,
		GoPoolSize:          1000,
	}

	pool, err := ants.NewPool(opts.GoPoolSize)
	if err != nil {
		panic(err)
	}
	opts.Submit = func(f func()) error {
		return pool.Submit(f)
	}

	for _, o := range opt {
		o(opts)
	}
	return opts
}

type Option func(opts *Options)

func WithNodeId(nodeId uint64) Option {
	return func(opts *Options) {
		opts.NodeId = nodeId
	}
}

func WithTickInterval(tickInterval time.Duration) Option {
	return func(opts *Options) {
		opts.TickInterval = tickInterval
	}
}

func WithSyncInterval(syncInterval int) Option {
	return func(opts *Options) {
		opts.SyncInterval = syncInterval
	}
}

func WithElectionOn(electionOn bool) Option {
	return func(opts *Options) {
		opts.ElectionOn = electionOn
	}
}

func WithHeartbeatInterval(heartbeatInterval int) Option {
	return func(opts *Options) {
		opts.HeartbeatInterval = heartbeatInterval
	}
}

func WithElectionInterval(electionInterval int) Option {
	return func(opts *Options) {
		opts.ElectionInterval = electionInterval
	}
}

func WithReplicas(replicas []uint64) Option {
	return func(opts *Options) {
		opts.Replicas = replicas
	}
}

func WithTransport(transport Transport) Option {
	return func(opts *Options) {
		opts.Transport = transport
	}
}

func WithStorage(storage Storage) Option {
	return func(opts *Options) {
		opts.Storage = storage
	}
}
