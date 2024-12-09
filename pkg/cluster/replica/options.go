package replica

type Options struct {
	NodeId    uint64 // 当前节点ID
	Storage   IStorage
	LogPrefix string // 日志前缀

	AppliedIndex uint64 // 已应用的日志下标

	LastIndex uint64 // 最新日志下标
	LastTerm  uint32 // 最新任期

	ElectionOn            bool // 是否开启选举
	ElectionIntervalTick  int  // 选举间隔tick次数，超过此tick数则发起选举
	HeartbeatIntervalTick int  // 心跳间隔tick次数, 就是tick触发几次算一次心跳，一般为1 一次tick算一次心跳
	SyncIntervalTick      int  // 同步间隔tick次数, 超过此tick数则发起同步

	MaxUncommittedLogSize      uint64  // 最大未提交的日志大小
	SyncLimitSize              uint64  // 每次同步日志数据的最大大小（过小影响吞吐量，过大导致消息阻塞，默认为10M）
	AckMode                    AckMode // AckMode
	AutoRoleSwith              bool    // 运行自动角色切换
	LearnerToFollowerMinLogGap uint64  // 学习者转换为跟随者的最小日志差距，需要AutoRoleSwith开启 (当学习者的日志与领导者的日志差距小于这个配置时，学习者会转换为跟随者)
	LearnerToLeaderMinLogGap   uint64  // 学习者转换为领导者的最小日志差距，需要AutoRoleSwith开启 (当学习者的日志与领导者的日志差距小于这个配置时，领导将停止接受任何提案，直到学习者完全追上领导者，然后再发起转换请求)
	LearnerToTimeoutTick       int     // 学习者转换为跟随者的超时tick次数，当超过这个次数将重新发起转换请求

	FollowerToLeaderMinLogGap uint64 // 跟随者转换为领导者的最小日志差距，需要AutoRoleSwith开启 (当跟随者的日志与领导者的日志差距小于这个配置时，跟随者会转换为领导者)

	RequestTimeoutTick int // 请求超时tick数

	RetryTick       int // 重试tick数, 超过此tick数则重试
	SyncTimeoutTick int // 同步超时的tick数
}

func NewOptions() *Options {
	return &Options{
		ElectionOn:                 false,
		ElectionIntervalTick:       10,
		SyncLimitSize:              1024 * 1024 * 10, // 10M
		HeartbeatIntervalTick:      1,
		SyncIntervalTick:           1,
		MaxUncommittedLogSize:      1024 * 1024 * 1024,
		AckMode:                    AckModeMajority,
		AutoRoleSwith:              false,
		LearnerToFollowerMinLogGap: 100,
		LearnerToLeaderMinLogGap:   100,
		FollowerToLeaderMinLogGap:  100,
		LearnerToTimeoutTick:       10,
		RequestTimeoutTick:         10,
		RetryTick:                  40,
		SyncTimeoutTick:            10,
	}
}

// AckMode AckMode 许多指定节点确认，后才算提交
type AckMode int

const (
	// AckModeNone 不需要其他节点确认，只需要本节点确认
	AckModeNone AckMode = iota
	// AckModeMajority 大多数节点确认
	AckModeMajority
	// AckModeAll 所有节点确认
	AckModeAll
)

type Option func(o *Options)

func WithSyncIntervalTick(tick int) Option {
	return func(o *Options) {
		o.SyncIntervalTick = tick
	}
}

func WithElectionOn(v bool) Option {

	return func(o *Options) {
		o.ElectionOn = v
	}
}

func WithElectionIntervalTick(tick int) Option {
	return func(o *Options) {
		o.ElectionIntervalTick = tick
	}
}

func WithHeartbeatIntervalTick(tick int) Option {
	return func(o *Options) {
		o.HeartbeatIntervalTick = tick
	}
}

func WithSyncLimitSize(size uint64) Option {
	return func(o *Options) {
		o.SyncLimitSize = size
	}
}

func WithMaxUncommittedLogSize(size uint64) Option {
	return func(o *Options) {
		o.MaxUncommittedLogSize = size
	}
}

func WithAckMode(mode AckMode) Option {
	return func(o *Options) {
		o.AckMode = mode
	}
}

func WithAutoRoleSwith(v bool) Option {
	return func(o *Options) {
		o.AutoRoleSwith = v
	}
}

func WithLearnerToFollowerMinLogGap(gap uint64) Option {
	return func(o *Options) {
		o.LearnerToFollowerMinLogGap = gap
	}
}

func WithNodeId(nodeId uint64) Option {
	return func(o *Options) {
		o.NodeId = nodeId
	}
}

func WithLogPrefix(prefix string) Option {
	return func(o *Options) {
		o.LogPrefix = prefix
	}
}

func WithAppliedIndex(index uint64) Option {
	return func(o *Options) {
		o.AppliedIndex = index
	}
}

func WithLastIndex(index uint64) Option {
	return func(o *Options) {
		o.LastIndex = index
	}
}

func WithLastTerm(term uint32) Option {
	return func(o *Options) {
		o.LastTerm = term
	}
}

func WithStorage(storage IStorage) Option {
	return func(o *Options) {
		o.Storage = storage
	}
}

func WithLearnerToLeaderMinLogGap(gap uint64) Option {
	return func(o *Options) {
		o.LearnerToLeaderMinLogGap = gap
	}
}

func WithLearnerToTimeoutTick(tick int) Option {
	return func(o *Options) {
		o.LearnerToTimeoutTick = tick
	}
}

func WithFollowerToLeaderMinLogGap(gap uint64) Option {
	return func(o *Options) {
		o.FollowerToLeaderMinLogGap = gap
	}
}

func WithRequestTimeoutTick(tick int) Option {
	return func(o *Options) {
		o.RequestTimeoutTick = tick
	}
}

func WithRetryTick(tick int) Option {
	return func(o *Options) {
		o.RetryTick = tick
	}
}

func WithSyncTimeoutTick(tick int) Option {
	return func(o *Options) {
		o.SyncTimeoutTick = tick
	}
}
