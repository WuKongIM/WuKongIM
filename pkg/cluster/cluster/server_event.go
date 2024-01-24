package cluster

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"go.uber.org/zap"
)

func (s *Server) handleClusterEvent(event ClusterEvent) {
	if len(event.Messages) == 0 {
		return
	}

	var err error
	for _, msg := range event.Messages {
		switch msg.Type {
		case ClusterEventTypeNodeAdd: // 处理节点添加事件
			err = s.handleNodeAddEvent(msg)
		case ClusterEventTypeNodeRemove: // 处理节点删除事件
			err = s.handleNodeRemoveEvent(msg)
		case ClusterEventTypeSlotAdd: // 槽添加
			err = s.handleSlotAddEvent(msg)
		}
		if err != nil {
			s.Error("handle cluster event error", zap.Error(err))
			return
		}
	}
	s.clusterEventListener.advance() // 继续推进

}

func (s *Server) handleNodeAddEvent(event EventMessage) error {
	if len(event.Nodes) == 0 {
		return nil
	}
	s.addNodes(event.Nodes)
	return nil
}

func (s *Server) handleNodeRemoveEvent(event EventMessage) error {
	if len(event.Nodes) == 0 {
		return nil
	}
	for _, node := range event.Nodes {
		n := s.nodeGroupManager.node(node.Id)
		if n != nil {
			s.nodeGroupManager.removeNode(n.id)
			n.stop()
		}
	}
	return nil
}

func (s *Server) handleSlotAddEvent(event EventMessage) error {
	if len(event.Slots) == 0 {
		return nil
	}
	s.addSlots(event.Slots)
	return nil
}

func (s *Server) newNodeByNodeInfo(nd *pb.Node) *node {
	n := newNode(nd.Id, s.serverUid(nd.Id), nd.ClusterAddr)
	n.start()
	return n
}

func (s *Server) serverUid(id uint64) string {
	return fmt.Sprintf("%d", id)
}

func (s *Server) addNodes(nodes []*pb.Node) {
	for _, node := range nodes {
		if s.nodeGroupManager.exist(node.Id) {
			continue
		}
		s.nodeGroupManager.addNode(s.newNodeByNodeInfo(node))
	}
}

func (s *Server) addSlots(slots []*pb.Slot) {
	for _, slot := range slots {
		if s.slotGroupManager.exist(slot.Id) {
			continue
		}
		s.slotGroupManager.addSlot(s.newSlotBySlotInfo(slot))
	}
}

func (s *Server) newSlotBySlotInfo(st *pb.Slot) *slot {
	ns := newSlot(st.Id, 0, st.Replicas, s.opts)
	return ns
}
