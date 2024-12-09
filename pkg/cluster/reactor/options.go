package reactor

import "time"

type ReactorType int

const (
	ReactorTypeNormal ReactorType = iota
	ReactorTypeSlot
	ReactorTypeChannel
	ReactorTypeConfig
)

func (r ReactorType) String() string {
	switch r {
	case ReactorTypeNormal:
		return "normal"
	case ReactorTypeSlot:
		return "slot"
	case ReactorTypeChannel:
		return "channel"
	case ReactorTypeConfig:
		return "config"
	default:
		return "unknown"
	}
}

type Options struct {
	SubReactorNum   int
	ProcessPoolSize int           // process的协程池大小
	TickInterval    time.Duration // 每次tick间隔
	NodeId          uint64
	Send            func(m Message) // 发送消息
	ReactorType     ReactorType     // reactor类型

	// MaxReceiveQueueSize is the maximum size in bytes of each receive queue.
	// Once the maximum size is reached, further replication messages will be
	// dropped to restrict memory usage. When set to 0, it means the queue size
	// is unlimited.
	MaxReceiveQueueSize uint64

	// ReceiveQueueLength 处理者接收队列的长度。
	ReceiveQueueLength uint64

	// LazyFreeCycle defines how often should entry queue and message queue
	// to be freed.
	LazyFreeCycle uint64

	InitialTaskQueueCap int

	// 执行任务的协程池大小
	TaskPoolSize int

	// MaxProposeLogCount 每次Propose最大日志数量
	MaxProposeLogCount int

	// EnableLazyCatchUp 延迟捕捉日志开关
	EnableLazyCatchUp bool

	// IsCommittedAfterApplied 是否在状态机应用日志后才视为提交, 如果为false 则多数节点追加日志后即视为提交
	IsCommittedAfterApplied bool
	AutoSlowDownOn          bool // 是否开启自动降速

	// LeaderTimeoutMaxTick 领导者最大超时tick数，超过这个tick数认为领导者已经丢失
	LeaderTimeoutMaxTick int

	// SlowdownCheckIntervalTick 检查是否需要降速的间隔tick数
	SlowdownCheckIntervalTick int

	// AppendLogWorkerNum 处理追加日志的协程数量 (如果太大会导致大量协程去写db，导致db性能下降，如果太小阻塞追加日志的速度，默认是10)
	AppendLogWorkerNum int

	Event struct {
		// OnHandlerRemove handler被移除事件
		OnHandlerRemove func(h IHandler)
	}

	// ProposeTimeout 提案超时
	ProposeTimeout time.Duration

	Request IRequest

	// SyncTimeoutMaxTick 同步超时最大tick次数
	SyncTimeoutMaxTick int
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		SubReactorNum:             256,
		TickInterval:              time.Millisecond * 150,
		ReceiveQueueLength:        128,
		LazyFreeCycle:             1,
		InitialTaskQueueCap:       100,
		TaskPoolSize:              100000,
		MaxProposeLogCount:        1000,
		EnableLazyCatchUp:         true,
		IsCommittedAfterApplied:   false,
		AutoSlowDownOn:            false,
		LeaderTimeoutMaxTick:      25,
		AppendLogWorkerNum:        2,
		ProposeTimeout:            time.Second * 30,
		SlowdownCheckIntervalTick: 10,
		SyncTimeoutMaxTick:        10,
		ProcessPoolSize:           4096,
	}

	for _, o := range opt {
		o(opts)
	}

	return opts
}

type Option func(*Options)

func WithSubReactorNum(num int) Option {
	return func(o *Options) {
		o.SubReactorNum = num
	}
}

func WithTickInterval(d time.Duration) Option {
	return func(o *Options) {
		o.TickInterval = d
	}
}

func WithNodeId(id uint64) Option {
	return func(o *Options) {
		o.NodeId = id
	}
}

func WithSend(f func(m Message)) Option {
	return func(o *Options) {
		o.Send = f
	}
}

func WithMaxReceiveQueueSize(size uint64) Option {
	return func(o *Options) {
		o.MaxReceiveQueueSize = size
	}
}

func WithReceiveQueueLength(length uint64) Option {
	return func(o *Options) {
		o.ReceiveQueueLength = length
	}
}

func WithLazyFreeCycle(cycle uint64) Option {
	return func(o *Options) {
		o.LazyFreeCycle = cycle
	}
}

func WithInitialTaskQueueCap(cap int) Option {
	return func(o *Options) {
		o.InitialTaskQueueCap = cap
	}
}

func WithTaskPoolSize(size int) Option {
	return func(o *Options) {
		o.TaskPoolSize = size
	}
}

func WithMaxProposeLogCount(count int) Option {
	return func(o *Options) {
		o.MaxProposeLogCount = count
	}
}

func WithEnableLazyCatchUp(enable bool) Option {
	return func(o *Options) {
		o.EnableLazyCatchUp = enable
	}
}

func WithIsCommittedAfterApplied(isCommittedAfterApplied bool) Option {
	return func(o *Options) {
		o.IsCommittedAfterApplied = isCommittedAfterApplied
	}
}

func WithReactorType(reactorType ReactorType) Option {
	return func(o *Options) {
		o.ReactorType = reactorType
	}
}

func WithAutoSlowDownOn(v bool) Option {
	return func(o *Options) {
		o.AutoSlowDownOn = v
	}
}

func WithLeaderTimeoutMaxTick(tick int) Option {
	return func(o *Options) {
		o.LeaderTimeoutMaxTick = tick
	}
}

func WithOnHandlerRemove(f func(h IHandler)) Option {
	return func(o *Options) {
		o.Event.OnHandlerRemove = f
	}
}

func WithRequest(req IRequest) Option {
	return func(o *Options) {
		o.Request = req
	}
}

func WithProposeTimeout(d time.Duration) Option {
	return func(o *Options) {
		o.ProposeTimeout = d
	}
}

func WithAppendLogWorkerNum(num int) Option {
	return func(o *Options) {
		o.AppendLogWorkerNum = num
	}
}

func WithSlowdownCheckIntervalTick(tick int) Option {
	return func(o *Options) {
		o.SlowdownCheckIntervalTick = tick
	}
}

func WithSyncTimeoutMaxTick(tick int) Option {
	return func(o *Options) {
		o.SyncTimeoutMaxTick = tick
	}
}
