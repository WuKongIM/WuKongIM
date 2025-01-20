package slot

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	rafttype "github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
)

// 配置发送改变
func (s *Server) OnConfigChange(cfg *types.Config) {

	for _, slot := range cfg.Slots {
		if slot.Leader == 0 {
			continue
		}
		s.AddOrUpdateSlotRaft(slot)
	}
}

// 根据slot的配置添加或者更新raft
func (s *Server) AddOrUpdateSlotRaft(slot *types.Slot) {

	s.slotUpdateLock.Lock()
	defer s.slotUpdateLock.Unlock()

	if slot.Leader == 0 {
		return
	}

	shardNo := SlotIdToKey(slot.Id)
	rft := s.raftGroup.GetRaft(shardNo)

	// 如果当前节点不是slot的replica或者learner则不处理
	if !wkutil.ArrayContainsUint64(slot.Replicas, s.opts.NodeId) && !wkutil.ArrayContainsUint64(slot.Learners, s.opts.NodeId) {
		if rft != nil {
			s.raftGroup.RemoveRaft(rft)
		}
		return
	}

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

	var role rafttype.Role
	if st.Status == types.SlotStatus_SlotStatusCandidate {
		role = rafttype.RoleCandidate
	} else if wkutil.ArrayContainsUint64(st.Learners, s.opts.NodeId) {
		role = rafttype.RoleLearner
	} else {
		if st.Leader == s.opts.NodeId {
			role = rafttype.RoleLeader
		} else {
			role = rafttype.RoleFollower
		}
	}

	cfg := rafttype.Config{
		MigrateFrom: st.MigrateFrom,
		MigrateTo:   st.MigrateTo,
		Replicas:    st.Replicas,
		Learners:    st.Learners,
		Term:        st.Term,
		Leader:      st.Leader,
		Role:        role,
	}

	return cfg

}
