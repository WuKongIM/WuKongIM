package server

import "time"

type Options struct {
	Cluster struct {
		NodeID     uint64        // 节点ID
		Addr       string        // 节点地址 例如：tcp://0.0.0.0:11110
		ReqTimeout time.Duration // 请求超时时间
		Join       []string      // 加入集群的地址
		SlotCount  uint32        // 节点槽数量
	}
}

func NewOptions() *Options {
	return &Options{
		Cluster: struct {
			NodeID     uint64
			Addr       string
			ReqTimeout time.Duration
			Join       []string
			SlotCount  uint32
		}{
			Addr:       "tcp://127.0.0.1:11110",
			ReqTimeout: time.Second * 10,
			SlotCount:  256,
		},
	}
}
