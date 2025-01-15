package event

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
)

func (h *handler) handleSlotLeaderElection() error {
	slots := h.cfgServer.Slots()
	if len(slots) == 0 {
		return nil
	}

	electionSlots := make([]*types.Slot, 0) // 需要选举的槽
	for _, slot := range slots {
		if slot.Status == types.SlotStatus_SlotStatusCandidate {
			electionSlots = append(electionSlots, slot)
		}
	}
	if len(electionSlots) == 0 { // 不存在候选的槽
		return nil
	}

	// 触发选举
	return h.s.event.OnSlotElection(electionSlots)

}
