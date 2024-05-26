package clusterevent

import (
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (s *Server) loop() {
	tk := time.NewTicker(time.Millisecond * 250)
	for !s.stopped.Load() {
		if s.hasReady() {
			msgs := s.ready()
			s.opts.Ready(msgs)
		}
		select {
		case <-tk.C:
			s.tick()
		case <-s.advanceC:
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Server) advance() {
	select {
	case <-s.advanceC:
	case <-s.stopper.ShouldStop():
	default:
	}
}

func (s *Server) hasReady() bool {
	return len(s.msgs) > 0
}

func (s *Server) ready() []Message {
	msgs := s.msgs

	return msgs
}

func (s *Server) acceptReady() {
	s.msgs = s.msgs[:0]
}

func (s *Server) tick() {

	s.checkConifgChange() // 检查配置内容是否改变，通过改变的内容触发事件

	if len(s.msgs) > 0 { // 如果有事件，则直接返回，先处理配置改变事件
		return
	}

	if s.IsLeader() {
		// 检查节点在线状态
		hasEvent := s.checkNodeOnlineStatus()
		if hasEvent {
			return
		}
		// 检查槽的领导是否有效，如果槽的领导节点离线，则槽变为候选状态
		hasEvent = s.checkSlotLeader()
		if hasEvent {
			return
		}

		// 检查槽是否在候选状态，如果在候选状态应该开始选举
		hasEvent = s.checkSlotElection()
		if hasEvent {
			return
		}
		// 检查槽是否可以执行领导转移
		hasEvent = s.checkSlotTransferLeader()
		if hasEvent {
			return
		}
		// 开始槽的领导转移
		hasEvent = s.startSlotTransferLeader()
		if hasEvent {
			return
		}
		// 检查是否有新节点加入，如果有新节点加入，则预备迁移槽
		hasEvent = s.checkSlotMigrate()
		if hasEvent {
			return
		}
		// 检查学习者日志是否达到条件
		hasEvent = s.checkLearnerLog()
		if hasEvent {
			return
		}
		// 检查是否开始迁移槽
		hasEvent = s.checkSlotMigrateStart()
		if hasEvent {
			return
		}
		// 检查节点是否加入成功
		hasEvent = s.checkNodeJoinSuccess()
		if hasEvent {
			return
		}

		// 自动平衡槽的领导，尽量让槽领导平均分配到各个节点上
		_ = s.autoBalanceSlotLeaders()

	}

}

// 检查配置内容是否有变化
func (s *Server) checkConifgChange() {
	if s.preRemoteCfg.Version < s.cfgServer.AppliedConfig().Version {
		s.preRemoteCfg = s.cfgServer.AppliedConfig().Clone()
	}

	if s.localCfg.Version >= s.preRemoteCfg.Version {
		if s.localCfg.Version > s.preRemoteCfg.Version {
			s.Warn("local config version > remote config version", zap.Uint64("version", s.localCfg.Version), zap.Uint64("remoteVersion", s.preRemoteCfg.Version))
		}
		return
	}

	// 检查自己的apiUrl是否被提案
	if strings.TrimSpace(s.opts.ApiServerAddr) != "" {
		localNode := s.cfgServer.Node(s.opts.NodeId)
		if localNode != nil && localNode.ApiServerAddr != s.opts.ApiServerAddr {
			s.msgs = append(s.msgs, Message{
				Type: EventTypeApiServerAddrUpdate,
			})
		}
	}

	// 检查节点的变化
	s.checkNodes()
	// 检查槽的变化
	_ = s.checkSlots()

	if s.localCfg.Version < s.preRemoteCfg.Version {
		err := s.saveLocalConfig(s.preRemoteCfg)
		if err != nil {
			s.Warn("save local config error", zap.Error(err))
		} else {
			s.localCfg = s.preRemoteCfg
		}
	}
}

func (s *Server) checkNodeOnlineStatus() bool {
	hasEvent := false
	// 节点在线状态检查
	s.pongTickMapLock.Lock()
	var changeNodes []*pb.Node
	for _, n := range s.Nodes() {
		if n.Id == s.opts.NodeId {
			continue
		}
		tk := s.pongTickMap[n.Id]
		tk += 1
		s.pongTickMap[n.Id] = tk

		if tk > s.opts.PongMaxTick && n.Online { // 超过最大pong tick数，认为节点离线
			cloneNode := n.Clone()
			cloneNode.Online = false
			cloneNode.OfflineCount++
			cloneNode.LastOffline = time.Now().Unix()
			changeNodes = append(changeNodes, cloneNode)

		} else if tk <= s.opts.PongMaxTick && !n.Online { // 原来是离线节点，现在在线
			cloneNode := n.Clone()
			cloneNode.Online = true
			changeNodes = append(changeNodes, cloneNode)
		}
	}
	if len(changeNodes) > 0 {
		hasEvent = true
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeNodeOnlieChange,
			Nodes: changeNodes,
		})
	}
	s.pongTickMapLock.Unlock()

	return hasEvent
}

func (s *Server) checkNodes() {
	// ==================== check add or update node ====================
	newNodes := make([]*pb.Node, 0, len(s.preRemoteCfg.Nodes))
	updateNodes := make([]*pb.Node, 0, len(s.preRemoteCfg.Nodes))
	for _, remoteNode := range s.preRemoteCfg.Nodes {
		exist := false
		update := false
		for _, localNode := range s.localCfg.Nodes {
			if remoteNode.Id == localNode.Id {
				exist = true
				if !remoteNode.Equal(localNode) {
					update = true
				}
				break
			}
		}
		if !exist {
			newNodes = append(newNodes, remoteNode)
		}
		if update {
			updateNodes = append(updateNodes, remoteNode)
		}
	}

	if len(newNodes) > 0 {
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeNodeAdd,
			Nodes: newNodes,
		})
	}
	if len(updateNodes) > 0 {
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeNodeUpdate,
			Nodes: updateNodes,
		})
	}

	// ==================== check delete node ====================
	deleteNodes := make([]*pb.Node, 0)
	for _, localNode := range s.localCfg.Nodes {
		exist := false
		for _, remoteNode := range s.preRemoteCfg.Nodes {
			if localNode.Id == remoteNode.Id {
				exist = true
			}
		}
		if !exist {
			deleteNodes = append(deleteNodes, localNode)
		}
	}
	if len(deleteNodes) > 0 {
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeNodeDelete,
			Nodes: deleteNodes,
		})
	}

}

func (s *Server) checkSlots() bool {
	// ==================== check add or update slot ====================
	newSlots := make([]*pb.Slot, 0)
	updateSlots := make([]*pb.Slot, 0, len(s.preRemoteCfg.Slots))

	hasEvent := false
	for _, remoteSlot := range s.preRemoteCfg.Slots {
		exist := false
		update := false
		for _, localSlot := range s.localCfg.Slots {
			if remoteSlot.Id == localSlot.Id {
				exist = true
				if !remoteSlot.Equal(localSlot) {
					update = true
				}
				break
			}
		}
		if !exist {
			newSlots = append(newSlots, remoteSlot)
		}
		if update {
			updateSlots = append(updateSlots, remoteSlot)
		}
	}
	if len(newSlots) > 0 {
		hasEvent = true
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeSlotAdd,
			Slots: newSlots,
		})
	}
	if len(updateSlots) > 0 {
		hasEvent = true
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeSlotUpdate,
			Slots: updateSlots,
		})
	}

	// // ==================== check delete slot ====================
	// deleteSlots := make([]*pb.Slot, 0)
	// for _, localSlot := range s.localCfg.Slots {
	// 	exist := false
	// 	for _, remoteSlot := range s.preRemoteCfg.Slots {
	// 		if localSlot.Id == remoteSlot.Id {
	// 			exist = true
	// 		}
	// 	}
	// 	if !exist {
	// 		deleteSlots = append(deleteSlots, localSlot)
	// 	}
	// }
	// if len(deleteSlots) > 0 {
	// 	hasEvent = true
	// 	s.msgs = append(s.msgs, Message{
	// 		Type:  EventTypeSlotDelete,
	// 		Slots: deleteSlots,
	// 	})
	// }
	return hasEvent
}

func (s *Server) checkSlotLeader() bool {

	if s.hasSlotStatusCandidate() {
		return false
	}
	// ==================== check need election slots ====================
	var needElectionSlots []*pb.Slot // 需要重新选举的槽
	for _, remoteSlot := range s.preRemoteCfg.Slots {
		slot := s.cfgServer.Slot(remoteSlot.Id)
		if slot == nil || remoteSlot.Leader == 0 {
			continue
		}
		if !s.cfgServer.NodeOnline(remoteSlot.Leader) && remoteSlot.Status != pb.SlotStatus_SlotStatusCandidate { // 领导节点没在线了，需要重新选举
			newSlot := remoteSlot.Clone()
			newSlot.Leader = 0
			newSlot.Status = pb.SlotStatus_SlotStatusCandidate
			needElectionSlots = append(needElectionSlots, newSlot)
		}
	}

	if len(needElectionSlots) == 0 {
		return false
	}
	s.msgs = append(s.msgs, Message{
		Type:  EventTypeSlotNeedElection,
		Slots: needElectionSlots,
	})

	return true

}

// 自动均衡槽的领导节点
func (s *Server) autoBalanceSlotLeaders() bool {

	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}

	// 判断是否有需要转移的槽领导，只要有就不执行自动均衡算法，等都转移完成后再执行
	for _, slot := range slots {
		if slot.LeaderTransferTo != 0 {
			return false
		}
	}

	hasEvent := false

	// 计算每个节点的槽数量和领导数量
	nodeSlotCountMap := make(map[uint64]uint32)   // 每个节点槽数量
	nodeLeaderCountMap := make(map[uint64]uint32) // 每个节点槽领导数量
	for _, slot := range slots {
		if slot.Leader == 0 {
			continue
		}
		if slot.LeaderTransferTo != 0 { // 已经需要转移的不参与计算
			continue
		}
		nodeLeaderCountMap[slot.Leader]++
		for _, replicaId := range slot.Replicas {
			nodeSlotCountMap[replicaId]++
		}
	}

	// ==================== 计算每个节点应该分配多少槽领导 ====================

	exportNodeLeaderCountMap := make(map[uint64]uint32) // 节点应该迁出领导数量
	importNodeLeaderCountMap := make(map[uint64]uint32) // 节点应该迁入领导数量

	firstSlot := slots[0]

	currentSlotReplicaCount := uint32(len(firstSlot.Replicas)) // 当前槽的副本数量

	for nodeId, slotCount := range nodeSlotCountMap {
		if slotCount == 0 {
			continue
		}
		leaderCount := nodeLeaderCountMap[nodeId]                 // 当前节点的领导数量
		avgLeaderCount := slotCount / currentSlotReplicaCount     // 此节点应该分配到的领导数量
		remaingLeaderCount := slotCount % currentSlotReplicaCount // 剩余的待分配的领导数量
		if remaingLeaderCount > 0 {
			avgLeaderCount += 1
		}

		// 如果当前节点的领导数量超过了平均领导数量，将一些槽的领导权移交给其他节点
		if leaderCount > avgLeaderCount {
			exportLeaderCount := leaderCount - avgLeaderCount
			exportNodeLeaderCountMap[nodeId] = exportLeaderCount
		} else if leaderCount < avgLeaderCount {
			importLeaderCount := avgLeaderCount - leaderCount
			importNodeLeaderCountMap[nodeId] = importLeaderCount
		}
	}

	// ==================== 迁移槽领导 ====================
	var newSlots []*pb.Slot
	for exportNodeId, exportLeaderCount := range exportNodeLeaderCountMap {
		if exportLeaderCount == 0 {
			continue
		}
		if !s.NodeOnline(exportNodeId) { // 节点不在线 不参与
			continue
		}
		for importNodeId, importLeaderCount := range importNodeLeaderCountMap {
			if importLeaderCount == 0 {
				continue
			}

			if !s.NodeOnline(importNodeId) { // 节点不在线 不参与
				continue
			}
			// 从exportNodeId迁移一个槽领导到importNodeId
			for _, slot := range slots {
				if slot.LeaderTransferTo != 0 { // 已经需要转移的不参与计算
					continue
				}
				if slot.Leader == exportNodeId && wkutil.ArrayContainsUint64(slot.Replicas, importNodeId) { // 只有这个槽的领导属于exportNodeId，且importNodeId是这个槽的副本节点才能转移
					newSlot := slot.Clone()
					newSlot.LeaderTransferTo = importNodeId
					newSlot.Learners = append(newSlot.Learners, &pb.Learner{
						LearnerId: importNodeId,
						Status:    pb.LearnerStatus_LearnerStatusLearning,
					})
					newSlots = append(newSlots, newSlot)
					exportLeaderCount--
					importLeaderCount--
					if exportLeaderCount == 0 || importLeaderCount == 0 {
						break
					}
				}
			}
		}
	}
	if len(newSlots) > 0 {
		hasEvent = true
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeSlotLeaderTransfer,
			Slots: newSlots,
		})
	}
	return hasEvent

}

func (s *Server) checkSlotElection() bool {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}

	// ==================== check need election slots ====================
	var needElectionSlots []*pb.Slot // 需要重新选举的槽
	for _, st := range slots {
		if st.Status == pb.SlotStatus_SlotStatusCandidate {
			needElectionSlots = append(needElectionSlots, st)
		}
	}

	if len(needElectionSlots) == 0 {
		return false
	}

	s.msgs = append(s.msgs, Message{
		Type:  EventTypeSlotElectionStart,
		Slots: needElectionSlots,
	})
	return true
}

func (s *Server) checkSlotTransferLeader() bool {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}

	hasEvent := false

	var newSlots []*pb.Slot
	for _, st := range slots {
		if st.LeaderTransferTo != 0 && st.Status == pb.SlotStatus_SlotStatusNormal {
			newSlots = append(newSlots, st)
		}
	}

	if len(newSlots) > 0 {
		hasEvent = true
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeSlotLeaderTransferCheck,
			Slots: newSlots,
		})
	}
	return hasEvent
}

func (s *Server) startSlotTransferLeader() bool {

	hasEvent := false
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}

	var newSlots []*pb.Slot
	for _, st := range slots {
		if st.Status == pb.SlotStatus_SlotStatusLeaderTransfer {
			newSlots = append(newSlots, st)
		}
	}

	if len(newSlots) > 0 {
		hasEvent = true
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeSlotLeaderTransferStart,
			Slots: newSlots,
		})
	}
	return hasEvent

}

func (s *Server) checkSlotMigrate() bool {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}

	if !s.allowMigrate() { // 是否允许迁移
		return false
	}

	var newNode *pb.Node
	voteNodes := s.cfgServer.AllowVoteNodes()
	for _, n := range voteNodes {
		if n.Id == s.opts.NodeId {
			continue
		}
		if !n.Online || !n.AllowVote { // 不在线的节点或者不允许投票的节点不参与
			continue
		}
		if n.Status == pb.NodeStatus_NodeStatusWillJoin {
			newNode = n
			break
		}
	}

	if newNode == nil {
		return false
	}

	firstSlot := slots[0]

	var newSlots []*pb.Slot

	if uint32(len(firstSlot.Replicas)) < s.cfgServer.SlotReplicaCount() { // 如果当前槽的副本数量小于配置的副本数量，则可以将新节点直接加入到学习节点中
		for _, slot := range slots {
			newSlot := slot.Clone()
			newSlot.Learners = append(slot.Learners, &pb.Learner{
				LearnerId: newNode.Id,
				Status:    pb.LearnerStatus_LearnerStatusLearning,
			})
			newSlots = append(newSlots, newSlot)
		}
	} else {
		voteNodeCount := uint32(len(voteNodes))                                                // 投票节点数量
		avgSlotCount := uint32(len(slots)) * s.cfgServer.SlotReplicaCount() / voteNodeCount    // 平均每个节点的槽数量
		remainSlotCount := uint32(len(slots)) * s.cfgServer.SlotReplicaCount() % voteNodeCount // 剩余的槽数量
		if remainSlotCount > 0 {
			avgSlotCount += 1
		}
		nodeSlotCountMap := make(map[uint64]uint32) // 每个节点目前的槽数量
		nodeSlotCountMap[newNode.Id] = 0            // 新节点槽数量肯定为0
		for _, slot := range slots {
			for _, replicaId := range slot.Replicas {
				nodeSlotCountMap[replicaId]++
			}
		}
		// 计算每个节点应该迁入/迁出的槽数量
		migrateToCountMap := make(map[uint64]uint32)   // 每个节点应该迁入的槽数量
		migrateFromCountMap := make(map[uint64]uint32) // 每个节点应该迁出的槽数量
		for nodeId, slotCount := range nodeSlotCountMap {
			if slotCount < avgSlotCount {
				migrateToCountMap[nodeId] = avgSlotCount - slotCount
			} else if slotCount > avgSlotCount {
				migrateFromCountMap[nodeId] = slotCount - avgSlotCount
			}
		}

		var migrateToNodeId uint64
		var migrateToCount uint32
		var migrateFromNodeId uint64
		var migrateFromCount uint32
		for _, slot := range slots {

			for migrateToNodeId, migrateToCount = range migrateToCountMap {
				if migrateToCount == 0 {
					continue
				}
				if wkutil.ArrayContainsUint64(slot.Replicas, migrateToNodeId) { // 如果副本集合里包含了迁出目标的节点，则不需要迁移
					continue
				}
				break
			}
			for migrateFromNodeId, migrateFromCount = range migrateFromCountMap {
				if migrateFromCount == 0 {
					continue
				}
				if !wkutil.ArrayContainsUint64(slot.Replicas, migrateFromNodeId) { // 如果副本集合中不包含迁出源节点，则不需要迁移
					continue
				}
				break
			}

			if migrateToNodeId == 0 || migrateFromNodeId == 0 {
				continue
			}

			if migrateToCount == 0 || migrateFromCount == 0 {
				continue
			}

			newSlot := slot.Clone()
			newSlot.MigrateFrom = migrateFromNodeId
			newSlot.MigrateTo = migrateToNodeId
			newSlot.Learners = append(newSlot.Learners, &pb.Learner{
				LearnerId: migrateToNodeId,
				Status:    pb.LearnerStatus_LearnerStatusLearning,
			})
			newSlots = append(newSlots, newSlot)
			migrateFromCountMap[migrateFromNodeId] = migrateFromCount - 1
			migrateToCountMap[migrateToNodeId] = migrateToCount - 1
		}
	}
	if len(newSlots) > 0 {
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeSlotMigratePrepared,
			Nodes: []*pb.Node{newNode.Clone()},
			Slots: newSlots,
		})
	}
	return true
}

func (s *Server) checkLearnerLog() bool {
	if !s.hasLearningLearner() {
		return false
	}

	if time.Since(s.preLearnCheckTime) < s.opts.LearnerCheckInterval { // 小于检查间隔时间
		return false
	}

	s.preLearnCheckTime = time.Now()

	s.msgs = append(s.msgs, Message{
		Type: EventTypeSlotLearnerCheck,
	})
	return true
}

func (s *Server) checkSlotMigrateStart() bool {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}

	if !s.hasGraduateLearner() { // 没有毕业的学习者
		return false
	}

	var newSlots []*pb.Slot
	for _, slot := range slots {
		if len(slot.Learners) == 0 {
			continue
		}
		for _, learner := range slot.Learners {
			if learner.Status != pb.LearnerStatus_LearnerStatusGraduate {
				continue
			}

			newSlot := slot.Clone()
			if slot.MigrateTo != 0 && slot.MigrateFrom != 0 {
				if slot.Leader == slot.MigrateTo { // 如果迁出的节点是槽是领导者，则需要先转移领导
					newSlot.LeaderTransferTo = slot.MigrateTo
					newSlot.Status = pb.SlotStatus_SlotStatusLeaderTransfer
				} else { // 如果迁出的槽不是领导者，则直接开始迁移
					if !wkutil.ArrayContainsUint64(slot.Replicas, slot.MigrateTo) { // 没在副本列表，则添加到副本列表里
						newSlot.Replicas = append(newSlot.Replicas, slot.MigrateTo)
					}
				}
				// 移除MigrateFrom的副本
				for i, replicaId := range newSlot.Replicas {
					if replicaId == slot.MigrateFrom {
						newSlot.Replicas = append(newSlot.Replicas[:i], newSlot.Replicas[i+1:]...)
						break
					}
				}
				newSlot.MigrateFrom = 0
				newSlot.MigrateTo = 0
			} else {
				if !wkutil.ArrayContainsUint64(slot.Replicas, learner.LearnerId) { // 没在副本列表，则添加到副本列表里
					newSlot.Replicas = append(newSlot.Replicas, learner.LearnerId)
				}
			}

			// 删除合并完成的学习者
			for i, lr := range newSlot.Learners {
				if lr.LearnerId == learner.LearnerId {
					newSlot.Learners = append(newSlot.Learners[:i], newSlot.Learners[i+1:]...)
					break
				}
			}
			newSlots = append(newSlots, newSlot)

		}
	}

	if len(newSlots) == 0 {
		return false
	}
	s.msgs = append(s.msgs, Message{
		Type:  EventTypeSlotLearnerToReplica,
		Slots: newSlots,
	})

	return false
}

func (s *Server) allowMigrate() bool {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}

	for _, st := range slots {
		if st.MigrateFrom != 0 || st.MigrateTo != 0 || len(st.Learners) > 0 {
			return false
		}
	}
	return true
}

func (s *Server) hasMigrate() bool {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}

	for _, st := range slots {
		if st.MigrateFrom != 0 || st.MigrateTo != 0 {
			return true
		}
	}
	return false
}

// 是否有学习者
func (s *Server) hasLearner() bool {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}

	for _, st := range slots {
		if len(st.Learners) > 0 {
			return true
		}
	}
	return false
}

// 是否有正在学习的学习者
func (s *Server) hasLearningLearner() bool {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}

	for _, st := range slots {
		for _, lr := range st.Learners {
			if lr.Status == pb.LearnerStatus_LearnerStatusLearning {
				return true
			}
		}
	}
	return false
}

// 是否有毕业的学习者
func (s *Server) hasGraduateLearner() bool {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}

	for _, st := range slots {
		for _, lr := range st.Learners {
			if lr.Status == pb.LearnerStatus_LearnerStatusGraduate {
				return true
			}
		}
	}
	return false
}

func (s *Server) hasSlotStatusCandidate() bool {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return false
	}
	for _, st := range slots {
		if st.Status == pb.SlotStatus_SlotStatusCandidate {
			return true
		}
	}
	return false
}

func (s *Server) checkNodeJoinSuccess() bool {
	nodes := s.cfgServer.Nodes()
	if len(nodes) == 0 {
		return false
	}

	for _, slot := range s.Slots() {
		if slot.Status != pb.SlotStatus_SlotStatusNormal {
			return false
		}
		if slot.LeaderTransferTo != 0 {
			return false
		}
		if slot.MigrateFrom != 0 || slot.MigrateTo != 0 {
			return false
		}
		if len(slot.Learners) > 0 {
			return false
		}
	}

	var joiningNode *pb.Node
	for _, n := range nodes {
		if n.Status == pb.NodeStatus_NodeStatusJoining {
			joiningNode = n
			break
		}
	}

	if joiningNode == nil {
		return false
	}

	s.msgs = append(s.msgs, Message{
		Type:  EventTypeNodeJoinSuccess,
		Nodes: []*pb.Node{joiningNode.Clone()},
	})

	return true
}
