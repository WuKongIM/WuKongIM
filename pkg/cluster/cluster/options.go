package cluster

import "time"

type Options struct {
	NodeID                       uint64
	Addr                         string
	InitNodes                    map[uint64]string // 初始化节点列表
	ChannelGroupScanInterval     time.Duration
	ShardLogStorage              IShardLogStorage
	MessageLogStorage            IShardLogStorage // 消息日志存储
	MaxChannelActivitiesPerGroup int              // 每个channelGroup最大处理活动的channel数量
	Transport                    ITransport
	ChannelInactiveTimeout       time.Duration // channel不活跃超时时间, 如果频道超过这个时间没有活跃, 则会被移除，等下次活跃时会重新加入
	SlotInactiveTimeout          time.Duration // slot不活跃超时时间，如果槽超过这个时间没有活跃，则会被移除，等下次活跃时会重新加入
	AdvanceCountOfBatch          int           // 每批次处理Advance的最大数量
	DataDir                      string        // 数据存储目录
	SlotCount                    uint32        // 槽数量
	SlotMaxReplicaCount          uint32        // 每个槽位最大副本数量
	ChannelReplicaCount          uint16        // 每个频道最大副本数量
	ProposeTimeout               time.Duration
	ReqTimeout                   time.Duration
	GetAppliedIndex              func(shardNo string) (uint64, error) // 获取分区已应用的日志索引
	ChannelGroupCount            int                                  // channelGroup数量
}

func NewOptions(optList ...Option) *Options {
	opts := &Options{
		Addr:                         "0.0.0.0:10001",
		ChannelGroupScanInterval:     time.Millisecond * 200,
		SlotInactiveTimeout:          time.Millisecond * 200,
		MaxChannelActivitiesPerGroup: 1000,
		ChannelInactiveTimeout:       time.Hour * 1,
		AdvanceCountOfBatch:          50,
		SlotCount:                    256,
		SlotMaxReplicaCount:          3,
		ProposeTimeout:               time.Second * 5,
		ChannelGroupCount:            128,
		ChannelReplicaCount:          3,
		ReqTimeout:                   time.Second * 3,
	}
	for _, opt := range optList {
		opt(opts)
	}
	return opts
}

func (o *Options) Replicas() []uint64 {
	replicas := make([]uint64, 0, len(o.InitNodes))
	for nodeID := range o.InitNodes {
		replicas = append(replicas, nodeID)
	}
	return replicas
}

type Option func(opts *Options)

func WithNodeID(nodeID uint64) Option {
	return func(opts *Options) {
		opts.NodeID = nodeID
	}
}

func WithAddr(addr string) Option {
	return func(opts *Options) {
		opts.Addr = addr
	}
}

func WithInitNodes(initNodes map[uint64]string) Option {
	return func(opts *Options) {
		opts.InitNodes = initNodes
	}
}

func WithChannelGroupScanInterval(interval time.Duration) Option {
	return func(opts *Options) {
		opts.ChannelGroupScanInterval = interval
	}
}

func WithShardLogStorage(storage IShardLogStorage) Option {
	return func(opts *Options) {
		opts.ShardLogStorage = storage
	}
}

func WithMaxChannelActivitiesPerGroup(count int) Option {
	return func(opts *Options) {
		opts.MaxChannelActivitiesPerGroup = count
	}
}

func WithTransport(transport ITransport) Option {
	return func(opts *Options) {
		opts.Transport = transport
	}
}

func WithChannelInactiveTimeout(timeout time.Duration) Option {
	return func(opts *Options) {
		opts.ChannelInactiveTimeout = timeout
	}
}

func WithSlotInactiveTimeout(timeout time.Duration) Option {
	return func(opts *Options) {
		opts.SlotInactiveTimeout = timeout
	}
}

func WithAdvanceCountOfBatch(count int) Option {
	return func(opts *Options) {
		opts.AdvanceCountOfBatch = count
	}
}

func WithDataDir(dataDir string) Option {
	return func(opts *Options) {
		opts.DataDir = dataDir
	}
}

func WithSlotCount(count uint32) Option {
	return func(opts *Options) {
		opts.SlotCount = count
	}
}

func WithSlotMaxReplicaCount(count uint32) Option {
	return func(opts *Options) {
		opts.SlotMaxReplicaCount = count
	}
}

func WithProposeTimeout(timeout time.Duration) Option {
	return func(opts *Options) {
		opts.ProposeTimeout = timeout
	}
}

func WithGetAppliedIndex(f func(shardNo string) (uint64, error)) Option {
	return func(opts *Options) {
		opts.GetAppliedIndex = f
	}
}
