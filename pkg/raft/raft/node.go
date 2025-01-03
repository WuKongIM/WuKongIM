package raft

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type electionState struct {
	electionElapsed           int             // 选举计时器
	randomizedElectionTimeout int             // 随机选举超时时间
	voteFor                   uint64          // 投票给了谁
	votes                     map[uint64]bool // 投票记录,key为节点id，value为是否同意
}

type syncState struct {
	replicaSync map[uint64]*SyncInfo // 同步记录
}

type softState struct {
	termStartIndex *types.TermStartIndexInfo
}

type Node struct {
	events      []types.Event
	opts        *Options
	syncElapsed int    // 同步计数
	leaderId    uint64 // 领导者ID
	tickFnc     func()
	stepFunc    func(event types.Event) error
	queue       *queue
	wklog.Log

	heartbeatElapsed int // 心跳计时器

	cfg types.Config // 分布式配置

	electionState // 选举状态
	syncState     // 同步状态

	// 最新的任期对应的开始日志下标
	lastTermStartIndex types.TermStartIndexInfo

	onlySync  bool      // 是否只同步,不做截断判断
	softState softState // 软状态
}

func NewNode(opts *Options) *Node {
	n := &Node{
		opts: opts,
		Log:  wklog.NewWKLog(fmt.Sprintf("raft.node[%d]", opts.NodeId)),
	}
	for _, id := range opts.Replicas {
		if id == opts.NodeId {
			continue
		}
		n.cfg.Replicas = append(n.cfg.Replicas, id)
	}

	// 获取raft状态
	state, err := opts.Storage.GetState()
	if err != nil {
		n.Panic("get log state failed", zap.Error(err))
	}
	n.cfg.Term = state.LastTerm
	// 初始化日志队列
	n.queue = newQueue(state.AppliedIndex, state.LastLogIndex, opts.NodeId)

	// 初始化选举状态
	n.votes = make(map[uint64]bool)
	n.replicaSync = make(map[uint64]*SyncInfo)
	n.resetRandomizedElectionTimeout()

	// 获取最新任期的开始日志下标
	startIndex, err := opts.Storage.GetTermStartIndex(n.cfg.Term)
	if err != nil {
		n.Panic("get term start index failed", zap.Error(err))
	}
	n.lastTermStartIndex.Index = startIndex
	n.lastTermStartIndex.Term = n.cfg.Term

	n.BecomeFollower(n.cfg.Term, None)

	return n
}

func (n *Node) Key() string {
	return n.opts.Key
}

func (n *Node) Ready() []types.Event {

	if n.queue.hasNextStoreLogs() {
		logs := n.queue.nextStoreLogs(0)
		if len(logs) > 0 {
			n.sendStoreReq(logs, n.softState.termStartIndex)
			n.softState.termStartIndex = nil
		}
	}
	events := n.events
	n.events = n.events[:0]
	return events
}

func (n *Node) switchConfig(cfg types.Config) {

}

func (n *Node) isLeader() bool {
	return n.cfg.Role == types.RoleLeader
}

func (n *Node) isLearner(nodeId uint64) bool {
	if len(n.cfg.Learners) == 0 {
		return false
	}
	for _, learner := range n.cfg.Learners {
		if learner == nodeId {
			return true
		}
	}
	return false
}

func (n *Node) advance() {
	if n.opts.Advance != nil {
		n.opts.Advance()
	}
}

// NewPropose 提案
func (n *Node) NewPropose(data []byte) types.Event {
	return types.Event{
		Type: types.Propose,
		Logs: []types.Log{
			{
				Term:  n.cfg.Term,
				Index: n.queue.lastLogIndex + 1,
				Data:  data,
			},
		},
	}
}

func (n *Node) updateLastTermStartIndex(term uint32, index uint64) {
	n.lastTermStartIndex.Term = term
	n.lastTermStartIndex.Index = index
}
