package cluster

import (
	"fmt"
	"strconv"

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
	for _, msg := range event.Messages {
		s.clusterEventListener.step(msg)
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
		n := s.nodeManager.node(node.Id)
		if n != nil {
			s.nodeManager.removeNode(n.id)
			n.stop()
		}
	}
	return nil
}

func (s *Server) handleSlotAddEvent(event EventMessage) error {
	if len(event.Slots) == 0 {
		return nil
	}
	return s.addSlots(event.Slots)
}

func (s *Server) newNodeByNodeInfo(nodeID uint64, addr string) *node {
	n := newNode(nodeID, s.serverUid(nodeID), addr)
	n.start()
	return n
}

func (s *Server) serverUid(id uint64) string {
	return fmt.Sprintf("%d", id)
}

func (s *Server) nodeIdByServerUid(uid string) uint64 {
	id, err := strconv.ParseUint(uid, 10, 64)
	if err != nil {
		s.Error("nodeByServerUid error", zap.Error(err))
		return 0
	}
	return id
}

func (s *Server) addNodes(nodes []*pb.Node) {
	for _, node := range nodes {
		if node.Id == s.opts.NodeID {
			continue
		}
		if s.nodeManager.exist(node.Id) {
			continue
		}
		s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
	}
}

func (s *Server) addNode(nodeId uint64, addr string) {
	if nodeId == s.opts.NodeID {
		return
	}
	if s.nodeManager.exist(nodeId) {
		return
	}
	s.nodeManager.addNode(s.newNodeByNodeInfo(nodeId, addr))
}

func (s *Server) addSlots(slots []*pb.Slot) error {
	for _, slot := range slots {
		if s.slotManager.exist(slot.Id) {
			continue
		}
		st, err := s.newSlotBySlotInfo(slot)
		if err != nil {
			return err
		}
		s.slotManager.addSlot(st)
	}
	return nil
}

func (s *Server) newSlotBySlotInfo(st *pb.Slot) (*slot, error) {
	ns := newSlot(st, 0, 1, s.opts)
	return ns, nil
}
