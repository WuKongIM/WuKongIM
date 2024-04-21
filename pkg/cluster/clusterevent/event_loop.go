package clusterevent

import (
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"go.uber.org/zap"
)

func (s *Server) loop() {
	tk := time.NewTicker(time.Millisecond * 250)
	for {
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
	s.msgs = s.msgs[:0]
	return msgs
}

func (s *Server) tick() {

	if s.IsLeader() {
		s.checkNodeOnlineStatus() // 检查在线状态
	}

	if s.preRemoteCfg.Version < s.cfgServer.AppliedConfig().Version {
		s.preRemoteCfg = s.cfgServer.AppliedConfig().Clone()
	}

	if s.localCfg.Version >= s.preRemoteCfg.Version {
		if s.localCfg.Version > s.preRemoteCfg.Version {
			s.Warn("local config version > remote config version", zap.Uint64("version", s.localCfg.Version), zap.Uint64("remoteVersion", s.preRemoteCfg.Version))
		}
		return
	}
	// 检查节点的变化
	s.checkNodes()

	// 检查槽的变化
	s.checkSlots()

	// 检查槽的领导是否有效
	if s.IsLeader() {
		s.checkSlotLeader()
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

	hasEvent := len(s.msgs) > 0

	if hasEvent && s.localCfg.Version < s.preRemoteCfg.Version {
		err := s.saveLocalConfig(s.preRemoteCfg)
		if err != nil {
			s.Warn("save local config error", zap.Error(err))
		} else {
			s.localCfg = s.preRemoteCfg
		}
	}

}

func (s *Server) checkNodeOnlineStatus() {
	// 节点在线状态检查
	if s.IsLeader() {
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
			s.msgs = append(s.msgs, Message{
				Type:  EventTypeNodeOnlieChange,
				Nodes: changeNodes,
			})
		}
		s.pongTickMapLock.Unlock()
	}
}

func (s *Server) checkNodes() {
	// ==================== check add or update node ====================
	newNodes := make([]*pb.Node, 0, len(s.preRemoteCfg.Nodes)-len(s.localCfg.Nodes))
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

func (s *Server) checkSlots() {
	// ==================== check add or update slot ====================
	newSlots := make([]*pb.Slot, 0, len(s.preRemoteCfg.Slots)-len(s.localCfg.Slots))
	updateSlots := make([]*pb.Slot, 0, len(s.preRemoteCfg.Slots))
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
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeSlotAdd,
			Slots: newSlots,
		})
	}
	if len(updateSlots) > 0 {
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeSlotUpdate,
			Slots: updateSlots,
		})
	}

	// ==================== check delete slot ====================
	deleteSlots := make([]*pb.Slot, 0)
	for _, localSlot := range s.localCfg.Slots {
		exist := false
		for _, remoteSlot := range s.preRemoteCfg.Slots {
			if localSlot.Id == remoteSlot.Id {
				exist = true
			}
		}
		if !exist {
			deleteSlots = append(deleteSlots, localSlot)
		}
	}
	if len(deleteSlots) > 0 {
		s.msgs = append(s.msgs, Message{
			Type:  EventTypeSlotDelete,
			Slots: deleteSlots,
		})
	}
}

func (s *Server) checkSlotLeader() {
	// ==================== check need election slots ====================
	var needElectionSlots []*pb.Slot // 需要重新选举的槽
	for _, remoteSlot := range s.preRemoteCfg.Slots {
		slot := s.cfgServer.Slot(remoteSlot.Id)
		if slot == nil || remoteSlot.Leader == 0 {
			continue
		}
		if !s.cfgServer.NodeOnline(remoteSlot.Leader) { // 领导节点没在线了，需要重新选举
			needElectionSlots = append(needElectionSlots, remoteSlot)
		}
	}

	if len(needElectionSlots) == 0 {
		return
	}

	s.msgs = append(s.msgs, Message{
		Type:  EventTypeSlotNeedElection,
		Slots: needElectionSlots,
	})

}
