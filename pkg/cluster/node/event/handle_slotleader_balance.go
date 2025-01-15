package event

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (s *Server) handleSlotLeaderAutoBalance() error {

	cfg := s.cfgServer.GetClusterConfig()

	// 有未加入的节点或者有槽正在迁移，则不进行自动均衡
	for _, node := range cfg.Nodes {
		if node.Status != types.NodeStatus_NodeStatusJoined {
			return nil
		}
	}
	for _, slot := range cfg.Slots {
		if slot.MigrateFrom != 0 || slot.MigrateTo != 0 {
			return nil
		}
		if slot.Status == types.SlotStatus_SlotStatusCandidate {
			return nil
		}
	}

	// 自动均衡槽领导
	changedSlots := s.autoBalanceSlotLeaders(cfg)
	if len(changedSlots) == 0 {
		return nil
	}
	err := s.cfgServer.ProposeSlots(changedSlots)
	if err != nil {
		s.Error("handleSlotLeaderAutoBalance failed,ProposeSlots failed", zap.Error(err))
		return err
	}
	return nil
}

// 自动均衡槽的领导节点
func (s *Server) autoBalanceSlotLeaders(cfg *types.Config) []*types.Slot {

	slots := cfg.Slots
	if len(slots) == 0 {
		return nil
	}

	// 判断是否有需要转移的槽领导，只要有就不执行自动均衡算法，等都转移完成后再执行
	for _, slot := range slots {
		if slot.MigrateFrom != 0 || slot.MigrateTo != 0 {
			return nil
		}
	}

	// 计算每个节点的槽数量和领导数量
	nodeSlotCountMap := make(map[uint64]uint32)   // 每个节点槽数量
	nodeLeaderCountMap := make(map[uint64]uint32) // 每个节点槽领导数量
	for _, slot := range slots {
		if slot.Leader == 0 {
			continue
		}
		nodeLeaderCountMap[slot.Leader]++
		for _, replicaId := range slot.Replicas {
			nodeSlotCountMap[replicaId]++
		}
	}

	// ==================== 计算每个节点应该分配多少槽领导 ====================

	exportNodeLeaderCountMap := make(map[uint64]uint32) // 节点应该迁出领导数量
	importNodeLeaderCountMap := make(map[uint64]uint32) // 节点应该迁入领导数量

	firstSlot := slots[0]

	currentSlotReplicaCount := uint32(len(firstSlot.Replicas)) // 当前槽的副本数量

	for nodeId, slotCount := range nodeSlotCountMap {
		if slotCount == 0 {
			continue
		}
		leaderCount := nodeLeaderCountMap[nodeId]                 // 当前节点的领导数量
		avgLeaderCount := slotCount / currentSlotReplicaCount     // 此节点应该分配到的领导数量
		remaingLeaderCount := slotCount % currentSlotReplicaCount // 剩余的待分配的领导数量
		if remaingLeaderCount > 0 {
			avgLeaderCount += 1
		}

		// 如果当前节点的领导数量超过了平均领导数量，将一些槽的领导权移交给其他节点
		if leaderCount > avgLeaderCount {
			exportLeaderCount := leaderCount - avgLeaderCount
			exportNodeLeaderCountMap[nodeId] = exportLeaderCount
		} else if leaderCount < avgLeaderCount {
			importLeaderCount := avgLeaderCount - leaderCount
			importNodeLeaderCountMap[nodeId] = importLeaderCount
		}
	}

	// ==================== 迁移槽领导 ====================

	var nodeOnline = func(nodeId uint64) bool {
		for _, node := range cfg.Nodes {
			if node.Id == nodeId {
				return node.Online
			}
		}
		return false
	}

	var newSlots []*types.Slot
	for exportNodeId, exportLeaderCount := range exportNodeLeaderCountMap {
		if exportLeaderCount == 0 {
			continue
		}

		if !nodeOnline(exportNodeId) { // 节点不在线 不参与
			continue
		}
		for importNodeId, importLeaderCount := range importNodeLeaderCountMap {
			if importLeaderCount == 0 {
				continue
			}

			if !nodeOnline(importNodeId) { // 节点不在线 不参与
				continue
			}
			// 从exportNodeId迁移一个槽领导到importNodeId
			for _, slot := range slots {
				if slot.MigrateFrom != 0 || slot.MigrateTo != 0 { // 已经需要转移的不参与计算
					continue
				}
				if slot.Leader == exportNodeId && wkutil.ArrayContainsUint64(slot.Replicas, importNodeId) { // 只有这个槽的领导属于exportNodeId，且importNodeId是这个槽的副本节点才能转移
					newSlot := slot.Clone()
					newSlot.MigrateFrom = exportNodeId
					newSlot.MigrateTo = importNodeId
					newSlots = append(newSlots, newSlot)
					exportLeaderCount--
					importLeaderCount--
					if exportLeaderCount == 0 || importLeaderCount == 0 {
						break
					}
				}
			}
		}
	}
	return newSlots

}
