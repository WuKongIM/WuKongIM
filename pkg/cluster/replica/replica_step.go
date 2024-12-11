package replica

import (
	"go.uber.org/zap"
)

func (r *Replica) Step(m Message) error {

	if r.detailLogOn {
		r.Info("step...", zap.String("msgType", m.MsgType.String()), zap.Uint32("term", m.Term), zap.Uint64("index", m.Index), zap.Int("logs", len(m.Logs)), zap.Int("status", int(r.status)), zap.String("role", r.role.String()))
	}

	switch {
	case m.Term == 0: // 本地消息
	case m.Term > r.term: // 高于当前任期
		if r.term > 0 {
			r.Info("received message with higher term", zap.Uint32("term", m.Term), zap.Uint32("currentTerm", r.term), zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.String("msgType", m.MsgType.String()))
		}
		// 高任期消息
		if m.MsgType == MsgPing || m.MsgType == MsgLeaderTermStartIndexResp || m.MsgType == MsgSyncResp {
			if r.role == RoleLearner {
				r.becomeLearner(m.Term, m.From)
			} else {
				r.becomeFollower(m.Term, m.From)
			}

		} else {
			if r.role == RoleLearner {
				r.Warn("become learner but leader is none", zap.Uint64("nodeId", r.nodeId), zap.Uint32("term", m.Term), zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.String("msgType", m.MsgType.String()))
				r.becomeLearner(m.Term, None)
			} else {
				r.Warn("become follower but leader is none", zap.Uint64("nodeId", r.nodeId), zap.Uint32("term", m.Term), zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.String("msgType", m.MsgType.String()))
				r.becomeFollower(m.Term, None)
			}
		}
	case m.Term < r.term: // 低于当前任期
		r.Info("received message with lower term", zap.Uint32("term", m.Term), zap.Uint32("currentTerm", r.term), zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.String("msgType", m.MsgType.String()))
		return nil // 直接忽略，不处理
	}

	switch m.MsgType {
	case MsgInitResp: // 初始化返回
		if !m.Reject {
			r.initState.ProcessSuccess()
			if !IsEmptyConfig(m.Config) {
				r.switchConfig(m.Config)
			}
		} else {
			r.initState.ProcessFail()
		}

	case MsgHup: // 触发选举
		r.hup()
	case MsgVoteReq: // 收到投票请求
		if r.canVote(m) {
			r.send(r.newMsgVoteResp(m.From, r.term, false))
			r.electionElapsed = 0
			r.voteFor = m.From
			r.Info("agree vote", zap.Uint64("voteFor", m.From), zap.Uint32("term", m.Term), zap.Uint64("index", m.Index))
		} else {
			if r.voteFor != None {
				r.Info("already vote for other", zap.Uint64("voteFor", r.voteFor))
			} else if m.Index < r.replicaLog.lastLogIndex {
				r.Info("lower config version, reject vote")
			} else if m.Term < r.term {
				r.Info("lower term, reject vote")
			}
			r.Info("reject vote", zap.Uint64("from", m.From), zap.Uint32("term", m.Term), zap.Uint64("index", m.Index))
			r.send(r.newMsgVoteResp(m.From, r.term, true))
		}
	case MsgStoreAppendResp: // 存储返回
		if !m.Reject {
			r.storageState.ProcessSuccess()
			r.replicaLog.storagedTo(m.Index)
		} else {
			r.storageState.ProcessFail()
			r.Info("store append reject", zap.Uint64("nodeId", r.nodeId), zap.Uint32("term", r.term), zap.Uint64("index", m.Index))
		}

	case MsgApplyLogsResp: // 应用日志返回
		if !m.Reject {
			r.applyState.ProcessSuccess()
			if m.Index < r.replicaLog.appliedIndex {
				r.Warn("applied index is less than current applied index", zap.Uint64("nodeId", r.nodeId), zap.Uint32("term", r.term), zap.Uint64("index", m.Index), zap.Uint64("appliedIndex", r.replicaLog.appliedIndex))
				return nil
			}
			r.replicaLog.appliedTo(m.Index)
			if m.AppliedSize == 0 {
				r.uncommittedSize = 0
			} else {
				r.reduceUncommittedSize(logEncodingSize(m.AppliedSize))
			}
		} else {
			r.applyState.ProcessFail()
		}

	case MsgConfigResp:
		if !m.Reject {
			cfg := Config{}
			if len(m.Logs) > 0 {
				confLog := m.Logs[0]
				if err := cfg.Unmarshal(confLog.Data); err != nil {
					r.Error("unmarshal config failed", zap.Error(err))
					return err
				}
			} else {
				cfg = m.Config
			}
			r.switchConfig(cfg)
		}
	case MsgSpeedLevelSet: // 控制速度
		r.setSpeedLevel(m.SpeedLevel)
	case MsgChangeRole: // 改变权限

		switch m.Role {
		case RoleLeader:
			r.becomeLeader(r.term)
		case RoleCandidate:
			r.becomeCandidate()
		case RoleFollower:
			r.becomeFollower(r.term, r.leader)
		}

	default:
		// if r.stepFunc == nil {
		// 	r.Panic("stepFunc is nil", zap.String("role", r.role.String()), zap.Uint64("leader", r.leader), zap.String("msgType", m.MsgType.String()))
		// }
		if r.stepFunc != nil {
			err := r.stepFunc(m)
			if err != nil {
				return err
			}
		}

	}

	return nil
}

func (r *Replica) stepLeader(m Message) error {

	switch m.MsgType {
	case MsgPropose: // 收到提案消息

		if r.stopPropose { // 停止提案
			return ErrProposalDropped
		}
		if len(m.Logs) == 0 {
			r.Panic("MsgPropose logs is empty", zap.Uint64("nodeId", r.nodeId))
		}
		if !r.appendLog(m.Logs...) {
			return ErrProposalDropped
		}
		if r.isSingleNode() || r.opts.AckMode == AckModeNone { // 单机
			r.Debug("no ack", zap.Uint64("nodeId", r.nodeId), zap.Uint32("term", r.term), zap.Uint64("lastLogIndex", r.replicaLog.lastLogIndex), zap.Uint64("committedIndex", r.replicaLog.committedIndex))
			r.updateLeaderCommittedIndex() // 更新领导的提交索引
		}

	case MsgBeat: // 心跳
		r.sendPing(m.To)
	case MsgPong:
		if m.To != r.nodeId {
			r.Warn("receive pong, but msg to is not self", zap.Uint64("nodeId", r.nodeId), zap.Uint32("term", m.Term), zap.Uint64("from", m.From), zap.Uint64("to", m.To))
			return nil
		}
		if m.Term != r.term {
			r.Warn("receive pong, but msg term is not self term", zap.Uint64("nodeId", r.nodeId), zap.Uint32("term", m.Term), zap.Uint64("from", m.From), zap.Uint64("to", m.To))
			return nil
		}
		// if r.lastSyncInfoMap[m.From] == nil {
		// 	r.lastSyncInfoMap[m.From] = &SyncInfo{}
		// }

	case MsgSyncGetResp:
		r.setSyncing(m.To, false)
		if !m.Reject {
			r.send(r.newMsgSyncResp(m.To, m.Index, m.Logs))
		}

	case MsgSyncReq:
		lastIndex := r.replicaLog.lastLogIndex
		if r.detailLogOn {
			r.Info("sync req", zap.Uint64("from", m.From), zap.Uint64("index", m.Index), zap.Uint64("lastIndex", lastIndex))
		}

		if r.isSyncing(m.From) {
			r.Info("sync req, but is syncing", zap.Uint64("from", m.From), zap.Uint64("index", m.Index), zap.Uint64("lastIndex", lastIndex))
			return nil
		}
		r.setSyncing(m.From, true)

		if m.Index <= lastIndex {
			unstableLogs, exceed, err := r.replicaLog.getLogsFromUnstable(m.Index, lastIndex+1, logEncodingSize(r.opts.SyncLimitSize))
			if err != nil {
				r.Error("get logs from unstable failed", zap.Error(err))
				return err
			}

			// 如果结果超过限制大小或者结果已经查询到最后，则直接发送同步返回
			if exceed || (len(unstableLogs) > 0 && unstableLogs[len(unstableLogs)-1].Index >= lastIndex) {
				if unstableLogs[0].Index != m.Index {
					r.Panic("get logs from unstable failed", zap.Uint64("nodeId", r.nodeId), zap.Uint64("from", m.From), zap.Uint64("index", m.Index), zap.Uint64("lastIndex", lastIndex), zap.Uint64("firstIndex", unstableLogs[0].Index))
				}
				r.send(r.newMsgSyncResp(m.From, m.Index, unstableLogs))
				r.setSyncing(m.From, false)
			} else {
				// 如果未满足条件，则发起日志获取请求，让上层去查询剩余日志
				r.send(r.newMsgSyncGet(m.From, m.Index, unstableLogs))
			}
		} else {
			r.send(r.newMsgSyncResp(m.From, m.Index, nil))
			r.setSyncing(m.From, false)
		}

		isLearner := r.isLearner(m.From) // 当前同步节点是否是学习者

		if !isLearner {
			r.updateReplicSyncInfo(m)      // 更新副本同步信息
			r.updateLeaderCommittedIndex() // 更新领导的提交索引
		}

		if r.opts.AutoRoleSwith && r.cfg.MigrateTo != 0 && r.cfg.MigrateFrom != 0 { // 开启了自动切换角色

			if isLearner && !r.isRoleTransitioning {
				// 如果迁移的源节点是领导者，那么学习者必须完全追上领导者的日志
				if r.cfg.MigrateFrom == r.leader { // 学习者转领导者
					if m.Index >= r.replicaLog.lastLogIndex+1 {
						r.isRoleTransitioning = true // 学习者转让中
						r.roleTransitioningTimeoutTick = 0
						// 发送学习者转为领导者
						r.send(r.newMsgLearnerToLeader(m.From))
					} else if m.Index+r.opts.LearnerToLeaderMinLogGap > r.replicaLog.lastLogIndex { // 如果日志差距达到预期，则当前领导停止接受任何提案，等待学习者日志完全追赶上
						r.stopPropose = true // 停止提案
					}

				} else { // 学习者转追随者
					// 如果learner的日志已经追上了follower的日志，那么将learner转为follower
					if m.Index+r.opts.LearnerToFollowerMinLogGap > r.replicaLog.lastLogIndex {
						r.isRoleTransitioning = true
						r.roleTransitioningTimeoutTick = 0
						// 发送学习者转为追随者
						r.send(r.newMsgLearnerToFollower(m.From))
					}
				}
			} else if !isLearner && r.cfg.MigrateFrom == r.leader && r.cfg.MigrateTo == m.From { // 追随者转为领导者
				if m.Index >= r.replicaLog.lastLogIndex+1 {
					r.isRoleTransitioning = true // 追随者转让中
					r.roleTransitioningTimeoutTick = 0
					// 发送追随者转为领导者
					r.send(r.newFollowerToLeader(m.From))
				} else if m.Index+r.opts.FollowerToLeaderMinLogGap > r.replicaLog.lastLogIndex { // 如果日志差距达到预期，则当前领导停止接受任何提案，等待学习者日志完全追赶上
					r.stopPropose = true // 停止提案
				}
			}

		}
	case MsgConfigReq: // 收到配置请求
		r.send(r.newMsgConfigResp(m.From))
	}
	return nil
}

// 是否同步中
func (r *Replica) isSyncing(from uint64) bool {
	v := r.handleSyncReplicaMap[from]
	return v
}

// 设置同步中
func (r *Replica) setSyncing(from uint64, v bool) {
	r.handleSyncReplicaMap[from] = v
}

func (r *Replica) stepFollower(m Message) error {

	switch m.MsgType {
	case MsgPing:
		r.electionElapsed = 0
		if r.leader == None {
			r.becomeFollower(m.Term, m.From)
		}

		if m.ConfVersion > r.cfg.Version { // 如果本地配置版本小于领导的配置版本，那么请求领导的配置
			r.send(r.newMsgConfigReq(m.From))
		}

		r.setSpeedLevel(m.SpeedLevel) // 设置同步速度
		r.send(r.newPong(m.From))
		// r.Debug("recv ping", zap.Uint64("nodeID", r.nodeID), zap.Uint32("term", m.Term), zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.Uint64("lastLogIndex", r.replicaLog.lastLogIndex), zap.Uint64("leaderCommittedIndex", m.CommittedIndex), zap.Uint64("committedIndex", r.replicaLog.committedIndex))
		r.updateFollowCommittedIndex(m.CommittedIndex) // 更新提交索引
	case MsgLogConflictCheckResp: // 日志冲突检查返回
		if !m.Reject {
			r.status = StatusReady
			r.coflictCheckState.ProcessSuccess()
			// r.Info("follower: truncate log to", zap.Uint64("leader", r.leader), zap.Uint32("term", r.term), zap.Uint64("index", m.Index), zap.Uint64("lastIndex", r.replicaLog.lastLogIndex))
			if m.Index != NoConflict && m.Index > 0 {
				truncateLogIndex := m.Index
				if truncateLogIndex > r.replicaLog.lastLogIndex+1 {
					truncateLogIndex = r.replicaLog.lastLogIndex + 1
				}

				if truncateLogIndex >= r.replicaLog.unstable.offset {
					r.replicaLog.unstable.truncateLogTo(truncateLogIndex)
				}
				r.replicaLog.updateLastIndex(truncateLogIndex - 1)
			}

		} else {
			r.coflictCheckState.ProcessFail()
		}

	case MsgSyncResp: // 同步日志返回
		r.electionElapsed = 0 // 重置选举计时器
		// 设置同步速度
		r.setSpeedLevel(m.SpeedLevel)
		if r.detailLogOn {
			r.Info("sync resp", zap.Uint64("from", m.From), zap.Int("logs", len(m.Logs)), zap.Uint64("syncIndex", m.Index))
		}

		if !m.Reject {
			r.syncState.ProcessSuccess()
			// 如果有同步到日志，则追加到本地，并立马进行下次同步
			if len(m.Logs) > 0 {
				r.syncState.Immediately() // 下次立即同步

				if m.Logs[len(m.Logs)-1].Index <= r.replicaLog.lastLogIndex {
					r.Warn("log exist, no append", zap.Uint64("syncIndex", m.Index), zap.Uint64("leader", r.leader), zap.Uint64("maxLogIndex", m.Logs[len(m.Logs)-1].Index), zap.Uint64("localLastLogIndex", r.replicaLog.lastLogIndex))
					return nil
				}
				if !r.appendLog(m.Logs...) {
					return ErrProposalDropped
				}
			} else {
				r.syncState.Delay() // 推迟同步
			}
			r.updateFollowCommittedIndex(m.CommittedIndex) // 更新提交索引
		} else {
			r.syncState.ProcessFail()
		}

	}
	return nil
}

func (r *Replica) stepLearner(m Message) error {
	switch m.MsgType {
	case MsgPing:
		r.electionElapsed = 0
		if r.leader == None {
			r.becomeLearner(m.Term, m.From)

		}

		if m.ConfVersion > r.cfg.Version { // 如果本地配置版本小于领导的配置版本，那么请求领导的配置
			r.send(r.newMsgConfigReq(m.From))
		}

		r.setSpeedLevel(m.SpeedLevel) // 设置同步速度
		r.send(r.newPong(m.From))
	case MsgLogConflictCheckResp: // 日志冲突检查返回
		if !m.Reject {
			r.coflictCheckState.ProcessSuccess()

			r.Info("learner: truncate log to", zap.Uint64("leader", r.leader), zap.Uint32("term", r.term), zap.Uint64("index", m.Index), zap.Uint64("lastIndex", r.replicaLog.lastLogIndex))
			if m.Index != NoConflict && m.Index > 0 {
				truncateLogIndex := m.Index
				if truncateLogIndex > r.replicaLog.lastLogIndex+1 {
					truncateLogIndex = r.replicaLog.lastLogIndex + 1
				}

				if truncateLogIndex >= r.replicaLog.unstable.offset {
					r.replicaLog.unstable.truncateLogTo(truncateLogIndex)
				}
				r.replicaLog.updateLastIndex(truncateLogIndex - 1)
			}
			r.status = StatusReady
		} else {
			r.coflictCheckState.willRetry = true
		}
	case MsgSyncResp: // 同步日志返回
		r.electionElapsed = 0
		// 设置同步速度
		r.setSpeedLevel(m.SpeedLevel)

		if !m.Reject {
			r.syncState.ProcessSuccess()
			// 如果有同步到日志，则追加到本地，并立马进行下次同步
			if len(m.Logs) > 0 {
				r.syncState.Immediately()
				if m.Logs[len(m.Logs)-1].Index <= r.replicaLog.lastLogIndex {
					r.Warn("learner: log exist, no append", zap.Uint64("syncIndex", m.Index), zap.Uint64("leader", r.leader), zap.Uint64("maxLogIndex", m.Logs[len(m.Logs)-1].Index), zap.Uint64("localLastLogIndex", r.replicaLog.lastLogIndex))
					return nil
				}
				if !r.appendLog(m.Logs...) {
					return ErrProposalDropped
				}
			} else {
				r.syncState.Delay()
			}
			r.updateFollowCommittedIndex(m.CommittedIndex) // 更新提交索引
		} else {
			r.syncState.ProcessFail()
		}

	}

	return nil
}

func (r *Replica) stepCandidate(m Message) error {
	switch m.MsgType {
	case MsgPing:
		if m.ConfVersion > r.cfg.Version { // 如果本地配置版本小于领导的配置版本，那么请求领导的配置
			r.send(r.newMsgConfigReq(m.From))
		}
		r.becomeFollower(m.Term, m.From)
		r.send(r.newPong(m.From))
	case MsgVoteResp:
		r.Info("received vote response", zap.Bool("reject", m.Reject), zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.Uint32("term", m.Term), zap.Uint64("index", m.Index))
		r.poll(m)
	}
	return nil
}

// 统计投票
func (r *Replica) poll(m Message) {
	r.votes[m.From] = !m.Reject
	var granted int
	for _, v := range r.votes {
		if v {
			granted++
		}
	}
	if len(r.votes) < r.quorum() { // 投票数小于法定数
		return
	}
	if granted >= r.quorum() {
		r.becomeLeader(r.term) // 成为领导者
		r.sendPing(All)
	} else {
		r.becomeFollower(r.term, None)
	}
}

func (r *Replica) appendLog(logs ...Log) (accepted bool) {
	if len(logs) == 0 {
		return true
	}

	if !r.increaseUncommittedSize(logs) {
		r.Warn("appending new logs would exceed uncommitted log size limit; dropping proposal", zap.Uint64("size", uint64(r.uncommittedSize)), zap.Uint64("max", r.opts.MaxUncommittedLogSize))
		return false
	}
	if logs[0].Index != r.replicaLog.lastLogIndex+1 { // 连续性判断
		if r.role == RoleLeader {
			r.Panic("log index is not continuous", zap.Uint64("lastLogIndex", r.replicaLog.lastLogIndex), zap.Uint64("startLogIndex", logs[0].Index), zap.Uint64("endLogIndex", logs[len(logs)-1].Index))
		} else {
			r.Warn("log index is not continuous", zap.Uint64("lastLogIndex", r.replicaLog.lastLogIndex), zap.Uint64("startLogIndex", logs[0].Index), zap.Uint64("endLogIndex", logs[len(logs)-1].Index))
		}
		return false
	}

	if after := logs[0].Index; after < r.replicaLog.committedIndex {
		r.Panic("log index is out of range", zap.Uint64("after", after), zap.Int("logCount", len(logs)), zap.Uint64("lastIndex", r.replicaLog.lastLogIndex), zap.Uint64("committed", r.replicaLog.committedIndex))
		return false
	}

	if len(logs) == 0 {
		return true
	}
	r.replicaLog.appendLog(logs...)
	return true
}

func (r *Replica) increaseUncommittedSize(logs []Log) bool {
	var size logEncodingSize
	for _, l := range logs {
		size += logEncodingSize(l.LogSize())
	}
	if r.uncommittedSize > 0 && size > 0 && r.uncommittedSize+size > logEncodingSize(r.opts.MaxUncommittedLogSize) {
		return false
	}
	r.uncommittedSize += size
	return true
}

func (r *Replica) reduceUncommittedSize(s logEncodingSize) {
	if s > r.uncommittedSize {
		r.uncommittedSize = 0
	} else {
		r.uncommittedSize -= s
	}
}

// 更新跟随者的提交索引
func (r *Replica) updateFollowCommittedIndex(leaderCommittedIndex uint64) {
	if leaderCommittedIndex == 0 || leaderCommittedIndex <= r.replicaLog.committedIndex {
		return
	}
	newCommittedIndex := r.committedIndexForFollow(leaderCommittedIndex)
	if newCommittedIndex > r.replicaLog.committedIndex {
		r.replicaLog.committedIndex = newCommittedIndex
		r.Debug("update follow committed index", zap.Uint64("nodeId", r.nodeId), zap.Uint32("term", r.term), zap.Uint64("committedIndex", r.replicaLog.committedIndex))
	}
}

// 获取跟随者的提交索引
func (r *Replica) committedIndexForFollow(leaderCommittedIndex uint64) uint64 {
	if leaderCommittedIndex > r.replicaLog.committedIndex {
		return min(leaderCommittedIndex, r.replicaLog.lastLogIndex)

	}
	return r.replicaLog.committedIndex
}

// 更新领导的提交索引
func (r *Replica) updateLeaderCommittedIndex() bool {
	newCommitted := r.committedIndexForLeader() // 通过副本同步信息计算领导已提交下标
	updated := false
	if newCommitted > r.replicaLog.committedIndex {
		r.replicaLog.committedIndex = newCommitted
		updated = true
		r.Debug("update leader committed index", zap.Uint64("lastIndex", r.replicaLog.lastLogIndex), zap.Uint32("term", r.term), zap.Uint64("committedIndex", r.replicaLog.committedIndex))
	}
	return updated
}

// 通过副本同步信息计算已提交下标
func (r *Replica) committedIndexForLeader() uint64 {

	committed := r.replicaLog.committedIndex
	quorum := r.quorum() // r.replicas 不包含本节点
	if quorum <= 1 {     // 如果少于或等于一个节点，那么直接返回最后一条日志下标
		return r.replicaLog.lastLogIndex
	}

	// 获取比指定参数小的最大日志下标
	getMaxLogIndexLessThanParam := func(maxIndex uint64) uint64 {
		secondMaxIndex := uint64(0)
		for _, syncInfo := range r.lastSyncInfoMap {
			if syncInfo.LastSyncIndex < maxIndex || maxIndex == 0 {
				if secondMaxIndex < syncInfo.LastSyncIndex {
					secondMaxIndex = syncInfo.LastSyncIndex
				}
			}
		}
		if secondMaxIndex > 0 {
			return secondMaxIndex - 1
		}
		return secondMaxIndex
	}

	maxLogIndex := uint64(0)
	newCommitted := uint64(0)
	for {
		count := 0
		maxLogIndex = getMaxLogIndexLessThanParam(maxLogIndex)
		if maxLogIndex == 0 {
			break
		}
		if maxLogIndex <= committed {
			break
		}
		if maxLogIndex > r.replicaLog.lastLogIndex {
			continue
		}
		for _, syncInfo := range r.lastSyncInfoMap {
			if syncInfo.LastSyncIndex-1 >= maxLogIndex {
				count++
			}
			if count+1 >= quorum {
				newCommitted = maxLogIndex
				break
			}
		}
	}
	if newCommitted > committed {
		return min(newCommitted, r.replicaLog.lastLogIndex)
	}
	return committed

}

// 更新副本同步信息
func (r *Replica) updateReplicSyncInfo(m Message) {
	from := m.From
	syncInfo := r.lastSyncInfoMap[from]
	if syncInfo == nil {
		r.Info("syncInfo is nil", zap.Uint64("from", from))
		return
	}
	if m.Index > syncInfo.LastSyncIndex {
		syncInfo.LastSyncIndex = m.Index
		// r.Debug("update replic sync info", zap.Uint32("term", r.replicaLog.term), zap.Uint64("from", from), zap.Uint64("lastSyncLogIndex", syncInfo.LastSyncLogIndex))
	}
	syncInfo.SyncTick = 0
}

func (r *Replica) quorum() int {
	return (len(r.replicas)+1)/2 + 1 //  r.replicas 不包含本节点
}

// 是否可以投票
func (r *Replica) canVote(m Message) bool {

	if r.term > m.Term { // 如果当前任期大于候选人任期，拒绝投票
		return false
	}

	if r.voteFor != None && r.voteFor != m.From { // 如果已经投票给其他节点，拒绝投票
		return false
	}

	lastIndex, lastTerm := r.replicaLog.lastIndexAndTerm()                                               // 获取当前节点最后一条日志下标和任期
	candidateLog := m.Logs[0]                                                                            // 候选人最后一条日志信息
	if candidateLog.Term < lastTerm || candidateLog.Term == lastTerm && candidateLog.Index < lastIndex { // 如果候选人日志小于本地日志，拒绝投票
		return false
	}

	return true
}
