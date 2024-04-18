package reactor

import "time"

type Options struct {
	SubReactorNum uint32
	TickInterval  time.Duration // 每次tick间隔
	NodeId        uint64
	Send          func(m Message) // 发送消息

	// MaxReceiveQueueSize is the maximum size in bytes of each receive queue.
	// Once the maximum size is reached, further replication messages will be
	// dropped to restrict memory usage. When set to 0, it means the queue size
	// is unlimited.
	MaxReceiveQueueSize uint64

	// ReceiveQueueLength 接收队列的长度。
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
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		SubReactorNum:           16,
		TickInterval:            time.Millisecond * 100,
		ReceiveQueueLength:      1024,
		LazyFreeCycle:           1,
		InitialTaskQueueCap:     100,
		TaskPoolSize:            10000,
		MaxProposeLogCount:      1000,
		EnableLazyCatchUp:       false,
		IsCommittedAfterApplied: false,
	}

	for _, o := range opt {
		o(opts)
	}

	return opts
}

type Option func(*Options)

func WithSubReactorNum(num uint32) Option {
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
