package raft

import (
	"fmt"
	"sync"

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

// type softState struct {
// 	termStartIndex *types.TermStartIndexInfo
// }

type Node struct {
	events      []types.Event
	opts        *Options
	syncElapsed int // 同步计数
	tickFnc     func()
	stepFunc    func(event types.Event) error
	queue       *queue // 日志队列
	wklog.Log
	heartbeatElapsed int          // 心跳计时器
	cfg              types.Config // 分布式配置
	electionState                 // 选举状态
	syncState                     // 同步状态
	// 最新的任期对应的开始日志下标
	lastTermStartIndex types.TermStartIndexInfo
	onlySync           bool // 是否只同步,不做截断判断
	// softState softState // 软状态
	sync.Mutex
	truncating  bool // 截断中
	stopPropose bool // 停止提案

	suspend bool // 挂起

	idleTick int // 服务空闲计数

	syncing             bool // 正在同步
	syncRespTimeoutTick int  // 同步响应超时计数
}

func NewNode(lastTermStartLogIndex uint64, raftState types.RaftState, opts *Options) *Node {
	n := &Node{
		opts: opts,
		Log:  wklog.NewWKLog(fmt.Sprintf("raft.node[%s]", opts.Key)),
	}

	if raftState.AppliedIndex > raftState.LastLogIndex {
		n.Panic("applied index > last log index", zap.Uint64("appliedIndex", raftState.AppliedIndex), zap.Uint64("lastLogIndex", raftState.LastLogIndex))
	}

	n.cfg.Replicas = append(n.cfg.Replicas, opts.Replicas...)

	if raftState.LastTerm == 0 {
		n.cfg.Term = 1
	} else {
		n.cfg.Term = raftState.LastTerm
	}
	// 初始化日志队列
	n.queue = newQueue(opts.Key, raftState.AppliedIndex, raftState.LastLogIndex)

	// 初始化选举状态
	n.votes = make(map[uint64]bool)
	n.replicaSync = make(map[uint64]*SyncInfo)
	n.resetRandomizedElectionTimeout()

	n.lastTermStartIndex.Index = lastTermStartLogIndex
	n.lastTermStartIndex.Term = n.cfg.Term

	onlySelf := false
	if len(n.cfg.Replicas) == 1 {
		if n.cfg.Replicas[0] == opts.NodeId {
			onlySelf = true
		}
	}

	if onlySelf {
		if n.cfg.Term == 0 {
			n.cfg.Term = 1
		}
		n.BecomeLeader(n.cfg.Term)
	} else {
		if len(n.cfg.Replicas) > 0 {
			n.BecomeFollower(n.cfg.Term, None)
		}
	}

	return n
}

func (n *Node) Key() string {
	return n.opts.Key
}

// LastLogIndex 获取最后一条日志下标
func (n *Node) LastLogIndex() uint64 {
	return n.queue.lastLogIndex
}

// LastLogTerm 获取最后一条日志任期
func (n *Node) LastLogTerm() uint32 {
	return n.lastTermStartIndex.Term
}

// LastTerm 当前领导任期
func (n *Node) LastTerm() uint32 {
	return n.cfg.Term
}

// HasReady 是否有待处理的事件
func (n *Node) HasReady() bool {
	if n.queue.hasNextStoreLogs() {
		return true
	}
	if n.queue.hasNextApplyLogs() {
		return true
	}
	return len(n.events) > 0
}

// Suspend 是否挂起
func (n *Node) Suspend() bool {
	return n.suspend
}

// Ready 获取待处理的事件
func (n *Node) Ready() []types.Event {

	if n.queue.hasNextStoreLogs() {
		logs := n.queue.nextStoreLogs(0)
		if len(logs) > 0 {
			var termStartIndexInfo *types.TermStartIndexInfo
			for _, log := range logs {
				if n.lastTermStartIndex.Term != log.Term || log.Index == 1 {
					termStartIndexInfo = &types.TermStartIndexInfo{
						Term:  log.Term,
						Index: log.Index,
					}
					n.updateLastTermStartIndex(log.Term, log.Index)
					break
				}
			}
			n.sendStoreReq(logs, termStartIndexInfo)
		}
	}

	if n.queue.hasNextApplyLogs() {
		start, end := n.queue.nextApplyLogs()
		if start > 0 {
			n.sendApplyReq(start, end)
		}
	}

	events := n.events
	n.events = n.events[:0]
	return events
}

func (n *Node) LeaderId() uint64 {
	return n.cfg.Leader
}

func (n *Node) Config() types.Config {
	return n.cfg
}

func (n *Node) IsLeader() bool {
	return n.cfg.Leader == n.opts.NodeId
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

func (n *Node) CommittedIndex() uint64 {
	return n.queue.committedIndex
}

func (n *Node) AppliedIndex() uint64 {
	return n.queue.appliedIndex
}

func (n *Node) NodeId() uint64 {
	return n.opts.NodeId
}

// 获取某个副本的最新日志下标（领导节点才有这个信息）
func (n *Node) GetReplicaLastLogIndex(replicaId uint64) uint64 {
	if replicaId == n.opts.NodeId {
		return n.LastLogIndex()
	}
	syncInfo := n.replicaSync[replicaId]
	if syncInfo != nil && syncInfo.LastSyncIndex > 0 {
		return syncInfo.LastSyncIndex - 1
	}
	return 0
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
