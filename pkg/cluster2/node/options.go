package node

import (
	"time"
)

type Options struct {
	// NodeId 节点id
	NodeId       uint64
	TickInterval time.Duration // 分布式tick间隔
	// InitNodes 初始化节点列表 key为节点id，value为分布式通讯的地址
	InitNodes              map[uint64]string
	SlotCount              uint32 // 槽位数量
	SlotMaxReplicaCount    uint32 // 每个槽位最大副本数量
	ChannelMaxReplicaCount uint32 // 每个频道最大副本数量

	ConfigPath string // 集群配置文件路径
	// Transport 传输层
	Transport Transport
}

func NewOptions(opts ...Option) *Options {
	o := &Options{
		SlotCount:              64,
		TickInterval:           time.Millisecond * 100,
		SlotMaxReplicaCount:    3,
		ChannelMaxReplicaCount: 3,
		ConfigPath:             "clusterconfig.json",
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

type Option func(*Options)

func WithNodeId(nodeId uint64) Option {
	return func(o *Options) {
		o.NodeId = nodeId
	}
}

func WithInitNodes(initNodes map[uint64]string) Option {
	return func(o *Options) {
		o.InitNodes = initNodes
	}
}

func WithSlotCount(slotCount uint32) Option {
	return func(o *Options) {
		o.SlotCount = slotCount
	}
}

func WithSlotMaxReplicaCount(slotMaxReplicaCount uint32) Option {
	return func(o *Options) {
		o.SlotMaxReplicaCount = slotMaxReplicaCount
	}
}

func WithChannelMaxReplicaCount(channelMaxReplicaCount uint32) Option {
	return func(o *Options) {
		o.ChannelMaxReplicaCount = channelMaxReplicaCount
	}
}

func WithTickInterval(tickInterval time.Duration) Option {
	return func(o *Options) {
		o.TickInterval = tickInterval
	}
}

func WithConfigPath(configPath string) Option {
	return func(o *Options) {
		o.ConfigPath = configPath
	}
}
func WithTransport(transport Transport) Option {
	return func(o *Options) {
		o.Transport = transport
	}
}
