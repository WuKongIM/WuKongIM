package reactor

import (
	"context"
	"errors"
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
		tmpHandlers: make([]*handler, 0, 100),
		avdanceC:    make(chan struct{}, 1),
		stepC:       make(chan stepReq, 2024),
		proposeC:    make(chan proposeReq, 2024),
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

	for !r.stopped.Load() {
		r.readyEvents()

		select {
		case <-tick.C:
			r.tick()
		case req := <-r.stepC:
			handler := r.handlers.get(req.handlerKey)
			if handler == nil {
				r.Info("ReactorSub: step handler not exist", zap.String("handlerKey", req.handlerKey), zap.String("msgType", req.msg.MsgType.String()), zap.Uint64("from", req.msg.From))
				continue
			}
			err := handler.handler.Step(req.msg)
			if err != nil {
				r.Error("step message failed", zap.Error(err))
				if req.resultC != nil {
					req.resultC <- err
				}
			} else {
				if req.resultC != nil {
					req.resultC <- nil
				}
			}
		case req := <-r.proposeC:

			lastLogIndex, term := req.handler.lastLogIndexAndTerm()
			for i := 0; i < len(req.logs); i++ {
				lg := req.logs[i]
				lg.Index = lastLogIndex + 1 + uint64(i)
				lg.Term = term
				req.logs[i] = lg
			}
			req.handler.didPropose(req.waitKey, req.logs[0].Index, req.logs[len(req.logs)-1].Index)
			err := req.handler.handler.Step(replica.NewProposeMessageWithLogs(r.opts.NodeId, term, req.logs))
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
			r.Info("stop reactor sub")
			return
		}
	}

}

func (r *ReactorSub) proposeAndWait(ctx context.Context, handleKey string, logs []replica.Log) ([]ProposeResult, error) {
	if r.stopped.Load() {
		return nil, ErrReactorSubStopped
	}
	if len(logs) == 0 {
		return nil, errors.New("proposeAndWait: logs is empty")
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
			if end > time.Millisecond*1000 {
				r.Info("ReactorSub: proposeAndWait", zap.Int64("cost", end.Milliseconds()), zap.String("handleKey", handleKey), zap.Int("logs", len(logs)), zap.Uint64("lastIndex", logs[len(logs)-1].Index))
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

	minId := logs[0].Id
	maxId := logs[len(logs)-1].Id

	waitKey := strconv.FormatUint(maxId, 10)

	timeoutCtx, cancel := context.WithTimeout(ctx, r.opts.ProposeTimeout)
	defer cancel()

	// -------------------- 获得等待提交提案的句柄 --------------------
	progress := handler.addWait(waitKey, minId, maxId)

	// -------------------- 添加提案请求 --------------------
	req := newProposeReq(handler, waitKey, logs)
	select {
	case r.proposeC <- req:
	case <-timeoutCtx.Done():
		if !handler.proposeWait.exist(waitKey) {
			r.Panic("proposeAndWait: propose wait not exist", zap.String("waitKey", waitKey), zap.String("handler", handler.key))
		}
		r.Error("proposeAndWait: proposeC is timeout", zap.String("handler", handler.key), zap.String("waitKey", waitKey))
		handler.removeWait(waitKey)
		close(progress.waitC)
		trace.GlobalTrace.Metrics.Cluster().ProposeFailedCountAdd(trace.ClusterKindChannel, 1)
		return nil, timeoutCtx.Err()
	case <-r.stopper.ShouldStop():
		handler.removeWait(waitKey)
		close(progress.waitC)
		trace.GlobalTrace.Metrics.Cluster().ProposeFailedCountAdd(trace.ClusterKindChannel, 1)
		return nil, ErrReactorSubStopped
	}

	// -------------------- 等待提案结果 --------------------
	select {
	case err := <-progress.waitC:
		handler.removeWait(waitKey)
		close(progress.waitC)
		if err != nil {
			return nil, err
		}
		results := make([]ProposeResult, 0)
		for i, lg := range logs {
			results = append(results, ProposeResult{
				Id:    lg.Id,
				Index: progress.minIndex + uint64(i),
			})
		}
		return results, nil
	case <-timeoutCtx.Done():
		r.Error("proposeAndWait: waitC is timeout", zap.String("handler", handler.key), zap.String("waitKey", waitKey), zap.Uint64("progressIndex", progress.progressIndex), zap.Uint64("minIndex", progress.minIndex), zap.Uint64("maxIndex", progress.maxIndex))
		handler.removeWait(waitKey)
		close(progress.waitC)
		trace.GlobalTrace.Metrics.Cluster().ProposeFailedCountAdd(trace.ClusterKindChannel, 1)
		return nil, timeoutCtx.Err()
	case <-r.stopper.ShouldStop():
		handler.removeWait(waitKey)
		close(progress.waitC)
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

	select {
	case r.stepC <- stepReq{
		handlerKey: handlerKey,
		msg:        msg,
	}:
	default:
		r.Panic("stepC is full", zap.String("handlerKey", handlerKey), zap.String("msgType", msg.MsgType.String()), zap.Uint64("from", msg.From))
	}
}

func (r *ReactorSub) stepWait(handlerKey string, msg replica.Message) error {

	start := time.Now()
	defer func() {
		end := time.Since(start)
		if end > time.Millisecond*100 {
			r.Info("stepWait cost too long", zap.Duration("cost", end), zap.String("handlerKey", handlerKey), zap.String("msgType", msg.MsgType.String()), zap.Uint64("from", msg.From))
		}
	}()

	resultC := make(chan error, 1)
	r.stepC <- stepReq{
		handlerKey: handlerKey,
		msg:        msg,
		resultC:    resultC,
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), r.opts.ProposeTimeout)
	defer cancel()
	select {
	case err := <-resultC:
		return err
	case <-timeoutCtx.Done():
		return timeoutCtx.Err()
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

	r.handlers.readHandlers(&r.tmpHandlers)

	for hasEvent && !r.stopped.Load() {
		hasEvent = false

		for _, handler := range r.tmpHandlers {
			has := r.handleReady(handler)
			if has {
				hasEvent = true
			}
		}
	}
	if len(r.tmpHandlers) > 0 {
		r.tmpHandlers = r.tmpHandlers[:0]
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

		if m.To == r.opts.NodeId { // 处理本地节点消息
			if m.MsgType == replica.MsgVoteResp {
				_ = handler.handler.Step(m)
			}
		}

		// 同步返回是否有日志数据
		syncRespHasLog := m.MsgType == replica.MsgSyncResp && len(m.Logs) > 0

		if syncRespHasLog { // 如果有返回同步日志，则可继续同步
			r.advance()
		}

		// 		// 同步速度处理
		if r.opts.AutoSlowDownOn {
			// 如果收到了同步日志的消息，速度重置（提速）
			if syncRespHasLog { // 还有副本同步到日志，不降速
				handler.resetSlowDown()
				r.Debug("sync resp...", zap.String("handler", handler.key), zap.Uint64("index", m.Index), zap.Int("logs", len(m.Logs)), zap.Uint64("to", m.To))

			}
		}

		if m.MsgType == replica.MsgSyncResp { // 如果有领导返回同步数据，则同步超时tick设置为0
			handler.syncTimeoutTick = 0
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
			r.mr.addStoreAppendReq(AppendLogReq{
				HandleKey: handler.key,
				Logs:      m.Logs,
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
		case replica.MsgFollowerToLeader: // 追随者转领导者
			r.mr.addFollowerToLeaderReq(&followerToLeaderReq{
				h:          handler,
				followerId: m.FollowerId,
			})

		case replica.MsgSpeedLevelChange:
			// fmt.Println("MsgSpeedLevelChange---------------->", handler.key, m.SpeedLevel.String())

		case replica.MsgSyncTimeout:
			handler.syncTimeoutTick++
			r.Info("sync timeout", zap.String("handler", handler.key), zap.Uint64("leader", handler.leaderId()), zap.Uint64("index", m.Index))

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

func (r *ReactorSub) tick() {

	r.handlers.readHandlers(&r.tmpHandlers)

	for _, handler := range r.tmpHandlers {
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
	}

	if len(r.needRemoveKeys) > 0 {
		for _, key := range r.needRemoveKeys {
			r.removeHandler(key)
		}
		r.needRemoveKeys = r.needRemoveKeys[:0]
	}

	r.tmpHandlers = r.tmpHandlers[:0]
}

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

	r.step(m.HandlerKey, m.Message)

}

type stepReq struct {
	handlerKey string
	msg        replica.Message
	resultC    chan error
}
