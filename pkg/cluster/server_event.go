package cluster

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterevent"
	"github.com/WuKongIM/WuKongIM/pkg/clusterevent/pb"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) loopClusterEvent() {
	for {
		select {
		case clusterEvent := <-s.clusterEventManager.Watch():
			s.Debug("收到集群事件")
			s.handleClusterEvent(clusterEvent)
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Server) handleClusterEvent(clusterEvent clusterevent.ClusterEvent) {
	if clusterEvent.SlotEvent != nil {
		s.handleClusterSlotEvent(clusterEvent.SlotEvent)
	}
	if clusterEvent.NodeEvent != nil {
		s.handleClusterNodeEvent(clusterEvent.NodeEvent)
	}
}

func (s *Server) handleClusterSlotEvent(slotEvent *pb.SlotEvent) {
	switch slotEvent.EventType {
	case pb.SlotEventType_SlotEventTypeInit: // slot初始化
		s.handleClusterSlotEventInit(slotEvent)
	case pb.SlotEventType_SlotEventTypeElection: // slot选举
		s.handleClusterSlotEventElection(slotEvent)
	}
}

func (s *Server) handleClusterNodeEvent(nodeEvent *pb.NodeEvent) {

}

// 处理slot初始化
func (s *Server) handleClusterSlotEventInit(slotEvent *pb.SlotEvent) {
	var err error
	for _, st := range slotEvent.Slots {
		s.clusterEventManager.AddOrUpdateSlotNoSave(st)
		slot := s.slotManager.GetSlot(st.Id)
		if slot == nil {
			slot, err = s.newSlot(st.Id)
			if err != nil {
				s.Error("slot init failed", zap.Error(err))
				return
			}
			s.slotManager.AddSlot(slot)
		}
	}
	s.clusterEventManager.SaveAndVersionInc()
}

// 处理slot选举
func (s *Server) handleClusterSlotEventElection(slotEvent *pb.SlotEvent) {
	if len(slotEvent.SlotIDs) == 0 {
		return
	}

	// 计算slot分布的节点
	slotNodeMap := map[uint64][]uint32{}
	slots := s.clusterEventManager.GetSlots()
	for _, slot := range slots {
		for _, replicaNodeID := range slot.Replicas {
			if replicaNodeID == s.opts.NodeID {
				slotNodeMap[replicaNodeID] = append(slotNodeMap[replicaNodeID], slot.Id)
			} else {
				node := s.nodeManager.getNode(replicaNodeID)
				if node != nil && node.online {
					slotNodeMap[replicaNodeID] = append(slotNodeMap[replicaNodeID], slot.Id)
				}
			}

		}
	}
	if len(slotNodeMap) == 0 {
		s.Debug("没有可用的节点")
		return
	}

	// 发送上报slot信息的请求
	slotInfoReportResps := make([]*SlotInfoReportResponse, 0)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	requestGroup, ctx := errgroup.WithContext(timeoutCtx)

	for nodeID, slotIDs := range slotNodeMap {
		if nodeID == s.opts.NodeID { // 本节点
			slotInfos, err := s.getSlotInfosFromLocalNode(slotIDs)
			if err != nil {
				s.Error("getSlotInfosFromLocalNode is failed", zap.Error(err))
				cancel()
				return
			}
			slotInfoReportResps = append(slotInfoReportResps, &SlotInfoReportResponse{
				NodeID:    nodeID,
				SlotInfos: slotInfos,
			})
			continue
		}
		requestGroup.Go(func(nID uint64, slotIDs []uint32) func() error {
			return func() error {
				select {
				case <-ctx.Done():
				default:
					req := &SlotInfoReportRequest{
						SlotIDs: slotIDs,
					}
					resp, err := s.nodeManager.requestSlotInfo(nID, req)
					if err != nil {
						return err
					}
					slotInfoReportResps = append(slotInfoReportResps, resp)
				}
				return nil
			}
		}(nodeID, slotIDs))
	}

	if err := requestGroup.Wait(); err != nil {
		s.Error("requestSlotInfo is failed", zap.Error(err))
		cancel()
		return
	}
	cancel()

	// 收集slot信息
	nodeSlotMap := map[uint64]map[uint32]uint64{} // nodeID -> slotID -> logIndex
	for _, slotInfoReportResp := range slotInfoReportResps {
		slotLogMap := nodeSlotMap[slotInfoReportResp.NodeID]
		if slotLogMap != nil {
			for _, slotInfo := range slotInfoReportResp.SlotInfos {
				slotLogMap[slotInfo.SlotID] = slotInfo.LogIndex
			}
		} else {
			slotLogMap = map[uint32]uint64{}
			for _, slotInfo := range slotInfoReportResp.SlotInfos {
				slotLogMap[slotInfo.SlotID] = slotInfo.LogIndex
			}
			nodeSlotMap[slotInfoReportResp.NodeID] = slotLogMap
		}
	}

	// 决策slot的领导者
	configChange := false
	for _, slotID := range slotEvent.SlotIDs {
		slot := s.clusterEventManager.GetSlot(slotID)
		if slot != nil {
			// 计算slot的领导者
			leaderNodeID := uint64(0)
			var leaderLogIndex uint64
			for _, replicaNodeID := range slot.Replicas {
				slotLogMap := nodeSlotMap[replicaNodeID]
				if slotLogMap != nil {
					logIndex, ok := slotLogMap[slotID]
					if ok {
						if leaderNodeID == 0 {
							leaderNodeID = replicaNodeID
							leaderLogIndex = logIndex
						} else {
							if leaderLogIndex < logIndex {
								leaderNodeID = replicaNodeID
								leaderLogIndex = logIndex
							}
						}
					}
				}
			}
			if leaderNodeID != 0 {
				configChange = true
				s.Debug("slot选举", zap.Uint32("slotID", slotID), zap.Uint64("leaderNodeID", leaderNodeID))
				s.clusterEventManager.UpdateSlotLeaderNoSave(slotID, leaderNodeID)
			}
		}
	}
	if configChange {
		s.clusterEventManager.SaveAndVersionInc()
	}

}

func (s *Server) getSlotInfosFromLocalNode(slotIDs []uint32) ([]*SlotInfo, error) {
	slotInfos := make([]*SlotInfo, 0)
	for _, slotID := range slotIDs {
		slot := s.slotManager.GetSlot(uint32(slotID))
		if slot != nil {
			lastLogIndex, err := slot.LastIndex()
			if err != nil {
				return nil, err
			}
			slotInfos = append(slotInfos, &SlotInfo{
				SlotID:   uint32(slotID),
				LogIndex: lastLogIndex,
			})
		}
	}
	return slotInfos, nil
}
