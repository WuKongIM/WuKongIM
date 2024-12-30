package raft

import "go.uber.org/zap"

func (n *Node) Step(e Event) error {

	switch {
	case n.cfg.Term == 0: // 本地消息
	case n.cfg.Term < e.Term: // 低于当前任期
		n.Info("received event with lower term", zap.Uint32("term", e.Term), zap.Uint32("currentTerm", n.cfg.Term), zap.Uint64("from", e.From), zap.Uint64("to", e.To), zap.String("type", e.Type.String()))
		return nil
	case n.cfg.Term > e.Term: // 高于当前任期
		if n.cfg.Term > 0 {
			n.Info("received event with higher term", zap.Uint32("term", n.cfg.Term), zap.Uint32("currentTerm", e.Term), zap.Uint64("from", e.From), zap.Uint64("to", e.To), zap.String("type", e.Type.String()))
		}
		if e.Type == Ping || e.Type == SyncResp {
			if n.cfg.Role == RoleLearner {
				n.becomeLearner(e.Term, e.From)
			} else {
				n.becomeFollower(e.Term, e.From)
			}
		} else {
			if n.cfg.Role == RoleLearner {
				n.Warn("become learner but leader is none", zap.Uint64("from", e.From), zap.String("type", e.Type.String()), zap.Uint32("term", e.Term))
				n.becomeLearner(e.Term, None)
			} else {
				n.Warn("become follower but leader is none", zap.Uint64("from", e.From), zap.String("type", e.Type.String()), zap.Uint32("term", e.Term))
				n.becomeFollower(e.Term, None)
			}
		}
	}

	switch e.Type {
	case ConfChange: // 配置变更
		n.switchConfig(e.Config)
	case VoteReq: // 投票请求
		if n.canVote(e) {
			n.sendVoteResp(e.From, false)
			n.voteFor = e.From
			n.electionElapsed = 0
			n.Info("agree vote", zap.Uint64("voteFor", e.From), zap.Uint32("term", e.Term), zap.Uint64("index", e.Index))
		} else {
			if n.voteFor != None {
				n.Info("already vote for other", zap.Uint64("voteFor", n.voteFor))
			} else if e.Index < n.queue.lastLogIndex {
				n.Info("lower config version, reject vote")
			} else if e.Term < n.cfg.Term {
				n.Info("lower term, reject vote")
			}
			n.Info("reject vote", zap.Uint64("from", e.From), zap.Uint32("term", e.Term), zap.Uint64("index", e.Index))
			n.sendVoteResp(e.From, true)
		}

	}
	return nil
}

func (n *Node) stepLeader(e Event) error {

	switch e.Type {
	case Propose: // 提案
	case SyncReq: // 同步
	}

	return nil
}

func (n *Node) stepFollower(e Event) error {

	return nil
}

func (n *Node) stepCandidate(e Event) error {

	return nil
}

// 是否可以投票
func (n *Node) canVote(e Event) bool {

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
