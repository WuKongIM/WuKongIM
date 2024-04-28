package wkdb

type Options struct {
	NodeId            uint64
	DataDir           string
	ConversationLimit int // 最近会话查询数量限制
	SlotCount         int // 槽位数量
	// 耗时配置开启
	EnableCost bool
}

func NewOptions(opt ...Option) *Options {
	o := &Options{
		DataDir:           "./data",
		ConversationLimit: 10000,
		SlotCount:         128,
		EnableCost:        false,
	}
	for _, f := range opt {
		f(o)
	}
	return o
}

type Option func(*Options)

func WithDir(dir string) Option {
	return func(o *Options) {
		o.DataDir = dir
	}
}

func WithNodeId(nodeId uint64) Option {
	return func(o *Options) {
		o.NodeId = nodeId
	}
}

func WithSlotCount(slotCount int) Option {
	return func(o *Options) {
		o.SlotCount = slotCount
	}
}
