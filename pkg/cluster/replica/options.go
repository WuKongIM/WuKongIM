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
	NodeId                uint64 // 当前节点ID
	Storage               IStorage
	LogPrefix             string // 日志前缀
	MaxUncommittedLogSize uint64
	AppliedIndex          uint64        // 已应用的日志下标
	SyncLimitSize         uint64        // 每次同步日志数据的最大大小（过小影响吞吐量，过大导致消息阻塞，默认为4M）
	MessageSendInterval   time.Duration // 消息发送间隔
	ActiveReplicaMaxTick  int           // 最大空闲时间
	AckMode               AckMode       // AckMode
	ReplicaMaxCount       int           // 副本最大数量
	ElectionOn            bool          // 是否开启选举
	ElectionTimeoutTick   int           // 选举超时tick次数，超过次tick数则发起选举
	HeartbeatTimeoutTick  int           // 心跳超时tick次数, 就是tick触发几次算一次心跳，一般为1 一次tick算一次心跳
	Config                *Config
	// LastSyncInfoMap       map[uint64]*SyncInfo
	OnConfigChange             func(cfg *Config)
	AutoLearnerToFollower      bool   // 自动将学习者转换为跟随者
	LearnerToFollowerMinLogGap uint64 // 学习者转换为跟随者的最小日志差距，需要AutoLearnerToFollower开启 (当学习者的日志与领导者的日志差距小于这个配置时，学习者会转换为跟随者)
}

func NewOptions() *Options {
	return &Options{
		MaxUncommittedLogSize: 1024 * 1024 * 1024,
		SyncLimitSize:         1024 * 1024 * 20, // 20M
		// LastSyncInfoMap:       map[uint64]*SyncInfo{},
		MessageSendInterval:        time.Millisecond * 150,
		ActiveReplicaMaxTick:       10,
		AckMode:                    AckModeMajority,
		ReplicaMaxCount:            3,
		ElectionOn:                 false,
		ElectionTimeoutTick:        10,
		HeartbeatTimeoutTick:       2,
		LogPrefix:                  "default",
		LearnerToFollowerMinLogGap: 100,
		AutoLearnerToFollower:      false,
	}
}

type Option func(o *Options)

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

func WithMessageSendInterval(interval time.Duration) Option {
	return func(o *Options) {
		o.MessageSendInterval = interval
	}
}

func WithAckMode(ackMode AckMode) Option {
	return func(o *Options) {
		o.AckMode = ackMode
	}
}

func WithReplicaMaxCount(count int) Option {
	return func(o *Options) {
		o.ReplicaMaxCount = count
	}
}

func WithElectionOn(electionOn bool) Option {
	return func(o *Options) {
		o.ElectionOn = electionOn
	}
}

func WithElectionTimeoutTick(tick int) Option {
	return func(o *Options) {
		o.ElectionTimeoutTick = tick
	}
}

func WithHeartbeatTimeoutTick(tick int) Option {
	return func(o *Options) {
		o.HeartbeatTimeoutTick = tick
	}
}

func WithLogPrefix(prefix string) Option {
	return func(o *Options) {
		o.LogPrefix = prefix
	}
}

func WithConfig(config *Config) Option {
	return func(o *Options) {
		o.Config = config
	}
}

func WithOnConfigChange(fn func(cfg *Config)) Option {
	return func(o *Options) {
		o.OnConfigChange = fn
	}
}

func WithAutoLearnerToFollower(auto bool) Option {
	return func(o *Options) {
		o.AutoLearnerToFollower = auto
	}
}

func WithLearnerToFollowerMinLogGap(gap uint64) Option {
	return func(o *Options) {
		o.LearnerToFollowerMinLogGap = gap
	}
}
