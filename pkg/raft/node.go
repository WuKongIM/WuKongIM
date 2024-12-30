package raft

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type Role uint8

const (
	RoleUnknown Role = iota
	// RoleFollower 跟随者
	RoleFollower
	// RoleCandidate 候选人
	RoleCandidate
	// RoleLeader 领导者
	RoleLeader
	// RoleLearner 学习者
	RoleLearner
)

type electionState struct {
	electionElapsed           int    // 选举计时器
	randomizedElectionTimeout int    // 随机选举超时时间
	voteFor                   uint64 // 投票给了谁
}

type Node struct {
	events      []Event
	opts        *Options
	syncElapsed int    // 同步计数
	leaderId    uint64 // 领导者ID
	tickFnc     func()
	stepFunc    func(event Event) error
	queue       *queue
	wklog.Log

	heartbeatElapsed int // 心跳计时器

	cfg Config // 分布式配置

	electionState // 选举状态
}

func NewNode(opts *Options) *Node {
	n := &Node{
		opts: opts,
		Log:  wklog.NewWKLog("raft.node"),
	}
	for _, id := range opts.Replicas {
		if id == opts.NodeId {
			continue
		}
		n.cfg.Replicas = append(n.cfg.Replicas, id)
	}
	n.cfg.Term = opts.Log.LastTerm
	n.queue = newQueue(opts.Log.AppliedIndex, opts.Log.LastIndex)
	return n
}

func (n *Node) Ready() []Event {

	return nil
}

func (n *Node) switchConfig(cfg Config) {

}
