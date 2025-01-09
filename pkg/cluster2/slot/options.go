package slot

import "github.com/WuKongIM/WuKongIM/pkg/raft/raftgroup"

type Options struct {
	// 节点Id
	NodeId uint64
	// 数据目录
	DataDir string
	// 槽位数据库分片数量
	SlotDbShardNum int

	// 传输层
	Transport raftgroup.ITransport
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
