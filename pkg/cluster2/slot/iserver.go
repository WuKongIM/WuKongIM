package slot

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

func (s *Server) GetSlotId(v string) uint32 {

	return s.getSlotId(v)
}

func (s *Server) SlotLeaderId(slotId uint32) uint64 {

	return s.raftGroup.GetRaft(SlotIdToKey(slotId)).LeaderId()

}

func (s *Server) Propose(slotId uint32, data []byte) (*types.ProposeResp, error) {
	shardNo := SlotIdToKey(slotId)
	logId := s.GenLogId()
	return s.raftGroup.Propose(shardNo, logId, data)
}

func (s *Server) ProposeUntilApplied(slotId uint32, data []byte) (*types.ProposeResp, error) {
	shardNo := SlotIdToKey(slotId)
	logId := s.GenLogId()
	return s.raftGroup.ProposeUntilApplied(shardNo, logId, data)
}
