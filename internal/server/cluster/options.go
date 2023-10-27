package cluster

import "github.com/WuKongIM/WuKongIM/pkg/multiraft"

type Options struct {
	NodeID       uint64
	Addr         string // 监听地址 例如： tcp://ip:port
	SlotCount    int    // 槽位数量
	ReplicaCount int    // 副本数量(包含主节点)
	DataDir      string
	Join         string // 集群中的其他节点地址 例如： tcp://ip:port
	Peers        []multiraft.Peer
	LeaderChange func(leaderID uint64)
}

func NewOptions() *Options {
	return &Options{
		SlotCount:    256,
		ReplicaCount: 3,
	}
}

type ClusterManagerOptions struct {
	NodeID       uint64                        // 节点ID
	SlotCount    int                           // 槽位数量
	ReplicaCount int                           // 副本数量
	ConfigPath   string                        // 集群配置文件路径
	GetSlotState func(slotID uint32) SlotState // 获取槽位状态
}

func NewClusterManagerOptions() *ClusterManagerOptions {
	return &ClusterManagerOptions{
		SlotCount:    256,
		ReplicaCount: 3,
	}
}
