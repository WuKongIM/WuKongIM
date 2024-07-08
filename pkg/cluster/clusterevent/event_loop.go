package clusterevent

import (
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (s *Server) loop() {
	tk := time.NewTicker(time.Millisecond * 250)

	for !s.stopped.Load() {
		err := s.checkClusterConfig()
		if err != nil {
			s.Error("checkClusterConfig failed", zap.Error(err))
		}

		select {
		case <-tk.C:
			s.tick()
		case nodeId := <-s.pongC:
			s.pongTickMap[nodeId] = 0
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Server) checkClusterConfig() error {

	// ================== 初始化配置 ==================
	if s.IsLeader() && !s.cfgServer.ConfigIsInitialized() { // 如果当前是领导并且配置还没被初始化，则初始化配置
		err := s.handleClusterConfigInit()
		return err
	}

	// ================== 比较本地配置和远程配置 ==================
	err := s.handleCompareLocalClusterConfig()
	if err != nil {
		s.Error("handleCompareLocalClusterConfig failed", zap.Error(err))
		return err
	}

	// ================== 处理新加入的节点 ==================
	if s.IsLeader() {
		err = s.handleNodeJoining()
		if err != nil {
			s.Error("handleJoinNode failed", zap.Error(err))
			return err
		}

	}

	// ================== 处理槽领导选举 ==================
	if s.IsLeader() {
		err = s.handleSlotLeaderElection()
		if err != nil {
			s.Error("handleSlotLeaderElection failed", zap.Error(err))
			return err
		}
	}

	return nil
}

func (s *Server) tick() {
	if s.IsLeader() {
		var err error
		for _, node := range s.cfgServer.Nodes() {
			if node.Id == s.opts.NodeId {
				continue
			}

			tk := s.pongTickMap[node.Id]
			tk++
			s.pongTickMap[node.Id] = tk

			if tk >= s.opts.PongMaxTick && node.Online { // 超过最大pong tick数，认为节点离线
				// 提案节点离线
				err = s.cfgServer.ProposeNodeOnlineStatus(node.Id, false)
				if err != nil {
					s.Error("propose node offline", zap.Error(err))
					break
				}
			} else if tk < s.opts.PongMaxTick && !node.Online {
				// 提案节点在线
				err = s.cfgServer.ProposeNodeOnlineStatus(node.Id, true)
				if err != nil {
					s.Error("propose node offline", zap.Error(err))
					break
				}
			}

		}
	}

}

func (s *Server) handleClusterConfigInit() error {

	cfg := &pb.Config{
		SlotCount:           s.opts.SlotCount,
		SlotReplicaCount:    s.opts.SlotMaxReplicaCount,
		ChannelReplicaCount: s.opts.ChannelMaxReplicaCount,
	}

	nodes := make([]*pb.Node, 0, len(s.opts.InitNodes))

	apiAddr := ""
	var replicas []uint64
	for nodeId, addr := range s.opts.InitNodes {
		if nodeId == s.opts.NodeId {
			apiAddr = s.opts.ApiServerAddr
		}
		nodes = append(nodes, &pb.Node{
			Id:            nodeId,
			ClusterAddr:   addr,
			ApiServerAddr: apiAddr,
			Online:        true,
			AllowVote:     true,
			Role:          pb.NodeRole_NodeRoleReplica,
			Status:        pb.NodeStatus_NodeStatusJoined,
			CreatedAt:     time.Now().Unix(),
		})
		replicas = append(replicas, nodeId)
	}
	cfg.Nodes = nodes

	if len(replicas) > 0 {
		offset := 0
		replicaCount := s.opts.SlotMaxReplicaCount
		for i := uint32(0); i < s.opts.SlotCount; i++ {
			slot := &pb.Slot{
				Id: i,
			}
			if len(replicas) <= int(replicaCount) {
				slot.Replicas = replicas
			} else {
				slot.Replicas = make([]uint64, 0, replicaCount)
				for i := uint32(0); i < replicaCount; i++ {
					idx := (offset + int(i)) % len(replicas)
					slot.Replicas = append(slot.Replicas, replicas[idx])
				}
			}
			offset++
			// 随机选举一个领导者
			randomIndex := globalRand.Intn(len(slot.Replicas))
			slot.Term = 1
			slot.Leader = slot.Replicas[randomIndex]
			cfg.Slots = append(cfg.Slots, slot)
		}
	}

	// 自动均衡槽领导
	newSlots := s.autoBalanceSlotLeaders(cfg)
	for _, newSlot := range newSlots {
		for _, oldSlot := range cfg.Slots {
			if newSlot.Id == oldSlot.Id {
				if newSlot.MigrateFrom != 0 && newSlot.MigrateTo != 0 {
					oldSlot.Leader = newSlot.MigrateTo
				}
				break
			}
		}
	}

	// 提案初始配置
	err := s.cfgServer.ProposeConfigInit(cfg)
	if err != nil {
		s.Error("ProposeConfigInit failed", zap.Error(err))
		return err
	}
	return nil
}

// 比较本地配置和远程配置
func (s *Server) handleCompareLocalClusterConfig() error {
	if s.localCfg.Version >= s.remoteCfg.Version {
		return nil
	}

	// 如果配置里自己节点的apiServerAddr配置不存在或不同，则提案配置
	if strings.TrimSpace(s.opts.ApiServerAddr) != "" {
		localNode := s.cfgServer.Node(s.opts.NodeId)
		if localNode != nil && localNode.ApiServerAddr != s.opts.ApiServerAddr {
			err := s.cfgServer.ProposeApiServerAddr(s.opts.NodeId, s.opts.ApiServerAddr)
			if err != nil {
				s.Error("ProposeApiServerAddr failed", zap.Error(err))
				return err
			}
		}
	}

	if s.IsLeader() {
		// 节点在线状态改变
		err := s.handleNodeOnlineStatusChange()
		if err != nil {
			s.Error("handleNodeOnlineStatusChange failed", zap.Error(err))
			return err
		}
	}

	if s.localCfg.Version < s.remoteCfg.Version {
		err := s.saveLocalConfig(s.remoteCfg)
		if err != nil {
			s.Warn("save local config error", zap.Error(err))
		} else {
			s.localCfg = s.remoteCfg
		}
	}

	return nil
}

// 自动均衡槽的领导节点
func (s *Server) autoBalanceSlotLeaders(cfg *pb.Config) []*pb.Slot {

	slots := cfg.Slots
	if len(slots) == 0 {
		return nil
	}

	// 判断是否有需要转移的槽领导，只要有就不执行自动均衡算法，等都转移完成后再执行
	for _, slot := range slots {
		if slot.MigrateFrom != 0 || slot.MigrateTo != 0 {
			return nil
		}
	}

	// 计算每个节点的槽数量和领导数量
	nodeSlotCountMap := make(map[uint64]uint32)   // 每个节点槽数量
	nodeLeaderCountMap := make(map[uint64]uint32) // 每个节点槽领导数量
	for _, slot := range slots {
		if slot.Leader == 0 {
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

	var nodeOnline = func(nodeId uint64) bool {
		for _, node := range cfg.Nodes {
			if node.Id == nodeId {
				return node.Online
			}
		}
		return false
	}

	var newSlots []*pb.Slot
	for exportNodeId, exportLeaderCount := range exportNodeLeaderCountMap {
		if exportLeaderCount == 0 {
			continue
		}

		if !nodeOnline(exportNodeId) { // 节点不在线 不参与
			continue
		}
		for importNodeId, importLeaderCount := range importNodeLeaderCountMap {
			if importLeaderCount == 0 {
				continue
			}

			if !nodeOnline(importNodeId) { // 节点不在线 不参与
				continue
			}
			// 从exportNodeId迁移一个槽领导到importNodeId
			for _, slot := range slots {
				if slot.MigrateFrom != 0 || slot.MigrateTo != 0 { // 已经需要转移的不参与计算
					continue
				}
				if slot.Leader == exportNodeId && wkutil.ArrayContainsUint64(slot.Replicas, importNodeId) { // 只有这个槽的领导属于exportNodeId，且importNodeId是这个槽的副本节点才能转移
					newSlot := slot.Clone()
					newSlot.MigrateFrom = exportNodeId
					newSlot.MigrateTo = importNodeId
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
	return newSlots

}

func (s *Server) handleNodeJoining() error {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return nil
	}
	var joiningNode *pb.Node
	for _, node := range s.cfgServer.Nodes() {
		if node.Status == pb.NodeStatus_NodeStatusJoining {
			joiningNode = node
			break
		}
	}

	if joiningNode == nil {
		return nil
	}

	firstSlot := slots[0]

	var migrateSlots []*pb.Slot // 迁移的槽列表

	voteNodes := s.cfgServer.AllowVoteNodes()

	if uint32(len(firstSlot.Replicas)) < s.cfgServer.SlotReplicaCount() { // 如果当前槽的副本数量小于配置的副本数量，则可以将新节点直接加入到学习节点中
		for _, slot := range slots {
			newSlot := slot.Clone()
			newSlot.Learners = append(slot.Learners, joiningNode.Id)
			migrateSlots = append(migrateSlots, newSlot)
		}
	} else {
		voteNodeCount := uint32(len(voteNodes))                                                // 投票节点数量
		avgSlotCount := uint32(len(slots)) * s.cfgServer.SlotReplicaCount() / voteNodeCount    // 平均每个节点的槽数量
		remainSlotCount := uint32(len(slots)) * s.cfgServer.SlotReplicaCount() % voteNodeCount // 剩余的槽数量
		if remainSlotCount > 0 {
			avgSlotCount += 1
		}

		avgSlotLeaderCount := uint32(len(slots)) / voteNodeCount    // 平均每个节点的槽领导数量
		remainSlotLeaderCount := uint32(len(slots)) % voteNodeCount // 剩余的槽领导数量
		if remainSlotLeaderCount > 0 {
			avgSlotLeaderCount += 1
		}

		nodeSlotLeaderCountMap := make(map[uint64]uint32) // 每个节点的槽领导数量
		nodeSlotCountMap := make(map[uint64]uint32)       // 每个节点目前的槽数量
		nodeSlotCountMap[joiningNode.Id] = 0              // 新节点槽数量肯定为0
		for _, slot := range slots {
			nodeSlotLeaderCountMap[slot.Leader]++
			for _, replicaId := range slot.Replicas {
				nodeSlotCountMap[replicaId]++
			}
		}

		// 计算每个节点应该迁入/迁出的槽数量
		migrateToCountMap := make(map[uint64]uint32)             // 每个节点应该迁入的槽数量
		migrateFromCountMap := make(map[uint64]uint32)           // 每个节点应该迁出的槽数量
		migrateSlotLeaderFromCountMap := make(map[uint64]uint32) // 每个节点应该迁出的槽领导数量
		for nodeId, slotCount := range nodeSlotCountMap {
			if slotCount < avgSlotCount {
				migrateToCountMap[nodeId] = avgSlotCount - slotCount
			} else if slotCount > avgSlotCount {
				migrateFromCountMap[nodeId] = slotCount - avgSlotCount
			}
		}

		for nodeId, slotLeaderCount := range nodeSlotLeaderCountMap {
			if slotLeaderCount > avgSlotLeaderCount {
				migrateSlotLeaderFromCountMap[nodeId] = slotLeaderCount - avgSlotLeaderCount
			}
		}

		for _, node := range voteNodes {
			fromSlotCount := migrateFromCountMap[node.Id]
			fromSlotLeaderCount := migrateSlotLeaderFromCountMap[node.Id]

			if fromSlotCount == 0 { // 已分配完毕
				continue
			}

			for _, slot := range slots {
				exist := false // 是否存在迁移，如果存在则忽略

				for _, migrateSlot := range migrateSlots {
					if slot.Id == migrateSlot.Id {
						exist = true
						break
					}
				}

				if exist { // 存在迁移则忽略
					continue
				}

				// ------------------- 分配槽领导 -------------------
				allocSlotLeader := false // 是否已经分配完槽领导
				if fromSlotCount > 0 && fromSlotLeaderCount > 0 && slot.Leader == node.Id {

					allocSlotLeader = true
					newSlot := slot.Clone()
					newSlot.MigrateFrom = slot.Leader
					newSlot.MigrateTo = joiningNode.Id
					newSlot.Learners = append(newSlot.Learners, joiningNode.Id)
					migrateSlots = append(migrateSlots, newSlot)
					fromSlotLeaderCount--
					nodeSlotLeaderCountMap[node.Id] = fromSlotLeaderCount

					fromSlotCount--
					migrateFromCountMap[node.Id] = fromSlotCount

				}

				// ------------------- 分配槽副本 -------------------
				if fromSlotCount > 0 && !allocSlotLeader {
					if wkutil.ArrayContainsUint64(slot.Replicas, node.Id) {
						newSlot := slot.Clone()
						newSlot.MigrateFrom = node.Id
						newSlot.MigrateTo = joiningNode.Id
						newSlot.Learners = append(newSlot.Learners, joiningNode.Id)
						migrateSlots = append(migrateSlots, newSlot)
						fromSlotCount--
						migrateFromCountMap[node.Id] = fromSlotCount
					}
				}

			}

		}

	}

	if len(migrateSlots) > 0 {
		err := s.ProposeJoined(joiningNode.Id, migrateSlots)
		if err != nil {
			return err
		}
	}

	return nil

}

func (s *Server) handleNodeOnlineStatusChange() error {
	fmt.Println("handleNodeOnlineStatusChange--------->")
	// 判断节点在线状态是否改变
	for _, node := range s.remoteCfg.Nodes {
		for _, localNode := range s.localCfg.Nodes {
			if node.Id == localNode.Id && node.Online != localNode.Online {
				err := s.handleNodeOnlineChange(node.Id, node.Online)
				if err != nil {
					s.Error("handleNodeOnlineChange failed", zap.Error(err))
					return err
				}
			}
		}
	}

	return nil
}

func (s *Server) handleNodeOnlineChange(nodeId uint64, online bool) error {
	var newSlots []*pb.Slot
	if online { // 节点上线

		s.Info("节点上线", zap.Uint64("nodeId", nodeId))
		slots := s.cfgServer.Slots()

		onlineNodeCount := s.cfgServer.AllowVoteAndJoinedOnlineNodeCount()

		avgSlotLeaderCount := s.cfgServer.Config().SlotCount / uint32(onlineNodeCount) // 平均每个节点的槽领导数量

		// 每个节点当前的槽领导数量
		nodeSlotLeaderCountMap := make(map[uint64]uint32)
		for _, slot := range slots {
			nodeSlotLeaderCountMap[slot.Leader]++
		}

		currentNodeSlotLeaderCount := nodeSlotLeaderCountMap[nodeId] // 当前节点的槽领导数量

		if currentNodeSlotLeaderCount >= avgSlotLeaderCount { // 当前节点的槽领导数量已经达到平均值，不需要迁入
			return nil
		}

		// 计算需要迁入的槽领导数量
		needSlotLeaderCount := avgSlotLeaderCount - currentNodeSlotLeaderCount

		for nId, slotLeaderCount := range nodeSlotLeaderCountMap {
			if slotLeaderCount <= avgSlotLeaderCount { // 槽领导数量小于平均值的节点不参与计算
				continue
			}

			if needSlotLeaderCount <= 0 {
				break
			}

			if nId == nodeId {
				continue
			}

			exportSlotLeaderCount := slotLeaderCount - avgSlotLeaderCount // 需要迁出的槽领导数量

			for _, slot := range slots {
				if slot.Leader == nId &&
					exportSlotLeaderCount > 0 &&
					needSlotLeaderCount > 0 &&
					slot.MigrateFrom == 0 &&
					slot.MigrateTo == 0 &&
					wkutil.ArrayContainsUint64(slot.Replicas, nodeId) {

					newSlot := slot.Clone()
					newSlot.MigrateFrom = nId
					newSlot.MigrateTo = nodeId
					newSlots = append(newSlots, newSlot)
					exportSlotLeaderCount--
					needSlotLeaderCount--
				}
			}

		}

	} else { // 节点下线
		s.Info("节点下线", zap.Uint64("nodeId", nodeId))
		slots := s.cfgServer.Slots()
		onlineNodeCount := s.cfgServer.AllowVoteAndJoinedOnlineNodeCount()
		avgSlotLeaderCount := s.cfgServer.Config().SlotCount / uint32(onlineNodeCount) // 平均每个节点的槽领导数量

		// 每个节点当前的槽领导数量
		nodeSlotLeaderCountMap := make(map[uint64]uint32)
		for _, slot := range slots {
			nodeSlotLeaderCountMap[slot.Leader]++
		}

		currentNodeSlotLeaderCount := nodeSlotLeaderCountMap[nodeId] // 当前节点的槽领导数量

		fmt.Println("currentNodeSlotLeaderCount---->", currentNodeSlotLeaderCount)

		for _, slot := range slots {
			if slot.Leader != nodeId {
				continue
			}

			for nId, slotLeaderCount := range nodeSlotLeaderCountMap {
				if nId == nodeId {
					continue
				}

				if currentNodeSlotLeaderCount <= 0 {
					break
				}

				if slotLeaderCount >= avgSlotLeaderCount { // 槽领导数量大于等于平均值的节点不参与计算
					continue
				}

				if slot.MigrateFrom == 0 && slot.MigrateTo == 0 && slot.Status != pb.SlotStatus_SlotStatusCandidate {
					newSlot := slot.Clone()
					newSlot.Status = pb.SlotStatus_SlotStatusCandidate
					newSlot.ExpectLeader = nId
					newSlots = append(newSlots, newSlot)
					nodeSlotLeaderCountMap[nId]++
					currentNodeSlotLeaderCount--
					break
				}
			}
		}

	}
	if len(newSlots) > 0 {
		fmt.Println("handleNodeOnlineChange---->")
		err := s.ProposeSlots(newSlots)
		if err != nil {
			s.Error("handleNodeOnlineChange failed,ProposeSlots failed", zap.Error(err))
			return err
		}
	}
	return nil
}

func (s *Server) handleSlotLeaderElection() error {
	slots := s.cfgServer.Slots()
	if len(slots) == 0 {
		return nil
	}

	electionSlots := make([]*pb.Slot, 0) // 需要选举的槽
	for _, slot := range slots {
		if slot.Status == pb.SlotStatus_SlotStatusCandidate {
			electionSlots = append(electionSlots, slot)
		}
	}
	if len(electionSlots) == 0 { // 不存在候选的槽
		return nil
	}

	// 触发选举
	return s.opts.OnSlotElection(electionSlots)

}
