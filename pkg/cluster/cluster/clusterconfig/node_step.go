package clusterconfig

import (
	"time"

	"go.uber.org/zap"
)

func (n *Node) Step(m Message) error {

	switch {
	case m.Term == 0:
		// local message
	case m.Term > n.state.term:
		n.Warn("received message with higher term", zap.Uint32("term", m.Term), zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.String("msgType", m.Type.String()))
		if m.Type == EventHeartbeat {
			n.becomeFollower(m.Term, m.From)
		} else if m.Type != EventVoteResp && m.Type != EventVote {
			n.becomeFollower(m.Term, None)
		}
	}

	switch m.Type {
	case EventHup:
		n.hup()
	case EventVote:
		canVote := (n.state.voteFor == None || (n.state.voteFor == m.From && n.state.leader == None)) && m.ConfigVersion >= n.localConfigVersion && m.Term >= n.state.term
		if canVote {
			n.send(Message{
				From:          n.opts.NodeId,
				To:            m.From,
				Type:          EventVoteResp,
				Term:          n.state.term,
				ConfigVersion: n.localConfigVersion,
			})
			n.electionElapsed = 0
			n.state.voteFor = m.From
			n.Info("agree vote", zap.Uint64("voteFor", m.From), zap.Uint32("term", m.Term), zap.Uint64("configVersion", m.ConfigVersion))
		} else {
			if n.state.voteFor != None {
				n.Info("already vote for other", zap.Uint64("voteFor", n.state.voteFor))
			} else if m.ConfigVersion < n.localConfigVersion {
				n.Info("lower config version, reject vote")
			} else if m.Term < n.state.term {
				n.Info("lower term, reject vote")
			}
		}

	case EventApplyResp:
		if m.ConfigVersion > n.appliedConfigVersion {
			n.Info("received apply response", zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.Uint32("term", m.Term), zap.Uint64("configVersion", m.ConfigVersion))
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
		if m.ConfigVersion > n.localConfigVersion {
			n.leaderConfigVersion = m.ConfigVersion
		}
		n.sendHeartbeatResp(m)
		// case EventNotifySync:
		// n.send(n.newSync())
	case EventSyncResp:
		if m.ConfigVersion > n.localConfigVersion {
			n.leaderConfigVersion = m.ConfigVersion
			n.localConfigVersion = m.ConfigVersion
			n.configData = m.Config
		}
		n.updateCommitForFollower(m.CommittedVersion)

	}
	return nil
}

func (n *Node) stepCandidate(m Message) error {
	switch m.Type {
	case EventHeartbeat:
		n.becomeFollower(m.Term, m.From)
		n.sendHeartbeatResp(m)
	case EventVoteResp:
		n.Info("received vote response", zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.Uint32("term", m.Term), zap.Uint64("configVersion", m.ConfigVersion))
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
			n.leaderConfigVersion = m.ConfigVersion
			n.localConfigVersion = m.ConfigVersion
			n.configData = m.Config
			if n.isSingleNode() {
				n.updateCommitForLeader()
			}
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
		n.activeReplicaMap[m.From] = time.Now()
	case EventSync:
		n.nodeConfigVersionMap[m.From] = m.ConfigVersion
		n.activeReplicaMap[m.From] = time.Now()
		// n.Info("received sync request", zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.Uint32("term", m.Term), zap.Uint64("configVersion", m.ConfigVersion), zap.Uint64("committedVersion", n.committedConfigVersion), zap.Uint64("localConfigVersion", n.localConfigVersion))
		if n.localConfigVersion > 0 && n.localConfigVersion > n.committedConfigVersion {
			n.updateCommitForLeader() // 更新提交的配置版本
		}
		n.send(Message{
			From:             n.opts.NodeId,
			To:               m.From,
			Type:             EventSyncResp,
			Term:             n.state.term,
			Config:           n.configData,
			ConfigVersion:    n.localConfigVersion,
			CommittedVersion: n.committedConfigVersion,
		})
	}
	return nil
}

func (n *Node) updateCommitForLeader() {
	if n.isSingleNode() {
		n.Info("update commit for leader for single", zap.Uint64("localConfigVersion", n.localConfigVersion))
		n.committedConfigVersion = n.localConfigVersion
		return
	}
	successCount := 0
	for _, version := range n.nodeConfigVersionMap {
		if version > n.localConfigVersion {
			successCount++
		}
	}
	if successCount+1 >= n.quorum() {
		n.Info("update commit for leader", zap.Uint64("localConfigVersion", n.localConfigVersion))
		n.committedConfigVersion = n.localConfigVersion
	}
}

func (n *Node) updateCommitForFollower(leaderCommittedConfgVersion uint64) {
	if leaderCommittedConfgVersion > n.committedConfigVersion {
		n.committedConfigVersion = leaderCommittedConfgVersion
		n.Info("update commit for follower", zap.Uint64("committedConfigVersion", n.committedConfigVersion), zap.Uint64("leaderConfigVersion", n.leaderConfigVersion))
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
			n.send(Message{To: nodeID, From: nodeID, Term: n.state.term, Type: EventVoteResp})
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
		From: n.opts.NodeId,
		Term: n.state.term,
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

// func (n *Node) bcastNotifySync() {
// 	for _, nodeID := range n.opts.Replicas {
// 		if nodeID == n.opts.NodeId {
// 			continue
// 		}
// 		if n.nodeConfigVersionMap[nodeID] <= n.localConfigVersion {
// 			n.send(n.newNotifySync(nodeID))
// 		}
// 	}
// }

func (n *Node) hasNotifySync() bool {
	if n.localConfigVersion <= 0 {
		return false
	}
	for _, nodeID := range n.opts.Replicas {
		if nodeID == n.opts.NodeId {
			continue
		}
		if n.nodeConfigVersionMap[nodeID] <= n.localConfigVersion {
			return true
		}
	}
	return false
}

// func (n *Node) newNotifySync(to uint64) Message {
// 	return Message{
// 		From:          n.opts.NodeId,
// 		To:            to,
// 		Type:          EventNotifySync,
// 		Term:          n.state.term,
// 		ConfigVersion: n.localConfigVersion,
// 	}
// }

func (n *Node) newSync(seq uint64) Message {

	return Message{
		Seq:           seq,
		From:          n.opts.NodeId,
		To:            n.state.leader,
		Type:          EventSync,
		Term:          n.state.term,
		ConfigVersion: n.localConfigVersion + 1,
	}
}

func (n *Node) newApply(seq uint64) Message {
	return Message{
		Seq:           seq,
		From:          n.opts.NodeId,
		To:            n.opts.NodeId,
		Type:          EventApply,
		Term:          n.state.term,
		ConfigVersion: n.localConfigVersion,
		Config:        n.configData,
	}
}

func (n *Node) newHeartbeat(to uint64) Message {
	return Message{
		From:             n.opts.NodeId,
		To:               to,
		Type:             EventHeartbeat,
		Term:             n.state.term,
		ConfigVersion:    n.localConfigVersion,
		CommittedVersion: n.committedConfigVersion,
	}
}
