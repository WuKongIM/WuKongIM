package reactor

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"go.uber.org/zap"
)

// =================================== handler初始化 ===================================

func (r *Reactor) addInitReq(req *initReq) {
	select {
	case r.processInitC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *Reactor) processInitLoop() {
	for {
		select {
		case req := <-r.processInitC:
			r.processInit(req)

		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processInit(req *initReq) {

	configResp, err := r.request.GetConfig(ConfigReq{
		HandlerKey: req.h.key,
	})
	if err != nil {
		r.Error("get config failed", zap.Error(err))
		r.Step(req.h.key, replica.Message{
			MsgType: replica.MsgInitResp,
			Reject:  true,
		})
		return
	}
	if IsEmptyConfigResp(configResp) {
		r.Debug("config is empty")
		// 如果配置为空，也算初始化成功，但是不更新配置
		r.Step(req.h.key, replica.Message{
			MsgType: replica.MsgInitResp,
		})
		return
	}
	lastTerm, err := req.h.handler.LeaderLastTerm()
	if err != nil {
		r.Error("get leader last term failed", zap.Error(err))
		r.Step(req.h.key, replica.Message{
			MsgType: replica.MsgInitResp,
			Reject:  true,
		})
		return
	}
	req.h.setLastLeaderTerm(lastTerm) // 设置领导的最新任期

	r.Step(configResp.HandlerKey, replica.Message{
		MsgType: replica.MsgInitResp,
		Config:  configResp.Config,
	})
}

type initReq struct {
	h *handler
}

// =================================== 日志冲突检查 ===================================

func (r *Reactor) addConflictCheckReq(req *conflictCheckReq) {
	select {
	case r.processConflictCheckC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *Reactor) processConflictCheckLoop() {
	for {
		select {
		case req := <-r.processConflictCheckC:
			r.processConflictCheck(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processConflictCheck(req *conflictCheckReq) {

	if req.leaderLastTerm == 0 { // 本地没有任期，说明本地还没有日志
		r.Debug("local has no log,no conflict", zap.String("handlerKey", req.h.key))
		r.Step(req.h.key, replica.Message{
			MsgType: replica.MsgLogConflictCheckResp,
			Index:   replica.NoConflict,
		})
		return
	}
	// 如果MsgLeaderTermStartIndexReq的term等于领导的term则领导返回当前最新日志下标，否则返回MsgLeaderTermStartIndexReq里的term+1的 任期的第一条日志下标，返回的这个值称为LastOffset

	index, err := r.request.GetLeaderTermStartIndex(LeaderTermStartIndexReq{
		HandlerKey: req.h.key,
		LeaderId:   req.leaderId,
		Term:       req.leaderLastTerm,
	})
	if err != nil {
		r.Error("get leader term start index failed", zap.Error(err), zap.String("key", req.h.key), zap.Uint64("leaderId", req.leaderId), zap.Uint32("leaderLastTerm", req.leaderLastTerm))
		r.Step(req.h.key, replica.Message{
			MsgType: replica.MsgLogConflictCheckResp,
			Reject:  true,
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
			r.Step(req.h.key, replica.Message{
				MsgType: replica.MsgLogConflictCheckResp,
				Reject:  true,
			})
			return
		}
	}

	r.Debug("get leader term start index", zap.Uint32("leaderLastTerm", req.leaderLastTerm), zap.Uint64("index", index), zap.String("handlerKey", req.h.key))

	r.Step(req.h.key, replica.Message{
		MsgType: replica.MsgLogConflictCheckResp,
		Index:   index,
	})

	// handlerKeys := make([]string, 0)
	// for _, req := range reqs {
	// 	handlerKeys = append(handlerKeys, req.h.key)
	// }
	// resps, err := r.request.GetLeaderTermStartIndex(handlerKeys)
	// if err != nil {
	// 	r.Error("get leader term start index failed", zap.Error(err))
	// 	for _, handlerKey := range handlerKeys {
	// 		r.Step(handlerKey, replica.Message{
	// 			MsgType: replica.MsgLogConflictCheckResp,
	// 			Reject:  true,
	// 		})
	// 	}
	// 	return
	// }

	// for _, resp := range resps {
	// 	r.Step(resp.HandlerKey, replica.Message{
	// 		MsgType: replica.MsgLogConflictCheckResp,
	// 		Index:   resp.Index,
	// 	})

	// }
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

	appliedIndex, err := handler.handler.AppliedIndex()
	if err != nil {
		r.Error("get applied index failed", zap.Error(err))
		return 0, err
	}

	if truncateIndex >= appliedIndex {
		truncateIndex = appliedIndex + 1
	}

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
}

// =================================== 追加日志 ===================================

func (r *Reactor) addStoreAppendReq(req AppendLogReq) {

	select {
	case r.processStoreAppendC <- req:
	case <-r.stopper.ShouldStop():
		return
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

			r.processStoreAppend(reqs)
			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processStoreAppend(reqs []AppendLogReq) {

	err := r.request.AppendLogBatch(reqs)
	if err != nil {
		r.Error("append logs failed", zap.Error(err))
		for _, req := range reqs {
			r.Step(req.HandleKey, replica.Message{
				MsgType: replica.MsgStoreAppendResp,
				Reject:  true,
			})
		}
		return
	}

	for _, req := range reqs {
		for _, log := range req.Logs {
			handler := r.handler(req.HandleKey)
			if log.Term > handler.getLastLeaderTerm() {
				handler.setLastLeaderTerm(log.Term)
				err = handler.handler.SetLeaderTermStartIndex(log.Term, log.Index)
				if err != nil {
					r.Error("set leader term start index failed", zap.Error(err), zap.String("handlerKey", req.HandleKey), zap.Uint32("term", log.Term), zap.Uint64("index", log.Index))
				}
			}
		}
	}

	for _, req := range reqs {

		lastLog := req.Logs[len(req.Logs)-1]
		r.Step(req.HandleKey, replica.Message{
			MsgType: replica.MsgStoreAppendResp,
			Index:   lastLog.Index,
		})
	}

}

type storeAppendReq struct {
	h    *handler
	logs []replica.Log
}

// =================================== 获取日志 ===================================

func (r *Reactor) addGetLogReq(req *getLogReq) {
	select {
	case r.processGetLogC <- req:
	case <-r.stopper.ShouldStop():
		return
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

	logs, err := r.getAndMergeLogs(req)
	if err != nil {
		r.Error("get logs failed", zap.Error(err))
		r.Step(req.h.key, replica.Message{
			MsgType: replica.MsgSyncGetResp,
			Reject:  true,
		})
		return
	}

	if len(logs) > 0 && logs[0].Index != req.startIndex {
		r.Panic("get logs failed", zap.Uint64("startIndex", req.startIndex), zap.Uint64("lastIndex", req.lastIndex), zap.Uint64("msgIndex", logs[0].Index))
	}

	r.Step(req.h.key, replica.Message{
		MsgType: replica.MsgSyncGetResp,
		Logs:    logs,
		To:      req.to,
		Index:   req.startIndex,
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
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *Reactor) processApplyLogLoop() {
	for {
		select {
		case req := <-r.processApplyLogC:
			r.processApplyLog(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Reactor) processApplyLog(req *applyLogReq) {

	if !r.opts.IsCommittedAfterApplied {
		// 提交日志
		req.h.didCommit(req.appyingIndex+1, req.committedIndex+1)
	}

	appliedSize, err := req.h.handler.ApplyLogs(req.appyingIndex+1, req.committedIndex+1)
	if err != nil {
		r.Panic("apply logs failed", zap.Error(err))
		r.Step(req.h.key, replica.Message{
			MsgType: replica.MsgApplyLogsResp,
			Reject:  true,
		})
		return
	}

	if r.opts.IsCommittedAfterApplied {
		// 提交日志
		req.h.didCommit(req.appyingIndex+1, req.committedIndex+1)
	}

	r.Step(req.h.key, replica.Message{
		MsgType:     replica.MsgApplyLogsResp,
		Index:       req.committedIndex,
		AppliedSize: appliedSize,
	})

}

type applyLogReq struct {
	h              *handler
	appyingIndex   uint64
	committedIndex uint64
}

// =================================== 学习者转追随者 ===================================

func (r *Reactor) addLearnerToFollowerReq(req *learnerToFollowerReq) {
	select {
	case r.processLearnerToFollowerC <- req:
	case <-r.stopper.ShouldStop():
		return
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
	case <-r.stopper.ShouldStop():
		return
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
	err := req.h.learnerToLeader(req.learnerId)
	if err != nil {
		r.Error("learner to leader failed", zap.Error(err))
	}
}

type learnerToLeaderReq struct {
	h         *handler
	learnerId uint64
}
