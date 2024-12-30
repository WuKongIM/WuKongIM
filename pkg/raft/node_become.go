package raft

import "go.uber.org/zap"

func (n *Node) becomeCandidate() {
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
	n.Info("become candidate", zap.Uint32("term", n.cfg.Term))
}

func (n *Node) becomeFollower(term uint32, leaderId uint64) {

}

func (n *Node) becomeLeader() {

}

func (n *Node) becomeLearner(term uint32, leaderId uint64) {

}

func (n *Node) reset() {

}
