package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterevent"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) onEvent(msgs []clusterevent.Message) {
	var err error
	for _, msg := range msgs {
		s.Debug("收到分布式事件....", zap.String("type", msg.Type.String()))

		err = s.handleClusterEvent(msg)
		if err == nil {
			s.clusterEventServer.Step(msg)
		} else {
			s.Warn("handle cluster event failed", zap.Error(err))
		}
	}
}

func (s *Server) handleClusterEvent(m clusterevent.Message) error {

	switch m.Type {
	case clusterevent.EventTypeNodeAdd: // 添加节点
		s.handleNodeAdd(m)
	case clusterevent.EventTypeNodeUpdate: // 修改节点
		s.handleNodeUpdate(m)
	case clusterevent.EventTypeSlotAdd: // 添加槽
		s.handleSlotAdd(m)
	case clusterevent.EventTypeApiServerAddrUpdate: // api服务地址更新
		return s.handleApiServerAddrUpdate()
	case clusterevent.EventTypeNodeOnlieChange: // 在线状态改变
		return s.handleNodeOnlineChange(m)
	case clusterevent.EventTypeSlotNeedElection: // 槽需要重新选举
		return s.handleSlotNeedElection(m)
	}
	return nil

}

func (s *Server) handleNodeAdd(m clusterevent.Message) {
	for _, node := range m.Nodes {
		if s.nodeManager.exist(node.Id) {
			continue
		}
		if s.opts.NodeId == node.Id {
			continue
		}
		if strings.TrimSpace(node.ClusterAddr) != "" {
			s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
		}
	}
}

func (s *Server) handleNodeUpdate(m clusterevent.Message) {
	for _, node := range m.Nodes {
		if node.Id == s.opts.NodeId {
			continue
		}
		if !s.nodeManager.exist(node.Id) && strings.TrimSpace(node.ClusterAddr) != "" {
			s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
			continue
		}
		n := s.nodeManager.node(node.Id)
		if n != nil && n.addr != node.ClusterAddr {
			s.nodeManager.removeNode(n.id)
			n.stop()
			s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
			continue
		}

	}
}

func (s *Server) handleApiServerAddrUpdate() error {

	localNode := s.clusterEventServer.Node(s.opts.NodeId)
	if localNode == nil {
		return nil
	}
	if strings.TrimSpace(localNode.ApiServerAddr) == strings.TrimSpace(s.opts.ApiServerAddr) {
		return nil
	}

	err := s.proposeApiServerAddrUpdate()
	if err != nil {
		s.Error("proposeApiServerAddrUpdate failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) proposeApiServerAddrUpdate() error {

	if s.clusterEventServer.IsLeader() {
		return s.clusterEventServer.ProposeUpdateApiServerAddr(s.opts.NodeId, s.opts.ApiServerAddr)
	}

	leaderId := s.clusterEventServer.LeaderId()
	leader := s.nodeManager.node(leaderId)
	if leader == nil {
		return fmt.Errorf("leader not exist")
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.ReqTimeout)
	defer cancel()
	return leader.requestUpdateNodeApiServerAddr(timeoutCtx, &UpdateApiServerAddrReq{
		NodeId:        s.opts.NodeId,
		ApiServerAddr: s.opts.ApiServerAddr,
	})

}

func (s *Server) handleNodeOnlineChange(m clusterevent.Message) error {
	if !s.clusterEventServer.IsLeader() {
		s.Panic("handleNodeOnlineChange failed, not leader")
		return nil
	}
	cfg := s.clusterEventServer.Config().Clone()
	for _, node := range m.Nodes {
		for i, cfgNode := range cfg.Nodes {
			if node.Id == cfgNode.Id {
				cfg.Nodes[i] = node
			}
		}
		if node.Online {
			s.Info("节点上线", zap.Uint64("nodeId", node.Id))
		} else {
			s.Info("节点下线", zap.Uint64("nodeId", node.Id))
		}
	}
	cfg.Version++
	err := s.clusterEventServer.ProposeConfig(s.cancelCtx, cfg)
	if err != nil {
		s.Error("propose online change failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) handleSlotAdd(m clusterevent.Message) {
	for _, slot := range m.Slots {
		if s.slotManager.exist(slot.Id) {
			continue
		}
		if !wkutil.ArrayContainsUint64(slot.Replicas, s.opts.NodeId) {
			continue
		}
		s.addSlot(slot)
	}
}

func (s *Server) handleSlotNeedElection(m clusterevent.Message) error {
	if len(m.Slots) == 0 {
		return nil // 如果没有需要处理的slots，则认为处理完毕，返回true
	}
	electionSlotLeaderMap := make(map[uint64][]uint32)
	electionSlotIds := make([]uint32, 0, len(m.Slots))
	for _, slot := range m.Slots {
		if slot.Leader == 0 {
			continue
		}
		if s.clusterEventServer.NodeOnline(slot.Leader) {
			continue
		}
		for _, replicaId := range slot.Replicas {
			if !s.clusterEventServer.NodeOnline(replicaId) {
				continue
			}
			electionSlotLeaderMap[replicaId] = append(electionSlotLeaderMap[replicaId], slot.Id)
		}
		electionSlotIds = append(electionSlotIds, slot.Id)
	}
	if len(electionSlotLeaderMap) == 0 {
		return nil
	}

	// 获取槽在各个副本上的日志信息
	slotInfoResps, err := s.requestSlotInfos(electionSlotLeaderMap)
	if err != nil {
		s.Error("request slot infos error", zap.Error(err))
		return err
	}
	if len(slotInfoResps) == 0 {
		return nil
	}

	// ================== 根据日志信息选举新的leader ==================
	// 收集各个槽的日志信息
	replicaLastLogInfoMap, err := s.collectSlotLogInfo(slotInfoResps)
	if err != nil {
		s.Error("collect slot log info error", zap.Error(err))
		return err
	}
	// 根据日志信息计算出槽的领导者
	slotLeaderMap := s.calculateSlotLeader(replicaLastLogInfoMap, electionSlotIds)
	if len(slotLeaderMap) == 0 {
		s.Warn("没有选举出任何槽领导者！！！", zap.Uint32s("slotIds", electionSlotIds))
		return nil
	}

	// ================== 提案新的配置 ==================
	newCfg := s.clusterEventServer.Config().Clone()
	getSlot := func(slotId uint32) *pb.Slot {
		for _, slot := range newCfg.Slots {
			if slot.Id == slotId {
				return slot
			}
		}
		return nil
	}
	for slotID, leaderNodeID := range slotLeaderMap {
		if leaderNodeID == 0 {
			continue
		}
		slot := getSlot(slotID)
		if slot.Leader != leaderNodeID {
			slot.Leader = leaderNodeID
			slot.Term++
		}
	}
	newCfg.Version++
	err = s.clusterEventServer.ProposeConfig(s.cancelCtx, newCfg)
	if err != nil {
		s.Error("propose slot election config failed", zap.Error(err))
		return err
	}
	return nil

}

// 请求槽的日志高度
func (s *Server) requestSlotInfos(waitElectionSlots map[uint64][]uint32) ([]*SlotLogInfoResp, error) {
	slotInfoResps := make([]*SlotLogInfoResp, 0, len(waitElectionSlots))
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	requestGroup, _ := errgroup.WithContext(timeoutCtx)
	for nodeId, slotIds := range waitElectionSlots {
		if nodeId == s.opts.NodeId { // 本节点
			slotInfos, err := s.slotInfos(slotIds)
			if err != nil {
				s.Error("get slot infos error", zap.Error(err))
				continue
			}
			slotInfoResps = append(slotInfoResps, &SlotLogInfoResp{
				NodeId: nodeId,
				Slots:  slotInfos,
			})
			continue
		} else {
			requestGroup.Go(func(nID uint64, sids []uint32) func() error {
				return func() error {
					resp, err := s.requestSlotLogInfo(nID, sids)
					if err != nil {
						s.Warn("request slot log info error", zap.Error(err))
						return nil
					}
					slotInfoResps = append(slotInfoResps, resp)
					return nil
				}
			}(nodeId, slotIds))
		}
	}
	_ = requestGroup.Wait()
	return slotInfoResps, nil

}

func (s *Server) requestSlotLogInfo(nodeId uint64, slotIds []uint32) (*SlotLogInfoResp, error) {
	req := &SlotLogInfoReq{
		SlotIds: slotIds,
	}
	resp, err := s.nodeManager.requestSlotLogInfo(s.cancelCtx, nodeId, req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Server) slotInfos(slotIds []uint32) ([]SlotInfo, error) {
	slotInfos := make([]SlotInfo, 0, len(slotIds))
	for _, slotId := range slotIds {
		shardNo := SlotIdToKey(slotId)
		lastLogIndex, term, err := s.opts.SlotLogStorage.LastIndexAndTerm(shardNo)
		if err != nil {
			return nil, err
		}
		slotInfos = append(slotInfos, SlotInfo{
			SlotId:   slotId,
			LogIndex: lastLogIndex,
			LogTerm:  term,
		})
	}
	return slotInfos, nil
}

// 收集slot的各个副本的日志高度
func (s *Server) collectSlotLogInfo(slotInfoResps []*SlotLogInfoResp) (map[uint64]map[uint32]SlotInfo, error) {
	slotLogInfos := make(map[uint64]map[uint32]SlotInfo, len(slotInfoResps))
	for _, resp := range slotInfoResps {
		slotLogInfos[resp.NodeId] = make(map[uint32]SlotInfo, len(resp.Slots))
		for _, slotInfo := range resp.Slots {
			slotLogInfos[resp.NodeId][slotInfo.SlotId] = slotInfo
		}
	}
	return slotLogInfos, nil
}

// 计算每个槽的领导节点
func (s *Server) calculateSlotLeader(slotLogInfos map[uint64]map[uint32]SlotInfo, slotIds []uint32) map[uint32]uint64 {
	slotLeaderMap := make(map[uint32]uint64, len(slotIds))
	for _, slotId := range slotIds {
		slotLeaderMap[slotId] = s.calculateSlotLeaderBySlot(slotLogInfos, slotId)
	}
	return slotLeaderMap
}

// 计算槽的领导节点
func (s *Server) calculateSlotLeaderBySlot(slotLogInfos map[uint64]map[uint32]SlotInfo, slotId uint32) uint64 {
	var leader uint64
	var maxLogIndex uint64
	var maxLogTerm uint32
	for replicaId, logIndexMap := range slotLogInfos {
		slotInfo := logIndexMap[slotId]
		if slotInfo.LogTerm > maxLogTerm {
			leader = replicaId
			maxLogIndex = slotInfo.LogIndex
			maxLogTerm = slotInfo.LogTerm
		} else if slotInfo.LogTerm == maxLogTerm {
			if slotInfo.LogIndex > maxLogIndex {
				leader = replicaId
				maxLogIndex = slotInfo.LogIndex
				maxLogTerm = slotInfo.LogTerm
			}
		}

	}
	return leader
}
