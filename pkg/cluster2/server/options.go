package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/raft/raftgroup"
)

type Options struct {
	DataDir string

	ConfigOptions *clusterconfig.Options

	SlotTransport raftgroup.ITransport
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
