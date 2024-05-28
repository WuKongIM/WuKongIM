package replica

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type Replica struct {
	nodeId   uint64
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
	activeReplicas map[uint64]bool // 已经激活了的副本

	// -------------------- follower --------------------
	// 禁止去同步领导的日志，因为这时候应该发起了 MsgLeaderTermStartOffsetReq 消息 还没有收到回复，只有收到回复后，追随者截断未知的日志后，才能去同步领导的日志，主要是解决日志冲突的问题
	disabledToSync bool

	// -------------------- election --------------------
	electionElapsed           int // 选举计时器
	heartbeatElapsed          int
	randomizedElectionTimeout int // 随机选举超时时间
	tickFnc                   func()
	voteFor                   uint64          // 投票给谁
	votes                     map[uint64]bool // 投票记录

	// -------------------- 其他 --------------------
	messageWait *messageWait
	wklog.Log
	replicaLog      *replicaLog
	uncommittedSize logEncodingSize

	// hasFirstSyncResp bool       // 是否有第一次同步的回应
	speedLevel SpeedLevel // 当前速度等级
}

func New(nodeId uint64, optList ...Option) *Replica {
	opts := NewOptions()
	for _, opt := range optList {
		opt(opts)
	}
	opts.NodeId = nodeId
	if opts.Storage == nil {
		opts.Storage = NewMemoryStorage()
	}
	rc := &Replica{
		nodeId:          nodeId,
		Log:             wklog.NewWKLog(fmt.Sprintf("replica[%d:%s]", nodeId, opts.LogPrefix)),
		opts:            opts,
		lastSyncInfoMap: map[uint64]*SyncInfo{},
		// pongMap:          map[uint64]bool{},
		activeReplicas: make(map[uint64]bool),
		messageWait:    newMessageWait(opts.ReplicaMaxCount),
		replicaLog:     newReplicaLog(opts),
		speedLevel:     LevelFast,
	}

	lastLeaderTerm, err := rc.opts.Storage.LeaderLastTerm()
	if err != nil {
		rc.Panic("get last leader term failed", zap.Error(err))
	}
	for _, replicaID := range rc.opts.Config.Replicas {
		if replicaID == nodeId {
			continue
		}
		rc.replicas = append(rc.replicas, replicaID)
	}

	rc.localLeaderLastTerm = lastLeaderTerm

	if rc.opts.ElectionOn {
		if rc.IsSingleNode() { // 如果是单节点，直接成为领导
			var term uint32 = 1
			if rc.replicaLog.term > 0 {
				term = rc.replicaLog.term
			}
			rc.becomeLeader(term)
		} else {
			rc.becomeFollower(rc.replicaLog.term, None)
		}
	}

	return rc
}

// 是否是单节点
func (r *Replica) IsSingleNode() bool {
	return len(r.replicas) == 0
}

func (r *Replica) activeReplica(replicaId uint64) {
	r.activeReplicas[replicaId] = true
}

func (r *Replica) isActiveReplica(replicaId uint64) bool {
	ok := r.activeReplicas[replicaId]
	return ok
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

func (r *Replica) BecomeCandidate() {
	r.becomeCandidate()
}

func (R *Replica) BecomeCandidateWithTerm(term uint32) {
	R.becomeCandidateWithTerm(term)
}

// 降速
func (r *Replica) SlowDown() {
	if !r.isLeader() {
		return
	}
	switch r.speedLevel {
	case LevelFast:
		r.speedLevel = LevelNormal
	case LevelNormal:
		r.speedLevel = LevelMiddle
	case LevelMiddle:
		r.speedLevel = LevelSlow
	case LevelSlow:
		r.speedLevel = LevelSlowest
	case LevelSlowest:
		r.speedLevel = LevelStop
	}
	r.setSpeedLevel(r.speedLevel)

}

func (r *Replica) SetSpeedLevel(level SpeedLevel) {

	var notify bool

	if r.speedLevel != level {
		notify = true
	}
	r.setSpeedLevel(level)
	if notify && r.isLeader() {
		r.sendPing()
	}
}

func (r *Replica) setSpeedLevel(level SpeedLevel) {

	r.speedLevel = level

	switch level {
	case LevelFast:
		r.messageWait.setSyncIntervalTickCount(1)
	case LevelNormal:
		r.messageWait.setSyncIntervalTickCount(2)
	case LevelSlow:
		r.messageWait.setSyncIntervalTickCount(10)
	case LevelSlowest:
		r.messageWait.setSyncIntervalTickCount(50)
	case LevelStop: // 这种情况基本是停止状态，要么等待重新激活，要么等待被销毁
		r.messageWait.setSyncIntervalTickCount(100000)
	}

}

func (r *Replica) SpeedLevel() SpeedLevel {
	return r.speedLevel
}

func (r *Replica) Role() Role {
	return r.role
}

func (r *Replica) becomeFollower(term uint32, leaderID uint64) {
	r.stepFunc = r.stepFollower
	r.reset(term)
	r.tickFnc = r.tickElection
	r.replicaLog.term = term
	r.leader = leaderID
	r.role = RoleFollower

	r.Info("become follower", zap.Uint32("term", term), zap.Uint64("leader", leaderID))

	r.messageWait.immediatelySync() // 立马可以同步了

	if r.replicaLog.lastLogIndex > 0 && r.leader != None {
		r.Info("disable to sync resolve log conflicts", zap.Uint64("leader", r.leader))
		r.disabledToSync = true // 禁止去同步领导的日志,等待本地日志冲突解决后，再去同步领导的日志
	}

}

func (r *Replica) becomeLearner(term uint32, leaderID uint64) {
	r.stepFunc = r.stepLearner
	r.reset(term)
	r.tickFnc = nil
	r.replicaLog.term = term
	r.leader = leaderID
	r.role = RoleLearner

	r.Info("become learner", zap.Uint32("term", term), zap.Uint64("leader", leaderID))
	r.messageWait.immediatelySync() // 立马可以同步了

	if r.replicaLog.lastLogIndex > 0 && r.leader != None {
		r.Info("disable to sync resolve log conflicts", zap.Uint64("leader", r.leader))
		r.disabledToSync = true // 禁止去同步领导的日志,等待本地日志冲突解决后，再去同步领导的日志
	}
}

func (r *Replica) becomeCandidate() {
	r.becomeCandidateWithTerm(r.replicaLog.term + 1)
}

func (r *Replica) becomeCandidateWithTerm(term uint32) {
	if r.role == RoleLeader {
		r.Panic("invalid transition [leader -> candidate]")
	}
	r.stepFunc = r.stepCandidate
	r.reset(term)
	r.tickFnc = r.tickElection
	r.voteFor = r.opts.NodeId
	r.leader = None
	r.role = RoleCandidate
	r.Info("become candidate", zap.Uint32("term", r.replicaLog.term))
}

func (r *Replica) BecomeLeader(term uint32) {

	r.becomeLeader(term)
}

func (r *Replica) BecomeLearner(term uint32, leaderID uint64) {
	r.becomeLearner(term, leaderID)
}

func (r *Replica) becomeLeader(term uint32) {

	r.stepFunc = r.stepLeader
	r.reset(term)
	r.tickFnc = r.tickHeartbeat
	r.replicaLog.term = term
	r.leader = r.nodeId
	r.role = RoleLeader
	r.disabledToSync = false

	r.Info("become leader", zap.Uint32("term", r.replicaLog.term))

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
	r.activeReplicas = make(map[uint64]bool)
	r.messageWait.immediatelyPing() // 立马可以发起ping

}

// 开始选举
func (r *Replica) campaign() {
	r.becomeCandidate()
	for _, nodeId := range r.opts.Config.Replicas {
		if nodeId == r.opts.NodeId {
			// 自己给自己投一票
			r.send(Message{To: nodeId, From: nodeId, Term: r.replicaLog.term, MsgType: MsgVoteResp})
			continue
		}
		r.Info("sent vote request", zap.Uint64("from", r.opts.NodeId), zap.Uint64("to", nodeId), zap.Uint32("term", r.replicaLog.term))
		r.sendRequestVote(nodeId)
	}
}

func (r *Replica) sendRequestVote(nodeId uint64) {
	r.send(r.newMsgVoteReq(nodeId))
}

func (r *Replica) hup() {
	r.campaign()
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

func (r *Replica) IsFollower() bool {
	return r.role == RoleFollower
}

func (r *Replica) LeaderId() uint64 {

	return r.leader
}

// 获取某个副本的最新日志下标（领导节点才有这个信息）
func (r *Replica) GetReplicaLastLog(replicaId uint64) uint64 {
	if replicaId == r.opts.NodeId {
		return r.LastLogIndex()
	}
	syncInfo := r.lastSyncInfoMap[replicaId]
	if syncInfo != nil && syncInfo.LastSyncLogIndex > 0 {
		return syncInfo.LastSyncLogIndex - 1
	}
	return 0
}

func (r *Replica) SetConfig(cfg *Config) {
	r.opts.Config = cfg
	r.replicas = nil
	for _, replicaID := range cfg.Replicas {
		if replicaID == r.nodeId {
			continue
		}
		r.replicas = append(r.replicas, replicaID)
	}
}

func (r *Replica) SwitchConfig(cfg *Config) {
	r.switchConfig(cfg)
}

func (r *Replica) switchConfig(cfg *Config) {
	r.SetConfig(cfg)
	if r.role == RoleLearner {
		if !wkutil.ArrayContainsUint64(cfg.Learners, r.opts.NodeId) && wkutil.ArrayContainsUint64(cfg.Replicas, r.opts.NodeId) {
			r.becomeFollower(r.replicaLog.term, r.leader)
		}
	}
}

func (r *Replica) isLearner(nodeId uint64) bool {
	if len(r.opts.Config.Learners) == 0 {
		return false
	}
	for _, learner := range r.opts.Config.Learners {
		if learner == nodeId {
			return true
		}
	}
	return false
}

// // HasFirstSyncResp 有收到领导的第一次同步的回应
// func (r *Replica) HasFirstSyncResp() bool {

// 	return r.hasFirstSyncResp
// }

func (r *Replica) isLeader() bool {
	return r.role == RoleLeader
}

func (r *Replica) hardStateChange() bool {
	return r.preHardState.LeaderId != r.leader || r.preHardState.Term != r.replicaLog.term
}

func (r *Replica) readyWithoutAccept() Ready {
	r.putMsgIfNeed()

	rd := Ready{
		Messages: r.msgs,
	}
	if r.hardStateChange() {
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

	if r.hardStateChange() {
		return true
	}

	if r.speedLevel != LevelStop {
		if r.disabledToSync {
			return !r.isLeader() && r.messageWait.canMsgLeaderTermStartIndex()
		}

		if r.followNeedSync() {
			return true
		}

		if r.isLeader() {
			if r.hasNeedPing() {
				return true
			}
		}
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
	if r.speedLevel != LevelStop {
		if r.disabledToSync {
			if !r.IsLeader() && r.messageWait.canMsgLeaderTermStartIndex() {
				r.messageWait.resetMsgLeaderTermStartIndex()
				r.msgs = append(r.msgs, r.newLeaderTermStartIndexReqMsg())
			}
			return
		}

		// 副本来同步日志
		if r.followNeedSync() {
			r.messageWait.resetSync()
			msg := r.newSyncMsg()
			r.msgs = append(r.msgs, msg)
		}

		if r.isLeader() {
			r.sendPingIfNeed()
		}
	}

	// 追加日志
	if r.hasUnstableLogs() {

		logs := r.replicaLog.nextUnstableLogs()
		r.msgs = append(r.msgs, r.newMsgStoreAppend(logs))
	}

	// 应用日志
	if r.hasUnapplyLogs() {
		var applyCommittedIndex = r.replicaLog.applyCommittedIndex()
		r.msgs = append(r.msgs, r.newApplyLogReqMsg(r.replicaLog.applyingIndex, r.replicaLog.appliedIndex, applyCommittedIndex))
	}

}

func (r *Replica) acceptReady(rd Ready) {

	r.msgs = r.msgs[:0]
	rd.HardState = EmptyHardState
	if r.hasUnstableLogs() {
		r.messageWait.resetAppendLog()
		r.replicaLog.acceptUnstable()
	}

	if r.hasUnapplyLogs() {
		var applyCommittedIndex = r.replicaLog.applyCommittedIndex()
		r.replicaLog.acceptApplying(applyCommittedIndex, 0)
	}
}

func (r *Replica) reset(term uint32) {
	if r.replicaLog.term != term {
		r.replicaLog.term = term
	}
	r.voteFor = None
	r.votes = make(map[uint64]bool)
	r.leader = None
	r.electionElapsed = 0
	r.heartbeatElapsed = 0
	r.setSpeedLevel(LevelFast)
	r.resetRandomizedElectionTimeout()
	r.messageWait.immediatelyLeaderTermStartIndex()
}

func (r *Replica) Tick() {
	r.messageWait.tick()
	if r.tickFnc != nil {
		r.tickFnc()
	}
}

func (r *Replica) tickElection() {

	if !r.opts.ElectionOn { // 禁止选举
		return
	}

	r.electionElapsed++

	// r.Debug("electionElapsed--->", zap.Int("electionElapsed", r.electionElapsed))
	if r.pastElectionTimeout() { // 超时开始进行选举
		r.electionElapsed = 0
		err := r.Step(Message{
			MsgType: MsgHup,
		})
		if err != nil {
			r.Debug("node tick election error", zap.Error(err))
			return
		}
	}
}

func (r *Replica) tickHeartbeat() {

	if !r.isLeader() {
		r.Warn("not leader, but call tickHeartbeat")
		return
	}

	if !r.opts.ElectionOn {
		return
	}

	r.heartbeatElapsed++
	r.electionElapsed++

	if r.electionElapsed >= r.opts.ElectionTimeoutTick {
		r.electionElapsed = 0
	}

	if r.heartbeatElapsed >= r.opts.HeartbeatTimeoutTick {
		r.heartbeatElapsed = 0
		if err := r.Step(Message{From: r.opts.NodeId, MsgType: MsgBeat}); err != nil {
			r.Debug("error occurred during checking sending heartbeat", zap.Error(err))
		}
	}
}

func (r *Replica) pastElectionTimeout() bool {
	return r.electionElapsed >= r.randomizedElectionTimeout
}

func (r *Replica) resetRandomizedElectionTimeout() {
	r.randomizedElectionTimeout = r.opts.ElectionTimeoutTick + globalRand.Intn(r.opts.ElectionTimeoutTick)
}

// 是否有未提交的日志
// func (r *Replica) hasUncommittedLogs() bool {
// 	return r.state.lastLogIndex > r.state.committedIndex
// }

// 是否有未存储的日志
func (r *Replica) hasUnstableLogs() bool {
	if !r.messageWait.canAppendLog() {
		return false
	}
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
		From:    r.nodeId,
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
		From:    r.nodeId,
		Term:    r.replicaLog.term,
		Logs:    logs,
	}
}

func NewProposeMessageWithLogs(nodeId uint64, term uint32, logs []Log) Message {
	return Message{
		MsgType: MsgPropose,
		From:    nodeId,
		Term:    term,
		Logs:    logs,
	}
}

func NewMsgApplyLogsRespMessage(nodeId uint64, term uint32, appliedIdx uint64) Message {
	return Message{
		MsgType:      MsgApplyLogsResp,
		From:         nodeId,
		Term:         term,
		AppliedIndex: appliedIdx,
	}
}

func (r *Replica) NewMsgStoreAppendResp(index uint64) Message {
	return Message{
		MsgType: MsgStoreAppendResp,
		From:    r.nodeId,
		To:      r.nodeId,
		Index:   index,
	}

}

func NewMsgStoreAppendResp(nodeId uint64, index uint64) Message {
	return Message{
		MsgType: MsgStoreAppendResp,
		From:    nodeId,
		To:      nodeId,
		Index:   index,
	}

}

func (r *Replica) newMsgSyncGet(from uint64, index uint64, unstableLogs []Log) Message {
	return Message{
		MsgType: MsgSyncGet,
		From:    from,
		To:      r.nodeId,
		Index:   index,
		Logs:    unstableLogs,
	}
}

func (r *Replica) newMsgSyncResp(to uint64, startIndex uint64, logs []Log) Message {
	return Message{
		MsgType:        MsgSyncResp,
		From:           r.nodeId,
		To:             to,
		Term:           r.replicaLog.term,
		Logs:           logs,
		Index:          startIndex,
		CommittedIndex: r.replicaLog.committedIndex,
		SpeedLevel:     r.speedLevel,
	}
}

func (r *Replica) NewMsgSyncGetResp(to uint64, startIndex uint64, logs []Log) Message {
	return Message{
		MsgType: MsgSyncGetResp,
		From:    r.nodeId,
		To:      to,
		Logs:    logs,
		Index:   startIndex,
	}
}

func NewMsgSyncGetResp(from uint64, to uint64, startIndex uint64, logs []Log) Message {
	return Message{
		MsgType: MsgSyncGetResp,
		From:    from,
		To:      to,
		Logs:    logs,
		Index:   startIndex,
	}
}

func (r *Replica) newApplyLogReqMsg(applyingIndex, appliedIndex, committedIndex uint64) Message {

	return Message{
		MsgType:        MsgApplyLogs,
		From:           r.nodeId,
		To:             r.nodeId,
		Term:           r.replicaLog.term,
		ApplyingIndex:  applyingIndex,
		AppliedIndex:   appliedIndex,
		CommittedIndex: committedIndex,
	}
}

func (r *Replica) newSyncMsg() Message {
	return Message{
		MsgType: MsgSyncReq,
		From:    r.nodeId,
		To:      r.leader,
		Term:    r.replicaLog.term,
		Index:   r.replicaLog.lastLogIndex + 1,
	}
}

func (r *Replica) newPing(to uint64) Message {
	return Message{
		MsgType:        MsgPing,
		From:           r.nodeId,
		To:             to,
		Term:           r.replicaLog.term,
		Index:          r.replicaLog.lastLogIndex,
		CommittedIndex: r.replicaLog.committedIndex,
		SpeedLevel:     r.speedLevel,
		ConfVersion:    r.opts.Config.Version,
	}
}

func (r *Replica) newLeaderTermStartIndexReqMsg() Message {
	leaderLastTerm, err := r.opts.Storage.LeaderLastTerm()
	if err != nil {
		r.Panic("get leader last term failed", zap.Error(err))
	}
	term := leaderLastTerm
	if leaderLastTerm == 0 {
		term = r.replicaLog.term
	}

	return Message{
		MsgType: MsgLeaderTermStartIndexReq,
		From:    r.nodeId,
		To:      r.leader,
		Term:    term,
	}
}

func (r *Replica) newPong(to uint64) Message {
	return Message{
		MsgType:        MsgPong,
		From:           r.nodeId,
		To:             to,
		Term:           r.replicaLog.term,
		CommittedIndex: r.replicaLog.committedIndex,
	}
}

func (r *Replica) newLeaderTermStartIndexResp(to uint64, term uint32, index uint64) Message {
	return Message{
		MsgType: MsgLeaderTermStartIndexResp,
		From:    r.nodeId,
		To:      to,
		Term:    term,
		Index:   index,
	}
}

func (r *Replica) newMsgStoreAppend(logs []Log) Message {
	return Message{
		MsgType: MsgStoreAppend,
		From:    r.nodeId,
		To:      r.nodeId,
		Logs:    logs,
	}

}

func (r *Replica) newMsgVoteReq(nodeId uint64) Message {
	return Message{
		From:    r.opts.NodeId,
		To:      nodeId,
		MsgType: MsgVoteReq,
		Term:    r.replicaLog.term,
		Index:   r.replicaLog.lastLogIndex,
	}
}

func (r *Replica) newMsgVoteResp(to uint64, term uint32, reject bool) Message {
	return Message{
		From:    r.opts.NodeId,
		To:      to,
		MsgType: MsgVoteResp,
		Term:    term,
		Index:   r.replicaLog.lastLogIndex,
		Reject:  reject,
	}
}

func (r *Replica) newMsgConfigReq(to uint64) Message {
	return Message{
		MsgType: MsgConfigReq,
		From:    r.nodeId,
		To:      to,
		Term:    r.replicaLog.term,
	}
}

func (r *Replica) newMsgConfigResp(to uint64) Message {
	data, _ := r.opts.Config.Marshal()
	return Message{
		MsgType:     MsgConfigResp,
		Term:        r.replicaLog.term,
		From:        r.nodeId,
		To:          to,
		ConfVersion: r.opts.Config.Version,
		Logs: []Log{
			{
				Data: data,
			},
		},
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
	if !r.messageWait.canSync() {
		return false
	}
	return true
}

func (r *Replica) removeLearner(learnerId uint64) {
	for i, id := range r.opts.Config.Learners {
		if id == learnerId {
			r.opts.Config.Learners = append(r.opts.Config.Learners[:i], r.opts.Config.Learners[i+1:]...)
			return
		}
	}
}

func (r *Replica) addReplica(replicaId uint64) {
	if !wkutil.ArrayContainsUint64(r.opts.Config.Replicas, replicaId) {
		r.opts.Config.Replicas = append(r.opts.Config.Replicas, replicaId)
	}

	if !wkutil.ArrayContainsUint64(r.replicas, replicaId) {
		r.replicas = append(r.replicas, replicaId)
	}

}
