package raft

import "go.uber.org/zap"

func (n *Node) Tick() {

	if n.tickFnc != nil {
		n.tickFnc()
	}
}

func (n *Node) tickFollower() {
	n.syncElapsed++
	if n.syncElapsed >= n.opts.SyncInterval && n.leaderId != 0 {
		n.sendSyncReq(n.leaderId)
		n.syncElapsed = 0
	}

	if n.opts.ElectionOn {
		n.tickElection()
	}
}

func (n *Node) tickCandidate() {
	n.tickElection()
}

func (n *Node) tickElection() {
	n.electionElapsed++
	if n.pastElectionTimeout() {
		n.electionElapsed = 0
		n.campaign()
	}
}

// 是否超过选举超时时间
func (n *Node) pastElectionTimeout() bool {
	return n.electionElapsed >= n.randomizedElectionTimeout
}

// 重置随机选举超时时间
func (n *Node) resetRandomizedElectionTimeout() {
	n.randomizedElectionTimeout = n.opts.ElectionInterval + globalRand.Intn(n.opts.ElectionInterval)
}

// 开始选举
func (n *Node) campaign() {
	n.becomeCandidate()
	for _, nodeId := range n.cfg.Replicas {
		n.Info("sent vote request", zap.Uint64("from", n.opts.NodeId), zap.Uint64("to", nodeId), zap.Uint32("term", n.cfg.Term))
		n.sendVoteReq(nodeId)
	}
	// 自己给自己投一票
	n.sendVoteReq(n.opts.NodeId)
}
