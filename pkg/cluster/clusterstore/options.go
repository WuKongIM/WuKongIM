package clusterstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/icluster"
)

type Options struct {
	NodeID    uint64           // 节点ID
	Cluster   icluster.Propose // 集群服务接口
	DataDir   string           // 数据目录
	SlotCount uint32           // 槽数量

	GetSlotId func(uid string) uint32
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
		SlotCount: 128,
	}
}

type Option func(*Options)

func WithCluster(cluster icluster.Propose) Option {
	return func(o *Options) {
		o.Cluster = cluster
	}
}

func WithDataDir(dir string) Option {
	return func(o *Options) {
		o.DataDir = dir
	}
}

func WithSlotCount(count uint32) Option {
	return func(o *Options) {
		o.SlotCount = count
	}
}
