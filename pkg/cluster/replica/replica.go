package replica

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type Replica struct {
	nodeID   uint64
	shardNo  string
	opts     *Options
	replicas []uint64 // 副本节点ID集合（不包含本节点）
	// -------------------- 节点状态 --------------------

	leader   uint64
	role     Role
	stepFunc func(m Message) error
	msgs     []Message

	// state               State
	preHardState        HardState // 上一个硬状态
	localLeaderLastTerm uint32    // 本地领导任期，本地保存的term和startLogIndex的数据中最大的term，如果没有则为0

	// -------------------- leader --------------------
	lastSyncInfoMap map[uint64]*SyncInfo // 副本日志信息
	// pongMap          map[uint64]bool      // 已回应pong的节点
	activeReplicaMap map[uint64]time.Time // 已经激活了的副本

	// -------------------- follower --------------------
	// 禁止去同步领导的日志，因为这时候应该发起了 MsgLeaderTermStartOffsetReq 消息 还没有收到回复，只有收到回复后，追随者截断未知的日志后，才能去同步领导的日志，主要是解决日志冲突的问题
	disabledToSync bool

	// -------------------- 其他 --------------------
	messageWait *messageWait
	wklog.Log
	replicaLog      *replicaLog
	uncommittedSize logEncodingSize

	hasFirstSyncResp bool // 是否有第一次同步的回应

}

func New(nodeID uint64, shardNo string, optList ...Option) *Replica {
	opts := NewOptions()
	for _, opt := range optList {
		opt(opts)
	}
	opts.NodeID = nodeID
	opts.ShardNo = shardNo
	rc := &Replica{
		nodeID:          nodeID,
		shardNo:         shardNo,
		Log:             wklog.NewWKLog(fmt.Sprintf("replica[%d:%s]", nodeID, shardNo)),
		opts:            opts,
		lastSyncInfoMap: map[uint64]*SyncInfo{},
		// pongMap:          map[uint64]bool{},
		activeReplicaMap: make(map[uint64]time.Time),
		messageWait:      newMessageWait(opts.MessageSendInterval),
		replicaLog:       newReplicaLog(opts),
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

	rc.localLeaderLastTerm = lastLeaderTerm
	// rc.becomeFollower(rc.state.term, None)

	rc.preHardState = HardState{
		Term:     rc.replicaLog.term,
		LeaderId: rc.leader,
	}

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

func (r *Replica) BecomeFollower(term uint32, leaderID uint64) {

	r.becomeFollower(term, leaderID)
}

func (r *Replica) becomeFollower(term uint32, leaderID uint64) {
	r.stepFunc = r.stepFollower
	r.reset(term)
	r.replicaLog.term = term
	r.leader = leaderID
	r.role = RoleFollower

	r.Debug("become follower", zap.Uint32("term", term), zap.Uint64("leader", leaderID))

	if r.replicaLog.lastLogIndex > 0 && r.leader != None {
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
	r.replicaLog.term = term
	r.leader = r.nodeID
	r.role = RoleLeader
	r.disabledToSync = false

	r.Debug("become leader", zap.Uint32("term", r.replicaLog.term))

	if r.replicaLog.lastLogIndex > 0 {
		lastTerm, err := r.opts.Storage.LeaderLastTerm()
		if err != nil {
			r.Panic("get leader last term failed", zap.Error(err))
		}
		if r.replicaLog.term > lastTerm {
			err = r.opts.Storage.SetLeaderTermStartIndex(r.replicaLog.term, r.replicaLog.lastLogIndex+1) // 保存领导任期和领导任期开始的日志下标
			if err != nil {
				r.Panic("set leader term start index failed", zap.Error(err))
			}
		}
	}
	r.activeReplicaMap = make(map[uint64]time.Time)
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

func (r *Replica) IsLeader() bool {

	return r.isLeader()
}

func (r *Replica) LeaderId() uint64 {

	return r.leader
}

func (r *Replica) SetReplicas(replicas []uint64) {

	r.opts.Replicas = replicas
	r.replicas = nil
	for _, replicaID := range r.opts.Replicas {
		if replicaID == r.nodeID {
			continue
		}
		r.replicas = append(r.replicas, replicaID)
	}
}

// HasFirstSyncResp 有收到领导的第一次同步的回应
func (r *Replica) HasFirstSyncResp() bool {

	return r.hasFirstSyncResp
}

func (r *Replica) isLeader() bool {
	return r.role == RoleLeader
}

func (r *Replica) readyWithoutAccept() Ready {
	r.putMsgIfNeed()

	rd := Ready{
		Messages: r.msgs,
	}
	if r.preHardState.LeaderId != r.leader || r.preHardState.Term != r.replicaLog.term {
		rd.HardState = HardState{
			LeaderId: r.leader,
			Term:     r.replicaLog.term,
		}
		r.preHardState = HardState{
			LeaderId: r.leader,
			Term:     r.replicaLog.term,
		}
	}
	return rd
}

func (r *Replica) HasReady() bool {
	if r.hasMsgs() {
		return true
	}
	if r.disabledToSync {
		if !r.isLeader() && !r.messageWait.has(r.leader, MsgLeaderTermStartIndexReq) {
			return true
		}
		return false
	}

	if r.followNeedSync() {
		return true
	}

	if r.isLeader() {
		if r.hasNeedPing() {
			return true
		}
		// if r.hasNeedNotifySync() {
		// 	return true
		// }
	}

	if r.hasUnstableLogs() { // 有未存储的日志
		return true
	}

	if r.hasUnapplyLogs() { // 有未应用的日志
		return true
	}

	return false
}

// 放入消息
func (r *Replica) putMsgIfNeed() {

	if r.disabledToSync {
		if !r.IsLeader() && !r.messageWait.has(r.leader, MsgLeaderTermStartIndexReq) {
			seq := r.messageWait.next(r.leader, MsgLeaderTermStartIndexReq)
			r.msgs = append(r.msgs, r.newLeaderTermStartIndexReqMsg(seq))
		}
		return
	}

	// 副本来同步日志
	if r.followNeedSync() {
		seq := r.messageWait.next(r.leader, MsgSync)
		r.msgs = append(r.msgs, r.newSyncMsg(seq))
	}

	if r.isLeader() {
		r.sendPingIfNeed()
	}

	// 追加日志
	if r.hasUnstableLogs() {
		logs := r.replicaLog.nextUnstableLogs()
		r.msgs = append(r.msgs, r.newMsgStoreAppend(logs))
	}

	// 应用日志
	if r.hasUnapplyLogs() {
		r.msgs = append(r.msgs, r.newApplyLogReqMsg(r.replicaLog.applyingIndex, r.replicaLog.appliedIndex, r.replicaLog.committedIndex))
	}

}

func (r *Replica) acceptReady(rd Ready) {

	r.msgs = nil
	rd.HardState = EmptyHardState
	r.replicaLog.acceptUnstable()
	if r.hasUnapplyLogs() {
		r.replicaLog.acceptApplying(r.replicaLog.committedIndex, 0)
	}
}

func (r *Replica) reset(term uint32) {
	if r.replicaLog.term != term {
		r.replicaLog.term = term
	}
	r.leader = None
}

// 是否有未提交的日志
// func (r *Replica) hasUncommittedLogs() bool {
// 	return r.state.lastLogIndex > r.state.committedIndex
// }

// 是否有未存储的日志
func (r *Replica) hasUnstableLogs() bool {

	return r.replicaLog.hasNextUnstableLogs()
}

// 是否有未应用的日志
func (r *Replica) hasUnapplyLogs() bool {

	return r.replicaLog.hasUnapplyLogs()
}

func (r *Replica) hasMsgs() bool {
	return len(r.msgs) > 0
}

func (r *Replica) reduceUncommittedSize(s logEncodingSize) {
	if s > r.uncommittedSize {
		// uncommittedSize may underestimate the size of the uncommitted Raft
		// log tail but will never overestimate it. Saturate at 0 instead of
		// allowing overflow.
		r.uncommittedSize = 0
	} else {
		r.uncommittedSize -= s
	}
}

func (r *Replica) NewProposeMessage(data []byte) Message {
	return Message{
		MsgType: MsgPropose,
		From:    r.nodeID,
		Term:    r.replicaLog.term,
		Logs: []Log{
			{
				Index: r.replicaLog.lastLogIndex + 1,
				Term:  r.replicaLog.term,
				Data:  data,
			},
		},
	}
}

func (r *Replica) NewProposeMessageWithLogs(logs []Log) Message {
	return Message{
		MsgType: MsgPropose,
		From:    r.nodeID,
		Term:    r.replicaLog.term,
		Logs:    logs,
	}
}

func (r *Replica) NewMsgApplyLogsRespMessage(appliedIdx uint64) Message {
	return Message{
		MsgType: MsgApplyLogsResp,
		From:    r.nodeID,
		Term:    r.replicaLog.term,
		Index:   appliedIdx,
	}
}

func (r *Replica) NewMsgStoreAppendResp(index uint64) Message {
	return Message{
		MsgType: MsgStoreAppendResp,
		From:    r.nodeID,
		To:      r.nodeID,
		Index:   index,
	}

}

func (r *Replica) newMsgSyncGet(from uint64, index uint64, unstableLogs []Log) Message {
	return Message{
		MsgType: MsgSyncGet,
		From:    from,
		To:      r.nodeID,
		Index:   index,
		Logs:    unstableLogs,
	}
}

func (r *Replica) newMsgSyncResp(to uint64, startIndex uint64, logs []Log) Message {
	return Message{
		MsgType:        MsgSyncResp,
		From:           r.nodeID,
		To:             to,
		Term:           r.replicaLog.term,
		Logs:           logs,
		Index:          startIndex,
		CommittedIndex: r.replicaLog.committedIndex,
	}
}

func (r *Replica) NewMsgSyncGetResp(to uint64, startIndex uint64, logs []Log) Message {
	return Message{
		MsgType: MsgSyncGetResp,
		From:    r.nodeID,
		To:      to,
		Logs:    logs,
		Index:   startIndex,
	}
}

func (r *Replica) newApplyLogReqMsg(applyingIndex, appliedIndex, committedIndex uint64) Message {

	return Message{
		MsgType:        MsgApplyLogsReq,
		From:           r.nodeID,
		To:             r.nodeID,
		Term:           r.replicaLog.term,
		ApplyingIndex:  applyingIndex,
		AppliedIndex:   appliedIndex,
		CommittedIndex: committedIndex,
	}
}

func (r *Replica) newSyncMsg(id uint64) Message {
	return Message{
		Id:      id,
		MsgType: MsgSync,
		From:    r.nodeID,
		To:      r.leader,
		Term:    r.replicaLog.term,
		Index:   r.replicaLog.lastLogIndex + 1,
	}
}

func (r *Replica) newPing(id uint64, to uint64) Message {
	return Message{
		Id:             id,
		MsgType:        MsgPing,
		From:           r.nodeID,
		To:             to,
		Term:           r.replicaLog.term,
		Index:          r.replicaLog.lastLogIndex,
		CommittedIndex: r.replicaLog.committedIndex,
	}
}

func (r *Replica) newLeaderTermStartIndexReqMsg(id uint64) Message {
	leaderLastTerm, err := r.opts.Storage.LeaderLastTerm()
	if err != nil {
		r.Panic("get leader last term failed", zap.Error(err))
	}

	return Message{
		Id:      id,
		MsgType: MsgLeaderTermStartIndexReq,
		From:    r.nodeID,
		To:      r.leader,
		Term:    leaderLastTerm,
	}
}

func (r *Replica) newPong(to uint64) Message {
	return Message{
		MsgType:        MsgPong,
		From:           r.nodeID,
		To:             to,
		Term:           r.replicaLog.term,
		CommittedIndex: r.replicaLog.committedIndex,
	}
}

func (r *Replica) newLeaderTermStartIndexResp(to uint64, term uint32, index uint64) Message {
	return Message{
		MsgType: MsgLeaderTermStartIndexResp,
		From:    r.nodeID,
		To:      to,
		Term:    term,
		Index:   index,
	}
}

func (r *Replica) newMsgStoreAppend(logs []Log) Message {
	return Message{
		MsgType: MsgStoreAppend,
		From:    r.nodeID,
		To:      r.nodeID,
		Logs:    logs,
	}

}

func (r *Replica) UnstableLogLen() int {
	return len(r.replicaLog.unstable.logs)
}

func hasMsg(msgType MsgType, msgs []Message) bool {
	for _, msg := range msgs {
		if msg.MsgType == msgType {
			return true
		}
	}
	return false
}

func (r *Replica) followNeedSync() bool {
	if r.leader == 0 {
		return false
	}
	if r.isLeader() {
		return false
	}
	if r.messageWait.has(r.leader, MsgSync) {
		return false
	}
	return true
}
