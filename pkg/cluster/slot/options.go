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

	// OnApply 应用日志回调
	OnApply func(slotId uint32, logs []types.Log) error

	// OnSaveConfig 保存槽配置
	OnSaveConfig func(slotId uint32, cfg types.Config) error
}

func NewOptions(opt ...Option) *Options {
	defaultOpts := &Options{
		DataDir:        "clusterdata",
		SlotDbShardNum: 8,
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
