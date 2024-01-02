package replica

import "time"

type Options struct {
	NodeID            uint64     // 当前节点ID
	ShardNo           string     // 分区编号
	DataDir           string     // 数据目录
	Replicas          []uint64   // 副本节点ID集合
	Transport         ITransport // 传输协议
	SyncLimit         uint32
	SyncCheckInterval time.Duration // 同步检测间隔，在此时间内会检查各个副本是否已经同步到最新
}

func NewOptions() *Options {
	return &Options{
		SyncLimit:         20,
		SyncCheckInterval: time.Second * 1,
	}
}

type Option func(o *Options)

func WithNodeID(nodeID uint64) Option {

	return func(o *Options) {
		o.NodeID = nodeID
	}
}

func WithDataDir(dataDir string) Option {
	return func(o *Options) {
		o.DataDir = dataDir
	}
}

func WithReplicas(replicas []uint64) Option {

	return func(o *Options) {
		o.Replicas = replicas
	}
}

func WithTransport(t ITransport) Option {
	return func(o *Options) {
		o.Transport = newProxyTransport(t)
	}
}
