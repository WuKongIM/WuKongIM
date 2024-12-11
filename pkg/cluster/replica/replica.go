package replica

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type Replica struct {
	msgs   []Message
	nodeId uint64
	no     string // 唯一编号
	wklog.Log
	stepFunc   func(m Message) error
	cfg        Config      // 当前副本配置
	replicaLog *replicaLog // 日志
	speedLevel SpeedLevel  // 当前速度等级
	opts       *Options

	detailLogOn bool // 开启详细日志

	lastSyncInfoMap map[uint64]*SyncInfo // 副本最后一次同步信息
	preHardState    HardState            // 上一个硬状态

	uncommittedSize logEncodingSize // 未提交的日志大小

	stopPropose                  bool // 是否停止提案
	isRoleTransitioning          bool // 是否角色转换中
	roleTransitioningTimeoutTick int  // 角色转换超时计时器

	handleSyncReplicaMap map[uint64]bool // 领导正在处理同步中的副本记录，防止重复处理同步请求，导致系统变慢

	replicas []uint64 // 副本节点ID集合（不包含本节点）
	// -------------------- 节点状态 --------------------
	leader uint64 // 领导者id
	role   Role   // 副本角色
	status Status // 副本状态
	term   uint32 // 当前任期

	// -------------------- election --------------------
	electionElapsed           int // 选举计时器
	heartbeatElapsed          int
	randomizedElectionTimeout int // 随机选举超时时间
	tickFnc                   func()
	voteFor                   uint64          // 投票给谁
	votes                     map[uint64]bool // 投票记录

	// -------------------- state --------------------
	initState         *ReadyState        // 初始化
	coflictCheckState *ReadyState        // 日志冲突检查
	syncState         *ReadyTimeoutState // 同步
	storageState      *ReadyState        // 存储
	applyState        *ReadyState        // 应用
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
		replicaLog:           newReplicaLog(opts),
		status:               StatusUnready,
		Log:                  wklog.NewWKLog(fmt.Sprintf("replica[%d:%s]", nodeId, opts.LogPrefix)),
		opts:                 opts,
		nodeId:               nodeId,
		lastSyncInfoMap:      make(map[uint64]*SyncInfo),
		no:                   wkutil.GenUUID(),
		handleSyncReplicaMap: make(map[uint64]bool),
	}
	rc.term = opts.LastTerm

	rc.initState = NewReadyState(opts.RetryTick)
	rc.coflictCheckState = NewReadyState(opts.RetryTick)
	rc.syncState = NewReadyTimeoutState(rc.opts.LogPrefix, opts.SyncTimeoutTick, opts.SyncIntervalTick, rc.syncTimeout)
	rc.storageState = NewReadyState(opts.RetryTick)
	rc.applyState = NewReadyState(opts.RetryTick)

	return rc
}

func (r *Replica) Propose(data []byte) error {

	return r.Step(r.NewProposeMessage(data))
}

func (r *Replica) HasReady() bool {

	isFollower := r.role == RoleFollower || r.role == RoleLearner

	// 是否未准备
	if r.isUnready() {

		return !r.initState.IsProcessing()
	}

	// 是否需要检查日志冲突
	if r.isLogConflictCheck() {
		if r.coflictCheckState.IsProcessing() {
			return false
		}
		return r.leader != 0 && isFollower
	}

	// 是否需要同步
	if isFollower && r.hasSync() {
		return true
	}

	// 是否有存储
	if r.hasStorage() {
		return true
	}

	// 是否有应用
	if r.hasApply() {
		return true
	}

	// 是否有消息
	if r.hasMsg() {
		return true
	}

	// 是否有硬状态改变
	if r.hardStateChange() {
		return true
	}

	return false
}

// 服务是否未准备
func (r *Replica) isUnready() bool {
	return r.status == StatusUnready
}

// 是否需要检查日志冲突
func (r *Replica) isLogConflictCheck() bool {
	return r.status == StatusLogCoflictCheck
}

// 是否有同步
func (r *Replica) hasSync() bool {
	if r.syncState.IsProcessing() {
		return false
	}
	if r.leader == 0 {
		return false
	}

	return r.syncState.Allow()
}

// 是否有消息
func (r *Replica) hasMsg() bool {
	return len(r.msgs) > 0
}

// 有存储消息
func (r *Replica) hasStorage() bool {
	if r.storageState.IsProcessing() {
		return false
	}
	return r.replicaLog.storagedIndex < r.replicaLog.lastLogIndex
}

// 有应用
func (r *Replica) hasApply() bool {
	if r.applyState.IsProcessing() {
		return false
	}
	i := min(r.replicaLog.storagedIndex, r.replicaLog.committedIndex)
	return r.replicaLog.appliedIndex < i
}

func (r *Replica) Ready() Ready {

	rd := Ready{}

	isFollower := r.role == RoleFollower || r.role == RoleLearner

	// ==================== 初始化 ====================
	if r.isUnready() {
		if r.initState.IsProcessing() {
			return rd
		}
		r.initState.StartProcessing()
		r.msgs = append(r.msgs, r.newMsgInit())
		rd.Messages = r.msgs
		r.msgs = r.msgs[:0]
		return rd
	}

	if r.hardStateChange() {
		rd.HardState = HardState{
			LeaderId:    r.leader,
			Term:        r.term,
			ConfVersion: r.cfg.Version,
		}
		r.preHardState = HardState{
			LeaderId:    r.leader,
			Term:        r.term,
			ConfVersion: r.cfg.Version,
		}
	}

	// ==================== 日志冲突检查 ====================
	if r.isLogConflictCheck() {
		if r.coflictCheckState.IsProcessing() {
			return rd
		}
		if r.leader != 0 && isFollower {
			r.coflictCheckState.StartProcessing()
			r.msgs = append(r.msgs, r.newMsgLogConflictCheck())
			rd.Messages = r.msgs
			r.msgs = r.msgs[:0]
		}
		return rd
	}

	// ==================== 发起同步 ====================
	if r.detailLogOn {
		r.Info("read sync", zap.Bool("isFollower", isFollower), zap.Bool("isProcessing", r.syncState.IsProcessing()), zap.Uint64("leader", r.leader), zap.Int("status", int(r.status)), zap.Int("idleTick", r.syncState.idleTick), zap.Int("intervalTick", r.syncState.intervalTick), zap.Int("timeoutCount", r.syncState.timeoutCount))
	}

	if isFollower && r.hasSync() {
		r.syncState.StartProcessing()
		r.msgs = append(r.msgs, r.newSyncMsg())
		// if strings.Contains(r.opts.LogPrefix, "config") {
		// 	r.Info("sync config --------->", zap.Uint64("index", r.replicaLog.lastLogIndex+1))
		// }

	}

	// ==================== 存储日志 ====================
	if r.hasStorage() {
		logs := r.replicaLog.nextStorageLogs()
		if len(logs) > 0 {
			r.storageState.processing = true
			r.msgs = append(r.msgs, r.newMsgStoreAppend(logs))
		}
	}

	// ==================== 应用日志 ====================
	if r.hasApply() {
		newCommittedIndex := min(r.replicaLog.storagedIndex, r.replicaLog.committedIndex)
		r.applyState.processing = true
		r.msgs = append(r.msgs, r.newApplyLogReqMsg(r.replicaLog.appliedIndex, newCommittedIndex))
	}

	rd.Messages = r.msgs

	r.msgs = r.msgs[:0]
	return rd
}
func (r *Replica) hardStateChange() bool {
	return r.preHardState.LeaderId != r.leader || r.preHardState.Term != r.term || r.preHardState.ConfVersion != r.cfg.Version
}

// 同步超时
func (r *Replica) syncTimeout() {
	r.send(r.newSyncTimeoutMsg()) // 同步超时
}

func (r *Replica) Tick() {

	// 非领导
	notLeader := r.role == RoleFollower || r.role == RoleLearner

	// if r.role == RoleFollower || r.role == RoleLearner {

	// 	if r.status == StatusReady {
	// 		r.syncTick++
	// 		if r.syncTick > r.syncIntervalTick*5 && r.status == StatusReady { // 同步超时 一直没有返回
	// 			r.send(r.newSyncTimeoutMsg()) // 同步超时

	// 			// 重置同步状态，从而可以重新发起同步
	// 			r.syncing = false
	// 			r.syncTick = 0
	// 		}
	// 	} else if r.status == StatusLogCoflictCheck { // 日志冲突检查超时，重新发起
	// 		r.logConflictCheckTick++
	// 	}

	// }

	r.initState.Tick()

	r.coflictCheckState.Tick()

	if r.status == StatusReady && notLeader {
		r.syncState.Tick()
	}

	r.storageState.Tick()
	r.applyState.Tick()

	if r.tickFnc != nil {
		r.tickFnc()
	}
}

func (r *Replica) LastLogIndex() uint64 {
	return r.replicaLog.lastLogIndex
}

func (r *Replica) Term() uint32 {
	return r.term
}

func (r *Replica) Role() Role {
	return r.role
}

func (r *Replica) switchConfig(cfg Config) {

	if r.cfg.Version > cfg.Version {
		return
	}

	// r.Info("switch config", zap.String("cfg", cfg.String()))

	r.cfg = cfg
	term := r.term
	if term == 0 {
		term = 1
	}
	if cfg.Term > term {
		term = cfg.Term
	}

	if wkutil.ArrayContainsUint64(cfg.Learners, r.nodeId) { // 节点是学习者
		if r.role != RoleLearner || term > r.term || (cfg.Leader != 0 && r.leader != cfg.Leader) {
			r.becomeLearner(term, cfg.Leader)
		}
	} else {
		switch cfg.Role {
		case RoleLeader:
			if r.role != RoleLeader || term > r.term {
				r.becomeLeader(term)
			}
		case RoleFollower:
			if r.role != RoleFollower || term > r.term || (cfg.Leader != 0 && r.leader != cfg.Leader) {
				r.becomeFollower(term, cfg.Leader)
			}

		case RoleCandidate:
			if r.role != RoleCandidate || term > r.term {
				r.becomeCandidateWithTerm(term)
			}
		case RoleUnknown:
			if r.role == RoleLearner {
				leader := cfg.Leader
				if leader == 0 {
					leader = r.leader
				}
				r.becomeFollower(term, leader)
			}
		}
	}

	r.initLeaderInfo()

	if r.opts.ElectionOn {
		if r.isSingleNode() { // 如果是单节点，直接成为领导
			if r.role == RoleUnknown {
				r.becomeLeader(term)
			}
		} else if r.role == RoleUnknown {
			r.becomeCandidateWithTerm(term + 1)
		}
	}

}

func (r *Replica) initLeaderInfo() {

	r.isRoleTransitioning = false
	r.roleTransitioningTimeoutTick = 0
	r.stopPropose = false

	r.lastSyncInfoMap = make(map[uint64]*SyncInfo)
	r.replicas = nil

	for _, replica := range r.cfg.Replicas {
		if replica == r.nodeId {
			continue
		}
		r.replicas = append(r.replicas, replica)
	}

	if r.isLeader() {
		for _, replica := range r.replicas {
			if replica == r.nodeId {
				continue
			}
			r.lastSyncInfoMap[replica] = &SyncInfo{
				LastSyncIndex: 0,
				SyncTick:      0,
			}
		}
		for _, learner := range r.cfg.Learners {
			if learner == r.nodeId {
				continue
			}
			r.lastSyncInfoMap[learner] = &SyncInfo{
				LastSyncIndex: 0,
				SyncTick:      0,
			}
		}
	}
}

func (r *Replica) becomeLeader(term uint32) {

	r.stepFunc = r.stepLeader
	r.reset(term)
	r.tickFnc = r.tickHeartbeat
	r.term = term
	r.leader = r.nodeId
	r.role = RoleLeader

	r.status = StatusReady

	r.initLeaderInfo()

	// r.Info("become leader", zap.Uint32("term", r.term))

}

// 成为追随者
func (r *Replica) becomeFollower(term uint32, leaderID uint64) {
	r.stepFunc = r.stepFollower
	r.reset(term)
	r.tickFnc = r.tickElection
	r.term = term
	r.leader = leaderID
	r.role = RoleFollower

	// r.Info("become follower", zap.Uint32("term", term), zap.Uint64("leader", leaderID))

	if r.replicaLog.lastLogIndex > 0 && r.leader != None {
		r.Debug("log conflict check", zap.Uint64("leader", r.leader), zap.Uint64("lastLogIndex", r.replicaLog.lastLogIndex))
		r.status = StatusLogCoflictCheck
	} else if r.replicaLog.lastLogIndex <= 0 && r.leader != None {
		r.status = StatusReady
	}

}

// 成为学习者
func (r *Replica) becomeLearner(term uint32, leaderID uint64) {
	r.stepFunc = r.stepLearner
	r.reset(term)
	r.tickFnc = nil
	r.term = term
	r.leader = leaderID
	r.role = RoleLearner

	r.Info("become learner", zap.Uint32("term", term), zap.Uint64("leader", leaderID))

	if r.replicaLog.lastLogIndex > 0 && r.leader != None {
		r.Debug("log conflict check", zap.Uint64("leader", r.leader))
		r.status = StatusLogCoflictCheck
	} else if r.replicaLog.lastLogIndex <= 0 && r.leader != None {
		r.status = StatusReady
	}
}

// 成为候选人
func (r *Replica) becomeCandidate() {
	r.becomeCandidateWithTerm(r.term + 1)
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
	r.Info("become candidate", zap.Uint32("term", r.term))
}

func (r *Replica) reset(term uint32) {
	if r.term != term {
		r.term = term
	}
	r.voteFor = None
	r.votes = make(map[uint64]bool)
	r.msgs = nil
	r.stopPropose = false
	r.isRoleTransitioning = false
	r.roleTransitioningTimeoutTick = 0
	r.leader = None
	r.electionElapsed = 0
	r.heartbeatElapsed = 0
	r.setSpeedLevel(LevelFast)
	r.resetRandomizedElectionTimeout()

	r.initState.Reset()
	r.coflictCheckState.Reset()
	r.syncState.Reset()
	r.storageState.Reset()
	r.applyState.Reset()

}

// 开始选举
func (r *Replica) campaign() {
	r.becomeCandidate()
	for _, nodeId := range r.cfg.Replicas {
		if nodeId == r.opts.NodeId {
			// 自己给自己投一票
			r.send(Message{To: nodeId, From: nodeId, Term: r.term, MsgType: MsgVoteResp})
			continue
		}
		r.Info("sent vote request", zap.Uint64("from", r.opts.NodeId), zap.Uint64("to", nodeId), zap.Uint32("term", r.term))
		r.sendRequestVote(nodeId)
	}
}

func (r *Replica) sendRequestVote(nodeId uint64) {
	r.send(r.newMsgVoteReq(nodeId))
}

func (r *Replica) hup() {
	r.campaign()
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

	if r.isRoleTransitioning {
		r.roleTransitioningTimeoutTick++

		if r.roleTransitioningTimeoutTick >= r.opts.LearnerToTimeoutTick {
			r.isRoleTransitioning = false
		}
	}

	if r.opts.ElectionOn { // 是否开启自动选举
		r.heartbeatElapsed++
		r.electionElapsed++

		if r.electionElapsed >= r.opts.ElectionIntervalTick {
			r.electionElapsed = 0
		}

		if r.heartbeatElapsed >= r.opts.HeartbeatIntervalTick {
			r.heartbeatElapsed = 0
			if err := r.Step(Message{From: r.opts.NodeId, To: All, MsgType: MsgBeat}); err != nil {
				r.Debug("error occurred during checking sending heartbeat", zap.Error(err))
			}
		}
	} else {
		// 如果某个副本在一段时间内没有发起同步请求，那么主动发起心跳,目的是唤醒副本
		for nodeId, syncInfo := range r.lastSyncInfoMap {
			syncInfo.SyncTick++
			if syncInfo.SyncTick > r.syncState.intervalTick*2 {
				syncInfo.SyncTick = 0
				if err := r.Step(Message{From: r.opts.NodeId, To: nodeId, MsgType: MsgBeat}); err != nil {
					r.Debug("error occurred during checking sending heartbeat", zap.Error(err))
				}
			}
		}
	}
}

func (r *Replica) pastElectionTimeout() bool {
	return r.electionElapsed >= r.randomizedElectionTimeout
}

func (r *Replica) resetRandomizedElectionTimeout() {
	r.randomizedElectionTimeout = r.opts.ElectionIntervalTick + globalRand.Intn(r.opts.ElectionIntervalTick)
}

func (r *Replica) SetSpeedLevel(level SpeedLevel) {
	if r.speedLevel == level {
		return
	}
	r.setSpeedLevel(level)
}

func (r *Replica) SpeedLevel() SpeedLevel {
	return r.speedLevel
}

func (r *Replica) setSpeedLevel(level SpeedLevel) {

	switch level {
	case LevelFast:
		r.syncState.SetIntervalTick(r.opts.SyncIntervalTick)
	case LevelMiddle:
		r.syncState.SetIntervalTick(r.opts.SyncIntervalTick * 2)
	case LevelSlow:
		r.syncState.SetIntervalTick(r.opts.SyncIntervalTick * 4)
	case LevelSlowest:
		r.syncState.SetIntervalTick(r.opts.SyncIntervalTick * 8)
	case LevelStop: // 这种情况基本是停止状态，要么等待重新激活，要么等待被销毁
		r.syncState.SetIntervalTick(r.opts.SyncIntervalTick * 100000)
	}
	if level != r.speedLevel {
		r.send(Message{MsgType: MsgSpeedLevelChange, SpeedLevel: level})
	}
	r.speedLevel = level

}

func (r *Replica) isLeader() bool {

	return r.role == RoleLeader
}

func (r *Replica) isLearner(nodeId uint64) bool {
	if len(r.cfg.Learners) == 0 {
		return false
	}
	for _, learner := range r.cfg.Learners {
		if learner == nodeId {
			return true
		}
	}
	return false
}

func (r *Replica) isReplica(nodeId uint64) bool {
	if len(r.replicas) == 0 {
		return false
	}
	for _, replica := range r.replicas {
		if replica == nodeId {
			return true
		}
	}
	return false
}

// 是否是单节点
func (r *Replica) isSingleNode() bool {
	return len(r.replicas) == 0
}

func (r *Replica) DetailLogOn(on bool) {
	r.detailLogOn = on
}

// 获取某个副本的最新日志下标（领导节点才有这个信息）
func (r *Replica) GetReplicaLastLog(replicaId uint64) uint64 {
	if replicaId == r.opts.NodeId {
		return r.LastLogIndex()
	}
	syncInfo := r.lastSyncInfoMap[replicaId]
	if syncInfo != nil && syncInfo.LastSyncIndex > 0 {
		return syncInfo.LastSyncIndex - 1
	}
	return 0
}

func (r *Replica) NewProposeMessage(data []byte) Message {
	return Message{
		MsgType: MsgPropose,
		From:    r.nodeId,
		Term:    r.term,
		Logs: []Log{
			{
				Index: r.replicaLog.lastLogIndex + 1,
				Term:  r.term,
				Data:  data,
			},
		},
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

func (r *Replica) newApplyLogReqMsg(appliedIndex, committedIndex uint64) Message {

	return Message{
		MsgType:        MsgApplyLogs,
		From:           r.nodeId,
		To:             r.nodeId,
		AppliedIndex:   appliedIndex,
		CommittedIndex: committedIndex,
	}
}

func (r *Replica) newMsgInit() Message {
	return Message{
		MsgType: MsgInit,
		From:    r.nodeId,
		To:      r.nodeId,
	}
}

func (r *Replica) newMsgLogConflictCheck() Message {
	return Message{
		MsgType: MsgLogConflictCheck,
		From:    r.nodeId,
		To:      r.nodeId,
	}
}

func (r *Replica) newSyncMsg() Message {
	return Message{
		MsgType: MsgSyncReq,
		From:    r.nodeId,
		To:      r.leader,
		Term:    r.term,
		Index:   r.replicaLog.lastLogIndex + 1,
	}
}

func (r *Replica) newSyncTimeoutMsg() Message {
	return Message{
		MsgType: MsgSyncTimeout,
		From:    r.nodeId,
		To:      r.nodeId,
		Index:   r.replicaLog.lastLogIndex + 1,
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

func (r *Replica) newMsgSyncResp(to uint64, index uint64, logs []Log) Message {
	return Message{
		MsgType:        MsgSyncResp,
		From:           r.nodeId,
		To:             to,
		Term:           r.term,
		Logs:           logs,
		Index:          index,
		CommittedIndex: r.replicaLog.committedIndex,
		SpeedLevel:     r.speedLevel,
	}
}

func (r *Replica) newPong(to uint64) Message {
	return Message{
		MsgType:        MsgPong,
		From:           r.nodeId,
		To:             to,
		Term:           r.term,
		CommittedIndex: r.replicaLog.committedIndex,
	}
}

func (r *Replica) newMsgConfigReq(to uint64) Message {
	return Message{
		MsgType:     MsgConfigReq,
		From:        r.nodeId,
		To:          to,
		Term:        r.term,
		ConfVersion: r.cfg.Version,
	}
}

func (r *Replica) newMsgConfigResp(to uint64) Message {

	confgData, err := r.cfg.Marshal()
	if err != nil {
		r.Panic("config marshal error", zap.Error(err))
	}
	return Message{
		MsgType:     MsgConfigResp,
		Term:        r.term,
		From:        r.nodeId,
		To:          to,
		ConfVersion: r.cfg.Version,
		Logs: []Log{
			{
				Index: r.replicaLog.lastLogIndex,
				Data:  confgData,
			},
		},
	}
}

func (r *Replica) newMsgConfigChange(cfg Config) Message {
	return Message{
		MsgType: MsgConfigChange,
		From:    r.nodeId,
		To:      r.nodeId,
		Config:  cfg,
	}
}

func (r *Replica) newMsgLearnerToFollower(learnerId uint64) Message {
	return Message{
		MsgType:   MsgLearnerToFollower,
		From:      r.nodeId,
		To:        r.nodeId,
		LearnerId: learnerId,
	}
}

func (r *Replica) newMsgLearnerToLeader(learnerId uint64) Message {
	return Message{
		MsgType:   MsgLearnerToLeader,
		From:      r.nodeId,
		To:        r.nodeId,
		LearnerId: learnerId,
	}
}

func (r *Replica) newFollowerToLeader(followerNodeId uint64) Message {
	return Message{
		MsgType:    MsgFollowerToLeader,
		From:       r.nodeId,
		To:         r.nodeId,
		FollowerId: followerNodeId,
	}
}

func (r *Replica) newPing(to uint64) Message {
	return Message{
		MsgType:        MsgPing,
		From:           r.nodeId,
		To:             to,
		Term:           r.term,
		Index:          r.replicaLog.lastLogIndex,
		CommittedIndex: r.replicaLog.committedIndex,
		SpeedLevel:     r.speedLevel,
		ConfVersion:    r.cfg.Version,
	}
}

func (r *Replica) newMsgVoteReq(nodeId uint64) Message {
	lastIndex, lastTerm := r.replicaLog.lastIndexAndTerm()
	return Message{
		From:    r.opts.NodeId,
		To:      nodeId,
		MsgType: MsgVoteReq,
		Term:    r.term,
		Index:   r.replicaLog.lastLogIndex,
		Logs: []Log{
			{
				Index: lastIndex,
				Term:  lastTerm,
			},
		},
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

func (r *Replica) NewProposeMessageWithLogs(logs []Log) Message {
	return Message{
		MsgType: MsgPropose,
		From:    r.nodeId,
		Term:    r.term,
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

func (r *Replica) send(m Message) {
	r.msgs = append(r.msgs, m)
}

func (r *Replica) sendPing(to uint64) {
	if !r.isLeader() {
		return
	}
	if to != All {
		r.send(r.newPing(to))
		return
	}
	for _, replicaId := range r.replicas {
		if replicaId == r.opts.NodeId {
			continue
		}
		r.send(r.newPing(replicaId))
	}
	if len(r.cfg.Learners) > 0 {
		for _, replicaId := range r.cfg.Learners {
			if replicaId == r.opts.NodeId {
				continue
			}
			r.send(r.newPing(replicaId))
		}
	}
}
