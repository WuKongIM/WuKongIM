package clusterconfig

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/icluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
)

type Options struct {
	NodeId                 uint64
	ConfigPath             string                  // 集群配置文件路径
	ElectionTimeoutTick    int                     // 选举超时tick次数
	HeartbeatTimeoutTick   int                     // 心跳超时tick次数
	InitNodes              map[uint64]string       // 初始化节点列表 key为节点id，value为分布式通讯的地址
	ProposeTimeout         time.Duration           // 提议超时时间
	ReqTimeout             time.Duration           // 请求超时时间
	SlotCount              uint32                  // 槽位数量
	SlotMaxReplicaCount    uint32                  // 每个槽位最大副本数量
	ChannelMaxReplicaCount uint32                  // 每个频道最大副本数量
	Role                   pb.NodeRole             // 节点角色
	MessageSendInterval    time.Duration           // 消息发送间隔
	MaxIdleInterval        time.Duration           // 最大空闲间隔
	Send                   func(m reactor.Message) // 发送消息

	Cluster icluster.Cluster // 分布式接口

	Event struct {
		OnAppliedConfig func()
	}
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		ConfigPath:             "clusterconfig.json",
		SlotCount:              128,
		ElectionTimeoutTick:    10,
		HeartbeatTimeoutTick:   1,
		MaxIdleInterval:        time.Second * 1,
		ProposeTimeout:         time.Second * 5,
		ReqTimeout:             time.Second * 5,
		SlotMaxReplicaCount:    3,
		ChannelMaxReplicaCount: 3,
		Event: struct {
			OnAppliedConfig func()
		}{
			OnAppliedConfig: func() {

			},
		},
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

type Option func(*Options)

func WithNodeId(nodeId uint64) Option {
	return func(o *Options) {
		o.NodeId = nodeId
	}
}

func WithConfigPath(configPath string) Option {
	return func(o *Options) {
		o.ConfigPath = configPath
	}
}

func WithElectionTimeoutTick(electionTimeoutTick int) Option {
	return func(o *Options) {
		o.ElectionTimeoutTick = electionTimeoutTick
	}
}

func WithHeartbeatTimeoutTick(heartbeatTimeoutTick int) Option {
	return func(o *Options) {
		o.HeartbeatTimeoutTick = heartbeatTimeoutTick
	}
}

func WithInitNodes(initNodes map[uint64]string) Option {
	return func(o *Options) {
		o.InitNodes = initNodes
	}

}

func WithProposeTimeout(proposeTimeout time.Duration) Option {
	return func(o *Options) {
		o.ProposeTimeout = proposeTimeout
	}
}

func WithSlotCount(slotCount uint32) Option {
	return func(o *Options) {
		o.SlotCount = slotCount
	}
}

func WithRole(role pb.NodeRole) Option {
	return func(o *Options) {
		o.Role = role
	}
}

func WithMessageSendInterval(interval time.Duration) Option {
	return func(o *Options) {
		o.MessageSendInterval = interval
	}
}

func WithMaxIdleInterval(interval time.Duration) Option {
	return func(o *Options) {
		o.MaxIdleInterval = interval
	}
}

func WithSend(send func(m reactor.Message)) Option {
	return func(o *Options) {
		o.Send = send
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

func WithOnAppliedConfig(f func()) Option {
	return func(o *Options) {
		o.Event.OnAppliedConfig = f
	}
}

func WithCluster(cluster icluster.Cluster) Option {
	return func(o *Options) {
		o.Cluster = cluster
	}
}
