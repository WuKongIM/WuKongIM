package logic

import (
	"path"
	"time"
)

type Options struct {
	ClusterStorePath string
	RootDir          string
	DataDir          string // 数据目录
	Cluster          struct {
		NodeID     uint64        // 节点ID
		Addr       string        // 节点地址 例如：tcp://0.0.0.0:11110
		ReqTimeout time.Duration // 请求超时时间
		Join       []string      // 加入集群的地址
	}
}

func NewOptions() *Options {
	rootDir := "logicdata"

	return &Options{
		RootDir:          rootDir,
		ClusterStorePath: path.Join(rootDir, "clusterconfig"),
		Cluster: struct {
			NodeID     uint64
			Addr       string
			ReqTimeout time.Duration
			Join       []string
		}{
			Addr:       "tcp://127.0.0.1:11110",
			ReqTimeout: time.Second * 10,
		},
	}
}

func (o *Options) ClusterOn() bool {
	return o.Cluster.NodeID != 0
}

type Option func(opts *Options)

func WithAddr(addr string) Option {
	return func(opts *Options) {
		opts.Cluster.Addr = addr
	}
}
