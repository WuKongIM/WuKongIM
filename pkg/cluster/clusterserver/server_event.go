package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterevent"
	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) onEvent(msgs []clusterevent.Message) {
	var err error
	for _, msg := range msgs {
		s.Info("收到分布式事件....", zap.String("type", msg.Type.String()))

		err = s.handleClusterEvent(msg)
		if err == nil {
			s.clusterEventServer.Step(msg)
		} else {
			s.Warn("handle cluster event failed", zap.Error(err))
		}
	}
}

func (s *Server) handleClusterEvent(m clusterevent.Message) error {

	if s.stopped.Load() {
		s.Debug("server stopped")
		return nil
	}

	switch m.Type {
	case clusterevent.EventTypeNodeAdd: // 添加节点
		s.handleNodeAdd(m)
	case clusterevent.EventTypeNodeUpdate: // 修改节点
		s.handleNodeUpdate(m)
	case clusterevent.EventTypeSlotAdd: // 添加槽
		s.handleSlotAdd(m)
	case clusterevent.EventTypeSlotUpdate: // 修改槽
		s.handleSlotUpdate(m)
	case clusterevent.EventTypeApiServerAddrUpdate: // api服务地址更新
		return s.handleApiServerAddrUpdate()
	case clusterevent.EventTypeNodeOnlieChange: // 在线状态改变
		return s.handleNodeOnlineChange(m)
	case clusterevent.EventTypeSlotNeedElection: // 槽需要重新选举
		return s.handleProposeSlots(m)
	case clusterevent.EventTypeSlotLeaderTransfer: // 槽领导者转移
		return s.handleProposeSlots(m)
	case clusterevent.EventTypeSlotElectionStart: // 槽领导者开始转移
		return s.handleSlotStartElection(m)
	case clusterevent.EventTypeSlotLeaderTransferCheck: // 检查槽的领导是否可以转移了
		return s.handleSlotLeaderTransferCheck(m)
	case clusterevent.EventTypeSlotLeaderTransferStart: // 槽的领导开始转移
		return s.handleSlotLeaderTransferStart(m)
	case clusterevent.EventTypeSlotMigratePrepared: // 槽的迁移已准备
		return s.handleSlotMigratePrepared(m)
	case clusterevent.EventTypeSlotLearnerCheck: // 检查学习者，该毕业的就毕业
		return s.handleSlotLearnerCheck(m)
	case clusterevent.EventTypeSlotLearnerToReplica: // 学习者转副本
		return s.handleSlotLearnerToReplica(m)
	case clusterevent.EventTypeNodeJoinSuccess:
		return s.handleNodeJoinSuccess(m)
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

func (s *Server) addOrUpdateNode(id uint64, addr string) {
	if !s.nodeManager.exist(id) && strings.TrimSpace(addr) != "" {
		s.nodeManager.addNode(s.newNodeByNodeInfo(id, addr))
		return
	}
	n := s.nodeManager.node(id)
	if n != nil && n.addr != addr {
		s.nodeManager.removeNode(n.id)
		n.stop()
		s.nodeManager.addNode(s.newNodeByNodeInfo(id, addr))
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
		s.Error("leader not exist", zap.Uint64("leaderId", leaderId))
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
		s.Warn("handleNodeOnlineChange failed, not leader")
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

func (s *Server) handleSlotUpdate(m clusterevent.Message) {
	for _, slot := range m.Slots {
		if !wkutil.ArrayContainsUint64(slot.Replicas, s.opts.NodeId) {
			continue
		}
		s.addOrUpdateSlot(slot)
	}
}

func (s *Server) handleProposeSlots(m clusterevent.Message) error {
	if len(m.Slots) == 0 {
		return nil // 如果没有需要处理的slots，则认为处理完毕，返回true
	}

	newCfg := s.clusterEventServer.Config().Clone()
	for _, newSlot := range m.Slots {
		for i, slot := range newCfg.Slots {
			if slot.Id == newSlot.Id {
				newCfg.Slots[i] = newSlot
				break
			}
		}
	}
	newCfg.Version++
	err := s.clusterEventServer.ProposeConfig(s.cancelCtx, newCfg)
	if err != nil {
		s.Error("propose slot election config failed", zap.Error(err))
		return err
	}
	return nil

	// electionSlotLeaderMap := make(map[uint64][]uint32) // 参与选举的节点Id和对应的槽Id集合
	// electionSlotIds := make([]uint32, 0, len(m.Slots))
	// for _, slot := range m.Slots {
	// 	if slot.Leader == 0 {
	// 		continue
	// 	}
	// 	if s.clusterEventServer.NodeOnline(slot.Leader) {
	// 		continue
	// 	}
	// 	for _, replicaId := range slot.Replicas {
	// 		if !s.clusterEventServer.NodeOnline(replicaId) {
	// 			continue
	// 		}
	// 		electionSlotLeaderMap[replicaId] = append(electionSlotLeaderMap[replicaId], slot.Id)
	// 	}
	// 	electionSlotIds = append(electionSlotIds, slot.Id)
	// }
	// if len(electionSlotLeaderMap) == 0 {
	// 	return nil
	// }

	// // 通知选举的节点对应的slot变更为Candidate状态
	// err := s.requestChangeSlotRole(electionSlotLeaderMap)
	// if err != nil {
	// 	s.Error("request change slot role error", zap.Error(err))
	// 	return err
	// }

	// // 获取槽在各个副本上的日志信息
	// slotInfoResps, err := s.requestSlotInfos(electionSlotLeaderMap)
	// if err != nil {
	// 	s.Error("request slot infos error", zap.Error(err))
	// 	return err
	// }
	// if len(slotInfoResps) == 0 {
	// 	return nil
	// }

	// // ================== 根据日志信息选举新的leader ==================
	// // 收集各个槽的日志信息
	// replicaLastLogInfoMap, err := s.collectSlotLogInfo(slotInfoResps)
	// if err != nil {
	// 	s.Error("collect slot log info error", zap.Error(err))
	// 	return err
	// }
	// // 根据日志信息计算出槽的领导者
	// slotLeaderMap := s.calculateSlotLeader(replicaLastLogInfoMap, electionSlotIds)
	// if len(slotLeaderMap) == 0 {
	// 	s.Warn("没有选举出任何槽领导者！！！", zap.Uint32s("slotIds", electionSlotIds))
	// 	return nil
	// }

	// // ================== 提案新的配置 ==================
	// newCfg := s.clusterEventServer.Config().Clone()
	// getSlot := func(slotId uint32) *pb.Slot {
	// 	for _, slot := range newCfg.Slots {
	// 		if slot.Id == slotId {
	// 			return slot
	// 		}
	// 	}
	// 	return nil
	// }
	// for slotID, leaderNodeID := range slotLeaderMap {
	// 	if leaderNodeID == 0 {
	// 		continue
	// 	}
	// 	slot := getSlot(slotID)
	// 	if slot.Leader != leaderNodeID {
	// 		slot.Leader = leaderNodeID
	// 		slot.Term++
	// 	}
	// }
	// newCfg.Version++
	// err = s.clusterEventServer.ProposeConfig(s.cancelCtx, newCfg)
	// if err != nil {
	// 	s.Error("propose slot election config failed", zap.Error(err))
	// 	return err
	// }

}

func (s *Server) handleSlotStartElection(m clusterevent.Message) error {
	electionSlotLeaderMap := make(map[uint64][]uint32) // 参与选举的节点Id和对应的槽Id集合
	electionSlotIds := make([]uint32, 0, len(m.Slots))
	for _, slot := range m.Slots {

		if slot.Status != pb.SlotStatus_SlotStatusCandidate {
			continue
		}
		for _, replicaId := range slot.Replicas {
			// 不在线的副本 不能参与选举
			if !s.clusterEventServer.NodeOnline(replicaId) {
				continue
			}

			// 如果副本的分布式配置的版本小于当前领导的则也不能参与选举，
			// 这样做的目的防止旧领导slot的状态没更新到Candidate，导致slot继续在接受日志，
			// 这时候通过日志选举出来的领导肯定不可信
			if s.clusterEventServer.NodeConfigVersion(replicaId) < s.clusterEventServer.AppliedConfig().Version {
				s.Warn("replica config version < leader config version,stop election", zap.Uint64("replicaCfgVersion", s.clusterEventServer.NodeConfigVersion(replicaId)), zap.Uint64("leaderCfgVersion", s.clusterEventServer.AppliedConfig().Version))
				return nil
			}

			electionSlotLeaderMap[replicaId] = append(electionSlotLeaderMap[replicaId], slot.Id)
		}
		electionSlotIds = append(electionSlotIds, slot.Id)
	}
	if len(electionSlotLeaderMap) == 0 {
		s.Debug("没有需要选举的槽！！！")
		return nil
	}

	// 获取槽在各个副本上的日志信息
	slotInfoResps, err := s.requestSlotInfos(electionSlotLeaderMap)
	if err != nil {
		s.Error("request slot infos error", zap.Error(err))
		return err
	}
	if len(slotInfoResps) == 0 {
		s.Debug("没有获取到槽的日志信息！！！")
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
		for _, slot := range m.Slots {
			if slot.Id == slotId {
				return slot
			}
		}
		return nil
	}
	newSlots := make([]*pb.Slot, 0, len(m.Slots))
	for slotID, leaderNodeID := range slotLeaderMap {
		if leaderNodeID == 0 {
			continue
		}
		slot := getSlot(slotID)
		if slot != nil {
			if slot.Leader != leaderNodeID {
				newSlot := slot.Clone()
				newSlot.Leader = leaderNodeID
				newSlot.Term++
				newSlot.Status = pb.SlotStatus_SlotStatusNormal // 槽状态变更正常
				s.Debug("slot leader election success", zap.Uint32("slotId", slotID), zap.Uint64("newLeader", leaderNodeID), zap.Uint64("leaderTransferTo", slot.LeaderTransferTo))
				if newSlot.LeaderTransferTo != 0 && newSlot.LeaderTransferTo == leaderNodeID {
					newSlot.LeaderTransferTo = 0
				}
				newSlots = append(newSlots, newSlot)
			}
		}
	}

	if len(newSlots) == 0 {
		return nil
	}

	for i, slot := range newCfg.Slots {
		for _, newSlot := range newSlots {
			if slot.Id == newSlot.Id {
				newCfg.Slots[i] = newSlot
				break
			}
		}
	}

	err = s.clusterEventServer.ProposeConfig(s.cancelCtx, newCfg)
	if err != nil {
		s.Error("propose slot election config failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) requestChangeSlotRole(nodeSlotsMap map[uint64][]uint32) error {

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*4)
	defer cancel()
	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	for nodeId, slotIds := range nodeSlotsMap {

		if nodeId == s.opts.NodeId {
			for _, slotId := range slotIds {
				handler := s.slotManager.get(slotId)
				if handler == nil {
					continue
				}
				st := handler.(*slot)
				st.changeRole(replica.RoleCandidate)

			}
			continue
		}

		requestGroup.Go(func(nID uint64, sids []uint32) func() error {
			return func() error {
				return s.nodeManager.requestChangeSlotRole(nID, &ChangeSlotRoleReq{
					Role:    replica.RoleCandidate,
					SlotIds: sids,
				})
			}
		}(nodeId, slotIds))
	}
	return requestGroup.Wait()
}

// 请求槽的日志高度
func (s *Server) requestSlotInfos(waitElectionSlots map[uint64][]uint32) ([]*SlotLogInfoResp, error) {
	slotInfoResps := make([]*SlotLogInfoResp, 0, len(waitElectionSlots))
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*4)
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
	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, time.Second*2)
	defer cancel()
	resp, err := s.nodeManager.requestSlotLogInfo(timeoutCtx, nodeId, req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Server) slotInfos(slotIds []uint32) ([]SlotInfo, error) {
	slotInfos := make([]SlotInfo, 0, len(slotIds))
	for _, slotId := range slotIds {
		slotHandler := s.slotManager.get(slotId)
		if slotHandler == nil {
			continue
		}
		st := slotHandler.(*slot)
		lastLogIndex, term := st.LastLogIndexAndTerm()
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
// slotLogInfos 槽在各个副本上的日志信息
// slotId 计算领导节点的槽Id
func (s *Server) calculateSlotLeaderBySlot(slotLogInfos map[uint64]map[uint32]SlotInfo, slotId uint32) uint64 {
	var leader uint64
	var maxLogIndex uint64
	var maxLogTerm uint32
	st := s.clusterEventServer.Slot(slotId)

	// 如果槽正在进行领导者转移，则优先计算转移节点的日志信息
	if st != nil && st.LeaderTransferTo != 0 {
		for replicaId, logIndexMap := range slotLogInfos {
			slotInfo, ok := logIndexMap[slotId]
			if !ok {
				continue
			}
			if replicaId == st.LeaderTransferTo {
				leader = replicaId
				maxLogIndex = slotInfo.LogIndex
				maxLogTerm = slotInfo.LogTerm
				break
			}
		}
	}

	for replicaId, logIndexMap := range slotLogInfos {
		slotInfo, ok := logIndexMap[slotId]
		if !ok {
			continue
		}
		if leader == 0 {
			leader = replicaId
			maxLogIndex = slotInfo.LogIndex
			maxLogTerm = slotInfo.LogTerm
			continue
		}
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

func (s *Server) handleSlotLeaderTransferCheck(m clusterevent.Message) error {
	if len(m.Slots) == 0 {
		return nil
	}

	nodeSlotMap := make(map[uint64][]uint32) // 转移的节点和对应的槽
	for _, st := range m.Slots {
		if st.LeaderTransferTo == 0 || st.Leader == st.LeaderTransferTo {
			continue
		}
		nodeSlotMap[st.LeaderTransferTo] = append(nodeSlotMap[st.LeaderTransferTo], st.Id)
		nodeSlotMap[st.Leader] = append(nodeSlotMap[st.Leader], st.Id)
	}

	// 请求槽的日志信息
	slotLogInfoResps, err := s.requestSlotInfos(nodeSlotMap)
	if err != nil {
		s.Error("handleSlotLeaderTransferCheck: request slot infos error", zap.Error(err))
		return err
	}

	// 收集各个槽的日志信息
	replicaLastLogInfoMap, err := s.collectSlotLogInfo(slotLogInfoResps)
	if err != nil {
		s.Error("handleSlotLeaderTransferCheck: collect slot log info error", zap.Error(err))
		return err
	}

	newCfg := s.clusterEventServer.Config().Clone()
	hasChange := false

	for _, st := range m.Slots {
		if st.LeaderTransferTo == 0 || st.Leader == st.LeaderTransferTo {
			continue
		}
		leaderSlotInfoMap := replicaLastLogInfoMap[st.Leader]
		transferSlotInfoMap := replicaLastLogInfoMap[st.LeaderTransferTo]
		if leaderSlotInfoMap == nil || transferSlotInfoMap == nil {
			continue
		}
		leaderSlotLogInfo, ok := leaderSlotInfoMap[st.Id]
		if !ok {
			continue
		}
		transferSlotLogInfo := transferSlotInfoMap[st.Id]
		if leaderSlotLogInfo.LogIndex < transferSlotLogInfo.LogIndex+s.opts.LeaderTransferMinLogGap { // 如果未来领导者的日志跟现任领导者的日志差距小于最小日志差距，则可以进行转移操作
			newSlot := st.Clone()
			newSlot.Status = pb.SlotStatus_SlotStatusLeaderTransfer // 槽状态变为可转移
			for i, oldSlot := range newCfg.Slots {
				if oldSlot.Id == newSlot.Id {
					newCfg.Slots[i] = newSlot
					hasChange = true
					break
				}
			}
		}
	}

	if hasChange {
		err = s.clusterEventServer.ProposeConfig(s.cancelCtx, newCfg)
		if err != nil {
			s.Error("propose slot election config failed", zap.Error(err))
			return err
		}
	}
	return nil
}

func (s *Server) handleSlotLeaderTransferStart(m clusterevent.Message) error {

	if len(m.Slots) == 0 {
		return nil
	}

	nodeSlotMap := make(map[uint64][]uint32) // 转移的节点和对应的槽
	for _, st := range m.Slots {
		if st.LeaderTransferTo == 0 || st.Leader == st.LeaderTransferTo {
			continue
		}
		if st.Status != pb.SlotStatus_SlotStatusLeaderTransfer {
			continue
		}
		nodeSlotMap[st.LeaderTransferTo] = append(nodeSlotMap[st.LeaderTransferTo], st.Id)
		nodeSlotMap[st.Leader] = append(nodeSlotMap[st.Leader], st.Id)
	}

	// 请求槽的日志信息
	slotLogInfoResps, err := s.requestSlotInfos(nodeSlotMap)
	if err != nil {
		s.Error("handleSlotLeaderTransferCheck: request slot infos error", zap.Error(err))
		return err
	}
	// 收集各个槽的日志信息
	replicaLastLogInfoMap, err := s.collectSlotLogInfo(slotLogInfoResps)
	if err != nil {
		s.Error("handleSlotLeaderTransferCheck: collect slot log info error", zap.Error(err))
		return err
	}

	if len(replicaLastLogInfoMap) == 0 {
		s.Warn("replicaLastLogInfoMap is empty")
		return nil
	}

	var newSlots []*pb.Slot
	for _, st := range m.Slots {

		oldLeaderCfgVersion := s.clusterEventServer.NodeConfigVersion(st.Leader)
		newLeaderCfgVersion := s.clusterEventServer.NodeConfigVersion(st.LeaderTransferTo)
		currentCfgVersion := s.clusterEventServer.AppliedConfig().Version

		// 如果槽新领导和槽旧领导的配置都小于当前节点领导的配置，则不能安全选举
		if oldLeaderCfgVersion < currentCfgVersion || newLeaderCfgVersion < currentCfgVersion {
			s.Warn("not safe transfer slot leader, stopped", zap.Uint32("slotId", st.Id), zap.Uint64("oldLeader", st.Leader), zap.Uint64("newLeader", st.LeaderTransferTo), zap.Uint64("oldLeaderCfgVersion", oldLeaderCfgVersion), zap.Uint64("newLeaderCfgVersion", newLeaderCfgVersion))
			continue
		}

		leaderSlotInfoMap := replicaLastLogInfoMap[st.Leader]
		transferSlotInfoMap := replicaLastLogInfoMap[st.LeaderTransferTo]
		if leaderSlotInfoMap == nil || transferSlotInfoMap == nil {
			s.Warn("leader or transfer slot info not found", zap.Uint32("slotId", st.Id))
			continue
		}
		leaderSlotLogInfo, ok := leaderSlotInfoMap[st.Id]
		if !ok {
			s.Warn("leader slot log info not found", zap.Uint32("slotId", st.Id))
			continue
		}
		transferSlotLogInfo := transferSlotInfoMap[st.Id]
		// 未来领导的日志完全跟上了现任领导，则可以安全转移领导角色了
		if leaderSlotLogInfo.LogIndex <= transferSlotLogInfo.LogIndex {
			newSlot := st.Clone()
			newSlot.Status = pb.SlotStatus_SlotStatusCandidate // 修改槽状态为选举状态
			newSlots = append(newSlots, newSlot)
		} else {
			s.Info("the log has not been followed", zap.Uint64("transfer", st.LeaderTransferTo), zap.Uint64("leaderLogIndex", leaderSlotLogInfo.LogIndex), zap.Uint64("transferLogIndex", transferSlotLogInfo.LogIndex), zap.Uint32("slotId", st.Id), zap.Uint64("leader", st.Leader))
		}
	}

	if len(newSlots) == 0 {
		return nil
	}
	newCfg := s.clusterEventServer.Config().Clone()
	for i, oldSlot := range newCfg.Slots {
		for _, newSlot := range newSlots {
			if oldSlot.Id == newSlot.Id {
				newCfg.Slots[i] = newSlot
				break
			}
		}
	}
	err = s.clusterEventServer.ProposeConfig(s.cancelCtx, newCfg)
	if err != nil {
		s.Error("propose slot election config failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) handleSlotMigratePrepared(m clusterevent.Message) error {
	if len(m.Slots) == 0 {
		return nil
	}
	newNode := m.Nodes[0]
	newNode.Status = pb.NodeStatus_NodeStatusJoining
	newCfg := s.clusterEventServer.Config().Clone()
	for i, n := range newCfg.Nodes {
		if n.Id == newNode.Id {
			newCfg.Nodes[i] = newNode
			break
		}
	}
	for i, slot := range newCfg.Slots {
		for _, st := range m.Slots {
			if slot.Id == st.Id {
				newCfg.Slots[i] = st
				break
			}
		}
	}
	err := s.clusterEventServer.ProposeConfig(s.cancelCtx, newCfg)
	if err != nil {
		s.Error("propose slot election config failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) handleSlotLearnerCheck(_ clusterevent.Message) error {

	slots := s.clusterEventServer.Slots()
	if len(slots) == 0 {
		return nil
	}

	hasNeedLearn := false // 是否有需要学习的学习者
	for _, st := range slots {
		if len(st.Learners) == 0 {
			continue
		}
		for _, learner := range st.Learners {
			if learner.Status == pb.LearnerStatus_LearnerStatusLearning {
				hasNeedLearn = true
				break
			}
		}
		if hasNeedLearn {
			break
		}
	}

	if !hasNeedLearn {
		return nil
	}

	nodeSlotMap := make(map[uint64][]uint32) // 转移的节点和对应的槽
	for _, st := range slots {
		if len(st.Learners) == 0 {
			continue
		}
		if st.Leader == 0 { // 学习都是学习领导的日志，没领导了，就没必要学习了
			continue
		}
		for _, learner := range st.Learners {
			if learner.Status == pb.LearnerStatus_LearnerStatusGraduate { // 已毕业的节点不参与检查
				continue
			}
			nodeSlotMap[learner.LearnerId] = append(nodeSlotMap[learner.LearnerId], st.Id)
		}
		nodeSlotMap[st.Leader] = append(nodeSlotMap[st.Leader], st.Id)
	}

	// 请求槽的日志信息
	slotLogInfoResps, err := s.requestSlotInfos(nodeSlotMap)
	if err != nil {
		s.Error("handleSlotLeaderTransferCheck: request slot infos error", zap.Error(err))
		return err
	}
	// 收集各个槽的日志信息
	replicaLastLogInfoMap, err := s.collectSlotLogInfo(slotLogInfoResps)
	if err != nil {
		s.Error("handleSlotLeaderTransferCheck: collect slot log info error", zap.Error(err))
		return err
	}

	var newSlots []*pb.Slot
	for _, st := range slots {
		if len(st.Learners) == 0 {
			continue
		}
		leaderCfgVersion := s.clusterEventServer.NodeConfigVersion(st.Leader) // 槽领导节点的配置版本
		currentCfgVersion := s.clusterEventServer.AppliedConfig().Version     // 当前领导节点的配置版本
		for _, learner := range st.Learners {
			if learner.Status == pb.LearnerStatus_LearnerStatusGraduate { // 已毕业的节点不参与检查
				continue
			}
			learnerCfgVersion := s.clusterEventServer.NodeConfigVersion(learner.LearnerId) // 学习者的配置版本

			//  如果槽领导节点或学习者的节点的配置小于当前领导节点的配置，则不能安全学习
			if leaderCfgVersion < currentCfgVersion || learnerCfgVersion < currentCfgVersion {
				s.Warn("not safe learn, stopped", zap.Uint32("slotId", st.Id), zap.Uint64("leader", st.Leader), zap.Uint64("learner", learner.LearnerId), zap.Uint64("leaderCfgVersion", leaderCfgVersion), zap.Uint64("learnerCfgVersion", learnerCfgVersion))
				continue
			}

			leaderSlotInfoMap := replicaLastLogInfoMap[st.Leader]
			learnerSlotInfoMap := replicaLastLogInfoMap[learner.LearnerId]
			if leaderSlotInfoMap == nil || learnerSlotInfoMap == nil {
				s.Debug("leader or learner slot info not found", zap.Uint32("slotId", st.Id), zap.Uint64("leader", st.Leader), zap.Uint64("learner", learner.LearnerId))
				continue
			}
			leaderSlotLogInfo, ok := leaderSlotInfoMap[st.Id] // 槽领导者的日志信息
			if !ok {
				s.Debug("leader slot info not found", zap.Uint32("slotId", st.Id), zap.Uint64("leader", st.Leader), zap.Uint64("learner", learner.LearnerId))
				continue
			}
			learnerSlotLogInfo := learnerSlotInfoMap[st.Id] // 学习者的日志信息
			// 学习者的日志跟上了领导者的日志，则表示学习完成
			if leaderSlotLogInfo.LogIndex > learnerSlotLogInfo.LogIndex+s.opts.LearnerMinLogGap {
				s.Debug("learner log is behind leader log", zap.Uint32("slotId", st.Id), zap.Uint64("leader", st.Leader), zap.Uint64("learner", learner.LearnerId), zap.Uint64("leaderLogIndex", leaderSlotLogInfo.LogIndex), zap.Uint64("learnerLogIndex", learnerSlotLogInfo.LogIndex))
				continue
			}
			newSlot := st.Clone()
			for _, oldLearner := range newSlot.Learners {
				if oldLearner.LearnerId == learner.LearnerId {
					oldLearner.Status = pb.LearnerStatus_LearnerStatusGraduate // 修改学习者的状态为已毕业
					break
				}
			}
			newSlots = append(newSlots, newSlot)
		}
	}
	if len(newSlots) == 0 {
		return nil
	}

	newCfg := s.clusterEventServer.Config().Clone()
	for i, oldSlot := range newCfg.Slots {
		for _, newSlot := range newSlots {
			if oldSlot.Id == newSlot.Id {
				newCfg.Slots[i] = newSlot
				break
			}
		}
	}
	err = s.clusterEventServer.ProposeConfig(s.cancelCtx, newCfg)
	if err != nil {
		s.Error("propose slot learner check failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) handleSlotLearnerToReplica(m clusterevent.Message) error {
	if len(m.Slots) == 0 {
		return nil
	}

	newCfg := s.clusterEventServer.Config().Clone()
	for i, slot := range newCfg.Slots {
		for _, st := range m.Slots {
			if slot.Id == st.Id {
				newCfg.Slots[i] = st
				break
			}
		}
	}
	err := s.clusterEventServer.ProposeConfig(s.cancelCtx, newCfg)
	if err != nil {
		s.Error("propose slot election config failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) handleNodeJoinSuccess(m clusterevent.Message) error {
	if len(m.Nodes) == 0 {
		return nil
	}
	newCfg := s.clusterEventServer.Config().Clone()
	exist := false
	for i, node := range newCfg.Nodes {
		for _, n := range m.Nodes {
			if node.Id == n.Id {
				n.Status = pb.NodeStatus_NodeStatusJoined
				newCfg.Nodes[i] = n
				exist = true
				break
			}
		}
	}
	if !exist {
		return nil
	}
	err := s.clusterEventServer.ProposeConfig(s.cancelCtx, newCfg)
	if err != nil {
		s.Error("propose node join success config failed", zap.Error(err))
		return err
	}
	return nil
}
