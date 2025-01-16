package raft

import (
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"go.uber.org/zap"
)

func (n *Node) BecomeCandidate() {
	if n.cfg.Role == types.RoleLeader {
		n.Panic("invalid transition [leader -> candidate]")
	}
	n.cfg.Term++
	n.stepFunc = n.stepCandidate
	n.reset()
	n.tickFnc = n.tickCandidate
	n.voteFor = n.opts.NodeId
	n.cfg.Leader = 0
	n.cfg.Role = types.RoleCandidate
	n.Info("become candidate", zap.Uint32("term", n.cfg.Term), zap.Int("nextElectionTimeout", n.randomizedElectionTimeout))
}

func (n *Node) BecomeFollower(term uint32, leaderId uint64) {
	n.cfg.Term = term
	n.stepFunc = n.stepFollower
	n.reset()
	n.tickFnc = n.tickFollower
	n.voteFor = None
	n.cfg.Leader = leaderId
	n.cfg.Role = types.RoleFollower
	n.Debug("become follower", zap.Uint32("term", n.cfg.Term), zap.Uint64("leaderId", leaderId))
}

func (n *Node) BecomeLeader(term uint32) {
	n.cfg.Term = term
	n.stepFunc = n.stepLeader
	n.reset()
	n.tickFnc = n.tickLeader
	n.cfg.Leader = n.opts.NodeId
	n.cfg.Role = types.RoleLeader
	n.Debug("become leader", zap.Uint32("term", n.cfg.Term))
}

func (n *Node) BecomeLearner(term uint32, leaderId uint64) {
	n.cfg.Term = term
	n.stepFunc = n.stepLearner
	n.reset()
	n.tickFnc = n.tickLearner
	n.voteFor = None
	n.cfg.Leader = leaderId
	n.cfg.Role = types.RoleLearner
	n.Info("become learner", zap.Uint32("term", n.cfg.Term), zap.Uint64("leaderId", leaderId))
}

func (n *Node) reset() {
	// 重置选举状态
	n.electionState = electionState{
		votes: make(map[uint64]bool),
	}
	n.stopPropose = false
	n.syncState.replicaSync = make(map[uint64]*SyncInfo)
	n.onlySync = false
	n.suspend = false
	// 重置选举超时时间
	n.resetRandomizedElectionTimeout()
}
