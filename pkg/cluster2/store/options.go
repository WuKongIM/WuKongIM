package store

import "github.com/WuKongIM/WuKongIM/pkg/cluster2/icluster"

type Options struct {
	NodeId       uint64 // 节点ID
	ShardNum     int    // 分片数量
	MemTableSize int    // MemTable大小
	DataDir      string // 数据目录
	SlotCount    uint32

	Slot icluster.Slot

	Channel icluster.Channel
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		ShardNum:     8,
		MemTableSize: 16 * 1024 * 1024,
		DataDir:      "./data",
		SlotCount:    64,
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

func WithShardNum(shardNum int) Option {
	return func(o *Options) {
		o.ShardNum = shardNum
	}
}

func WithMemTableSize(memTableSize int) Option {
	return func(o *Options) {
		o.MemTableSize = memTableSize
	}
}

func WithDataDir(dataDir string) Option {
	return func(o *Options) {
		o.DataDir = dataDir
	}
}

func WithSlotCount(slotCount uint32) Option {
	return func(o *Options) {
		o.SlotCount = slotCount
	}
}

func WithSlot(slot icluster.Slot) Option {
	return func(o *Options) {
		o.Slot = slot
	}
}
