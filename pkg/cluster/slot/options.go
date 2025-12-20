package slot

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/icluster"
	"github.com/WuKongIM/WuKongIM/pkg/raft/raftgroup"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
)

type Options struct {
	// 节点Id
	NodeId uint64
	// 数据目录
	DataDir string
	// 槽位数据库分片数量
	SlotDbShardNum int
	// 节点接口
	Node icluster.Node
	// 传输层
	Transport raftgroup.ITransport
	// 槽数量
	SlotCount uint32
	// api接口
	RPC icluster.RPC
	// OnApply 应用日志回调
	OnApply func(slotId uint32, logs []types.Log) error

	// OnSaveConfig 保存槽配置
	OnSaveConfig func(slotId uint32, cfg types.Config) error

	// Raft 日志保留策略
	RaftLogRetention RaftLogRetentionConfig
}

// RaftLogRetentionConfig Raft 日志保留配置
type RaftLogRetentionConfig struct {
	// KeepAppliedLogs 保留最近 N 条已应用的日志（0 表示不限制）
	KeepAppliedLogs uint64
	// KeepHours 保留最近 N 小时的日志（0 表示不限制）
	KeepHours int
	// MinKeepLogs 最小保留日志数，即使超过时间也保留
	MinKeepLogs uint64
}

func NewOptions(opt ...Option) *Options {
	defaultOpts := &Options{
		DataDir:        "clusterdata",
		SlotDbShardNum: 8,
		// 默认保留 10万 条已应用的日志，或保留 48 小时，最少保留 1 万条
		RaftLogRetention: RaftLogRetentionConfig{
			KeepAppliedLogs: 100000,
			KeepHours:       48,
			MinKeepLogs:     10000,
		},
	}
	for _, o := range opt {
		o(defaultOpts)
	}

	return defaultOpts
}

type Option func(*Options)

func WithNodeId(nodeId uint64) Option {
	return func(o *Options) {
		o.NodeId = nodeId
	}
}

func WithDataDir(dataDir string) Option {
	return func(o *Options) {
		o.DataDir = dataDir
	}
}

func WithSlotDbShardNum(slotDbShardNum int) Option {
	return func(o *Options) {
		o.SlotDbShardNum = slotDbShardNum
	}
}

func WithTransport(transport raftgroup.ITransport) Option {
	return func(o *Options) {
		o.Transport = transport
	}
}

func WithNode(node icluster.Node) Option {
	return func(o *Options) {
		o.Node = node
	}
}

func WithSlotCount(slotCount uint32) Option {
	return func(o *Options) {
		o.SlotCount = slotCount
	}
}

func WithOnApply(onApply func(slotId uint32, logs []types.Log) error) Option {
	return func(o *Options) {
		o.OnApply = onApply
	}
}

func WithOnSaveConfig(onSaveConfig func(slotId uint32, cfg types.Config) error) Option {
	return func(o *Options) {
		o.OnSaveConfig = onSaveConfig
	}
}
func WithRPC(rpc icluster.RPC) Option {
	return func(o *Options) {
		o.RPC = rpc
	}
}

func WithRaftLogRetention(config RaftLogRetentionConfig) Option {
	return func(o *Options) {
		o.RaftLogRetention = config
	}
}
