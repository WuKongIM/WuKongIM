package replica

import (
	"time"

	"go.uber.org/zap"
)

func (r *Replica) Step(m Message) error {

	switch {
	case m.Term == 0: // 本地消息
		r.Warn("term is zero", zap.Uint64("nodeID", r.nodeID), zap.Uint32("term", m.Term), zap.Uint64("from", m.From), zap.Uint64("to", m.To))
	case m.Term > r.state.term:

		// 高任期消息
		if m.MsgType == MsgNotifySync {
			r.becomeFollower(m.Term, m.From)
		}
	default:
		if m.MsgType == MsgApplyLogsResp {
			if m.Index > r.state.committedIndex {
				r.Error("invalid apply logs resp", zap.Uint64("nodeID", r.nodeID), zap.Uint32("term", m.Term), zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.Uint64("index", m.Index), zap.Uint64("committedIndex", m.CommittedIndex))
			} else {
				if m.Index > r.state.appliedIndex {
					r.state.appliedIndex = m.Index
				}
			}

		}

	}
	err := r.stepFunc(m)
	if err != nil {
		return err
	}
	return nil
}

func (r *Replica) stepLeader(m Message) error {

	switch m.MsgType {
	case MsgPropose: // 收到提案消息
		if len(m.Logs) == 0 {
			r.Panic("MsgPropose logs is empty", zap.Uint64("nodeID", r.nodeID))
		}
		if !r.appendLog(m.Logs...) {
			return ErrProposalDropped
		}
		r.bcastNotifySync() // 广播通知同步消息

	case MsgSync: // 追随者向领导同步消息

		r.Info("receive sync", zap.Uint64("nodeID", r.nodeID), zap.Uint32("term", m.Term), zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.Uint64("index", m.Index))
		if m.Index <= r.state.lastLogIndex {
			logs, err := r.opts.Storage.Logs(m.Index, 0, r.opts.SyncLimit)
			if err != nil {
				return err
			}

			r.updateReplicSyncInfo(m) // 更新副本同步信息

			r.updateLeaderCommittedIndex() // 更新领导的提交索引

			r.send(Message{
				MsgType:        MsgSyncResp,
				From:           r.nodeID,
				To:             m.From,
				Term:           r.state.term,
				Logs:           logs,
				CommittedIndex: r.state.committedIndex,
			})
		} else {
			r.updateReplicSyncInfo(m) // 更新副本同步信息

			r.updateLeaderCommittedIndex() // 更新领导的提交索引
		}

	case MsgLeaderTermStartIndexReq: // 领导收到跟随者的任期开始索引请求
		// 如果MsgLeaderTermStartIndexReq的term等于领导的term则领导返回当前最新日志下标，否则返回MsgLeaderTermStartIndexReq里的term+1的 任期的第一条日志下标，返回的这个值称为LastOffset
		if m.Term == r.state.term {
			r.send(Message{
				MsgType: MsgLeaderTermStartIndexResp,
				From:    r.nodeID,
				To:      m.From,
				Term:    r.state.term,
				Index:   r.state.lastLogIndex,
			})
		} else {
			syncTerm := m.Term + 1
			lastIndex, err := r.opts.Storage.LeaderTermStartIndex(syncTerm)
			if err != nil {
				return err
			}
			if lastIndex == 0 {
				r.Error("leader term start index not found", zap.Uint32("term", syncTerm))
				return ErrLeaderTermStartIndexNotFound
			}
			r.send(Message{
				MsgType: MsgLeaderTermStartIndexResp,
				From:    r.nodeID,
				To:      m.From,
				Term:    syncTerm,
				Index:   lastIndex,
			})
		}
	}

	return nil
}

func (r *Replica) stepFollower(m Message) error {
	switch m.MsgType {
	case MsgNotifySync: // 收到同步通知
		if r.disabledToSync {
			r.Info("disabled to sync", zap.Uint64("leader", r.leader), zap.Uint32("term", r.state.term))
			return nil
		}
		// r.Info("receive notify sync", zap.Uint64("lastLogIndex", r.state.lastLogIndex), zap.Uint64("nodeID", r.nodeID), zap.Uint32("term", m.Term), zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.Uint64("index", m.Index))
		r.updateFollowCommittedIndex(m.CommittedIndex) // 更新提交索引
		// 发送同步消息
		r.send(Message{
			MsgType: MsgSync,
			From:    r.nodeID,
			To:      r.leader,
			Term:    r.state.term,
			Index:   r.state.lastLogIndex + 1,
		})
	case MsgSyncResp: // 领导返回同步消息的结果
		if m.Reject {
			r.Warn("sync rejected", zap.Uint64("leader", r.leader), zap.Uint64("index", m.Index), zap.Uint32("term", m.Term))
			return nil
		}
		if r.disabledToSync {
			r.Info("disabled to sync", zap.Uint64("leader", r.leader), zap.Uint32("term", r.state.term))
			return nil
		}
		if len(m.Logs) == 0 {
			r.Debug("sync logs is empty", zap.Uint64("leader", r.leader), zap.Uint64("index", m.Index), zap.Uint32("term", m.Term))
			return nil
		}
		if !r.appendLog(m.Logs...) {
			return ErrProposalDropped
		}
		r.updateFollowCommittedIndex(m.CommittedIndex) // 更新提交索引

	case MsgAppointLeaderReq: // 收到任命消息
		if m.AppointmentLeader == r.nodeID { // 如果任命中的领导是自己，则成为领导否者称为跟随者
			r.becomeLeader(m.Term)
		} else {
			r.becomeFollower(m.Term, m.AppointmentLeader)
		}
		r.send(Message{
			MsgType: MsgAppointLeaderResp,
			From:    r.nodeID,
			To:      m.From,
			Term:    r.state.term,
		})
	case MsgLeaderTermStartIndexResp: // 收到领导的任期开始索引响应
		// Follower检查本地的LeaderTermSequence
		// 是否有term对应的StartOffset大于领导返回的LastOffset，
		// 如果有则将当前term的startOffset设置为LastOffset，
		// 并且当前term为最新的term（也就是删除比当前term大的LeaderTermSequence的记录）
		termStartIndex, err := r.opts.Storage.LeaderTermStartIndex(m.Term)
		if err != nil {
			r.Error("leader term start index not found", zap.Uint32("term", m.Term))
			return err
		}
		if termStartIndex == 0 {
			err := r.opts.Storage.SetLeaderTermStartIndex(m.Term, m.Index)
			if err != nil {
				r.Error("set leader term start index failed", zap.Error(err))
				return err
			}
		} else if termStartIndex > m.Index {
			err := r.opts.Storage.SetLeaderTermStartIndex(m.Term, m.Index)
			if err != nil {
				r.Error("set leader term start index failed", zap.Error(err))
				return err
			}
			err = r.opts.Storage.DeleteLeaderTermStartIndexGreaterThanTerm(m.Term)
			if err != nil {
				r.Error("delete leader term start index failed", zap.Error(err))
				return err
			}
		}
		// Follower截断本地的日志到领导返回的index的位置（剩余的日志包含index）
		err = r.opts.Storage.TruncateLogTo(m.Index)
		if err != nil {
			r.Error("truncate log failed", zap.Error(err))
			return err
		}
		lastIdx, err := r.opts.Storage.LastIndex()
		if err != nil {
			r.Error("get last index failed", zap.Error(err))
			return err
		}
		r.state.lastLogIndex = lastIdx
		r.localLeaderLastTerm = m.Term
		r.disabledToSync = false // 现在可以去同步领导的日志了
		r.Info("enable to sync", zap.Uint64("leader", r.leader), zap.Uint64("lastIndex", r.state.lastLogIndex), zap.Uint32("term", m.Term))
	}
	return nil
}

func (r *Replica) appendLog(logs ...Log) (accepted bool) {
	if len(logs) == 0 {
		return true
	}

	if !r.increaseUncommittedSize(logs) {
		r.Warn("appending new logs would exceed uncommitted log size limit; dropping proposal", zap.Int("size", r.uncommittedSize), zap.Uint64("max", r.opts.MaxUncommittedLogSize))
		return false
	}

	for i, lg := range logs {
		if r.localLeaderLastTerm != lg.Term {
			r.localLeaderLastTerm = lg.Term
			err := r.opts.Storage.SetLeaderTermStartIndex(lg.Term, lg.Index)
			if err != nil {
				r.Panic("set leader term start index failed", zap.Error(err))
				return false
			}
		}
		if r.state.lastLogIndex >= lg.Index {
			logs = logs[:i]
			break
		}
	}
	if len(logs) == 0 {
		return true
	}
	r.Info("append log", zap.Uint64("nodeID", r.nodeID), zap.Uint32("term", r.state.term), zap.Uint64("lastLogIndex", r.state.lastLogIndex), zap.Uint64("committedIndex", r.state.committedIndex), zap.Int("logSize", len(logs)))
	lastLog := logs[len(logs)-1]
	err := r.opts.Storage.AppendLog(logs)
	if err != nil {
		r.Panic("append log failed", zap.Error(err))
		return false
	}
	r.state.lastLogIndex = lastLog.Index
	r.Info("append log success", zap.Uint64("nodeID", r.nodeID), zap.Uint32("term", r.state.term), zap.Uint64("lastLogIndex", r.state.lastLogIndex), zap.Uint64("committedIndex", r.state.committedIndex), zap.Int("logSize", len(logs)))

	return true
}

func (r *Replica) increaseUncommittedSize(logs []Log) bool {
	size := 0
	for _, l := range logs {
		size += l.LogSize()
	}
	if r.uncommittedSize > 0 && size > 0 && r.uncommittedSize+size > int(r.opts.MaxUncommittedLogSize) {
		return false
	}
	r.uncommittedSize += size
	return true
}

func (r *Replica) send(m Message) {
	if m.To == r.opts.NodeID {
		r.Panic("can not send message to self", zap.Uint64("nodeID", r.opts.NodeID))
	}
	r.msgs = append(r.msgs, m)
}

// 广播通知同步消息
func (r *Replica) bcastNotifySync() {
	for _, replicaID := range r.replicas {
		if replicaID == r.nodeID {
			continue
		}
		r.send(r.newNotifySyncMsg(replicaID))
	}
}

// 获取跟随者的提交索引
func (r *Replica) committedIndexForFollow(leaderCommittedIndex uint64) uint64 {
	if leaderCommittedIndex > r.state.committedIndex {
		var newCommitted uint64
		if leaderCommittedIndex > r.state.lastLogIndex {
			newCommitted = r.state.lastLogIndex
		} else {
			newCommitted = leaderCommittedIndex
		}
		return newCommitted

	}
	return r.state.committedIndex
}

// 通过副本同步信息计算已提交下标
func (r *Replica) committedIndexForLeader() uint64 {

	committed := r.state.committedIndex
	quorum := len(r.replicas) / 2 // r.replicas 不包含本节点

	if quorum == 0 { // 如果少于或等于一个节点，那么直接返回最后一条日志下标
		return r.state.lastLogIndex
	}

	// 获取比指定参数小的最大日志下标
	getMaxLogIndexLessThanParam := func(maxIndex uint64) uint64 {
		secondMaxIndex := uint64(0)
		for _, syncInfo := range r.lastSyncInfoMap {
			if syncInfo.LastSyncLogIndex < maxIndex || maxIndex == 0 {
				if secondMaxIndex < syncInfo.LastSyncLogIndex {
					secondMaxIndex = syncInfo.LastSyncLogIndex
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
		if maxLogIndex > r.state.lastLogIndex {
			continue
		}
		for _, syncInfo := range r.lastSyncInfoMap {
			if syncInfo.LastSyncLogIndex >= maxLogIndex {
				count++
			}
			if count >= quorum {
				newCommitted = maxLogIndex
				break
			}
		}
	}
	if newCommitted > committed {
		return newCommitted
	}
	return committed

}

// 更新跟随者的提交索引
func (r *Replica) updateFollowCommittedIndex(leaderCommittedIndex uint64) {
	if leaderCommittedIndex == 0 {
		return
	}
	newCommittedIndex := r.committedIndexForFollow(leaderCommittedIndex)
	if newCommittedIndex > r.state.committedIndex {
		r.state.committedIndex = newCommittedIndex
		r.Info("update follow committed index", zap.Uint64("nodeID", r.nodeID), zap.Uint32("term", r.state.term), zap.Uint64("committedIndex", r.state.committedIndex))
	}
}

// 更新领导的提交索引
func (r *Replica) updateLeaderCommittedIndex() {
	newCommitted := r.committedIndexForLeader() // 通过副本同步信息计算领导已提交下标
	if newCommitted > r.state.committedIndex {
		r.state.committedIndex = newCommitted
		r.Info("update leader committed index", zap.Uint64("nodeID", r.nodeID), zap.Uint32("term", r.state.term), zap.Uint64("committedIndex", r.state.committedIndex))
	}
}

// 更新副本同步信息
func (r *Replica) updateReplicSyncInfo(m Message) {
	from := m.From
	syncInfo := r.lastSyncInfoMap[from]
	if syncInfo == nil {
		syncInfo = &SyncInfo{}
		r.lastSyncInfoMap[from] = syncInfo
	}
	syncInfo.LastSyncLogIndex = m.Index
	syncInfo.LastSyncTime = uint64(time.Now().UnixNano())
}
