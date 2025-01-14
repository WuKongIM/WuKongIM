package clusterconfig

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/raft"
)

type Options struct {
	// NodeId 节点id
	NodeId uint64

	ApiServerAddr string        // 分布式api服务地址（内网地址）
	TickInterval  time.Duration // 分布式tick间隔
	// InitNodes 初始化节点列表 key为节点id，value为分布式通讯的地址
	InitNodes              map[uint64]string
	SlotCount              uint32 // 槽位数量
	SlotMaxReplicaCount    uint32 // 每个槽位最大副本数量
	ChannelMaxReplicaCount uint32 // 每个频道最大副本数量
	ConfigPath             string // 集群配置文件路径
	// Transport 传输层
	Transport raft.Transport

	PongMaxTick int // pongMaxTick 节点超过多少tick没有回应心跳就认为是掉线

	// Seed  种子节点，可以引导新节点加入集群  格式：nodeId@ip:port （nodeId为种子节点的nodeId）
	Seed string
}

func NewOptions(opts ...Option) *Options {
	o := &Options{
		SlotCount:              64,
		TickInterval:           time.Millisecond * 100,
		SlotMaxReplicaCount:    3,
		ChannelMaxReplicaCount: 3,
		ConfigPath:             "clusterconfig.json",
		PongMaxTick:            30,
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
func WithTransport(transport raft.Transport) Option {
	return func(o *Options) {
		o.Transport = transport
	}
}

func WithApiServerAddr(apiServerAddr string) Option {
	return func(o *Options) {
		o.ApiServerAddr = apiServerAddr
	}
}

func WithPongMaxTick(pongMaxTick int) Option {
	return func(o *Options) {
		o.PongMaxTick = pongMaxTick
	}
}

func WithSeed(seed string) Option {
	return func(o *Options) {
		o.Seed = seed
	}
}
