package raft

import (
	"github.com/WuKongIM/WuKongIM/pkg/raft/track"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"go.uber.org/zap"
)

func (n *Node) Step(e types.Event) error {
	// n.Info("step event", zap.Uint64("from", e.From), zap.Uint64("to", e.To), zap.Uint32("term", e.Term), zap.Uint64("index", e.Index), zap.String("type", e.Type.String()))
	switch {
	case e.Term == 0: // 本地消息
	case e.Term < n.cfg.Term: // 低于当前任期
		n.Info("received event with lower term", zap.Uint32("term", e.Term), zap.Uint32("currentTerm", n.cfg.Term), zap.Uint64("from", e.From), zap.Uint64("to", e.To), zap.String("type", e.Type.String()))
		return nil
	case e.Term > n.cfg.Term: // 高于当前任期
		if n.cfg.Term > 0 {
			n.Info("received event with higher term", zap.Uint32("term", n.cfg.Term), zap.Uint32("currentTerm", e.Term), zap.Uint64("from", e.From), zap.Uint64("to", e.To), zap.String("type", e.Type.String()))
		}
		if e.Type == types.Ping || e.Type == types.SyncResp {
			if n.cfg.Role == types.RoleLearner {
				n.BecomeLearner(e.Term, e.From)
			} else {
				n.BecomeFollower(e.Term, e.From)
			}
		} else {
			if n.cfg.Role == types.RoleLearner {
				n.Warn("become learner but leader is none", zap.Uint64("from", e.From), zap.String("type", e.Type.String()), zap.Uint32("term", e.Term))
				n.BecomeLearner(e.Term, None)
			} else {
				n.Warn("become follower but leader is none", zap.Uint64("from", e.From), zap.String("type", e.Type.String()), zap.Uint32("term", e.Term))
				n.BecomeFollower(e.Term, None)
			}
		}
	}

	switch e.Type {
	case types.ConfChange: // 配置变更
		n.switchConfig(e.Config)
	case types.Campaign:
		n.campaign()
	case types.VoteReq: // 投票请求
		if n.canVote(e) {
			if e.From == n.opts.NodeId {
				n.sendVoteResp(types.LocalNode, types.ReasonOk)
			} else {
				n.sendVoteResp(e.From, types.ReasonOk)
			}

			n.voteFor = e.From
			n.electionElapsed = 0
			if e.From != n.opts.NodeId {
				n.Info("agree vote", zap.Uint64("voteFor", e.From), zap.Uint32("term", e.Term), zap.Uint64("index", e.Index))
			}
		} else {
			if n.voteFor != None {
				n.Info("already vote for other", zap.Uint64("voteFor", n.voteFor))
			} else if e.Index < n.queue.lastLogIndex {
				n.Info("lower config version, reject vote")
			} else if e.Term < n.cfg.Term {
				n.Info("lower term, reject vote")
			}
			n.Info("reject vote", zap.Uint64("from", e.From), zap.Uint32("term", e.Term), zap.Uint64("index", e.Index))
			n.sendVoteResp(e.From, types.ReasonError)
		}
	default:
		if n.stepFunc != nil {
			return n.stepFunc(e)
		}

	}
	return nil
}

func (n *Node) stepLeader(e types.Event) error {

	switch e.Type {
	case types.Propose: // 提案
		err := n.queue.append(e.Logs...)
		if err != nil {
			return err
		}
		n.advance()
	case types.SyncReq: // 同步
		isLearner := n.isLearner(e.From) // 当前同步节点是否是学习者
		if !isLearner {
			n.updateSyncInfo(e)            // 更新副本同步信息
			n.updateLeaderCommittedIndex() // 更新领导的提交索引
		}

		syncInfo := n.replicaSync[e.From]

		// 无数据可同步
		if e.Reason == types.ReasonOnlySync && e.Index > n.queue.storedIndex {
			n.sendSyncResp(e.From, e.Index, nil, types.ReasonOk)
			return nil
		}
		if !syncInfo.GetingLogs {
			syncInfo.GetingLogs = true

			n.sendGetLogsReq(e)
			n.advance()
		}

	case types.StoreResp: // 异步存储日志返回
		n.queue.appending = false
		if e.Reason == types.ReasonOk {
			if e.Index > n.queue.lastLogIndex {
				n.Panic("invalid append response", zap.Uint64("index", e.Index), zap.Uint64("lastLogIndex", n.queue.lastLogIndex))
			}
			n.queue.storeTo(e.Index)
			if n.quorum() <= 1 {
				// 如果少于或等于一个节点，那么直接提交
				n.updateLeaderCommittedIndex()
			}
			// 通知副本过来同步日志
			n.sendNotifySync(All)
			n.advance()

		}
	case types.GetLogsResp: // 获取日志返回
		for i := range e.Logs {
			e.Logs[i].Record.Add(track.PositionSyncResp)
		}
		syncInfo := n.replicaSync[e.To]
		syncInfo.GetingLogs = false
		n.sendSyncResp(e.To, e.Index, e.Logs, e.Reason)
		n.advance()

	}

	return nil
}

func (n *Node) stepFollower(e types.Event) error {
	switch e.Type {
	case types.Ping: // 心跳
		n.electionElapsed = 0
		if n.cfg.Leader == None {
			n.BecomeFollower(e.Term, e.From)
		}
		n.updateFollowCommittedIndex(e.CommittedIndex) // 更新提交索引
	case types.NotifySync:
		n.sendSyncReq()
		n.advance()
	case types.SyncResp: // 同步返回
		n.electionElapsed = 0
		if !n.onlySync {
			n.onlySync = true
		}
		if e.Reason == types.ReasonOk {
			if len(e.Logs) > 0 {
				err := n.queue.append(e.Logs...)
				if err != nil {
					return err
				}
				// 推进去存储
				n.advance()
			}
			n.updateFollowCommittedIndex(e.CommittedIndex) // 更新提交索引

		} else if e.Reason == types.ReasonTrunctate {
			err := n.opts.Storage.TruncateLogTo(e.Index)
			if err != nil {
				n.Panic("truncate log failed", zap.Error(err), zap.Uint64("index", e.Index))
				return err
			}
			// 删除本地的leader term start index
			err = n.opts.Storage.DeleteLeaderTermStartIndexGreaterThanTerm(e.Term)
			if err != nil {
				n.Panic("delete leader term start index failed", zap.Error(err), zap.Uint32("term", e.Term))
				return err
			}
			n.queue.truncateLogTo(e.Index)
			n.sendSyncReq()
			n.advance()
		}
	case types.StoreResp: // 异步存储日志返回
		n.queue.appending = false
		if e.Reason == types.ReasonOk {
			if e.Index > n.queue.lastLogIndex {
				n.Panic("invalid append response", zap.Uint64("index", e.Index), zap.Uint64("lastLogIndex", n.queue.lastLogIndex))
			}
			n.queue.storeTo(e.Index)
		}

		if n.lastTermStartIndex.Term != e.Term {
			n.softState.termStartIndex = &types.TermStartIndexInfo{
				Term:  e.Term,
				Index: e.Index,
			}
			n.updateLastTermStartIndex(e.Term, e.Index)
		}
		n.sendSyncReq()
		n.advance()
	}
	return nil
}

func (n *Node) stepCandidate(e types.Event) error {
	switch e.Type {
	case types.VoteResp: // 投票返回
		if e.From != n.opts.NodeId {
			n.Info("received vote response", zap.Uint8("reason", e.Reason.Uint8()), zap.Uint64("from", e.From), zap.Uint64("to", e.To), zap.Uint32("term", e.Term), zap.Uint64("index", e.Index))
		}
		n.poll(e)
	}
	return nil
}

func (n *Node) stepLearner(e types.Event) error {

	return nil
}

// 统计投票
func (n *Node) poll(e types.Event) {
	n.votes[e.From] = e.Reason == types.ReasonOk
	var granted int
	for _, v := range n.votes {
		if v {
			granted++
		}
	}
	if len(n.votes) < n.quorum() { // 投票数小于法定数
		return
	}
	if granted >= n.quorum() {
		n.BecomeLeader(n.cfg.Term) // 成为领导者
		n.sendPing(All)            // 成为领导后立马发生ping，开始工作
		n.advance()
	} else {
		n.BecomeFollower(n.cfg.Term, None)
	}
}

// 合法投票数
func (n *Node) quorum() int {
	return (len(n.cfg.Replicas)+1)/2 + 1 //  n.cfg.Replicas 不包含本节点
}

// 是否可以投票
func (n *Node) canVote(e types.Event) bool {

	if n.cfg.Term > e.Term { // 如果当前任期大于候选人任期，拒绝投票
		return false
	}

	if n.voteFor != None && n.voteFor != e.From { // 如果已经投票给其他节点，拒绝投票
		return false
	}

	lastIndex := n.queue.lastLogIndex
	lastTerm := n.cfg.Term                                                                               // 获取当前节点最后一条日志下标和任期
	candidateLog := e.Logs[0]                                                                            // 候选人最后一条日志信息
	if candidateLog.Term < lastTerm || candidateLog.Term == lastTerm && candidateLog.Index < lastIndex { // 如果候选人日志小于本地日志，拒绝投票
		return false
	}

	return true
}

// 更新同步信息
func (n *Node) updateSyncInfo(e types.Event) {
	syncInfo := n.syncState.replicaSync[e.From]
	if syncInfo == nil {
		syncInfo = &SyncInfo{}
		n.syncState.replicaSync[e.From] = syncInfo
	}
	syncInfo.SyncTick = 0
	syncInfo.LastSyncIndex = e.Index
	syncInfo.StoredIndex = e.StoredIndex
}

// 更新领导的提交索引
func (n *Node) updateLeaderCommittedIndex() bool {
	newCommitted := n.committedIndexForLeader() // 通过副本同步信息计算领导已提交下标
	updated := false
	if newCommitted > n.queue.committedIndex {
		n.queue.committedIndex = newCommitted
		updated = true
		n.Info("update leader committed index", zap.Uint64("lastIndex", n.queue.lastLogIndex), zap.Uint32("term", n.cfg.Term), zap.Uint64("committedIndex", n.queue.committedIndex))
	}
	return updated
}

// 通过副本同步信息计算已提交下标
func (n *Node) committedIndexForLeader() uint64 {

	committed := n.queue.committedIndex
	quorum := n.quorum() // r.replicas 不包含本节点
	if quorum <= 1 {     // 如果少于或等于一个节点，那么直接返回最后一条日志下标
		return n.queue.storedIndex
	}

	// 获取比指定参数小的最大日志下标
	getMaxLogIndexLessThanParam := func(maxIndex uint64) uint64 {
		secondMaxIndex := uint64(0)
		for _, syncInfo := range n.replicaSync {
			if syncInfo.StoredIndex < maxIndex || maxIndex == 0 {
				if secondMaxIndex < syncInfo.StoredIndex {
					secondMaxIndex = syncInfo.StoredIndex
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
		if maxLogIndex > n.queue.storedIndex {
			continue
		}
		for _, syncInfo := range n.replicaSync {
			if syncInfo.StoredIndex >= maxLogIndex+1 {
				count++
			}
			if count+1 >= quorum {
				newCommitted = maxLogIndex
				break
			}
		}
	}
	if newCommitted > committed {
		return min(newCommitted, n.queue.storedIndex)
	}
	return committed

}

// 更新跟随者的提交索引
func (n *Node) updateFollowCommittedIndex(leaderCommittedIndex uint64) {
	if leaderCommittedIndex == 0 || leaderCommittedIndex <= n.queue.committedIndex {
		return
	}
	newCommittedIndex := n.committedIndexForFollow(leaderCommittedIndex)
	if newCommittedIndex > n.queue.committedIndex {
		n.queue.committedIndex = newCommittedIndex
		n.Info("update follow committed index", zap.Uint64("nodeId", n.opts.NodeId), zap.Uint32("term", n.cfg.Term), zap.Uint64("committedIndex", n.queue.committedIndex))
	}
}

// 获取跟随者的提交索引
func (n *Node) committedIndexForFollow(leaderCommittedIndex uint64) uint64 {
	if leaderCommittedIndex > n.queue.committedIndex {
		return min(leaderCommittedIndex, n.queue.storedIndex)

	}
	return n.queue.committedIndex
}
