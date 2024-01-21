package replica

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type Replica struct {
	nodeID     uint64
	shardNo    string
	opts       *Options
	replicas   []uint64  // 副本节点ID集合（不包含本节点）
	lastPutMsg time.Time //  最后一次放入消息的时间
	// -------------------- 节点状态 --------------------

	leader          uint64
	role            Role
	stepFunc        func(m Message) error
	uncommittedSize int // 未提交的日志大小
	msgs            []Message

	state State

	localLeaderLastTerm uint32 // 本地领导任期，本地保存的term和startLogIndex的数据中最大的term，如果没有则为0

	// -------------------- leader --------------------
	lastSyncInfoMap map[uint64]*SyncInfo // 副本日志信息

	// -------------------- follower --------------------
	// 禁止去同步领导的日志，因为这时候应该发起了 MsgLeaderTermStartOffsetReq 消息 还没有收到回复，只有收到回复后，追随者截断未知的日志后，才能去同步领导的日志，主要是解决日志冲突的问题
	disabledToSync bool

	// -------------------- 其他 --------------------
	wklog.Log
}

func New(nodeID uint64, shardNo string, optList ...Option) *Replica {
	opts := NewOptions()
	for _, opt := range optList {
		opt(opts)
	}
	opts.NodeID = nodeID
	opts.ShardNo = shardNo
	rc := &Replica{
		nodeID:  nodeID,
		shardNo: shardNo,
		Log:     wklog.NewWKLog(fmt.Sprintf("replica[%d:%s]", nodeID, shardNo)),
		opts:    opts,
		state: State{
			appliedIndex: opts.AppliedIndex,
		},
		lastSyncInfoMap: map[uint64]*SyncInfo{},
	}
	lastIndex, err := rc.opts.Storage.LastIndex()
	if err != nil {
		rc.Panic("get last index failed", zap.Error(err))
	}
	lastLeaderTerm, err := rc.opts.Storage.LeaderLastTerm()
	if err != nil {
		rc.Panic("get last leader term failed", zap.Error(err))
	}
	for _, replicaID := range rc.opts.Replicas {
		if replicaID == nodeID {
			continue
		}
		rc.replicas = append(rc.replicas, replicaID)
	}
	rc.state.lastLogIndex = lastIndex
	rc.state.committedIndex = opts.AppliedIndex
	if lastLeaderTerm == 0 {
		rc.state.term = 1
	} else {
		rc.state.term = lastLeaderTerm
	}
	rc.localLeaderLastTerm = lastLeaderTerm
	rc.becomeFollower(rc.state.term, None)
	return rc
}

func (r *Replica) Ready() Ready {
	rd := r.readyWithoutAccept()
	r.acceptReady(rd)
	return rd
}

func (r *Replica) Propose(data []byte) error {

	return r.Step(r.NewProposeMessage(data))
}

func (r *Replica) NewProposeMessage(data []byte) Message {
	return Message{
		MsgType: MsgPropose,
		From:    r.nodeID,
		Term:    r.state.term,
		Logs: []Log{
			{
				Index: r.state.lastLogIndex + 1,
				Term:  r.state.term,
				Data:  data,
			},
		},
	}
}

func (r *Replica) NewMsgApplyLogsRespMessage(appliedIdx uint64) Message {
	return Message{
		MsgType: MsgApplyLogsResp,
		From:    r.nodeID,
		Term:    r.state.term,
		Index:   appliedIdx,
	}
}

func (r *Replica) BecomeFollower(term uint32, leaderID uint64) {
	r.becomeFollower(term, leaderID)
}

func (r *Replica) becomeFollower(term uint32, leaderID uint64) {
	r.stepFunc = r.stepFollower
	r.reset(term)
	r.state.term = term
	r.leader = leaderID
	r.role = RoleFollower

	r.Info("become follower", zap.Uint32("term", term), zap.Uint64("leader", leaderID))

	if r.state.lastLogIndex > 0 && r.leader != None {
		r.Info("disable to sync resolve log conflicts", zap.Uint64("leader", r.leader))
		r.disabledToSync = true // 禁止去同步领导的日志,等待本地日志冲突解决后，再去同步领导的日志
	}

}

func (r *Replica) BecomeLeader(term uint32) {
	r.becomeLeader(term)
}

func (r *Replica) becomeLeader(term uint32) {
	r.stepFunc = r.stepLeader
	r.reset(term)
	r.leader = r.nodeID
	r.role = RoleLeader
	r.disabledToSync = false

	r.Info("become leader", zap.Uint32("term", r.state.term))

	if r.state.lastLogIndex > 0 {
		err := r.opts.Storage.SetLeaderTermStartIndex(r.state.term, r.state.lastLogIndex+1) // 保存领导任期和领导任期开始的日志下标
		if err != nil {
			r.Panic("set leader term start index failed", zap.Error(err))
		}
	}

}

// func (r *Replica) becomeCandidate() {
// 	if r.role == RoleLeader {
// 		r.Panic("invalid transition [leader -> candidate]", zap.Uint64("nodeID", r.nodeID))
// 	}
// 	r.stepFunc = r.stepCandidate
// 	r.reset(r.term + 1)
// 	r.voteFor = r.nodeID
// 	r.role = RoleCandidate

// 	r.Info("become candidate", zap.Uint32("term", r.term), zap.Uint64("nodeID", r.nodeID))

// }

func (r *Replica) State() State {
	return r.state
}

func (r *Replica) IsLeader() bool {
	return r.isLeader()
}

func (r *Replica) isLeader() bool {
	return r.role == RoleLeader
}

func (r *Replica) readyWithoutAccept() Ready {
	r.putMsgIfNeed()

	rd := Ready{
		Messages: r.msgs,
	}
	return rd
}

func (r *Replica) HasReady() bool {
	if r.hasMsgs() {
		return true
	}
	if r.lastPutMsg.Add(r.opts.PutMsgInterval).Before(time.Now()) {
		if r.disabledToSync && !r.isLeader() {
			return true
		}
		if r.hasUncommittedLogs() && r.isLeader() {
			return true
		}
		if r.hasUnapplyLogs() {
			return true
		}
		return false
	}

	return false
}

// 放入消息
func (r *Replica) putMsgIfNeed() {

	if r.lastPutMsg.Add(r.opts.PutMsgInterval).After(time.Now()) {
		return
	}

	oldCount := len(r.msgs)
	defer func() {
		if len(r.msgs) > oldCount {
			r.lastPutMsg = time.Now()
		}
	}()

	if r.disabledToSync {
		if !r.IsLeader() && !hasMsg(MsgLeaderTermStartIndexReq, r.msgs) {
			r.msgs = append(r.msgs, r.newLeaderTermStartIndexReqMsg())
		}
		return
	}
	if r.hasUncommittedLogs() {
		if r.isLeader() && !hasMsg(MsgSync, r.msgs) {
			r.msgs = append(r.msgs, r.newNotifySyncMsgs()...)
		}
	}
	if r.hasUnapplyLogs() {
		if !hasMsg(MsgApplyLogsReq, r.msgs) {
			logs, err := r.opts.Storage.Logs(r.state.appliedIndex+1, r.state.committedIndex+1, r.opts.SyncLimit)
			if err != nil {
				r.Error("get logs failed", zap.Error(err))
				return
			}
			if len(logs) == 0 {
				r.Error("get unapply logs failed", zap.Error(err))
				return
			}
			if len(logs) > 0 {
				r.msgs = append(r.msgs, r.newApplyLogReqMsg(logs))
			}
		}
	}
}

func (r *Replica) acceptReady(rd Ready) {
	r.msgs = nil
}

func (r *Replica) reset(term uint32) {
	if r.state.term != term {
		r.state.term = term
	}
	r.leader = None
}

// 是否有未提交的日志
func (r *Replica) hasUncommittedLogs() bool {
	return r.state.lastLogIndex > r.state.committedIndex
}

// 是否有未应用的日志
func (r *Replica) hasUnapplyLogs() bool {
	return r.state.committedIndex > r.state.appliedIndex
}

func (r *Replica) hasMsgs() bool {
	return len(r.msgs) > 0
}

func (r *Replica) newNotifySyncMsgs() []Message {
	var msgs []Message
	for _, replicaID := range r.replicas {
		if replicaID == r.nodeID {
			continue
		}
		syncInfo := r.lastSyncInfoMap[replicaID]
		if syncInfo != nil {
			if syncInfo.LastSyncLogIndex > r.state.lastLogIndex { // 如果副本已经同步到最后一条日志，那么不需要继续同步
				continue
			}
		}
		msgs = append(msgs, r.newNotifySyncMsg(replicaID))
	}
	return msgs
}

func (r *Replica) newNotifySyncMsg(replicaID uint64) Message {
	return Message{
		No:      fmt.Sprintf("notifySync:%d:%d:%s", r.state.term, replicaID, r.shardNo),
		MsgType: MsgNotifySync,
		From:    r.nodeID,
		To:      replicaID,
		Term:    r.state.term,
	}
}

func (r *Replica) newApplyLogReqMsg(logs []Log) Message {

	return Message{
		MsgType: MsgApplyLogsReq,
		From:    r.nodeID,
		To:      r.nodeID,
		Term:    r.state.term,
		Logs:    logs,
	}
}

func (r *Replica) newSyncMsg() Message {
	return Message{
		MsgType: MsgSync,
		From:    r.nodeID,
		To:      r.leader,
		Term:    r.state.term,
	}
}

func (r *Replica) newLeaderTermStartIndexReqMsg() Message {
	leaderLastTerm, err := r.opts.Storage.LeaderLastTerm()
	if err != nil {
		r.Panic("get leader last term failed", zap.Error(err))
	}
	return Message{
		No:      fmt.Sprintf("leaderTermStartOffsetReq:%d:%d:%s", leaderLastTerm, r.nodeID, r.shardNo),
		MsgType: MsgLeaderTermStartIndexReq,
		From:    r.nodeID,
		To:      r.leader,
		Term:    leaderLastTerm,
	}
}

func hasMsg(msgType MsgType, msgs []Message) bool {
	for _, msg := range msgs {
		if msg.MsgType == msgType {
			return true
		}
	}
	return false
}
