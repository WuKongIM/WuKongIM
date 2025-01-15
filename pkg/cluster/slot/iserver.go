package slot

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
)

func (s *Server) GetSlotId(v string) uint32 {

	return s.getSlotId(v)
}

func (s *Server) SlotLeaderId(slotId uint32) uint64 {
	raft := s.raftGroup.GetRaft(SlotIdToKey(slotId))
	if raft != nil {
		return raft.LeaderId()
	}
	return 0

}

func (s *Server) ProposeUntilApplied(slotId uint32, data []byte) (*types.ProposeResp, error) {

	start := time.Now()
	defer func() {
		if trace.GlobalTrace != nil {
			trace.GlobalTrace.Metrics.Cluster().ProposeLatencyAdd(trace.ClusterKindSlot, time.Since(start).Milliseconds())
			if err := recover(); err != nil {
				trace.GlobalTrace.Metrics.Cluster().ProposeFailedCountAdd(trace.ClusterKindSlot, 1)
			}
		}
	}()

	shardNo := SlotIdToKey(slotId)
	logId := s.GenLogId()
	return s.raftGroup.ProposeUntilApplied(shardNo, logId, data)
}

func (s *Server) ProposeUntilAppliedTimeout(ctx context.Context, slotId uint32, data []byte) (*types.ProposeResp, error) {

	start := time.Now()
	defer func() {
		if trace.GlobalTrace != nil {
			trace.GlobalTrace.Metrics.Cluster().ProposeLatencyAdd(trace.ClusterKindSlot, time.Since(start).Milliseconds())
			if err := recover(); err != nil {
				trace.GlobalTrace.Metrics.Cluster().ProposeFailedCountAdd(trace.ClusterKindSlot, 1)
			}
		}
	}()

	shardNo := SlotIdToKey(slotId)
	logId := s.GenLogId()
	resps, err := s.raftGroup.ProposeBatchUntilAppliedTimeout(ctx, shardNo, types.ProposeReqSet{
		{
			Id:   logId,
			Data: data,
		},
	})
	if err != nil {
		return nil, err
	}
	if len(resps) == 0 {
		return nil, nil
	}
	return resps[0], nil
}

func (s *Server) MustWaitAllSlotsReady(timeout time.Duration) {

}
