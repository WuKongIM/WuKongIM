package cluster

import (
	"context"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// 分布式配置改变
func (s *Server) onClusterConfigChange(cfg *pb.Config) {
	if s.stopped.Load() {
		s.Info("server stopped")
		return
	}
	err := s.handleClusterConfigChange(cfg)
	if err != nil {
		s.Error("handleClusterConfigChange failed", zap.Error(err))
	}
}

// 处理槽选举
func (s *Server) onSlotElection(slots []*pb.Slot) error {
	// fmt.Println("onSlotElection----->", slots)
	if s.stopped.Load() {
		s.Info("server stopped")
		return nil
	}
	err := s.handleSlotElection(slots)
	if err != nil {
		s.Error("handleSlotElection failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) handleClusterConfigChange(cfg *pb.Config) error {

	// ================== 处理节点 ==================
	err := s.handleClusterConfigNodeChange(cfg)
	if err != nil {
		s.Error("handleClusterConfigNodeChange failed", zap.Error(err))
		return err
	}

	// ================== 处理槽 ==================
	err = s.handleClusterConfigSlotChange(cfg)
	if err != nil {
		s.Error("handleClusterConfigSlotChange failed", zap.Error(err))
		return err
	}

	return err
}

func (s *Server) handleClusterConfigNodeChange(cfg *pb.Config) error {

	// 添加新节点
	for _, node := range cfg.Nodes {
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

	// 移除节点
	for _, node := range s.nodeManager.nodes() {
		exist := false
		for _, cfgNode := range cfg.Nodes {
			if node.id == cfgNode.Id {
				exist = true
				break
			}
		}
		if !exist {
			s.nodeManager.removeNode(node.id)
			node.stop()
		}
	}

	// 修改节点
	for _, cfgNode := range cfg.Nodes {
		if cfgNode.Id == s.opts.NodeId {
			continue
		}
		if !s.nodeManager.exist(cfgNode.Id) {
			continue
		}
		n := s.nodeManager.node(cfgNode.Id)
		if n != nil && n.addr != cfgNode.ClusterAddr {
			s.nodeManager.removeNode(n.id)
			n.stop()
			s.nodeManager.addNode(s.newNodeByNodeInfo(cfgNode.Id, cfgNode.ClusterAddr))
		}
	}

	return nil
}

func (s *Server) handleClusterConfigSlotChange(cfg *pb.Config) error {

	// 添加属于此节点的槽
	for _, slot := range cfg.Slots {
		if s.slotManager.exist(slot.Id) {
			continue
		}
		if !wkutil.ArrayContainsUint64(slot.Replicas, s.opts.NodeId) && !wkutil.ArrayContainsUint64(slot.Learners, s.opts.NodeId) {
			continue
		}
		s.addSlot(slot)
	}

	// 移除不属于此节点的槽
	removeSlotIds := make([]uint32, 0)
	s.slotManager.iterate(func(slot *slot) bool {
		for _, cfgSlot := range cfg.Slots {
			if slot.st.Id == cfgSlot.Id {
				if !wkutil.ArrayContainsUint64(cfgSlot.Replicas, s.opts.NodeId) && !wkutil.ArrayContainsUint64(cfgSlot.Learners, s.opts.NodeId) {
					removeSlotIds = append(removeSlotIds, slot.st.Id)
				}
				break
			}
		}
		return true
	})
	if len(removeSlotIds) > 0 {
		for _, slotId := range removeSlotIds {
			s.slotManager.remove(slotId)
		}
	}

	// 处理槽修改
	for _, cfgSlot := range cfg.Slots {

		slot := s.slotManager.get(cfgSlot.Id)
		if slot == nil {
			continue
		}

		if !cfgSlot.Equal(slot.st) {
			s.addOrUpdateSlot(cfgSlot)
		}
	}

	return nil
}

func (s *Server) handleSlotElection(slots []*pb.Slot) error {
	if len(slots) == 0 {
		return nil
	}
	electionSlotLeaderMap := make(map[uint64][]uint32) // 参与选举的节点Id和对应的槽Id集合
	electionSlotIds := make([]uint32, 0, len(slots))
	for _, slot := range slots {

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
			if s.clusterEventServer.NodeConfigVersion(replicaId) < s.clusterEventServer.Config().Version {
				s.Warn("replica config version < leader config version,stop election", zap.Uint64("replicaCfgVersion", s.clusterEventServer.NodeConfigVersion(replicaId)), zap.Uint64("leaderCfgVersion", s.clusterEventServer.Config().Version))
				return nil
			}

			electionSlotLeaderMap[replicaId] = append(electionSlotLeaderMap[replicaId], slot.Id)
		}
		electionSlotIds = append(electionSlotIds, slot.Id)
	}
	if len(electionSlotLeaderMap) == 0 {
		s.Info("没有需要选举的槽！！！")
		return nil
	}

	// 获取槽在各个副本上的日志信息
	slotInfoResps, err := s.requestSlotInfos(electionSlotLeaderMap)
	if err != nil {
		s.Error("request slot infos error", zap.Error(err))
		return err
	}
	if len(slotInfoResps) == 0 {
		s.Info("没有获取到槽的日志信息！！！")
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

	// ================== 提案新的槽配置 ==================
	getSlot := func(slotId uint32) *pb.Slot {
		for _, slot := range slots {
			if slot.Id == slotId {
				return slot
			}
		}
		return nil
	}

	newSlots := make([]*pb.Slot, 0, len(slots))
	for slotId, newLeaderId := range slotLeaderMap {
		if newLeaderId == 0 { // 没有选出领导忽略掉
			s.Info("no leader elected", zap.Uint32("slotId", slotId))
			continue
		}
		slot := getSlot(slotId)
		if slot != nil {
			if slot.Leader != newLeaderId {
				newSlot := slot.Clone()
				newSlot.Leader = newLeaderId
				newSlot.Term++
				newSlot.ExpectLeader = 0
				newSlot.Status = pb.SlotStatus_SlotStatusNormal // 槽状态变更正常
				s.Info("slot leader election success", zap.Uint32("slotId", slotId), zap.Uint64("newLeader", newLeaderId))
				if newSlot.MigrateTo != 0 && newSlot.MigrateTo == newLeaderId {
					newSlot.MigrateFrom = 0
					newSlot.MigrateTo = 0
					newSlot.Learners = wkutil.RemoveUint64(newSlot.Learners, newLeaderId)
				}
				newSlots = append(newSlots, newSlot)
			}
		}
	}

	if len(newSlots) > 0 {
		err = s.clusterEventServer.ProposeSlots(newSlots)
		if err != nil {
			s.Error("handle slot election failed, propose slot failed", zap.Error(err))
			return err
		}
	}
	return nil
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
		st := s.slotManager.get(slotId)
		if st == nil {
			continue
		}
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
	if st != nil && st.ExpectLeader != 0 && st.ExpectLeader != st.Leader {
		for replicaId, logIndexMap := range slotLogInfos {
			slotInfo, ok := logIndexMap[slotId]
			if !ok {
				continue
			}
			if replicaId == st.ExpectLeader {
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
