package clusterstore

import "github.com/WuKongIM/WuKongIM/pkg/wkstore"

type Options struct {
	NodeID    uint64   // 节点ID
	Cluster   ICluster // 集群服务接口
	DataDir   string   // 数据目录
	SlotCount uint32   // 槽数量

	DecodeMessageFnc func(msg []byte) (wkstore.Message, error)
}

func NewOptions(nodeID uint64, opts ...Option) *Options {
	opt := newOptions()
	opt.NodeID = nodeID
	for _, o := range opts {
		o(opt)
	}
	return opt
}

func newOptions() *Options {
	return &Options{
		SlotCount: 256,
	}
}

type Option func(*Options)

func WithCluster(cluster ICluster) Option {
	return func(o *Options) {
		o.Cluster = cluster
	}
}

func WithDataDir(dir string) Option {
	return func(o *Options) {
		o.DataDir = dir
	}
}
