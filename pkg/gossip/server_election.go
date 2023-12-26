package gossip

import (
	"math/rand"
	"time"

	"github.com/hashicorp/memberlist"
	"go.uber.org/zap"
)

func (s *Server) loopTick() {
	tick := time.NewTicker(s.opts.Heartbeat)
	for {
		select {
		case <-tick.C:
			s.tick()
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Server) tick() {
	switch s.nodeMeta.role {
	case ServerRoleSlave:
		s.stepSlave()
	case ServerRoleMaster:
		s.stepMaster()
	}
}

// 选举
func (s *Server) stepElection() {

	s.electionLock.Lock()
	defer s.electionLock.Unlock()

	s.currentEpoch.Inc() // 选举周期递增
	_, ok := s.voteTo[s.currentEpoch.Load()]
	if ok { // 如果已投票，说明此周期已经选举过了
		return
	}

	s.voteTo[s.currentEpoch.Load()] = s.opts.NodeID // 投票给自己

	// 发送选举请求
	failoverAuthRequest := &FailoverAuthRequest{
		NodeID: s.opts.NodeID,
		Epoch:  s.currentEpoch.Load(),
	}
	failoverAuthRequestData, _ := failoverAuthRequest.Marshal()
	for _, node := range s.allNodes() {
		s.stopper.RunWorker(func() {
			err := s.SendCMD(nameToNodeID(node.Name), &CMD{
				Type:  CMDTypeFailoverAuthRequest,
				Param: failoverAuthRequestData,
			})
			if err != nil {
				s.Warn("Send failoverAuthRequest cmd failed!", zap.Error(err), zap.String("nodeID", node.Name))
			}
		})
	}
}

func (s *Server) stepMaster() {

	for _, node := range s.aliveNodes() {
		go func(n *memberlist.Node) {
			err := s.sendPing(nameToNodeID(n.Name))
			if err != nil {
				s.Warn("sendPing failed!", zap.Error(err), zap.String("nodeID", n.Name))
			}
		}(node)
	}
}

func (s *Server) stepSlave() {
	if s.tickCount.Load() > s.getRandElectionTick() { // 超时，开始选举
		s.stepElection() // 选举
		return
	}
	s.tickCount.Inc()
}

func (s *Server) hasMaster() bool {
	for _, node := range s.aliveNodes() {
		nodeMeta, err := decodeNodeMeta(node.Meta)
		if err != nil {
			s.Panic("decodeNodeMeta failed!", zap.Error(err))
		}
		if nodeMeta.role == ServerRoleMaster {
			return true
		}
	}
	return false
}

func (s *Server) getRandElectionTick() uint32 {
	randElectionTick := s.opts.ElectionTick + rand.Uint32()%s.opts.ElectionTick
	return randElectionTick
}

func (s *Server) changeToMaster() {
	s.electionLock.Lock()
	defer s.electionLock.Unlock()

	if s.role == ServerRoleMaster {
		return
	}
	s.nodeMeta.currentEpoch = s.currentEpoch.Load()
	s.nodeMeta.role = ServerRoleMaster
	err := s.UpdateNodeMeta()
	if err != nil {
		s.Error("UpdateNodeMeta failed!", zap.Error(err))
		return
	}
	s.role = ServerRoleMaster

}

func (s *Server) changeToSlave(epoch uint32) {
	s.electionLock.Lock()
	defer s.electionLock.Unlock()

	if s.role == ServerRoleSlave {
		return
	}
	s.nodeMeta.currentEpoch = epoch
	s.nodeMeta.role = ServerRoleSlave
	err := s.UpdateNodeMeta()
	if err != nil {
		s.Error("UpdateNodeMeta failed!", zap.Error(err))
		return
	}
	s.role = ServerRoleSlave
	s.currentEpoch.Store(epoch)
}

func (s *Server) sendPing(nodeID uint64) error {

	pingReq := &PingRequest{
		NodeID: nodeID,
		Epoch:  s.currentEpoch.Load(),
	}
	param, err := pingReq.Marshal()
	if err != nil {
		return err
	}
	return s.SendCMDToUDP(nodeID, &CMD{
		Type:  CMDTypePingRquest,
		Param: param,
	})
}

func (s *Server) handlePingRequest(cmd *CMD) {
	pingReq := &PingRequest{}
	err := pingReq.Unmarshal(cmd.Param)
	if err != nil {
		s.Warn("Unmarshal fail", zap.Error(err))
		return
	}
	s.tickCount.Store(0)

	if s.role == ServerRoleMaster {
		s.changeToSlave(pingReq.Epoch)
	}
}
