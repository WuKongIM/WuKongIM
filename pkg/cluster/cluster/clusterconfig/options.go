package clusterconfig

type Options struct {
	NodeId               uint64
	ConfigPath           string   // 集群配置文件路径
	ConfigVersion        uint64   // 配置版本
	ElectionTimeoutTick  int      // 选举超时tick次数
	HeartbeatTimeoutTick int      // 心跳超时tick次数
	Replicas             []uint64 // 副本列表 (必须包含自己本身的id)
	GetConfigData        func() ([]byte, error)
}

func NewOptions() *Options {
	return &Options{
		ConfigPath:           "clusterconfig.json",
		ElectionTimeoutTick:  10,
		HeartbeatTimeoutTick: 1,
	}
}

type Option func(opts *Options)

func WithNodeId(nodeId uint64) Option {
	return func(opts *Options) {
		opts.NodeId = nodeId
	}
}
