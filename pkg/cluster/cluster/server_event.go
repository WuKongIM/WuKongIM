package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
)

func (s *Server) handleClusterEvent(event ClusterEvent) {
	switch event.Type {
	case ClusterEventTypeNodeInit: // 处理节点初始化事件
		s.handleNodeInitEvent(event)
	case ClusterEventTypeNodeAdd: // 处理节点添加事件
		s.handleNodeAddEvent(event)
	case ClusterEventTypeSlotInit: // 槽初始化
		s.handleSlotInitEvent(event)
	}

}

// 节点初始化
func (s *Server) handleNodeInitEvent(event ClusterEvent) {
	if len(event.Nodes) > 0 {
		return
	}
	s.addNodes(event.Nodes)
}

func (s *Server) handleNodeAddEvent(event ClusterEvent) {
	if len(event.Nodes) > 0 {
		return
	}
	s.addNodes(event.Nodes)
}

func (s *Server) handleSlotInitEvent(event ClusterEvent) {
	if len(event.Slots) > 0 {
		return
	}
	s.addSlots(event.Slots)
}

func (s *Server) newNodeByNodeInfo(nd clusterconfig.NodeInfo) *Node {

	return &Node{}
}

func (s *Server) addNodes(nodes []clusterconfig.NodeInfo) {
	for _, node := range nodes {
		if s.nodeGroupManager.Exist(node.Id) {
			continue
		}
		s.nodeGroupManager.AddNode(s.newNodeByNodeInfo(node))
	}
}

func (s *Server) addSlots(slots []clusterconfig.SlotInfo) {
	for _, slot := range slots {
		if s.slotGroupManager.Exist(slot.Id) {
			continue
		}
		s.slotGroupManager.AddSlot(s.newSlotBySlotInfo(slot))
	}
}

func (s *Server) newSlotBySlotInfo(slot clusterconfig.SlotInfo) *Slot {
	return &Slot{}
}
