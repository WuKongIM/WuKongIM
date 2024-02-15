package clusterconfig

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

var globalRand = &lockedRand{}

type lockedRand struct {
	mu sync.Mutex
}

func (r *lockedRand) Intn(n int) int {
	r.mu.Lock()
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	r.mu.Unlock()
	return int(v.Int64())
}

type Node struct {
	opts *Options
	wklog.Log
	state State

	leaderConfigVersion    uint64 // leader配置版本号
	localConfigVersion     uint64 // 本地配置版本号
	committedConfigVersion uint64 // 已提交的配置版本号
	appliedConfigVersion   uint64 // 已应用的配置版本号

	configData []byte // 配置数据

	role RoleType

	votes map[uint64]bool // 投票结果

	electionElapsed           int // 选举计时器
	heartbeatElapsed          int // 心跳计时器
	randomizedElectionTimeout int // 随机选举超时时间

	nodeConfigVersionMap map[uint64]uint64 // 每个节点当前配置的版本号

	tickFnc func()
	stepFnc func(m Message) error

	msgs []Message
}

func NewNode(opts *Options) *Node {

	n := &Node{
		opts:                 opts,
		Log:                  wklog.NewWKLog(fmt.Sprintf("Node[%d]", opts.NodeId)),
		nodeConfigVersionMap: make(map[uint64]uint64),
	}

	n.appliedConfigVersion = opts.AppliedConfigVersion
	n.committedConfigVersion = n.appliedConfigVersion
	n.localConfigVersion = n.appliedConfigVersion

	if len(opts.Replicas) == 0 {
		n.reset(1)
		n.becomeLeader()
	} else {
		n.becomeFollower(n.state.term, None)
	}

	return n
}

func (n *Node) Tick() {
	n.tickFnc()
}

func (n *Node) HasReady() bool {
	if len(n.msgs) > 0 {
		return true
	}
	if !n.isLeader() {
		if n.leaderConfigVersion != n.localConfigVersion {
			return true
		}
	}
	if n.committedConfigVersion > n.appliedConfigVersion {
		return true
	}

	return false
}

func (n *Node) Ready() Ready {
	rd := Ready{
		Messages: n.msgs,
	}
	if n.isLeader() {
		if n.hasNotifySync() {
			for _, nodeID := range n.opts.Replicas {
				if nodeID == n.opts.NodeId {
					continue
				}
				if n.nodeConfigVersionMap[nodeID] <= n.localConfigVersion {
					rd.Messages = append(rd.Messages, n.newNotifySync(nodeID))
				}
			}
		}
	}
	if n.committedConfigVersion > n.appliedConfigVersion {
		if !n.hasMsg(EventApply, rd.Messages) {
			n.Info("send apply request", zap.Uint64("term", uint64(n.state.term)), zap.Uint64("leader", n.state.leader), zap.Uint64("appliedConfigVersion", n.appliedConfigVersion), zap.Uint64("committedConfigVersion", n.committedConfigVersion))
			rd.Messages = append(rd.Messages, n.newApply())
		}
	}

	return rd
}

func (n *Node) AcceptReady(rd Ready) {
	n.msgs = nil
}

func (n *Node) HasLeader() bool { return n.state.leader != None }

func (n *Node) State() State {
	return n.state
}

func (n *Node) ProposeConfigChange(version uint64, configData []byte) error {
	if !n.isLeader() {
		n.Error("not leader, can not propose config change")
		return nil
	}
	return n.Step(Message{
		Type:          EventPropose,
		Term:          n.state.term,
		ConfigVersion: version,
		Config:        configData,
	})
}

func (n *Node) GetConfigData() []byte {
	return n.configData
}

func (n *Node) SetConfigData(data []byte) {
	n.configData = data
}

// 是否是单机
func (n *Node) isSingle() bool {

	return len(n.opts.Replicas) == 0 || (len(n.opts.Replicas) == 1 && n.opts.Replicas[0] == n.opts.NodeId)
}

func (n *Node) becomeFollower(term uint32, leader uint64) {
	n.stepFnc = n.stepFollower
	n.reset(term)
	n.tickFnc = n.tickElection
	n.state.leader = leader
	n.role = RoleFollower
	n.Info("become follower", zap.Uint64("term", uint64(n.state.term)), zap.Uint64("leader", n.state.leader))
}

func (n *Node) becomeCandidate() {
	if n.role == RoleLeader {
		n.Panic("invalid transition [leader -> candidate]")
	}
	n.stepFnc = n.stepCandidate
	n.reset(n.state.term + 1)
	n.tickFnc = n.tickElection
	n.state.voteFor = n.opts.NodeId
	n.state.leader = None
	n.role = RoleCandidate
	n.Info("become candidate", zap.Uint32("term", n.state.term))
}

func (n *Node) becomeLeader() {
	// if n.role == RoleFollower {
	// 	n.Panic("invalid transition [follower -> leader]")
	// }
	n.stepFnc = n.stepLeader
	n.reset(n.state.term)
	n.tickFnc = n.tickHeartbeat
	n.state.leader = n.opts.NodeId
	n.role = RoleLeader
	n.Info("become leader", zap.Uint32("term", n.state.term))

}

func (n *Node) reset(term uint32) {
	n.state.term = term
	n.state.voteFor = None
	n.votes = make(map[uint64]bool)
	n.electionElapsed = 0
	n.resetRandomizedElectionTimeout()
}

func (n *Node) tickElection() {
	n.electionElapsed++
	if n.pastElectionTimeout() { // 超时开始进行选举
		n.electionElapsed = 0
		err := n.Step(Message{
			Type: EventHup,
		})
		if err != nil {
			n.Debug("node tick election error", zap.Error(err))
			return
		}
	}
}

func (n *Node) hasMsg(eventType EventType, msgs []Message) bool {
	for _, msg := range msgs {
		if msg.Type == eventType {
			return true
		}
	}
	return false
}

func (n *Node) tickHeartbeat() {

	if !n.isLeader() {
		n.Warn("not leader, but call tickHeartbeat")
		return
	}
	n.heartbeatElapsed++
	n.electionElapsed++

	if n.electionElapsed >= n.opts.ElectionTimeoutTick {
		n.electionElapsed = 0
		// if n.isLeader() {
		// 	n.Warn("electionTimeout timeout, but still leader")
		// 	return
		// }
	}
	if n.heartbeatElapsed >= n.opts.HeartbeatTimeoutTick {
		n.heartbeatElapsed = 0
		if err := n.Step(Message{From: n.opts.NodeId, Type: EventBeat}); err != nil {
			n.Info("node tick heartbeat error", zap.Error(err))
		}
	}
}

func (n *Node) send(m Message) {
	n.msgs = append(n.msgs, m)
}

func (n *Node) pastElectionTimeout() bool {
	return n.electionElapsed >= n.randomizedElectionTimeout
}

func (n *Node) resetRandomizedElectionTimeout() {
	n.randomizedElectionTimeout = n.opts.ElectionTimeoutTick + globalRand.Intn(n.opts.ElectionTimeoutTick)
}

func (n *Node) isLeader() bool {
	return n.role == RoleLeader
}

type State struct {
	leader  uint64
	term    uint32
	voteFor uint64
}

func (s State) Leader() uint64 {
	return s.leader
}

func (s State) Term() uint32 {
	return s.term
}

func (s State) VoteFor() uint64 {
	return s.voteFor
}

type Ready struct {
	Messages []Message
}

func IsEmptyReady(rd Ready) bool {
	return len(rd.Messages) == 0
}

var EmptyReady = Ready{}
