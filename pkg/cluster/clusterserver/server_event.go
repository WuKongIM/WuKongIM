package cluster

import (
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

// 分布式配置改变
func (s *Server) onClusterConfigChange(cfg *pb.Config) {
	s.Info("onClusterConfigChange----->")
	if s.stopped.Load() {
		s.Info("server stopped")
		return
	}
	err := s.handleClusterConfigChange(cfg)
	if err != nil {
		s.Error("handleClusterConfigChange failed", zap.Error(err))
	}
}

func (s *Server) handleClusterConfigChange(cfg *pb.Config) error {

	// ================== 处理节点 ==================
	err := s.handleClusterConfigNodeChange(cfg)
	if err != nil {
		s.Error("handleClusterConfigNodeChange failed", zap.Error(err))
		return err
	}

	// ================== 处理槽 ==================
	err = s.handleClusterConfigSlotChange(cfg)
	if err != nil {
		s.Error("handleClusterConfigSlotChange failed", zap.Error(err))
		return err
	}

	return err
}

func (s *Server) handleClusterConfigNodeChange(cfg *pb.Config) error {

	// 添加新节点
	for _, node := range cfg.Nodes {
		if s.nodeManager.exist(node.Id) {
			continue
		}
		if s.opts.NodeId == node.Id {
			continue
		}
		if strings.TrimSpace(node.ClusterAddr) != "" {
			s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
		}
	}

	// 移除节点
	for _, node := range s.nodeManager.nodes() {
		exist := false
		for _, cfgNode := range cfg.Nodes {
			if node.id == cfgNode.Id {
				exist = true
				break
			}
		}
		if !exist {
			s.nodeManager.removeNode(node.id)
			node.stop()
		}
	}

	// 修改节点
	for _, cfgNode := range cfg.Nodes {
		if cfgNode.Id == s.opts.NodeId {
			continue
		}
		if !s.nodeManager.exist(cfgNode.Id) {
			continue
		}
		n := s.nodeManager.node(cfgNode.Id)
		if n != nil && n.addr != cfgNode.ClusterAddr {
			s.nodeManager.removeNode(n.id)
			n.stop()
			s.nodeManager.addNode(s.newNodeByNodeInfo(cfgNode.Id, cfgNode.ClusterAddr))
		}
	}

	return nil
}

func (s *Server) handleClusterConfigSlotChange(cfg *pb.Config) error {

	// 添加属于此节点的槽
	for _, slot := range cfg.Slots {
		if s.slotManager.exist(slot.Id) {
			continue
		}
		if !wkutil.ArrayContainsUint64(slot.Replicas, s.opts.NodeId) && !wkutil.ArrayContainsUint64(slot.Learners, s.opts.NodeId) {
			continue
		}
		s.addSlot(slot)
	}

	// 移除不属于此节点的槽
	removeSlotIds := make([]uint32, 0)
	s.slotManager.iterate(func(slot *slot) bool {
		for _, cfgSlot := range cfg.Slots {
			if slot.st.Id == cfgSlot.Id {
				if !wkutil.ArrayContainsUint64(cfgSlot.Replicas, s.opts.NodeId) && !wkutil.ArrayContainsUint64(cfgSlot.Learners, s.opts.NodeId) {
					removeSlotIds = append(removeSlotIds, slot.st.Id)
				}
				break
			}
		}
		return true
	})
	if len(removeSlotIds) > 0 {
		for _, slotId := range removeSlotIds {
			s.slotManager.remove(slotId)
		}
	}

	// 处理槽修改
	for _, cfgSlot := range cfg.Slots {

		slot := s.slotManager.get(cfgSlot.Id)
		if slot == nil {
			continue
		}

		if !cfgSlot.Equal(slot.st) {
			s.addOrUpdateSlot(cfgSlot)
		}
	}

	// 处理迁移配置
	for _, cfgSlot := range cfg.Slots {
		if cfgSlot.MigrateFrom == 0 || cfgSlot.MigrateTo == 0 {
			continue
		}

		if cfgSlot.MigrateTo != s.opts.NodeId {
			continue
		}
		s.addOrUpdateSlot(cfgSlot)
	}

	return nil
}
