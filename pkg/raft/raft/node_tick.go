package raft

import (
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"go.uber.org/zap"
)

func (n *Node) Tick() {

	if n.tickFnc != nil {
		n.tickFnc()
	}
}

func (n *Node) tickLeader() {
	n.tickHeartbeat()
}

func (n *Node) tickFollower() {
	n.syncElapsed++
	if n.syncElapsed >= n.opts.SyncInterval && n.cfg.Leader != 0 {
		if !n.hasSyncReq() {
			n.sendSyncReq()
		}
		n.syncElapsed = 0
	}

	if n.opts.ElectionOn {
		n.tickElection()
	}
}

func (n *Node) hasSyncReq() bool {
	if len(n.events) == 0 {
		return false
	}
	for _, e := range n.events {
		if e.Type == types.SyncReq {
			return true
		}
	}
	return false
}

func (n *Node) tickLearner() {
	n.syncElapsed++
	if n.syncElapsed >= n.opts.SyncInterval && n.cfg.Leader != 0 {
		if !n.hasSyncReq() {
			n.sendSyncReq()
		}
		n.syncElapsed = 0
	}

}

func (n *Node) tickCandidate() {
	if n.opts.ElectionOn {
		n.tickElection()
	}
}

func (n *Node) tickElection() {
	n.electionElapsed++
	if n.pastElectionTimeout() {
		n.electionElapsed = 0
		n.campaign()
	}
}

func (n *Node) tickHeartbeat() {
	if n.opts.ElectionOn {
		// 如果开启了选举，需要定时发送心跳
		n.heartbeatElapsed++
		if n.heartbeatElapsed >= n.opts.HeartbeatInterval {
			n.heartbeatElapsed = 0
			n.sendPing(All)
		}
		return
	}

	// 如果没有开启选举，follower一段时间没有发生sync请求，leader就发起心跳
	for _, replicaId := range n.cfg.Replicas {
		if replicaId == n.opts.NodeId {
			continue
		}
		syncInfo := n.syncState.replicaSync[replicaId]
		if syncInfo != nil {
			syncInfo.SyncTick++
		}
		if syncInfo == nil || syncInfo.SyncTick > n.opts.SyncInterval {
			n.sendPing(replicaId)
		}
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
	if n.IsLeader() {
		// 如果当前是领导，先变成follower
		n.BecomeFollower(n.cfg.Term, 0)
	} else {
		n.BecomeCandidate()
		for _, nodeId := range n.cfg.Replicas {
			if nodeId == n.opts.NodeId {
				// 自己给自己投一票
				n.sendVoteReq(types.LocalNode)
				continue
			}
			n.Info("sent vote request", zap.Uint64("from", n.opts.NodeId), zap.Uint64("to", nodeId), zap.Uint32("term", n.cfg.Term))
			n.sendVoteReq(nodeId)
		}
	}
}
