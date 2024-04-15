package clusterevent

type Options struct {
	NodeId              uint64
	InitNodes           map[uint64]string
	SlotCount           uint32 // 槽位数量
	SlotMaxReplicaCount uint32 // 每个槽位最大副本数量
	ConfigDir           string
	Ready               func(msgs []Message)
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		SlotCount:           128,
		SlotMaxReplicaCount: 3,
		ConfigDir:           "clusterconfig",
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

type Option func(opts *Options)

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

func WithReady(f func(msgs []Message)) Option {

	return func(o *Options) {
		o.Ready = f
	}
}
