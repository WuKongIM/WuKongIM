package gossip

import (
	"strconv"

	"github.com/hashicorp/memberlist"
)

// -------------------- 以下是memberlist的Delegate接口实现 --------------------
func (s *Server) NodeMeta(limit int) []byte {
	return nil
}

func (s *Server) NotifyMsg(msg []byte) {
}

func (s *Server) GetBroadcasts(overhead, limit int) [][]byte {

	return nil
}

func (s *Server) LocalState(join bool) []byte {
	return nil
}

func (s *Server) MergeRemoteState(buf []byte, join bool) {
}

// -------------------- 以下是memberlist的EventDelegate接口实现 --------------------

func (s *Server) NotifyJoin(n *memberlist.Node) {
	if s.opts.OnNodeEvent != nil {
		nodeEvent := NodeEvent{
			NodeID:    nameToNodeID(n.Name),
			Addr:      n.Address(),
			EventType: NodeEventJoin,
			State:     NodeStateType(n.State),
		}
		s.opts.OnNodeEvent(nodeEvent)
	}
}

func (s *Server) NotifyLeave(n *memberlist.Node) {
	if s.opts.OnNodeEvent != nil {
		nodeEvent := NodeEvent{
			NodeID:    nameToNodeID(n.Name),
			Addr:      n.Address(),
			EventType: NodeEventLeave,
			State:     NodeStateType(n.State),
		}
		s.opts.OnNodeEvent(nodeEvent)
	}
}

func (s *Server) NotifyUpdate(n *memberlist.Node) {
	if s.opts.OnNodeEvent != nil {
		nodeEvent := NodeEvent{
			NodeID:    nameToNodeID(n.Name),
			Addr:      n.Address(),
			EventType: NodeEventUpdate,
			State:     NodeStateType(n.State),
		}
		s.opts.OnNodeEvent(nodeEvent)
	}
}

func nameToNodeID(name string) uint64 {
	nodeID, _ := strconv.ParseUint(name, 10, 64)
	return nodeID
}
