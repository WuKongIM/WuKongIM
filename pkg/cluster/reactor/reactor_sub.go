package reactor

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type ReactorSub struct {
	stopper  *syncutil.Stopper
	opts     *Options
	handlers *handlerList // 当前处理者集合

	needRemoveKeys []string // 需要移除的处理者key

	wklog.Log

	tmpHandlers []*handler

	avdanceC chan struct{}
	stepC    chan stepReq
	proposeC chan proposeReq
	mr       *Reactor
	stopped  atomic.Bool
}

func NewReactorSub(index int, mr *Reactor) *ReactorSub {
	return &ReactorSub{
		mr:          mr,
		stopper:     syncutil.NewStopper(),
		opts:        mr.opts,
		handlers:    newHandlerList(),
		Log:         wklog.NewWKLog(fmt.Sprintf("ReactorSub[%s:%d:%d]", mr.opts.ReactorType.String(), mr.opts.NodeId, index)),
		tmpHandlers: make([]*handler, 0, 10000),
		avdanceC:    make(chan struct{}, 1),
		stepC:       make(chan stepReq, 1024),
		proposeC:    make(chan proposeReq, 1024),
	}
}

func (r *ReactorSub) Start() error {
	r.stopper.RunWorker(r.run)

	return nil
}

func (r *ReactorSub) Stop() {
	r.stopped.Store(true)
	r.stopper.Stop()
}

func (r *ReactorSub) run() {

	var (
		tick = time.NewTicker(r.opts.TickInterval)
	)

	for {
		r.readyEvents()
		select {
		case <-tick.C:
			r.tick()
		case req := <-r.stepC:
			handler := r.handlers.get(req.handlerKey)
			if handler == nil {
				r.Info("ReactorSub: step handler not exist", zap.String("handlerKey", req.handlerKey))
				continue
			}
			err := handler.handler.Step(req.msg)
			if err != nil {
				r.Error("step message failed", zap.Error(err))
			}
		case req := <-r.proposeC:
			handler := r.handlers.get(req.handlerKey)
			if handler == nil {
				r.Info("ReactorSub: propose handler not exist", zap.String("handlerKey", req.handlerKey))
				continue
			}
			lastLogIndex, term := handler.lastLogIndexAndTerm()
			for i := 0; i < len(req.logs); i++ {
				lg := req.logs[i]
				lg.Index = lastLogIndex + 1 + uint64(i)
				lg.Term = term
				req.logs[i] = lg
				handler.didPropose(req.waitKey, lg.Id, lg.Index)
			}
			err := handler.handler.Step(replica.NewProposeMessageWithLogs(r.opts.NodeId, term, req.logs))
			if err != nil {
				r.Error("step propose message failed", zap.Error(err))
			}

		// case handler := <-r.storeAppendRespC:
		// 	err := handler.handler.Step(replica.NewMsgStoreAppendResp(r.opts.NodeId, handler.lastIndex.Load()))
		// 	if err != nil {
		// 		r.Error("step local store message failed", zap.Error(err))
		// 	}

		case <-r.avdanceC:
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *ReactorSub) proposeAndWait(ctx context.Context, handleKey string, logs []replica.Log) ([]ProposeResult, error) {
	if r.stopped.Load() {
		return nil, ErrReactorSubStopped
	}
	// -------------------- 延迟统计 --------------------
	startTime := time.Now()
	defer func() {
		end := time.Since(startTime)
		switch r.opts.ReactorType {
		case ReactorTypeSlot:
			trace.GlobalTrace.Metrics.Cluster().ProposeLatencyAdd(trace.ClusterKindSlot, end.Milliseconds())
		case ReactorTypeChannel:
			trace.GlobalTrace.Metrics.Cluster().ProposeLatencyAdd(trace.ClusterKindChannel, end.Milliseconds())
		}
		if r.opts.EnableLazyCatchUp {
			if end > 0 {
				r.Info("proposeAndWait", zap.Int64("cost", end.Milliseconds()), zap.Int("logs", len(logs)))
			}
		}
	}()
	// -------------------- 初始化提案数据 --------------------
	handler := r.handlers.get(handleKey)
	if handler == nil {
		return nil, nil
	}
	handler.resetProposeIntervalTick() // 重置提案间隔tick

	if handler.pausePropopose() {
		return nil, ErrPausePropopose
	}

	if !handler.isLeader() {
		r.Error("not leader", zap.String("handler", handler.key), zap.Uint64("leader", handler.leaderId()))
		return nil, ErrNotLeader
	}

	ids := make([]uint64, 0, len(logs))
	for _, log := range logs {
		ids = append(ids, log.Id)
	}
	waitKey := strconv.FormatUint(ids[len(ids)-1], 10)

	timeoutCtx, cancel := context.WithTimeout(ctx, r.opts.ProposeTimeout)
	defer cancel()

	// -------------------- 获得等待提交提案的句柄 --------------------
	waitC := handler.addWait(ctx, waitKey, ids)

	// -------------------- 添加提案请求 --------------------
	req := newProposeReq(handleKey, waitKey, logs)
	select {
	case r.proposeC <- req:
	case <-timeoutCtx.Done():
		handler.removeWait(waitKey)
		trace.GlobalTrace.Metrics.Cluster().ProposeFailedCountAdd(trace.ClusterKindChannel, 1)
		return nil, timeoutCtx.Err()
	case <-r.stopper.ShouldStop():
		handler.removeWait(waitKey)
		trace.GlobalTrace.Metrics.Cluster().ProposeFailedCountAdd(trace.ClusterKindChannel, 1)
		return nil, ErrReactorSubStopped
	}

	// -------------------- 等待提案结果 --------------------
	select {
	case items := <-waitC:
		return items, nil
	case <-timeoutCtx.Done():
		handler.removeWait(waitKey)
		trace.GlobalTrace.Metrics.Cluster().ProposeFailedCountAdd(trace.ClusterKindChannel, 1)
		return nil, timeoutCtx.Err()
	case <-r.stopper.ShouldStop():
		handler.removeWait(waitKey)
		trace.GlobalTrace.Metrics.Cluster().ProposeFailedCountAdd(trace.ClusterKindChannel, 1)
		return nil, ErrReactorSubStopped
	}

}

// func (r *ReactorSub) propose(handleKey string, logs []replica.Log) error {
// 	// -------------------- 初始化提案数据 --------------------
// 	handler := r.handlers.get(handleKey)
// 	if handler == nil {
// 		return nil
// 	}
// 	ids := make([]uint64, 0, len(logs))
// 	for _, log := range logs {
// 		ids = append(ids, log.Id)
// 	}
// 	key := strconv.FormatUint(ids[len(ids)-1], 10)

// 	// -------------------- 添加提案请求 --------------------
// 	req := newProposeReq(key, logs)
// 	handler.addPropose(req)
// 	r.advance()
// 	return nil
// }

func (r *ReactorSub) step(handlerKey string, msg replica.Message) {
	r.stepC <- stepReq{
		handlerKey: handlerKey,
		msg:        msg,
	}
}

func (r *ReactorSub) advance() {
	select {
	case r.avdanceC <- struct{}{}:
	default:
	}
}

func (r *ReactorSub) readyEvents() {
	hasEvent := true

	for hasEvent && !r.stopped.Load() {
		hasEvent = false

		r.handlers.iterator(func(h *handler) bool {
			has := r.handleReady(h)
			if has {
				hasEvent = true
			}
			return true
		})
	}
}

// func (r *ReactorSub) readyEvents() {

// 	r.handlers.readHandlers(&r.tmpHandlers)

// 	hasEvent := true
// 	var err error

// 	for hasEvent {
// 		if r.stopped.Load() {
// 			return
// 		}
// 		hasEvent = false
// 		startTime := time.Now()
// 		for _, handler := range r.tmpHandlers {
// 			if !handler.isPrepared() {
// 				continue
// 			}
// 			has := r.handleReady(handler)
// 			if has {
// 				hasEvent = true
// 			}
// 			has, err = r.handleEvent(handler)
// 			if err != nil {
// 				r.Error("handle event failed", zap.Error(err))
// 			}
// 			if has {
// 				hasEvent = true
// 			}
// 		}
// 		end := time.Since(startTime)
// 		if end > time.Second*1 {
// 			r.Info("handle event", zap.Duration("cost", end), zap.Int("handlers", len(r.tmpHandlers)))
// 		}
// 	}

// 	r.tmpHandlers = r.tmpHandlers[:0]
// }

func (r *ReactorSub) handleReady(handler *handler) bool {
	if !handler.hasReady() {
		return false
	}
	rd := handler.ready()
	if replica.IsEmptyReady(rd) {
		return false
	}

	if !replica.IsEmptyHardState(rd.HardState) {
		handler.setHardState(rd.HardState)
	}

	// if !replica.IsEmptyHardState(rd.HardState) {
	// 	handler.resetSync() // 领导发生改变 重置同步状态
	// 	err := r.mr.submitTask(func() {
	// 		handler.setHardState(rd.HardState)
	// 	})
	// 	if err != nil {
	// 		r.Error("submit set hard state task failed", zap.Error(err))
	// 	}

	// }

	for _, m := range rd.Messages {

		// fmt.Println("m---------->", m.MsgType.String())

		if m.To == r.opts.NodeId { // 处理本地节点消息
			if m.MsgType == replica.MsgVoteResp {
				_ = handler.handler.Step(m)
			}
		}

		// 		// 同步速度处理
		if r.opts.AutoSlowDownOn {
			// 如果收到了同步日志的消息，速度重置（提速）
			if m.MsgType == replica.MsgSyncResp && len(m.Logs) > 0 { // 还有副本同步到日志，不降速
				handler.resetSlowDown()
			}
		}

		switch m.MsgType {
		case replica.MsgInit: // 初始化
			r.mr.addInitReq(&initReq{
				h: handler,
			})
		case replica.MsgLogConflictCheck: // 日志冲突检查
			r.mr.addConflictCheckReq(&conflictCheckReq{
				h:              handler,
				leaderLastTerm: handler.getLastLeaderTerm(),
				leaderId:       handler.leaderId(),
			})
		case replica.MsgStoreAppend: // 追加日志
			r.mr.addStoreAppendReq(&storeAppendReq{
				h:    handler,
				logs: m.Logs,
			})
		case replica.MsgSyncGet: // 获取日志
			lastIndex, _ := handler.handler.LastLogIndexAndTerm()
			r.mr.addGetLogReq(&getLogReq{
				h:          handler,
				startIndex: m.Index,
				lastIndex:  lastIndex,
				logs:       m.Logs,
				to:         m.From,
			})
		case replica.MsgApplyLogs: // 应用日志
			r.mr.addApplyLogReq(&applyLogReq{
				h:              handler,
				logs:           m.Logs,
				appyingIndex:   m.ApplyingIndex,
				committedIndex: m.CommittedIndex,
			})
		case replica.MsgLearnerToFollower: // 学习者转追随者
			r.mr.addLearnerToFollowerReq(&learnerToFollowerReq{
				h:         handler,
				learnerId: m.LearnerId,
			})
		case replica.MsgLearnerToLeader: // 学习者转领导者
			r.mr.addLearnerToLeaderReq(&learnerToLeaderReq{
				h:         handler,
				learnerId: m.LearnerId,
			})
		case replica.MsgSpeedLevelChange:
			fmt.Println("MsgSpeedLevelChange---------------->", handler.key, m.SpeedLevel.String())

		case replica.MsgSyncTimeout:
			fmt.Println("MsgSyncTimeout---------------->", handler.key)

		default:
			if m.To != 0 && m.To != r.opts.NodeId {
				// 发送消息
				r.opts.Send(Message{
					HandlerKey: handler.key,
					Message:    m,
				})
			}
		}
	}

	return true
}

// func (r *ReactorSub) handleReady(handler *handler) bool {
// 	if !handler.hasReady() {
// 		return false
// 	}
// 	rd := handler.ready()
// 	if replica.IsEmptyReady(rd) {
// 		return false
// 	}

// 	if !replica.IsEmptyHardState(rd.HardState) {
// 		handler.resetSync() // 领导发生改变 重置同步状态
// 		err := r.mr.submitTask(func() {
// 			handler.setHardState(rd.HardState)
// 		})
// 		if err != nil {
// 			r.Error("submit set hard state task failed", zap.Error(err))
// 		}

// 	}

// 	for _, m := range rd.Messages {
// 		if m.To == r.opts.NodeId { // 处理本地节点消息
// 			r.handleLocalMsg(handler, m)
// 			continue
// 		}
// 		if m.To == 0 {
// 			r.Error("msg.To is 0", zap.String("msg", m.MsgType.String()))
// 			continue
// 		}

// 		// 日志同步请求
// 		if m.MsgType == replica.MsgSyncReq {
// 			switch handler.sync.syncStatus {
// 			case syncStatusNone:
// 				handler.sync.startSyncTime = time.Now()
// 				handler.sync.syncingLogIndex = m.Index
// 				handler.sync.syncStatus = syncStatusSyncing
// 			case syncStatusSyncing:
// 				if m.Index > handler.sync.syncingLogIndex || time.Since(handler.sync.startSyncTime) > handler.sync.syncTimeout {
// 					handler.sync.syncingLogIndex = m.Index
// 					handler.sync.startSyncTime = time.Now()
// 					// r.Warn("sync timeout...", zap.Uint64("index", m.Index), zap.Uint64("to", m.To))
// 				} else {
// 					// r.Debug("syncing...", zap.Uint64("index", m.Index))
// 					continue
// 				}
// 			case syncStatusSynced:
// 				r.Warn("synced...", zap.Uint64("index", m.Index))
// 				continue
// 			}
// 		}

// 		// 同步速度处理
// 		if r.opts.AutoSlowDownOn {
// 			// 如果收到了同步日志的消息，速度重置（提速）
// 			if m.MsgType == replica.MsgSyncResp && len(m.Logs) > 0 { // 还有副本同步到日志，不降速
// 				handler.resetSlowDown()
// 			}
// 		}

// 		// 发送消息
// 		r.opts.Send(Message{
// 			HandlerKey: handler.key,
// 			Message:    m,
// 		})
// 	}
// 	return true
// }

// func (r *ReactorSub) handleEvent(handler *handler) (bool, error) {
// 	var (
// 		hasEvent = false
// 		start    time.Time
// 		end      time.Duration
// 		err      error
// 		handleOk bool
// 	)

// 	// -------------------- 处理提案请求 --------------------
// 	if r.opts.EnableLazyCatchUp {
// 		start = time.Now()
// 	}
// 	handleOk, err = r.handleProposes(handler)
// 	if r.opts.EnableLazyCatchUp {
// 		end = time.Since(start)
// 		if end > time.Millisecond*1 {
// 			r.Info("handleProposes", zap.Duration("time", end))
// 		}
// 	}
// 	if err != nil {
// 		return false, err
// 	}
// 	if handleOk {
// 		hasEvent = true
// 	}

// 	// // -------------------- 处理追加日志任务结果 --------------------
// 	// if r.opts.EnableLazyCatchUp {
// 	// 	start = time.Now()
// 	// }
// 	// handleOk, err = r.handleAppendLogTask(handler)
// 	// if r.opts.EnableLazyCatchUp {
// 	// 	end = time.Since(start)
// 	// 	if end > time.Millisecond*1 {
// 	// 		r.Info("handleAppendLogTask", zap.Duration("time", end))
// 	// 	}
// 	// }

// 	// if err != nil {
// 	// 	return false, err
// 	// }
// 	// if handleOk {
// 	// 	hasEvent = true
// 	// }

// 	// -------------------- 处理收到的消息 --------------------
// 	if r.opts.EnableLazyCatchUp {
// 		start = time.Now()
// 	}
// 	handleOk = r.handleRecvMessages(handler)
// 	if r.opts.EnableLazyCatchUp {
// 		end = time.Since(start)
// 		if end > time.Millisecond*20 {
// 			r.Info("handleRecvMessages", zap.Duration("time", end))
// 		}
// 	}
// 	if handleOk {
// 		hasEvent = true
// 	}

// 	// -------------------- 处理同步日志任务结果 --------------------
// 	if r.opts.EnableLazyCatchUp {
// 		start = time.Now()
// 	}

// 	handleOk, err = r.handleSyncTask(handler)
// 	if r.opts.EnableLazyCatchUp {
// 		end = time.Since(start)
// 		if end > time.Millisecond*1 {
// 			r.Info("handleSyncTask", zap.Duration("time", end))
// 		}
// 	}
// 	if err != nil {
// 		return false, err
// 	}
// 	if handleOk {
// 		hasEvent = true
// 	}

// 	// -------------------- 处理获取日志任务结果 --------------------
// 	if r.opts.EnableLazyCatchUp {
// 		start = time.Now()
// 	}
// 	handleOk, err = r.handleGetLogTask(handler)
// 	if r.opts.EnableLazyCatchUp {
// 		end = time.Since(start)
// 		if end > time.Millisecond*1 {
// 			r.Info("handleGetLogTask", zap.Duration("time", end))
// 		}
// 	}

// 	if err != nil {
// 		return false, err
// 	}
// 	if handleOk {
// 		hasEvent = true
// 	}

// 	// -------------------- 处理应用日志任务结果 --------------------
// 	if r.opts.EnableLazyCatchUp {
// 		start = time.Now()
// 	}
// 	handleOk, err = r.handleApplyLogTask(handler)
// 	if r.opts.EnableLazyCatchUp {
// 		end = time.Since(start)
// 		if end > time.Millisecond*1 {
// 			r.Info("handleApplyLogTask", zap.Duration("time", end))
// 		}
// 	}
// 	if err != nil {
// 		return false, err
// 	}
// 	if handleOk {
// 		hasEvent = true
// 	}
// 	return hasEvent, nil
// }

// func (r *ReactorSub) handleProposes(handler *handler) (bool, error) {
// 	var (
// 		ok              bool = true
// 		proposeReq      proposeReq
// 		proposeLogCount = 0 // 每批提案的日志数量
// 		hasEvent        bool
// 	)

// 	for ok {
// 		proposeReq, ok = handler.popPropose()
// 		if !ok {
// 			break
// 		}
// 		if len(proposeReq.logs) == 0 {
// 			continue
// 		}

// 		proposeLogCount += len(proposeReq.logs)
// 		lastLogIndex, term := handler.lastLogIndexAndTerm()
// 		for i := 0; i < len(proposeReq.logs); i++ {
// 			lg := proposeReq.logs[i]
// 			lg.Index = lastLogIndex + 1 + uint64(i)
// 			lg.Term = term
// 			proposeReq.logs[i] = lg
// 			handler.didPropose(proposeReq.key, lg.Id, lg.Index)
// 		}

// 		err := handler.step(replica.NewProposeMessageWithLogs(r.opts.NodeId, term, proposeReq.logs))
// 		if err != nil {
// 			r.Error("step propose log failed", zap.Error(err))
// 			return false, err
// 		}
// 		hasEvent = true
// 		if r.opts.MaxProposeLogCount > 0 && proposeLogCount > r.opts.MaxProposeLogCount {
// 			break
// 		}
// 	}
// 	return hasEvent, nil
// }

// func (r *ReactorSub) handleAppendLogTask(handler *handler) (bool, error) {
// 	firstTask := handler.firstAppendLogStoreTask()
// 	var (
// 		hasEvent bool
// 	)
// 	for firstTask != nil {
// 		if !firstTask.isTaskFinished() {
// 			break
// 		}
// 		if firstTask.hasErr() {
// 			r.Panic("append log store message failed", zap.Error(firstTask.err()))
// 			return false, firstTask.err()
// 		}
// 		err := handler.step(firstTask.resp())
// 		if err != nil {
// 			r.Panic("step local store message failed", zap.Error(err))
// 			return false, err
// 		}
// 		handler.lastIndex.Store(firstTask.resp().Index)

// 		err = r.mr.submitTask(func() {
// 			err = handler.setLastIndex()
// 			if err != nil {
// 				r.Panic("set last index failed", zap.Error(err))
// 			}
// 		})
// 		if err != nil {
// 			r.Error("submit set last index task failed", zap.Error(err))
// 		}

// 		hasEvent = true
// 		handler.removeFirstAppendLogStoreTask()
// 		firstTask = handler.firstAppendLogStoreTask()
// 	}
// 	return hasEvent, nil
// }

// func (r *ReactorSub) handleRecvMessages(handler *handler) bool {
// 	// 处理收到的消息
// 	msgs := handler.getMessages()
// 	for _, msg := range msgs {
// 		if msg.MsgType == replica.MsgSyncResp {
// 			handler.setMsgSyncResp(msg.Message)
// 			continue
// 		}
// 		err := handler.step(msg.Message)
// 		if err != nil {
// 			r.Warn("step error", zap.Error(err))
// 		}
// 	}
// 	return false
// }

// func (r *ReactorSub) handleSyncTask(handler *handler) (bool, error) {
// 	msgSyncResp, ok := handler.getAndResetMsgSyncResp()
// 	if ok {
// 		err := handler.step(msgSyncResp)
// 		if err != nil {
// 			r.Error("step sync message failed", zap.Error(err))
// 			return false, err
// 		}
// 		return true, nil
// 	}
// 	return false, nil
// }

// func (r *ReactorSub) handleGetLogTask(handler *handler) (bool, error) {
// 	var (
// 		err      error
// 		hasEvent bool
// 	)
// 	getLogsTasks := handler.allGetLogsTask()
// 	for _, getLogsTask := range getLogsTasks {
// 		if getLogsTask.isTaskFinished() {
// 			if getLogsTask.hasErr() {
// 				r.Error("get logs task error", zap.Error(getLogsTask.err()))
// 			} else {
// 				err = handler.step(getLogsTask.resp())
// 				if err != nil {
// 					r.Error("step get logs task failed", zap.Error(err))
// 				}
// 			}
// 			handler.removeGetLogsTask(getLogsTask.taskKey())
// 			hasEvent = true
// 		}
// 	}
// 	return hasEvent, nil
// }

// func (r *ReactorSub) handleApplyLogTask(handler *handler) (bool, error) {
// 	firstTask := handler.firstApplyLogStoreTask()
// 	var (
// 		hasEvent bool
// 	)
// 	for firstTask != nil {
// 		if !firstTask.isTaskFinished() {
// 			break
// 		}
// 		if firstTask.hasErr() {
// 			handler.Panic("apply log store message failed", zap.Error(firstTask.err()))
// 			return false, firstTask.err()
// 		}
// 		resp := firstTask.resp()
// 		err := handler.step(resp)
// 		if err != nil {
// 			handler.Panic("step apply store message failed", zap.Error(err))
// 			return false, err
// 		}
// 		handler.appliedIndex.Store(resp.AppliedIndex)
// 		err = r.mr.submitTask(func() {
// 			err = handler.setAppliedIndex()
// 			if err != nil {
// 				handler.Panic("set applied index failed", zap.Error(err))
// 			}
// 		})
// 		if err != nil {
// 			handler.Panic("submit set applied index task failed", zap.Error(err))
// 		}

// 		hasEvent = true
// 		handler.removeFirstApplyLogStoreTask()
// 		firstTask = handler.firstApplyLogStoreTask()
// 	}
// 	return hasEvent, nil
// }

func (r *ReactorSub) tick() {

	r.handlers.iterator(func(handler *handler) bool {
		handler.tick()

		if r.opts.AutoSlowDownOn {
			if handler.shouldSlowDown() {
				handler.slowDown()
			}

			if handler.speedLevel() == replica.LevelStop && handler.shouldDestroy() { // 如果速度将为停止并可销毁
				r.Debug("remove handler, speed stop", zap.String("handler", handler.key))
				r.needRemoveKeys = append(r.needRemoveKeys, handler.key)
			}
		}

		return true
	})
	if len(r.needRemoveKeys) > 0 {
		for _, key := range r.needRemoveKeys {
			r.handlers.remove(key)
		}
		r.needRemoveKeys = r.needRemoveKeys[:0]
	}
}

// func (r *ReactorSub) handleLocalMsg(handler *handler, msg replica.Message) {
// 	switch msg.MsgType {
// 	case replica.MsgSyncGet: // 获取日志
// 		r.handleSyncGet(handler, msg)
// 	case replica.MsgStoreAppend: // 追加日志
// 		r.handleStoreAppend(handler, msg)
// 	case replica.MsgApplyLogs: // 应用日志
// 		r.handleApplyLogsReq(handler, msg)
// 	case replica.MsgLearnerToFollower: // 学习者转追随者
// 		r.handleLearnerToFollower(handler, msg)
// 	case replica.MsgLearnerToLeader: // 学习者转领导者
// 		r.handleLearnerToLeader(handler, msg)
// 	case replica.MsgVoteResp:
// 		err := handler.step(msg)
// 		if err != nil {
// 			r.Error("step vote resp message failed", zap.Error(err))
// 		}
// 	case replica.MsgConfigChange:
// 		r.handleConfigChange(handler, msg)
// 	}
// }

// func (r *ReactorSub) handleSyncGet(handler *handler, msg replica.Message) {
// 	if msg.Index <= 0 {
// 		return
// 	}
// 	tk := newGetLogsTask(msg.From, msg.Index)

// 	lastIndex := handler.lastIndex.Load()
// 	tk.setExec(func() error {
// 		var start time.Time
// 		if r.opts.EnableLazyCatchUp {
// 			start = time.Now()
// 		}
// 		resultLogs, err := handler.getAndMergeLogs(lastIndex, msg)
// 		if err != nil {
// 			r.Error("get logs error", zap.Error(err))
// 		}

// 		resp := replica.NewMsgSyncGetResp(r.opts.NodeId, msg.From, msg.Index, resultLogs)
// 		tk.setResp(resp)
// 		tk.taskFinished()
// 		if len(resultLogs) > 0 {
// 			r.advance()
// 		}
// 		if r.opts.EnableLazyCatchUp {
// 			end := time.Since(start)
// 			if end > time.Millisecond*100 {
// 				r.Info("get logs", zap.Duration("cost", end), zap.Int("logs", len(resultLogs)), zap.Uint64("index", msg.Index), zap.Uint64("from", msg.From))
// 			}
// 		}
// 		return nil
// 	})
// 	handler.addGetLogsTask(tk)
// }

// func (r *ReactorSub) handleStoreAppend(handler *handler, msg replica.Message) {
// 	if len(msg.Logs) == 0 {
// 		return
// 	}

// 	// 追加日志
// 	r.mr.appendLogs(handler.key, msg.Logs)

// 	// lastLog := msg.Logs[len(msg.Logs)-1]
// 	// tk := newStoreAppendTask(lastLog.Index)
// 	// tk.setExec(func() error {
// 	// 	var start time.Time
// 	// 	if r.opts.EnableLazyCatchUp {
// 	// 		start = time.Now()
// 	// 	}

// 	// 	err := handler.appendLogs(msg.Logs)
// 	// 	if err != nil {
// 	// 		r.Panic("append logs error", zap.Error(err))
// 	// 		return err
// 	// 	}
// 	// 	tk.setResp(replica.NewMsgStoreAppendResp(r.opts.NodeId, lastLog.Index))
// 	// 	tk.taskFinished()
// 	// 	r.advance()

// 	// 	if r.opts.EnableLazyCatchUp {
// 	// 		end := time.Since(start)
// 	// 		if end > time.Millisecond*100 {
// 	// 			r.Debug("append logs", zap.Duration("cost", end), zap.Int("logs", len(msg.Logs)), zap.Uint64("index", msg.Index), zap.Uint64("from", msg.From))
// 	// 		}
// 	// 	}
// 	// 	return nil
// 	// })
// 	// handler.addAppendLogStoreTask(tk)
// }

// func (r *ReactorSub) handleApplyLogsReq(handler *handler, msg replica.Message) {

// 	applyingIndex := msg.ApplyingIndex
// 	committedIndex := msg.CommittedIndex
// 	if applyingIndex > committedIndex {
// 		r.Debug("not apply logs req", zap.Uint64("applyingIndex", applyingIndex), zap.Uint64("committedIndex", committedIndex))
// 		return
// 	}
// 	if msg.CommittedIndex == 0 {
// 		r.Panic("committedIndex is 0", zap.Uint64("applyingIndex", applyingIndex), zap.Uint64("committedIndex", committedIndex))
// 	}

// 	if !r.opts.IsCommittedAfterApplied {
// 		// r.Debug("did commit", zap.Uint64("applyingIndex", applyingIndex), zap.Uint64("committedIndex", committedIndex))
// 		handler.didCommit(msg.ApplyingIndex+1, msg.CommittedIndex+1)
// 	}

// 	tk := newApplyLogsTask(committedIndex)
// 	tk.setExec(func() error {

// 		var start time.Time
// 		if r.opts.EnableLazyCatchUp {
// 			start = time.Now()
// 		}

// 		err := handler.applyLogs(msg.ApplyingIndex+1, msg.CommittedIndex+1)
// 		if err != nil {
// 			r.Panic("apply logs error", zap.Error(err))
// 			return err
// 		}
// 		if r.opts.IsCommittedAfterApplied {
// 			handler.didCommit(msg.ApplyingIndex+1, msg.CommittedIndex+1)
// 		}
// 		tk.setResp(replica.NewMsgApplyLogsRespMessage(r.opts.NodeId, msg.Term, committedIndex))
// 		tk.taskFinished()
// 		r.advance()

// 		if r.opts.EnableLazyCatchUp {
// 			end := time.Since(start)
// 			if end > time.Millisecond*100 {
// 				r.Info("apply logs", zap.Duration("cost", end), zap.Uint64("startApplyingIndex", msg.ApplyingIndex+1), zap.Uint64("endApplyingIndex", msg.CommittedIndex+1))
// 			}
// 		}

// 		return nil
// 	})
// 	handler.addApplyLogStoreTask(tk)
// }

// // 处理配置改变 （这里需要开协程，因为saveConfig是耗时操作，要不然会影响整个reactor的效率）
// func (r *ReactorSub) handleLearnerToFollower(handler *handler, msg replica.Message) {
// 	err := handler.learnerToFollower(msg.LearnerId)
// 	if err != nil {
// 		r.Error("learnerToFollower failed", zap.Error(err))
// 		return
// 	}
// }

// func (r *ReactorSub) handleLearnerToLeader(handler *handler, msg replica.Message) {
// 	err := handler.learnerToLeader(msg.LearnerId)
// 	if err != nil {
// 		r.Error("learnerToLeader failed", zap.Error(err))
// 		return
// 	}
// }

// func (r *ReactorSub) handleConfigChange(handler *handler, msg replica.Message) {
// 	cfgData := msg.Logs[0].Data

// 	cfg := replica.Config{}
// 	err := cfg.Unmarshal(cfgData)
// 	if err != nil {
// 		r.Error("handleConfigChange: unmarshal config failed", zap.Error(err))
// 		return
// 	}
// 	err = handler.saveConfig(cfg)
// 	if err != nil {
// 		r.Error("handleConfigChange: save config failed", zap.Error(err))
// 		return
// 	}
// }

func (r *ReactorSub) addHandler(handler *handler) {
	r.handlers.add(handler)

}

func (r *ReactorSub) removeHandler(key string) *handler {
	hd := r.handlers.remove(key)
	if r.opts.Event.OnHandlerRemove != nil {
		r.opts.Event.OnHandlerRemove(hd.handler)
	}
	return hd
}

func (r *ReactorSub) handler(key string) *handler {

	return r.handlers.get(key)
}

func (r *ReactorSub) existHandler(key string) bool {
	return r.handlers.exist(key)
}

func (r *ReactorSub) handlerLen() int {
	return r.handlers.len()
}

func (r *ReactorSub) iterator(f func(h *handler) bool) {
	r.handlers.iterator(f)
}

// 收到消息
func (r *ReactorSub) addMessage(m Message) {

	// if r.opts.ReactorType == ReactorTypeChannel {
	// 	if m.MsgType == replica.MsgPing {
	// 		r.Debug("add message", zap.String("handlerKey", m.HandlerKey), zap.Uint64("from", m.From), zap.String("msgType", m.Message.MsgType.String()))
	// 	}
	// }

	// if m.MsgType == replica.MsgSyncReq {
	// 	r.Debug("add message", zap.String("handlerKey", m.HandlerKey), zap.Uint64("from", m.From), zap.Uint64("index", m.Index), zap.String("msgType", m.Message.MsgType.String()))
	// }

	// r.Debug("add message", zap.String("handlerKey", m.HandlerKey), zap.Uint64("from", m.From), zap.Uint64("index", m.Index), zap.String("msgType", m.Message.MsgType.String()))

	r.step(m.HandlerKey, m.Message)

	// handler := r.handlers.get(m.HandlerKey)
	// if handler == nil {
	// 	r.Warn("handler not exist", zap.String("handlerKey", m.HandlerKey), zap.Int("handlers", r.handlers.len()))
	// 	return
	// }

	// handler.addMessage(m)
	// r.advance() // 推进

}

type stepReq struct {
	handlerKey string
	msg        replica.Message
}
