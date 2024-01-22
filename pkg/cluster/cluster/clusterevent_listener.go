package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
)

type ClusterEventListener struct {
	clusterconfigServer *clusterconfig.Server // 分布式配置服务

}

func NewClusterEventListener() *ClusterEventListener {
	return &ClusterEventListener{}
}

func (c *ClusterEventListener) Wait() ClusterEvent {
	return ClusterEvent{}
}

type ClusterEventType int

const (
	ClusterEventTypeNone        ClusterEventType = iota
	ClusterEventTypeNodeInit                     // 节点初始化
	ClusterEventTypeNodeAdd                      // 节点添加
	ClusterEventTypeNodeRemove                   // 节点移除
	ClusterEventTypeNodeUpdate                   // 节点更新
	ClusterEventTypeSlotInit                     // 槽初始化
	ClusterEventTypeSlotAdd                      // 槽添加
	ClusterEventTypeSlotMigrate                  // 槽迁移
)

type ClusterEvent struct {
	Type        ClusterEventType
	Nodes       []clusterconfig.NodeInfo
	Slots       []clusterconfig.SlotInfo
	SlotMigrate SlotMigrate
}

type SlotMigrate struct {
	SlotId uint32
	From   uint64
	To     uint64
}
