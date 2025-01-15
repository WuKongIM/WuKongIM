package event

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
)

func (s *Server) handleNodeJoining() error {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return nil
	}
	var joiningNode *types.Node
	for _, node := range s.cfgServer.Nodes() {
		if node.Status == types.NodeStatus_NodeStatusJoining {
			joiningNode = node
			break
		}
	}

	if joiningNode == nil {
		return nil
	}

	firstSlot := slots[0]

	var migrateSlots []*types.Slot // 迁移的槽列表

	voteNodes := s.cfgServer.AllowVoteNodes()

	if uint32(len(firstSlot.Replicas)) < s.cfgServer.SlotReplicaCount() { // 如果当前槽的副本数量小于配置的副本数量，则可以将新节点直接加入到学习节点中
		for _, slot := range slots {
			newSlot := slot.Clone()
			newSlot.MigrateFrom = joiningNode.Id
			newSlot.MigrateTo = joiningNode.Id
			newSlot.Learners = append(slot.Learners, joiningNode.Id)
			migrateSlots = append(migrateSlots, newSlot)
		}
	} else {
		voteNodeCount := uint32(len(voteNodes))                                                // 投票节点数量
		avgSlotCount := uint32(len(slots)) * s.cfgServer.SlotReplicaCount() / voteNodeCount    // 平均每个节点的槽数量
		remainSlotCount := uint32(len(slots)) * s.cfgServer.SlotReplicaCount() % voteNodeCount // 剩余的槽数量
		if remainSlotCount > 0 {
			avgSlotCount += 1
		}

		avgSlotLeaderCount := uint32(len(slots)) / voteNodeCount    // 平均每个节点的槽领导数量
		remainSlotLeaderCount := uint32(len(slots)) % voteNodeCount // 剩余的槽领导数量
		if remainSlotLeaderCount > 0 {
			avgSlotLeaderCount += 1
		}

		nodeSlotLeaderCountMap := make(map[uint64]uint32) // 每个节点的槽领导数量
		nodeSlotCountMap := make(map[uint64]uint32)       // 每个节点目前的槽数量
		nodeSlotCountMap[joiningNode.Id] = 0              // 新节点槽数量肯定为0
		for _, slot := range slots {
			nodeSlotLeaderCountMap[slot.Leader]++
			for _, replicaId := range slot.Replicas {
				nodeSlotCountMap[replicaId]++
			}
		}

		// 计算每个节点应该迁入/迁出的槽数量
		migrateToCountMap := make(map[uint64]uint32)             // 每个节点应该迁入的槽数量
		migrateFromCountMap := make(map[uint64]uint32)           // 每个节点应该迁出的槽数量
		migrateSlotLeaderFromCountMap := make(map[uint64]uint32) // 每个节点应该迁出的槽领导数量
		for nodeId, slotCount := range nodeSlotCountMap {
			if slotCount < avgSlotCount {
				migrateToCountMap[nodeId] = avgSlotCount - slotCount
			} else if slotCount > avgSlotCount {
				migrateFromCountMap[nodeId] = slotCount - avgSlotCount
			}
		}

		for nodeId, slotLeaderCount := range nodeSlotLeaderCountMap {
			if slotLeaderCount > avgSlotLeaderCount {
				migrateSlotLeaderFromCountMap[nodeId] = slotLeaderCount - avgSlotLeaderCount
			}
		}

		for _, node := range voteNodes {
			fromSlotCount := migrateFromCountMap[node.Id]
			fromSlotLeaderCount := migrateSlotLeaderFromCountMap[node.Id]

			if fromSlotCount == 0 { // 已分配完毕
				continue
			}

			for _, slot := range slots {
				exist := false // 是否存在迁移，如果存在则忽略

				for _, migrateSlot := range migrateSlots {
					if slot.Id == migrateSlot.Id {
						exist = true
						break
					}
				}

				if exist { // 存在迁移则忽略
					continue
				}

				// ------------------- 分配槽领导 -------------------
				allocSlotLeader := false // 是否已经分配完槽领导
				if fromSlotCount > 0 && fromSlotLeaderCount > 0 && slot.Leader == node.Id {

					allocSlotLeader = true
					newSlot := slot.Clone()
					newSlot.MigrateFrom = slot.Leader
					newSlot.MigrateTo = joiningNode.Id
					newSlot.Learners = append(newSlot.Learners, joiningNode.Id)
					migrateSlots = append(migrateSlots, newSlot)
					fromSlotLeaderCount--
					nodeSlotLeaderCountMap[node.Id] = fromSlotLeaderCount

					fromSlotCount--
					migrateFromCountMap[node.Id] = fromSlotCount

				}

				// ------------------- 分配槽副本 -------------------
				if fromSlotCount > 0 && !allocSlotLeader {
					if wkutil.ArrayContainsUint64(slot.Replicas, node.Id) {
						newSlot := slot.Clone()
						newSlot.MigrateFrom = node.Id
						newSlot.MigrateTo = joiningNode.Id
						newSlot.Learners = append(newSlot.Learners, joiningNode.Id)
						migrateSlots = append(migrateSlots, newSlot)
						fromSlotCount--
						migrateFromCountMap[node.Id] = fromSlotCount
					}
				}

			}

		}

	}

	if len(migrateSlots) > 0 {
		err := s.cfgServer.ProposeJoined(joiningNode.Id, migrateSlots)
		if err != nil {
			return err
		}
	}

	return nil

}
