package node

type Options struct {
	// NodeId 节点id
	NodeId uint64
	// InitNodes 初始化节点列表 key为节点id，value为分布式通讯的地址
	InitNodes map[uint64]string
}

func NewOptions(opts ...Option) *Options {
	o := &Options{}
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
