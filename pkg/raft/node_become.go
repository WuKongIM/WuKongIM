package raft

import "go.uber.org/zap"

func (n *Node) BecomeCandidate() {
	if n.cfg.Role == RoleLeader {
		n.Panic("invalid transition [leader -> candidate]")
	}
	n.cfg.Term++
	n.stepFunc = n.stepCandidate
	n.reset()
	n.tickFnc = n.tickCandidate
	n.voteFor = n.opts.NodeId
	n.leaderId = 0
	n.cfg.Role = RoleCandidate
	n.Info("become candidate", zap.Uint32("term", n.cfg.Term), zap.Int("nextElectionTimeout", n.randomizedElectionTimeout))
}

func (n *Node) BecomeFollower(term uint32, leaderId uint64) {
	n.cfg.Term = term
	n.stepFunc = n.stepFollower
	n.reset()
	n.tickFnc = n.tickFollower
	n.voteFor = None
	n.leaderId = leaderId
	n.cfg.Role = RoleFollower
	n.Info("become follower", zap.Uint32("term", n.cfg.Term), zap.Uint64("leaderId", leaderId))
}

func (n *Node) BecomeLeader(term uint32) {
	n.cfg.Term = term
	n.stepFunc = n.stepLeader
	n.reset()
	n.tickFnc = n.tickLeader
	n.leaderId = n.opts.NodeId
	n.cfg.Role = RoleLeader
	n.Info("become leader", zap.Uint32("term", n.cfg.Term))
}

func (n *Node) BecomeLearner(term uint32, leaderId uint64) {
	n.cfg.Term = term
	n.stepFunc = n.stepLearner
	n.reset()
	n.tickFnc = n.tickLearner
	n.voteFor = None
	n.leaderId = leaderId
	n.cfg.Role = RoleLearner
	n.Info("become learner", zap.Uint32("term", n.cfg.Term), zap.Uint64("leaderId", leaderId))
}

func (n *Node) reset() {
	// 重置选举状态
	n.electionState = electionState{
		votes: make(map[uint64]bool),
	}

	n.syncState.replicaSync = make(map[uint64]*SyncInfo)
	n.onlySync = false
	// 重置选举超时时间
	n.resetRandomizedElectionTimeout()
}