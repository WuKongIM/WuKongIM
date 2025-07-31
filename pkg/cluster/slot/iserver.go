package slot

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
)

func (s *Server) GetSlotId(v string) uint32 {

	return s.getSlotId(v)
}

func (s *Server) SlotLeaderId(slotId uint32) uint64 {
	slotConfig := s.opts.Node.Slot(slotId)
	if slotConfig != nil {
		return slotConfig.Leader
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

	slotConfig := s.opts.Node.Slot(slotId)
	if slotConfig == nil {
		return nil, fmt.Errorf("slot[%d] config not found", slotId)
	}

	logId := s.GenLogId()

	// 如果当前节点不是槽的领导节点，则向槽的领导节点请求提案
	if slotConfig.Leader != s.opts.NodeId {

		resps, err := s.opts.RPC.RequestSlotProposeBatchUntilApplied(slotConfig.Leader, slotId, types.ProposeReqSet{
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

	// 如果当前节点是槽的领导节点，则本地提案
	resps, err := s.ProposeUntilAppliedTimeoutForLocal(ctx, slotId, types.ProposeReqSet{
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

func (s *Server) ProposeUntilAppliedTimeoutForLocal(ctx context.Context, slotId uint32, reqs types.ProposeReqSet) (types.ProposeRespSet, error) {
	shardNo := SlotIdToKey(slotId)

	resps, err := s.raftGroup.ProposeBatchUntilAppliedTimeout(ctx, shardNo, reqs)
	if err != nil {
		return nil, err
	}
	if len(resps) == 0 {
		return nil, nil
	}
	return resps, nil
}

func (s *Server) MustWaitAllSlotsReady(timeout time.Duration) {
	tk := time.NewTicker(time.Millisecond * 10)
	defer tk.Stop()
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		select {
		case <-tk.C:
			slots := s.opts.Node.Slots()
			if len(slots) > 0 {
				notReady := false
				for _, st := range slots {
					if st.Leader == 0 {
						notReady = true
						break
					}
				}
				if !notReady {
					return
				}

			}
		case <-timeoutCtx.Done():
			s.Panic("wait all slots ready timeout")
			return
		}
	}
}
