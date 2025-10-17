package raft

import (
	"time"
)

type Options struct {
	// Key raft的唯一标识，raftgroup中的key
	Key string
	// NodeId 节点ID
	NodeId uint64
	// TickInterval tick间隔,多久触发一次tick
	TickInterval time.Duration
	// SyncInterval 同步间隔, 单位: tick, 表示多少个tick发起一次同步
	SyncIntervalTick int
	// SyncRespTimeoutTick 同步响应超时tick次数,超过此tick数则重新发起同步
	SyncRespTimeoutTick int
	// ElectionOn 是否开启选举， 如果开启选举，那么节点之间会自己选举出一个领导者，默认为false
	ElectionOn bool

	// HeartbeatInterval 心跳间隔tick次数, 就是tick触发几次算一次心跳，一般为1 一次tick算一次心跳
	HeartbeatInterval int
	// ElectionInterval 选举间隔tick次数，超过此tick数则发起选举
	ElectionInterval int
	// Replicas 副本的节点id，包括自己, 如果只有自己表示单节点启动，如果为空，则表示暂不集群化，等后续SwitchConfig
	Replicas []uint64

	// Transport 传输层
	Transport Transport
	//	 Storage 存储层
	Storage Storage
	// Advance 用于推进状态机
	Advance func()
	// MaxLogCountPerBatch 每次同步的最大日志数量
	MaxLogCountPerBatch uint64

	// GoPoolSize 协程池大小, 如果设置了Submit, 那么这个参数无效
	GoPoolSize int

	// ProposeTimeout 提案超时时间
	ProposeTimeout time.Duration

	// LearnerToLeaderMinLogGap 学习者转换为领导者的最小日志差距
	LearnerToLeaderMinLogGap uint64

	// LearnerToFollowerMinLogGap 学习者转换为跟随者的最小日志差距
	LearnerToFollowerMinLogGap uint64

	// FollowerToLeaderMinLogGap 跟随者转换为领导者的最小日志差距
	FollowerToLeaderMinLogGap uint64

	// AutoSuspend 是否允许自动挂起
	AutoSuspend bool

	// AutoDestory 是否自动销毁
	AutoDestory bool

	// 在空同步指定次数后，进入挂起状态
	SuspendAfterEmptySyncTick int

	// 空闲多久后销毁
	DestoryAfterIdleTick int
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		SyncIntervalTick:           2,
		SyncRespTimeoutTick:        10,
		ElectionOn:                 false,
		HeartbeatInterval:          1,
		ElectionInterval:           10,
		TickInterval:               time.Millisecond * 150,
		MaxLogCountPerBatch:        1000,
		GoPoolSize:                 1000,
		ProposeTimeout:             time.Second * 5,
		LearnerToFollowerMinLogGap: 100,
		LearnerToLeaderMinLogGap:   100,
		FollowerToLeaderMinLogGap:  100,
		SuspendAfterEmptySyncTick:  10,
		AutoSuspend:                false,
		AutoDestory:                false,
		DestoryAfterIdleTick:       10 * 60 * 30, // 如果TickInterval是100ms, 那么10 * 60 * 30这个值是30分钟，具体时间根据TickInterval来定
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
		opts.SyncIntervalTick = syncInterval
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

func WithAdvance(advance func()) Option {
	return func(opts *Options) {
		opts.Advance = advance
	}
}

func WithMaxLogCountPerBatch(maxLogCountPerBatch uint64) Option {
	return func(opts *Options) {
		opts.MaxLogCountPerBatch = maxLogCountPerBatch
	}
}

func WithGoPoolSize(goPoolSize int) Option {
	return func(opts *Options) {
		opts.GoPoolSize = goPoolSize
	}
}

func WithKey(key string) Option {
	return func(opts *Options) {
		opts.Key = key
	}
}

func WithProposeTimeout(proposeTimeout time.Duration) Option {
	return func(opts *Options) {
		opts.ProposeTimeout = proposeTimeout
	}
}

func WithLearnerToLeaderMinLogGap(gap uint64) Option {
	return func(opts *Options) {
		opts.LearnerToLeaderMinLogGap = gap
	}
}

func WithLearnerToFollowerMinLogGap(gap uint64) Option {
	return func(opts *Options) {
		opts.LearnerToFollowerMinLogGap = gap
	}
}

func WithFollowerToLeaderMinLogGap(gap uint64) Option {
	return func(opts *Options) {
		opts.FollowerToLeaderMinLogGap = gap
	}
}

func WithSuspendAfterEmptySyncTick(suspendAfterEmptySyncTick int) Option {
	return func(opts *Options) {
		opts.SuspendAfterEmptySyncTick = suspendAfterEmptySyncTick
	}
}

func WithDestoryAfterIdleTick(destoryAfterIdleTick int) Option {
	return func(opts *Options) {
		opts.DestoryAfterIdleTick = destoryAfterIdleTick
	}
}

func WithAutoSuspend(autoSuspend bool) Option {
	return func(opts *Options) {
		opts.AutoSuspend = autoSuspend
	}
}

func WithAutoDestory(autoDestory bool) Option {

	return func(opts *Options) {
		opts.AutoDestory = autoDestory
	}
}
