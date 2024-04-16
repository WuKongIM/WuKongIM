package clusterevent

import (
	"time"

	pb "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/cpb"
	"go.uber.org/zap"
)

func (s *Server) loop() {
	tk := time.NewTicker(time.Millisecond * 500)
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

	if s.preRemoteCfg.Version < s.cfgServer.AppliedConfig().Version {
		s.preRemoteCfg = s.cfgServer.AppliedConfig().Clone()
		s.Debug("clone......", zap.Uint64("version", s.preRemoteCfg.Version))
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

func (s *Server) checkNodes() {
	s.Debug("checkNodes....")
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
