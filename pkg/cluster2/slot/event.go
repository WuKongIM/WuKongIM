package slot

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/types"
	rafttype "github.com/WuKongIM/WuKongIM/pkg/raft/types"
)

// 配置发送改变
func (s *Server) OnConfigChange(cfg *types.Config) {

	for _, slot := range cfg.Slots {
		if slot.Leader == 0 {
			continue
		}
		s.addOrUpdateSlotRaft(slot)
	}
}

// 根据slot的配置添加或者更新raft
func (s *Server) addOrUpdateSlotRaft(slot *types.Slot) {

	s.slotUpdateLock.Lock()
	defer s.slotUpdateLock.Unlock()

	if slot.Leader == 0 {
		return
	}

	shardNo := SlotIdToKey(slot.Id)
	rft := s.raftGroup.GetRaft(shardNo)
	if rft == nil { // 添加slot的raft
		slotNode := newSlot(slot, s)
		s.raftGroup.AddRaft(slotNode)

		s.raftGroup.AddEvent(shardNo, rafttype.Event{
			Type:   rafttype.ConfChange,
			Config: s.slotToConfig(slot),
		})
	} else { // 更新slot的分布式配置
		raftSlot := rft.(*Slot)
		if raftSlot.needUpdate(slot) {
			s.raftGroup.AddEvent(shardNo, rafttype.Event{
				Type:   rafttype.ConfChange,
				Config: s.slotToConfig(slot),
			})
		}
	}
}

func (s *Server) slotToConfig(st *types.Slot) rafttype.Config {
	return rafttype.Config{
		MigrateFrom: st.MigrateFrom,
		MigrateTo:   st.MigrateTo,
		Replicas:    st.Replicas,
		Learners:    st.Learners,
		Term:        st.Term,
		Leader:      st.Leader,
	}

}
