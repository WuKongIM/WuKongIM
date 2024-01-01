package clusterevent

import "time"

type Options struct {
	NodeID            uint64
	InitNodes         map[uint64]string // 初始节点 例如： key为节点ID value为 ip:port
	ClusterConfigName string            // 分布式配置文件名字
	SlotCount         uint32            // 槽数量
	SlotReplicaCount  uint32            // 槽复制数量
	DataDir           string            // 数据存储目录
	Heartbeat         time.Duration     // 心跳
}

func NewOptions() *Options {
	return &Options{
		InitNodes:         make(map[uint64]string),
		SlotCount:         256,
		ClusterConfigName: "clusterconfig.json",
		SlotReplicaCount:  3,
		Heartbeat:         time.Millisecond * 1000,
	}
}
