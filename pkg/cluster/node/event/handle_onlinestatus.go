package event

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (h *handler) handleOnlineStatus(nodeId uint64, online bool) {
	// 提案节点状态
	err := h.cfgServer.ProposeNodeOnlineStatus(nodeId, online)
	if err != nil {
		h.Error("propose node offline", zap.Error(err))
		return
	}

	// 处理槽迁移
	err = h.handleSlotChangeByNodeOnlineStatus(nodeId, online)
	if err != nil {
		h.Error("handleSlotChangeByNodeOnlineStatus failed", zap.Error(err))
		return
	}
}

func (h *handler) handleSlotChangeByNodeOnlineStatus(nodeId uint64, online bool) error {
	var newSlots []*types.Slot
	if online { // 节点上线

		h.Info("节点上线", zap.Uint64("nodeId", nodeId))
		slots := h.cfgServer.Slots()

		onlineNodeCount := h.cfgServer.AllowVoteAndJoinedOnlineNodeCount()

		avgSlotLeaderCount := h.cfgServer.GetClusterConfig().SlotCount / uint32(onlineNodeCount) // 平均每个节点的槽领导数量

		// 每个节点当前的槽领导数量
		nodeSlotLeaderCountMap := make(map[uint64]uint32)
		for _, slot := range slots {
			nodeSlotLeaderCountMap[slot.Leader]++
		}

		currentNodeSlotLeaderCount := nodeSlotLeaderCountMap[nodeId] // 当前节点的槽领导数量

		if currentNodeSlotLeaderCount >= avgSlotLeaderCount { // 当前节点的槽领导数量已经达到平均值，不需要迁入
			return nil
		}

		// 计算需要迁入的槽领导数量
		needSlotLeaderCount := avgSlotLeaderCount - currentNodeSlotLeaderCount

		for nId, slotLeaderCount := range nodeSlotLeaderCountMap {
			if slotLeaderCount < avgSlotLeaderCount { // 槽领导数量小于平均值的节点不参与计算
				continue
			}

			if needSlotLeaderCount <= 0 {
				break
			}

			if nId == nodeId {
				continue
			}

			exportSlotLeaderCount := slotLeaderCount - avgSlotLeaderCount // 需要迁出的槽领导数量

			for _, slot := range slots {
				if slot.Leader == nId &&
					exportSlotLeaderCount > 0 &&
					needSlotLeaderCount > 0 &&
					slot.MigrateFrom == 0 &&
					slot.MigrateTo == 0 &&
					wkutil.ArrayContainsUint64(slot.Replicas, nodeId) {

					newSlot := slot.Clone()
					newSlot.MigrateFrom = nId
					newSlot.MigrateTo = nodeId
					newSlots = append(newSlots, newSlot)
					exportSlotLeaderCount--
					needSlotLeaderCount--
				}
			}

		}

	} else { // 节点下线
		h.Info("节点下线", zap.Uint64("nodeId", nodeId))
		slots := h.cfgServer.Slots()
		onlineNodeCount := h.cfgServer.AllowVoteAndJoinedOnlineNodeCount()
		avgSlotLeaderCount := h.cfgServer.GetClusterConfig().SlotCount / uint32(onlineNodeCount) // 平均每个节点的槽领导数量

		// 每个节点当前的槽领导数量
		nodeSlotLeaderCountMap := make(map[uint64]uint32)
		for _, slot := range slots {
			nodeSlotLeaderCountMap[slot.Leader]++
		}

		currentNodeSlotLeaderCount := nodeSlotLeaderCountMap[nodeId] // 当前节点的槽领导数量

		// fmt.Println("currentNodeSlotLeaderCount---->", currentNodeSlotLeaderCount)

		for _, slot := range slots {
			if slot.Leader != nodeId {
				continue
			}

			for nId, slotLeaderCount := range nodeSlotLeaderCountMap {
				if nId == nodeId {
					continue
				}

				if currentNodeSlotLeaderCount <= 0 {
					break
				}

				if slotLeaderCount > avgSlotLeaderCount { // 槽领导数量大于等于平均值的节点不参与计算
					continue
				}

				if slot.MigrateFrom == 0 && slot.MigrateTo == 0 && slot.Status != types.SlotStatus_SlotStatusCandidate {
					newSlot := slot.Clone()
					newSlot.Status = types.SlotStatus_SlotStatusCandidate
					newSlot.ExpectLeader = nId
					newSlots = append(newSlots, newSlot)
					nodeSlotLeaderCountMap[nId]++
					currentNodeSlotLeaderCount--
					break
				}
			}
		}

	}
	if len(newSlots) > 0 {
		err := h.cfgServer.ProposeSlots(newSlots)
		if err != nil {
			h.Error("handleNodeOnlineChange failed,ProposeSlots failed", zap.Error(err))
			return err
		}
	}
	return nil
}
