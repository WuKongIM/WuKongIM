package reactor

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"go.uber.org/zap"
)

// =================================== handler初始化 ===================================

func (r *Reactor) addInitReq(req *initReq) {
	select {
	case r.processInitC <- req:
	default:
		r.Warn("processInitC is full, ignore", zap.String("key", req.h.key))
		req.sub.step(req.h.key, replica.Message{
			MsgType: replica.MsgInitResp,
			Reject:  true,
		})

	}
}

func (r *Reactor) processInitLoop() {
	batchSize := 1024
	reqs := make([]*initReq, 0, batchSize)
	done := false
	for {
		select {
		case req := <-r.processInitC:
			reqs = append(reqs, req)

			for !done {
				select {
				case req := <-r.processInitC:
					reqs = append(reqs, req)
					if len(reqs) >= batchSize {
						done = true
					}
				default:
					done = true
				}
			}
			r.processInits(reqs)
			reqs = reqs[:0]
			done = false

		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processInits(reqs []*initReq) {

	var err error
	for _, req := range reqs {
		err = r.processGoPool.Submit(func(rq *initReq) func() {
			return func() {
				r.processInit(rq)
			}
		}(req))
		if err != nil {
			r.Error("processInit failed,submit error", zap.Error(err))
			req.sub.step(req.h.key, replica.Message{
				MsgType:   replica.MsgInitResp,
				Reject:    true,
				HandlerNo: req.h.no,
			})
		}
	}
}

func (r *Reactor) processInit(req *initReq) {

	configResp, err := r.request.GetConfig(ConfigReq{
		HandlerKey: req.h.key,
	})
	if err != nil {
		r.Error("get config failed", zap.Error(err), zap.String("key", req.h.key))
		req.sub.step(req.h.key, replica.Message{
			MsgType:   replica.MsgInitResp,
			Reject:    true,
			HandlerNo: req.h.no,
		})
		return
	}
	if IsEmptyConfigResp(configResp) {
		r.Debug("config is empty")
		// 如果配置为空，也算初始化成功，但是不更新配置
		req.sub.step(req.h.key, replica.Message{
			MsgType:   replica.MsgInitResp,
			HandlerNo: req.h.no,
		})
		return
	}
	lastTerm, err := req.h.handler.LeaderLastTerm()
	if err != nil {
		r.Error("get leader last term failed", zap.Error(err))
		req.sub.step(req.h.key, replica.Message{
			MsgType:   replica.MsgInitResp,
			Reject:    true,
			HandlerNo: req.h.no,
		})
		return
	}

	req.h.setLastLeaderTerm(lastTerm) // 设置领导的最新任期

	req.sub.step(configResp.HandlerKey, replica.Message{
		MsgType:   replica.MsgInitResp,
		Config:    configResp.Config,
		HandlerNo: req.h.no,
	})
}

type initReq struct {
	h   *handler
	sub *ReactorSub
}

// =================================== 日志冲突检查 ===================================

func (r *Reactor) addConflictCheckReq(req *conflictCheckReq) {
	select {
	case r.processConflictCheckC <- req:
	default:
		r.Warn("processConflictCheckC is full, ignore", zap.String("key", req.h.key))
		req.sub.step(req.h.key, replica.Message{
			MsgType:   replica.MsgLogConflictCheckResp,
			Reject:    true,
			HandlerNo: req.h.no,
		})

	}
}

func (r *Reactor) processConflictCheckLoop() {
	batchSize := 1024
	reqs := make([]*conflictCheckReq, 0, batchSize)
	done := false
	for {
		select {
		case req := <-r.processConflictCheckC:
			reqs = append(reqs, req)
			for !done {
				select {
				case req := <-r.processConflictCheckC:
					reqs = append(reqs, req)
					if len(reqs) >= batchSize {
						done = true
					}
				default:
					done = true
				}
			}
			r.processConflictChecks(reqs)
			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processConflictChecks(reqs []*conflictCheckReq) {

	var err error
	for _, req := range reqs {
		req := req
		err = r.processGoPool.Submit(func() {
			r.processConflictCheck(req)
		})
		if err != nil {
			r.Error("processConflictChecks failed, submit error", zap.Error(err))
			req.sub.step(req.h.key, replica.Message{
				MsgType:   replica.MsgLogConflictCheckResp,
				Reject:    true,
				HandlerNo: req.h.no,
			})
		}
	}
}

func (r *Reactor) processConflictCheck(req *conflictCheckReq) {

	if req.leaderLastTerm == 0 { // 本地没有任期，说明本地还没有日志
		r.Debug("local has no log,no conflict", zap.String("handlerKey", req.h.key))
		req.sub.step(req.h.key, replica.Message{
			MsgType:   replica.MsgLogConflictCheckResp,
			Index:     replica.NoConflict,
			HandlerNo: req.h.no,
		})
		return
	}
	// 如果MsgLeaderTermStartIndexReq的term等于领导的term则领导返回当前最新日志下标，
	// 否则返回MsgLeaderTermStartIndexReq里的term+1的 任期的第一条日志下标，返回的这个值称为LastOffset
	index, err := r.request.GetLeaderTermStartIndex(LeaderTermStartIndexReq{
		HandlerKey: req.h.key,
		LeaderId:   req.leaderId,
		Term:       req.leaderLastTerm,
	})
	if err != nil {
		r.Error("get leader term start index failed", zap.Error(err), zap.String("key", req.h.key), zap.Uint64("leaderId", req.leaderId), zap.Uint32("leaderLastTerm", req.leaderLastTerm))
		req.sub.step(req.h.key, replica.Message{
			MsgType:   replica.MsgLogConflictCheckResp,
			Reject:    true,
			HandlerNo: req.h.no,
		})
		return
	}

	if index == 0 {
		r.Debug("leader index is 0,no conflict", zap.String("handlerKey", req.h.key))
		index = replica.NoConflict
	} else {
		index, err = r.handleLeaderTermStartIndexResp(req.h, index, req.leaderLastTerm)
		if err != nil {
			r.Error("handle leader term start index failed", zap.Error(err))
			req.sub.step(req.h.key, replica.Message{
				MsgType:   replica.MsgLogConflictCheckResp,
				Reject:    true,
				HandlerNo: req.h.no,
			})
			return
		}
	}

	r.Debug("get leader term start index", zap.Uint32("leaderLastTerm", req.leaderLastTerm), zap.Uint64("index", index), zap.String("handlerKey", req.h.key))

	req.sub.step(req.h.key, replica.Message{
		MsgType:   replica.MsgLogConflictCheckResp,
		Index:     index,
		HandlerNo: req.h.no,
	})
}

// Follower检查本地的LeaderTermSequence
// 是否有term对应的StartOffset大于领导返回的LastOffset，
// 如果有则将当前term的startOffset设置为LastOffset，
// 并且当前term为最新的term（也就是删除比当前term大的LeaderTermSequence的记录）
func (r *Reactor) handleLeaderTermStartIndexResp(handler *handler, index uint64, term uint32) (uint64, error) {
	if index == 0 {
		return 0, nil
	}
	termStartIndex, err := handler.handler.LeaderTermStartIndex(term)
	if err != nil {
		r.Error("leader term start index not found", zap.Uint32("term", term))
		return 0, err
	}
	if termStartIndex == 0 {
		err := handler.handler.SetLeaderTermStartIndex(term, index)
		if err != nil {
			r.Error("set leader term start index failed", zap.Error(err))
			return 0, err
		}
	} else if termStartIndex > index {
		err := handler.handler.SetLeaderTermStartIndex(term, index)
		if err != nil {
			r.Error("set leader term start index failed", zap.Error(err))
			return 0, err
		}
		handler.setLastLeaderTerm(term)

		err = handler.handler.DeleteLeaderTermStartIndexGreaterThanTerm(term)
		if err != nil {
			r.Error("delete leader term start index failed", zap.Error(err))
			return 0, err
		}
	}

	truncateIndex := index

	// appliedIndex, err := handler.handler.AppliedIndex()
	// if err != nil {
	// 	r.Error("get applied index failed", zap.Error(err))
	// 	return 0, err
	// }

	// TODO: 这里不能 truncateIndex = appliedIndex + 1，因为频道没有应用日志的概念，频道返回的appliedIndex是0
	// if truncateIndex >= appliedIndex {
	// 	truncateIndex = appliedIndex + 1
	// }

	err = handler.handler.TruncateLogTo(truncateIndex)
	if err != nil {
		r.Error("truncate log failed", zap.Error(err), zap.String("handlerKey", handler.key), zap.Uint64("index", index))
		return 0, err
	}
	return truncateIndex, nil
}

type conflictCheckReq struct {
	h              *handler
	leaderId       uint64
	leaderLastTerm uint32
	sub            *ReactorSub
}

// =================================== 追加日志 ===================================

func (r *Reactor) addStoreAppendReq(req AppendLogReq) {

	select {
	case r.processStoreAppendC <- req:
	default:
		r.Warn("processStoreAppendC is full, ingore", zap.String("key", req.HandleKey))
		req.sub.step(req.HandleKey, replica.Message{
			MsgType:   replica.MsgStoreAppendResp,
			Reject:    true,
			HandlerNo: req.handler.no,
		})
	}
}

func (r *Reactor) processStoreAppendLoop() {
	reqs := make([]AppendLogReq, 0)
	done := false
	for {
		select {
		case req := <-r.processStoreAppendC:
			reqs = append(reqs, req)
			for !done {
				select {
				case req := <-r.processStoreAppendC:
					reqs = append(reqs, req)
				default:
					done = true
				}
			}
			r.processStoreAppends(reqs)
			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processStoreAppends(reqs []AppendLogReq) {

	var err error
	for _, req := range reqs {
		req := req
		err = r.processGoPool.Submit(func(rq AppendLogReq) func() {
			return func() {
				r.processStoreAppend(rq)
			}
		}(req))
		if err != nil {
			r.Error("processStoreAppend failed, submit error")
			req.sub.step(req.HandleKey, replica.Message{
				MsgType:   replica.MsgStoreAppendResp,
				Reject:    true,
				HandlerNo: req.handler.no,
			})
		}
	}
}

func (r *Reactor) processStoreAppend(req AppendLogReq) {

	err := r.request.Append(req)
	if err != nil {
		r.Error("append logs failed", zap.Error(err))
		req.sub.step(req.HandleKey, replica.Message{
			MsgType:   replica.MsgStoreAppendResp,
			Reject:    true,
			HandlerNo: req.handler.no,
		})
		return
	}

	startLogIndex := req.Logs[0].Index
	endLogIndex := req.Logs[len(req.Logs)-1].Index

	req.handler.proposeWait.didAppend(startLogIndex, endLogIndex+1)

	for _, log := range req.Logs {
		if log.Term > req.handler.getLastLeaderTerm() {
			req.handler.setLastLeaderTerm(log.Term)
			err = req.handler.handler.SetLeaderTermStartIndex(log.Term, log.Index)
			if err != nil {
				r.Error("set leader term start index failed", zap.Error(err), zap.String("handlerKey", req.HandleKey), zap.Uint32("term", log.Term), zap.Uint64("index", log.Index))
			}
		}
	}
	lastLog := req.Logs[len(req.Logs)-1]
	req.sub.step(req.HandleKey, replica.Message{
		MsgType:   replica.MsgStoreAppendResp,
		Index:     lastLog.Index,
		HandlerNo: req.handler.no,
	})
}

// =================================== 获取日志 ===================================

func (r *Reactor) addGetLogReq(req *getLogReq) {
	select {
	case r.processGetLogC <- req:
	default:
		r.Warn("processGetLogC is full, ignore", zap.String("key", req.h.key))
		req.sub.step(req.h.key, replica.Message{
			MsgType:   replica.MsgSyncGetResp,
			Reject:    true,
			HandlerNo: req.h.no,
		})
	}
}

func (r *Reactor) processGetLogLoop() {
	for {
		select {
		case req := <-r.processGetLogC:
			r.processGetLog(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processGetLog(req *getLogReq) {
	err := r.processGoPool.Submit(func() {
		r.handleGetLog(req)
	})
	if err != nil {
		r.Error("processGetLog failed,submit error", zap.Error(err))
		req.sub.step(req.h.key, replica.Message{
			MsgType:   replica.MsgSyncGetResp,
			Reject:    true,
			HandlerNo: req.h.no,
		})
	}
}

func (r *Reactor) handleGetLog(req *getLogReq) {

	logs, err := r.getAndMergeLogs(req)
	if err != nil {
		r.Error("get logs failed", zap.Error(err))
		r.Step(req.h.key, replica.Message{
			MsgType:   replica.MsgSyncGetResp,
			Reject:    true,
			HandlerNo: req.h.no,
		})
		return
	}

	if len(logs) > 0 && logs[0].Index != req.startIndex {
		r.Panic("get logs failed", zap.Uint64("startIndex", req.startIndex), zap.Uint64("lastIndex", req.lastIndex), zap.Uint64("msgIndex", logs[0].Index))
	}

	req.sub.step(req.h.key, replica.Message{
		MsgType:   replica.MsgSyncGetResp,
		Logs:      logs,
		To:        req.to,
		Index:     req.startIndex,
		HandlerNo: req.h.no,
	})
}

// GetAndMergeLogs 获取并合并日志
func (r *Reactor) getAndMergeLogs(req *getLogReq) ([]replica.Log, error) {

	unstableLogs := req.logs
	startIndex := req.startIndex
	if len(unstableLogs) > 0 {
		startIndex = unstableLogs[len(unstableLogs)-1].Index + 1
	}

	var resultLogs []replica.Log
	if startIndex <= req.lastIndex {
		logs, err := req.h.handler.GetLogs(startIndex, req.lastIndex+1)
		if err != nil {
			r.Error("get logs error", zap.Error(err), zap.Uint64("startIndex", startIndex), zap.Uint64("lastIndex", req.lastIndex))
			return nil, err
		}

		startLogLen := len(logs)
		// 检查logs的连续性，只保留连续的日志
		for i, log := range logs {
			if log.Index != startIndex+uint64(i) {
				logs = logs[:i]
				break
			}
		}
		if len(logs) != startLogLen {
			r.Warn("the log is not continuous and has been truncated ", zap.Uint64("lastIndex", req.lastIndex), zap.Uint64("msgIndex", req.startIndex), zap.Int("startLogLen", startLogLen), zap.Int("endLogLen", len(logs)))
		}

		resultLogs = extend(unstableLogs, logs)
	} else {
		resultLogs = unstableLogs
	}

	return resultLogs, nil
}

type getLogReq struct {
	h          *handler
	startIndex uint64
	lastIndex  uint64
	logs       []replica.Log
	to         uint64
	sub        *ReactorSub
}

func extend(dst, vals []replica.Log) []replica.Log {
	need := len(dst) + len(vals)
	if need <= cap(dst) {
		return append(dst, vals...) // does not allocate
	}
	buf := make([]replica.Log, need) // allocates precisely what's needed
	copy(buf, dst)
	copy(buf[len(dst):], vals)
	return buf
}

// =================================== 应用日志 ===================================

func (r *Reactor) addApplyLogReq(req *applyLogReq) {
	select {
	case r.processApplyLogC <- req:
	default:
		r.Warn("processApplyLogC is full,ignore")
		req.sub.step(req.h.key, replica.Message{
			MsgType:   replica.MsgApplyLogsResp,
			Reject:    true,
			HandlerNo: req.h.no,
		})

	}
}

func (r *Reactor) processApplyLogLoop() {
	reqs := make([]*applyLogReq, 0)
	done := false
	for {
		select {
		case req := <-r.processApplyLogC:
			reqs = append(reqs, req)
			for !done {
				select {
				case req := <-r.processApplyLogC:
					reqs = append(reqs, req)
				default:
					done = true
				}
			}
			r.processApplyLogs(reqs)
			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processApplyLogs(reqs []*applyLogReq) {

	var err error
	for _, req := range reqs {
		err = r.processGoPool.Submit(func(rq *applyLogReq) func() {
			return func() {
				r.processApplyLog(rq)
			}
		}(req))
		if err != nil {
			r.Error("processApplyLogs failed, submit error", zap.Error(err))
			req.sub.step(req.h.key, replica.Message{
				MsgType:   replica.MsgApplyLogsResp,
				Reject:    true,
				HandlerNo: req.h.no,
			})
		}
	}

}

func (r *Reactor) processApplyLog(req *applyLogReq) {

	if !r.opts.IsCommittedAfterApplied {
		// 提交日志
		if req.leaderId == r.opts.NodeId {
			req.h.didCommit(req.appliedIndex+1, req.committedIndex+1)
		}

	}

	appliedSize, err := req.h.handler.ApplyLogs(req.appliedIndex+1, req.committedIndex+1)
	if err != nil {
		r.Panic("apply logs failed", zap.Error(err))
		req.sub.step(req.h.key, replica.Message{
			MsgType:   replica.MsgApplyLogsResp,
			Reject:    true,
			HandlerNo: req.h.no,
		})
		return
	}

	if r.opts.IsCommittedAfterApplied {
		// 提交日志
		if req.leaderId == r.opts.NodeId {
			req.h.didCommit(req.appliedIndex+1, req.committedIndex+1)
		}
	}

	req.sub.step(req.h.key, replica.Message{
		MsgType:     replica.MsgApplyLogsResp,
		Index:       req.committedIndex,
		AppliedSize: appliedSize,
		HandlerNo:   req.h.no,
	})
}

type applyLogReq struct {
	h              *handler
	appliedIndex   uint64
	committedIndex uint64
	sub            *ReactorSub
	leaderId       uint64
}

// =================================== 学习者转追随者 ===================================

func (r *Reactor) addLearnerToFollowerReq(req *learnerToFollowerReq) {
	select {
	case r.processLearnerToFollowerC <- req:
	default:
		r.Warn("processLearnerToFollowerC is full, ignore")
	}
}

func (r *Reactor) processLearnerToFollowerLoop() {
	for {
		select {
		case req := <-r.processLearnerToFollowerC:
			r.processLearnerToFollower(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processLearnerToFollower(req *learnerToFollowerReq) {
	err := r.processGoPool.Submit(func() {
		r.handleLearnerToFollower(req)
	})
	if err != nil {
		r.Error("processLearnerToFollower failed", zap.Error(err), zap.String("key", req.h.key))
	}
}

func (r *Reactor) handleLearnerToFollower(req *learnerToFollowerReq) {
	err := req.h.learnerToFollower(req.learnerId)
	if err != nil {
		r.Error("learner to follower failed", zap.Error(err))
	}
}

type learnerToFollowerReq struct {
	h         *handler
	learnerId uint64
}

// =================================== 学习者转领导者 ===================================

func (r *Reactor) addLearnerToLeaderReq(req *learnerToLeaderReq) {
	select {
	case r.processLearnerToLeaderC <- req:
	default:
		r.Warn("processLearnerToLeaderC is full, ignore")
	}
}

func (r *Reactor) processLearnerToLeaderLoop() {
	for {
		select {
		case req := <-r.processLearnerToLeaderC:
			r.processLearnerToLeader(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processLearnerToLeader(req *learnerToLeaderReq) {
	err := r.processGoPool.Submit(func() {
		r.handleLearnerToLeader(req)
	})
	if err != nil {
		r.Error("processLearnerToLeader failed, submit error")
	}
}

func (r *Reactor) handleLearnerToLeader(req *learnerToLeaderReq) {
	err := req.h.learnerToLeader(req.learnerId)
	if err != nil {
		r.Error("learner to leader failed", zap.Error(err))
	}
}

type learnerToLeaderReq struct {
	h         *handler
	learnerId uint64
}

// =================================== 追随者转领导者 ===================================

func (r *Reactor) addFollowerToLeaderReq(req *followerToLeaderReq) {
	select {
	case r.processFollowerToLeaderC <- req:
	default:
		r.Warn("processFollowerToLeaderC is full, ignore")
	}
}

func (r *Reactor) processFollowerToLeaderLoop() {
	for {
		select {
		case req := <-r.processFollowerToLeaderC:
			r.processFollowerToLeader(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processFollowerToLeader(req *followerToLeaderReq) {
	err := r.processGoPool.Submit(func() {
		r.handleFollowerToLeader(req)
	})
	if err != nil {
		r.Error("processFollowerToLeader failed", zap.Error(err), zap.String("key", req.h.key))
	}
}

func (r *Reactor) handleFollowerToLeader(req *followerToLeaderReq) {
	err := req.h.followerToLeader(req.followerId)
	if err != nil {
		r.Error("follower to leader failed", zap.Error(err))
	}
}

type followerToLeaderReq struct {
	h          *handler
	followerId uint64
}
