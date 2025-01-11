package raft

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	wt "github.com/WuKongIM/WuKongIM/pkg/wait"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type Raft struct {
	stopper  *syncutil.Stopper
	opts     *Options
	node     *Node
	advanceC chan struct{}
	stepC    chan stepReq
	wklog.Log
	pause     atomic.Bool // 是否暂停
	pauseCond *sync.Cond

	wait *wait

	fowardProposeWait wt.Wait // 转发提按给领导等待领导的回应

	pool *ants.Pool
}

func New(opts *Options) *Raft {

	raftState, err := opts.Storage.GetState()
	if err != nil {
		panic(fmt.Sprintf("get state failed, err:%v", err))
	}

	lastTermStartLogIndex, err := opts.Storage.GetTermStartIndex(raftState.LastTerm)
	if err != nil {
		panic(fmt.Sprintf("get term start index failed, err:%v", err))
	}

	pool, err := ants.NewPool(opts.GoPoolSize)
	if err != nil {
		panic(err)
	}

	r := &Raft{
		stopper:           syncutil.NewStopper(),
		opts:              opts,
		node:              NewNode(lastTermStartLogIndex, raftState, opts),
		advanceC:          make(chan struct{}, 1),
		stepC:             make(chan stepReq, 1024),
		Log:               wklog.NewWKLog("raft"),
		pauseCond:         sync.NewCond(&sync.Mutex{}),
		wait:              newWait("raft"),
		fowardProposeWait: wt.New(),
		pool:              pool,
	}
	opts.Advance = r.advance

	return r
}

func (r *Raft) Start() error {
	r.stopper.RunWorker(r.loop)

	return nil
}

func (r *Raft) Stop() {
	r.stopper.Stop()
}

// Pause 暂停服务
func (r *Raft) Pause() {
	r.pause.Store(true)

}

// Resume 恢复服务
func (r *Raft) Resume() {
	r.pause.Store(false)

	// 唤醒等待中的 loop
	r.pauseCond.L.Lock()
	r.pauseCond.Signal() // 唤醒一个等待的 Goroutine
	r.pauseCond.L.Unlock()
}

func (r *Raft) Step(e types.Event) {
	if r.pause.Load() {
		r.Info("raft is paused, ignore event", zap.String("event", e.String()))
		return
	}
	if e.Type == types.SendPropose {
		go r.handleSendPropose(e)
		return
	} else if e.Type == types.SendProposeResp {
		r.handleSendProposeResp(e)
		return
	}
	select {
	case r.stepC <- stepReq{event: e}:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *Raft) StepWait(ctx context.Context, e types.Event) error {
	if r.pause.Load() {
		r.Info("raft is paused, ignore event", zap.String("event", e.String()))
		return ErrPaused
	}
	// 处理其他副本发过来的提案
	if e.Type == types.SendPropose {
		r.handleSendPropose(e)
		return nil
	} else if e.Type == types.SendProposeResp {
		r.handleSendProposeResp(e)
		return nil
	}

	resp := make(chan error, 1)
	select {
	case r.stepC <- stepReq{event: e, resp: resp}:
	case <-ctx.Done():
		return ctx.Err()
	case <-r.stopper.ShouldStop():
		return ErrStopped
	}

	select {
	case err := <-resp:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-r.stopper.ShouldStop():
		return ErrStopped
	}
}

func (r *Raft) IsLeader() bool {
	return r.node.IsLeader()
}

func (r *Raft) LeaderId() uint64 {
	return r.node.LeaderId()
}

func (r *Raft) Options() *Options {
	return r.opts
}

// WaitUtilCommit 等待日志提交到指定的下标
func (r *Raft) WaitUtilCommit(ctx context.Context, index uint64) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if r.node.queue.committedIndex >= index {
			return nil
		}
		time.Sleep(time.Millisecond * 10)
	}
}

func (r *Raft) BecomeLeader(term uint32) {
	r.node.BecomeLeader(term)
}

func (r *Raft) BecomeFollower(term uint32, leader uint64) {
	r.node.BecomeFollower(term, leader)
}

func (r *Raft) loop() {
	tk := time.NewTicker(r.opts.TickInterval)
	for {
		if r.pause.Load() {
			r.pauseCond.L.Lock()
			r.pauseCond.Wait() // 会在某个条件满足时被唤醒
			r.pauseCond.L.Unlock()
			continue
		}

		r.readyEvents()

		select {
		case <-tk.C:
			r.node.Tick()
		case <-r.advanceC:
		case req := <-r.stepC:
			err := r.node.Step(req.event)
			if req.resp != nil {
				req.resp <- err
			}
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *Raft) readyEvents() {
	events := r.node.Ready()
	for _, e := range events {
		switch e.Type {
		case types.StoreReq:
			r.handleStoreReq(e)
			continue
		case types.GetLogsReq:
			r.handleGetLogsReq(e)
			continue
		case types.TruncateReq: // 截断请求
			r.handleTruncateReq(e)
			continue
		case types.ApplyReq:
			r.handleApplyReq(e)
			continue
		}

		if e.To == None {
			fmt.Println("none node event--->", e)
			continue
		}

		if e.To == types.LocalNode {
			err := r.node.Step(e)
			if err != nil {
				r.node.Error("step error", zap.Error(err))
			}
			continue
		}
		r.opts.Transport.Send(e)
	}
}

func (r *Raft) handleStoreReq(e types.Event) {

	err := r.pool.Submit(func() {
		// 追加消息
		err := r.opts.Storage.AppendLogs(e.Logs, e.TermStartIndexInfo)
		if err != nil {
			r.Error("append logs failed", zap.Error(err))
		}
		reason := types.ReasonOk
		if err != nil {
			reason = types.ReasonError
		}
		r.stepC <- stepReq{event: types.Event{
			Type:   types.StoreResp,
			Index:  e.Logs[len(e.Logs)-1].Index,
			Reason: reason,
		}}
	})

	if err != nil {
		r.Error("submit append logs failed", zap.Error(err))
		r.stepC <- stepReq{event: types.Event{
			Type:   types.StoreResp,
			Reason: types.ReasonError,
		}}
	}

}

func (r *Raft) handleGetLogsReq(e types.Event) {

	var leaderLastLogTerm = r.node.lastTermStartIndex.Term

	err := r.pool.Submit(func() {
		// 获取裁断日志下标
		var (
			trunctIndex uint64
		)
		if e.Reason != types.ReasonOnlySync {
			var treason types.Reason
			trunctIndex, treason = r.getTrunctLogIndex(e, leaderLastLogTerm)
			if treason != types.ReasonOk {
				r.stepC <- stepReq{event: types.Event{
					To:     e.From,
					Type:   types.GetLogsResp,
					Index:  e.Index,
					Reason: treason,
				}}
				return
			}
		}

		// 需要裁剪
		if trunctIndex > 0 {
			r.stepC <- stepReq{event: types.Event{
				To:     e.From,
				Type:   types.GetLogsResp,
				Index:  trunctIndex,
				Reason: types.ReasonTruncate,
			}}
			return
		}

		// 获取日志数据
		logs, err := r.opts.Storage.GetLogs(e.Index, e.StoredIndex+1, r.opts.MaxLogCountPerBatch)
		if err != nil {
			r.node.Error("get logs failed", zap.Error(err))
			r.stepC <- stepReq{event: types.Event{
				To:     e.From,
				Type:   types.GetLogsResp,
				Index:  e.Index,
				Reason: types.ReasonError,
			}}
			return
		}
		r.stepC <- stepReq{event: types.Event{
			To:     e.From,
			Type:   types.GetLogsResp,
			Index:  e.Index,
			Logs:   logs,
			Reason: types.ReasonOk,
		}}
	})
	if err != nil {
		r.Error("submit get logs failed", zap.Error(err))
	}
}

func (r *Raft) handleTruncateReq(e types.Event) {
	err := r.pool.Submit(func() {
		err := r.opts.Storage.TruncateLogTo(e.Index)
		if err != nil {
			r.Error("truncate logs failed", zap.Error(err))
			r.stepC <- stepReq{event: types.Event{
				Type:   types.TruncateResp,
				Reason: types.ReasonError,
			}}
			return
		}
		// 删除本地的leader term start index
		err = r.opts.Storage.DeleteLeaderTermStartIndexGreaterThanTerm(e.Term)
		if err != nil {
			r.Error("delete leader term start index failed", zap.Error(err), zap.Uint32("term", e.Term))
			r.stepC <- stepReq{event: types.Event{
				Type:   types.TruncateResp,
				Reason: types.ReasonError,
			}}
			return
		}
		r.stepC <- stepReq{event: types.Event{
			Type:   types.TruncateResp,
			Index:  e.Index,
			Reason: types.ReasonOk,
		}}
	})
	if err != nil {
		r.Error("submit truncate logs failed", zap.Error(err))
		r.stepC <- stepReq{event: types.Event{
			Type:   types.TruncateResp,
			Reason: types.ReasonError,
		}}
	}
}

func (r *Raft) handleApplyReq(e types.Event) {
	err := r.pool.Submit(func() {

		// 已提交
		r.wait.didCommit(e.EndIndex - 1)

		logs, err := r.opts.Storage.GetLogs(e.StartIndex, e.EndIndex, 0)
		if err != nil {
			r.Error("apply logs failed", zap.Error(err))
			r.stepC <- stepReq{event: types.Event{
				Type:   types.ApplyResp,
				Reason: types.ReasonError,
			}}
			return
		}
		if len(logs) == 0 {
			r.Error("logs is empty", zap.Uint64("startIndex", e.StartIndex), zap.Uint64("endIndex", e.EndIndex))
			r.stepC <- stepReq{event: types.Event{
				Type:   types.ApplyResp,
				Reason: types.ReasonError,
			}}
			return
		}
		err = r.opts.Storage.Apply(logs)
		if err != nil {
			r.Panic("apply logs failed", zap.Error(err))
			r.stepC <- stepReq{event: types.Event{
				Type:   types.ApplyResp,
				Reason: types.ReasonError,
			}}
			return
		}
		lastLogIndex := logs[len(logs)-1].Index
		r.stepC <- stepReq{event: types.Event{
			Type:   types.ApplyResp,
			Reason: types.ReasonOk,
			Index:  lastLogIndex,
		}}
		// 已应用
		r.wait.didApply(lastLogIndex)
	})
	if err != nil {
		r.Error("submit apply logs failed", zap.Error(err))
		r.stepC <- stepReq{event: types.Event{
			Type:   types.ApplyResp,
			Reason: types.ReasonError,
		}}
	}
}

// 根据副本的同步数据，来获取副本的需要裁剪的日志下标，如果不需要裁剪，则返回0
func (r *Raft) getTrunctLogIndex(e types.Event, leaderLastLogTerm uint32) (uint64, types.Reason) {

	// 如果副本的最新日志任期大于领导的最新日志任期，则不合法，副本最新日志任期不可能大于领导最新日志任期
	if e.LastLogTerm > r.node.lastTermStartIndex.Term {
		r.Error("log term is greater than leader term", zap.Uint32("lastLogTerm", e.LastLogTerm), zap.Uint32("term", r.node.cfg.Term))
		return 0, types.ReasonError
	}
	// 副本的最新日志任期为0，说明副本没有日志，不需要裁剪
	if e.LastLogTerm == 0 {
		return 0, types.ReasonOk
	}
	// 如果副本的最新日志任期等于当前领导的最新日志任期，则不需要裁剪
	if e.LastLogTerm == leaderLastLogTerm {
		return 0, types.ReasonOk
	}

	// 如果副本的最新日志任期小于当前领导的最新日志任期，则需要裁剪
	if e.LastLogTerm < leaderLastLogTerm {
		// 获取副本的最新日志任期+1的开始日志下标
		termStartIndex, err := r.opts.Storage.GetTermStartIndex(e.LastLogTerm + 1)
		if err != nil {
			r.Error("get term start index failed", zap.Error(err))
			return 0, types.ReasonError
		}
		if termStartIndex > 0 {
			return termStartIndex - 1, types.ReasonOk
		}
		return termStartIndex, types.ReasonOk
	}
	return 0, types.ReasonOk
}

func (r *Raft) advance() {
	select {
	case r.advanceC <- struct{}{}:
	default:
	}
}
