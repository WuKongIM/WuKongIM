package gossip

import (
	"fmt"
	"strconv"

	"github.com/hashicorp/memberlist"
	"go.uber.org/zap"
)

// -------------------- 以下是memberlist的Delegate接口实现 --------------------
func (s *Server) NodeMeta(limit int) []byte {
	return s.nodeMeta.encode()
}

func (s *Server) NotifyMsg(msg []byte) {
	if len(msg) == 0 {
		return
	}
	cmd := &CMD{}
	err := cmd.Unmarshal(msg)
	if err != nil {
		s.Error("Unmarshal cmd failed!", zap.Error(err))
		return
	}
	s.handleCMD(cmd)

}

func (s *Server) GetBroadcasts(overhead, limit int) [][]byte {

	return nil
}

func (s *Server) LocalState(join bool) []byte {
	fmt.Println("LocalState---->")
	return nil
}

func (s *Server) MergeRemoteState(buf []byte, join bool) {
	fmt.Println("MergeRemoteState--->")
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

// -------------------- 处理cmd --------------------

func (s *Server) handleCMD(cmd *CMD) {
	fmt.Println("cmd--->", cmd.Type.Uint32())
	switch cmd.Type {
	case CMDTypeFailoverAuthRequest:
		s.handleFailoverAuthRequest(cmd)
	case CMDTypeFailoverAuthRespose:
		s.handleFailoverAuthRespose(cmd)
	case CMDTypePingRquest:
		s.handlePingRequest(cmd)
	}
}

func (s *Server) handleFailoverAuthRequest(cmd *CMD) {
	failoverAuthRequest := &FailoverAuthRequest{}
	err := failoverAuthRequest.Unmarshal(cmd.Param)
	if err != nil {
		s.Error("Unmarshal failoverAuthRequest failed!", zap.Error(err))
		return
	}

	// -------------------- 判断是否存在master --------------------
	if s.hasMaster() { // 如果存在master，忽略该请求
		return
	}

	// -------------------- 判断当前周期是否已投票 --------------------
	s.electionLock.RLock()
	_, ok := s.voteTo[failoverAuthRequest.Epoch]
	s.electionLock.RUnlock()
	if !ok { // 未投票
		resp := &FailoverAuthResponse{
			NodeID: s.opts.NodeID,
		}
		param, err := resp.Marshal()
		if err != nil {
			s.Error("Marshal failoverAuthResponse failed!", zap.Error(err))
			return
		}
		err = s.SendCMD(failoverAuthRequest.NodeID, &CMD{
			Type:  CMDTypeFailoverAuthRespose,
			Param: param,
		})
		if err != nil {
			s.Warn("Send failoverAuthRespose cmd failed!", zap.Error(err))
			return
		}
		s.electionLock.Lock()
		s.voteTo[failoverAuthRequest.Epoch] = s.opts.NodeID
		s.electionLock.Unlock()
	}
}

func (s *Server) handleFailoverAuthRespose(cmd *CMD) {
	failoverAuthResponse := &FailoverAuthResponse{}
	err := failoverAuthResponse.Unmarshal(cmd.Param)
	if err != nil {
		s.Error("Unmarshal failoverAuthResponse failed!", zap.Error(err))
		return
	}

	s.electionLock.Lock()
	nodeIDs := s.votesFrom[s.nodeMeta.currentEpoch]
	if nodeIDs == nil {
		nodeIDs = make([]uint64, 0)
	}

	existNode := false
	for _, nodeID := range nodeIDs {
		if nodeID == failoverAuthResponse.NodeID {
			existNode = true
			break
		}
	}
	if !existNode {
		nodeIDs = append(nodeIDs, failoverAuthResponse.NodeID)
		s.votesFrom[s.nodeMeta.currentEpoch] = nodeIDs
	}
	s.electionLock.Unlock()

	if len(nodeIDs) > len(s.aliveNodes())/2 { // 超过半数同意
		s.changeToMaster()
	}

}
