package clusterconfig

import "go.uber.org/zap"

func (n *Node) Step(m Message) error {

	switch {
	case m.Term == 0:
		// local message
	case m.Term > n.state.term:
		n.Warn("received message with higher term", zap.Uint32("term", m.Term), zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.String("msgType", m.Type.String()))
	}

	switch m.Type {
	case EventHup:
		n.hup()
	case EventApplyResp:
		if m.ConfigVersion != n.appliedConfigVersion {
			n.appliedConfigVersion = m.ConfigVersion
			n.configData = m.Config
		}
	default:
		err := n.stepFnc(m)
		if err != nil {
			return err
		}
	}

	return nil
}

func (n *Node) stepFollower(m Message) error {
	switch m.Type {
	case EventHeartbeat:
		n.electionElapsed = 0
		n.state.leader = m.From
		n.state.term = m.Term
		if m.ConfigVersion != n.localConfigVersion {
			n.leaderConfigVersion = m.ConfigVersion
		}
		n.updateCommitForFollower(m.CommittedVersion)
		n.sendHeartbeatResp(m)
	case EventNotifySync:
		n.send(Message{
			Type: EventSync,
			To:   m.From,
			From: n.opts.NodeId,
		})
	case EventSyncResp:
		if m.ConfigVersion != n.localConfigVersion {
			n.leaderConfigVersion = m.ConfigVersion
			n.configData = m.Config
		}

	}
	return nil
}

func (n *Node) stepCandidate(m Message) error {
	switch m.Type {
	case EventHeartbeat:
		n.becomeFollower(m.Term, m.From)
		n.sendHeartbeatResp(m)
	case EventVoteResp:
		n.poll(m)

	}
	return nil
}

// 统计投票结果
func (n *Node) poll(m Message) {
	n.votes[m.From] = m.Type == EventVoteResp

	var granted int
	for _, v := range n.votes {
		if v {
			granted++
		}
	}

	if granted >= n.quorum() {
		n.becomeLeader()
	} else if len(n.votes)-granted >= n.quorum() {
		n.becomeFollower(n.state.term, None)
	}
}

func (n *Node) quorum() int {
	return len(n.opts.Replicas)/2 + 1
}

func (n *Node) stepLeader(m Message) error {
	switch m.Type {
	case EventPropose:
		if m.ConfigVersion > n.localConfigVersion {
			n.localConfigVersion = m.ConfigVersion
			n.leaderConfigVersion = n.localConfigVersion
			n.bcastNotifySync()
		}
	case EventBeat:
		n.bcastHeartbeat()
	case EventHeartbeat:
		if m.Term > n.state.term {
			n.becomeFollower(m.Term, m.From)
			n.sendHeartbeatResp(m)
		} else if m.Term == n.state.term && m.ConfigVersion > n.localConfigVersion {
			n.becomeFollower(m.Term, m.From)
			n.sendHeartbeatResp(m)
		}
	case EventHeartbeatResp:
	case EventSync:
		cfgData, err := n.opts.GetConfigData()
		if err != nil {
			n.Error("get config data failed", zap.Error(err))
			return err
		}
		n.nodeConfigVersionMap[m.From] = m.ConfigVersion
		n.updateCommitForLeader() // 更新提交的配置版本
		n.send(Message{
			From:          n.opts.NodeId,
			To:            m.From,
			Type:          EventSyncResp,
			Term:          n.state.term,
			Config:        cfgData,
			ConfigVersion: n.localConfigVersion,
		})
	}
	return nil
}

func (n *Node) updateCommitForLeader() {
	successCount := 0
	for _, version := range n.nodeConfigVersionMap {
		if version > n.localConfigVersion {
			successCount++
		}
	}
	if successCount+1 >= n.quorum() {
		n.Info("update commit for leader", zap.Uint64("localConfigVersion", n.localConfigVersion), zap.Uint64("leaderConfigVersion", n.leaderConfigVersion))
		n.committedConfigVersion = n.localConfigVersion
	}
}

func (n *Node) updateCommitForFollower(leaderCommittedConfgVersion uint64) {
	if leaderCommittedConfgVersion > n.committedConfigVersion {
		n.committedConfigVersion = leaderCommittedConfgVersion
		n.Info("update commit for follower", zap.Uint64("localConfigVersion", n.localConfigVersion), zap.Uint64("leaderConfigVersion", n.leaderConfigVersion))
	}
}

func (n *Node) hup() {
	n.campaign()
}

// 开始选举
func (n *Node) campaign() {
	n.becomeCandidate()
	for _, nodeID := range n.opts.Replicas {
		if nodeID == n.opts.NodeId {
			// 自己给自己投一票
			n.send(Message{To: nodeID, Term: n.state.term, Type: EventVoteResp})
			continue
		}
		n.Info("sent vote request", zap.Uint64("from", n.opts.NodeId), zap.Uint64("to", nodeID), zap.Uint32("term", n.state.term))
		n.sendRequestVote(nodeID)
	}
}

func (n *Node) sendRequestVote(nodeID uint64) {
	n.send(Message{
		From:          n.opts.NodeId,
		To:            nodeID,
		Type:          EventVote,
		Term:          n.state.term,
		ConfigVersion: n.localConfigVersion,
	})
}

func (n *Node) sendHeartbeat(nodeID uint64) {
	n.send(Message{
		From:             n.opts.NodeId,
		To:               nodeID,
		Type:             EventHeartbeat,
		Term:             n.state.term,
		ConfigVersion:    n.localConfigVersion,
		CommittedVersion: n.committedConfigVersion,
	})
}

func (n *Node) sendHeartbeatResp(m Message) {
	n.send(Message{
		To:   m.From,
		Type: EventHeartbeatResp,
	})
}

func (n *Node) bcastHeartbeat() {

	for _, nodeID := range n.opts.Replicas {
		if nodeID == n.opts.NodeId {
			continue
		}
		n.sendHeartbeat(nodeID)
	}

}

func (n *Node) bcastNotifySync() {
	for _, nodeID := range n.opts.Replicas {
		if nodeID == n.opts.NodeId {
			continue
		}
		n.send(n.newNotifySync(nodeID))
	}
}

func (n *Node) newNotifySync(to uint64) Message {
	return Message{
		From:          n.opts.NodeId,
		To:            to,
		Type:          EventNotifySync,
		Term:          n.state.term,
		ConfigVersion: n.localConfigVersion,
	}
}

func (n *Node) newSync() Message {

	return Message{
		From:          n.opts.NodeId,
		Type:          EventSync,
		Term:          n.state.term,
		ConfigVersion: n.localConfigVersion + 1,
	}
}

func (n *Node) newApply() Message {
	return Message{
		From:          n.opts.NodeId,
		To:            n.opts.NodeId,
		Type:          EventApply,
		Term:          n.state.term,
		ConfigVersion: n.committedConfigVersion,
		Config:        n.configData,
	}
}
