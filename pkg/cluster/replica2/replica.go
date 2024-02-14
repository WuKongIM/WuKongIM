package replica

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type timeRecord struct {
	lastSentPing                   time.Time // 最后一次发送ping消息的时间
	lastNotifySync                 time.Time // 最后一次发送notifySync消息的时间
	lastMsgLeaderTermStartIndexReq time.Time // 最后一次发送MsgLeaderTermStartIndexReq消息的时间
}

type Replica struct {
	nodeID   uint64
	shardNo  string
	opts     *Options
	replicas []uint64 // 副本节点ID集合（不包含本节点）
	// -------------------- 节点状态 --------------------

	leader          uint64
	role            Role
	stepFunc        func(m Message) error
	uncommittedSize int // 未提交的日志大小
	msgs            []Message

	state               State
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
		// pongMap:          map[uint64]bool{},
		activeReplicaMap: make(map[uint64]time.Time),
		messageWait:      newMessageWait(opts.MessageSendInterval),
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
	// rc.becomeFollower(rc.state.term, None)

	rc.preHardState = HardState{
		Term:     rc.state.term,
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

func (r *Replica) NewProposeMessageWithLogs(logs []Log) Message {
	return Message{
		MsgType: MsgPropose,
		From:    r.nodeID,
		Term:    r.state.term,
		Logs:    logs,
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
		lastTerm, err := r.opts.Storage.LeaderLastTerm()
		if err != nil {
			r.Panic("get leader last term failed", zap.Error(err))
		}
		if r.state.term > lastTerm {
			err = r.opts.Storage.SetLeaderTermStartIndex(r.state.term, r.state.lastLogIndex+1) // 保存领导任期和领导任期开始的日志下标
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

func (r *Replica) State() State {
	return r.state
}

func (r *Replica) IsLeader() bool {
	return r.isLeader()
}

func (r *Replica) LeaderId() uint64 {
	return r.leader
}

func (r *Replica) SetReplicas(replicas []uint64) {
	r.replicas = replicas
}

func (r *Replica) isLeader() bool {
	return r.role == RoleLeader
}

func (r *Replica) readyWithoutAccept() Ready {
	r.putMsgIfNeed()

	rd := Ready{
		Messages: r.msgs,
	}
	if r.preHardState.LeaderId != r.leader || r.preHardState.Term != r.state.term {
		rd.HardState = HardState{
			LeaderId: r.leader,
			Term:     r.state.term,
		}
		r.preHardState = HardState{
			LeaderId: r.leader,
			Term:     r.state.term,
		}
	}
	return rd
}

func (r *Replica) HasReady() bool {
	if r.hasMsgs() {
		return true
	}
	if r.disabledToSync {
		if !r.IsLeader() && !r.messageWait.has(r.leader, MsgLeaderTermStartIndexReq) {
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

	if r.hasUnapplyLogs() {
		return true
	}

	return false
}

// 放入消息
func (r *Replica) putMsgIfNeed() {

	// if r.lastPutMsg.Add(r.opts.PutMsgInterval).After(time.Now()) {
	// 	fmt.Println("putMsgIfNeed.....", r.shardNo)
	// 	return
	// }

	if r.disabledToSync {
		if !r.IsLeader() && !r.messageWait.has(r.leader, MsgLeaderTermStartIndexReq) {
			seq := r.messageWait.next(r.leader, MsgLeaderTermStartIndexReq)
			r.msgs = append(r.msgs, r.newLeaderTermStartIndexReqMsg(seq))
		}
		return
	}

	if r.followNeedSync() {
		seq := r.messageWait.next(r.leader, MsgSync)
		r.msgs = append(r.msgs, r.newSyncMsg(seq))
	}

	if r.isLeader() {
		r.sendPingIfNeed()
	}

	// if r.hasNeedNotifySync() {
	// 	r.sendNotifySyncIfNeed()
	// }
	if r.hasUnapplyLogs() {
		// start := r.state.appliedIndex + 1
		// end := r.state.committedIndex + 1
		// logs, err := r.opts.Storage.Logs(r.state.appliedIndex+1, r.state.committedIndex+1, r.opts.SyncLimit)
		// if err != nil {
		// 	lastIndex, _ := r.opts.Storage.LastIndex()
		// 	r.Error("get logs failed", zap.Error(err), zap.Uint64("start", start), zap.Uint64("end", end), zap.Uint64("lastIndex", lastIndex))
		// 	return
		// }
		// if len(logs) == 0 {
		// 	lastIndex, _ := r.opts.Storage.LastIndex()
		// 	r.Error("get unapply logs failed", zap.Error(err), zap.Uint64("lastIndex", lastIndex), zap.Uint64("startLogIndex", start), zap.Uint64("endLogIndex", end))
		// 	return
		// }
		// if len(logs) > 0 {

		// }
		seq := r.messageWait.next(r.nodeID, MsgApplyLogsReq)
		r.msgs = append(r.msgs, r.newApplyLogReqMsg(seq, r.state.appliedIndex, r.state.committedIndex))
	}

}

func (r *Replica) acceptReady(rd Ready) {
	r.msgs = nil
	rd.HardState = EmptyHardState
}

func (r *Replica) reset(term uint32) {
	if r.state.term != term {
		r.state.term = term
	}
	r.leader = None
}

// 是否有未提交的日志
// func (r *Replica) hasUncommittedLogs() bool {
// 	return r.state.lastLogIndex > r.state.committedIndex
// }

// 是否有未应用的日志
func (r *Replica) hasUnapplyLogs() bool {
	if r.messageWait.has(r.nodeID, MsgApplyLogsReq) {
		return false
	}
	return r.state.committedIndex > r.state.appliedIndex
}

func (r *Replica) hasMsgs() bool {
	return len(r.msgs) > 0
}

// func (r *Replica) newNotifySyncMsgs() []Message {
// 	var msgs []Message
// 	for _, replicaID := range r.replicas {
// 		if replicaID == r.nodeID {
// 			continue
// 		}
// 		syncInfo := r.lastSyncInfoMap[replicaID]
// 		if syncInfo != nil {
// 			if syncInfo.LastSyncLogIndex > r.state.lastLogIndex { // 如果副本已经同步到最后一条日志，那么不需要继续同步
// 				continue
// 			}
// 		}
// 		seq := r.messageWait.next(replicaID, MsgNotifySync)
// 		msgs = append(msgs, r.newNotifySyncMsg(seq, replicaID))
// 	}
// 	return msgs
// }

func (r *Replica) newNotifySyncMsg(id uint64, replicaID uint64) Message {
	return Message{
		Id:             id,
		Key:            fmt.Sprintf("%s_replicaId:%d term:%d committedIndex:%d", r.shardNo, replicaID, r.state.term, r.state.committedIndex),
		MsgType:        MsgNotifySync,
		From:           r.nodeID,
		To:             replicaID,
		Term:           r.state.term,
		CommittedIndex: r.state.committedIndex,
		Responses: []Message{
			{
				Id:      id,
				MsgType: MsgNotifySyncAck,
				From:    replicaID,
				To:      r.nodeID,
			},
		},
	}
}

func (r *Replica) newApplyLogReqMsg(id uint64, appliedIndex, committedIndex uint64) Message {

	return Message{
		Id:             id,
		MsgType:        MsgApplyLogsReq,
		From:           r.nodeID,
		To:             r.nodeID,
		Term:           r.state.term,
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
		Term:    r.state.term,
		Index:   r.state.lastLogIndex + 1,
		Responses: []Message{
			{
				Id:      id,
				MsgType: MsgSyncAck,
				From:    r.leader,
				To:      r.nodeID,
			},
		},
	}
}

func (r *Replica) newPing(id uint64, to uint64) Message {
	return Message{
		Id:             id,
		MsgType:        MsgPing,
		From:           r.nodeID,
		To:             to,
		Term:           r.state.term,
		CommittedIndex: r.state.committedIndex,
		Responses: []Message{
			{
				Id:      id,
				MsgType: MsgPingAck,
				From:    to,
				To:      r.nodeID,
			},
		},
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
		Responses: []Message{
			{
				Id:      id,
				MsgType: MsgLeaderTermStartIndexReqAck,
				From:    r.leader,
				To:      r.nodeID,
			},
		},
	}
}

func (r *Replica) newPong(to uint64) Message {
	return Message{
		MsgType:        MsgPong,
		From:           r.nodeID,
		To:             to,
		Term:           r.state.term,
		CommittedIndex: r.state.committedIndex,
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

func hasMsg(msgType MsgType, msgs []Message) bool {
	for _, msg := range msgs {
		if msg.MsgType == msgType {
			return true
		}
	}
	return false
}

func (r *Replica) followNeedSync() bool {
	if r.LeaderId() == 0 {
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
