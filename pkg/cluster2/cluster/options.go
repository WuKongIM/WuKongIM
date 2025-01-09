package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/raft/raftgroup"
)

type Options struct {
	DataDir string

	// 分布式配置
	ConfigOptions *clusterconfig.Options

	// slot数据传输层
	SlotTransport raftgroup.ITransport

	// channel数据传输层
	ChannelTransport raftgroup.ITransport
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

type Option func(*Options)

func WithDataDir(dataDir string) Option {
	return func(o *Options) {
		o.DataDir = dataDir
	}
}

func WithConfigOptions(configOptions *clusterconfig.Options) Option {
	return func(o *Options) {
		o.ConfigOptions = configOptions
	}
}

func WithSlotTransport(transport raftgroup.ITransport) Option {
	return func(o *Options) {
		o.SlotTransport = transport
	}
}
