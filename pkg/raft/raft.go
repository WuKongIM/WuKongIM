package raft

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
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
}

func New(opts *Options) *Raft {

	r := &Raft{
		stopper:   syncutil.NewStopper(),
		opts:      opts,
		node:      NewNode(opts),
		advanceC:  make(chan struct{}, 1),
		stepC:     make(chan stepReq, 1024),
		Log:       wklog.NewWKLog("raft"),
		pauseCond: sync.NewCond(&sync.Mutex{}),
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

func (r *Raft) Step(e Event) {
	if r.pause.Load() {
		r.Info("raft is paused, ignore event", zap.String("event", e.String()))
		return
	}
	select {
	case r.stepC <- stepReq{event: e}:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *Raft) StepWait(e Event) error {
	resp := make(chan error, 1)
	select {
	case r.stepC <- stepReq{event: e, resp: resp}:
	case <-r.stopper.ShouldStop():
		return ErrStopped
	}
	return <-resp
}

func (r *Raft) Propose(data []byte) error {
	event := r.node.NewPropose(data)
	return r.StepWait(event)
}

func (r *Raft) IsLeader() bool {
	return r.node.isLeader()
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
		case StoreReq:
			r.handleStoreReq(e)
			continue
		case GetLogsReq:
			r.handleGetLogsReq(e)
			continue
		}

		if e.To == None {
			fmt.Println("none node event--->", e)
			continue
		}

		if e.To == r.opts.NodeId {
			err := r.node.Step(e)
			if err != nil {
				r.node.Error("step error", zap.Error(err))
			}
			continue
		}
		r.opts.Transport.Send(e)
	}
}

func (r *Raft) handleStoreReq(e Event) {

	var (
		newTermStartIndex *TermStartIndex
	)
	if r.node.lastTermStartIndex.Term != e.Term {
		newTermStartIndex = &TermStartIndex{
			Term:  e.Term,
			Index: e.Index,
		}
		r.node.updateLastTermStartIndex(e.Term, e.Index)
	}

	err := r.opts.Submit(func() {
		// 追加消息
		err := r.opts.Storage.AppendLogs(e.Logs, newTermStartIndex)
		if err != nil {
			r.Error("append logs failed", zap.Error(err))
		}
		reason := ReasonOk
		if err != nil {
			reason = ReasonError
		}
		r.stepC <- stepReq{event: Event{
			Type:   StoreResp,
			Index:  e.Logs[len(e.Logs)-1].Index,
			Reason: reason,
		}}
	})

	if err != nil {
		r.Error("submit append logs failed", zap.Error(err))
		r.stepC <- stepReq{event: Event{
			Type:   StoreResp,
			Reason: ReasonError,
		}}
	}

}

func (r *Raft) handleGetLogsReq(e Event) {
	if r.node.queue.lastLogIndex+1 < e.Index {
		r.Error("invalid get logs request", zap.Uint64("index", e.Index), zap.Uint64("lastLogIndex", r.node.queue.lastLogIndex))
		return
	}

	count := r.node.queue.lastLogIndex + 1 - e.Index
	if count > r.opts.MaxLogCountPerBatch {
		count = r.opts.MaxLogCountPerBatch
	}
	var leaderLastLogTerm = r.node.lastTermStartIndex.Term

	err := r.opts.Submit(func() {
		// 获取裁断日志下标
		var (
			trunctIndex uint64
		)
		if e.Reason != ReasonOnlySync {
			var treason Reason
			trunctIndex, treason = r.getTrunctLogIndex(e, leaderLastLogTerm)
			if treason != ReasonOk {
				r.stepC <- stepReq{event: Event{
					To:     e.From,
					Type:   GetLogsResp,
					Index:  e.Index,
					Reason: treason,
				}}
				return
			}
		}

		// 需要裁剪
		if trunctIndex > 0 {
			r.stepC <- stepReq{event: Event{
				To:     e.From,
				Type:   GetLogsResp,
				Index:  trunctIndex,
				Reason: ReasonTrunctate,
			}}
			return
		}

		// 获取日志数据
		logs, err := r.opts.Storage.GetLogs(e.Index, e.Index+count)
		if err != nil {
			r.node.Error("get logs failed", zap.Error(err))
			r.stepC <- stepReq{event: Event{
				To:     e.From,
				Type:   GetLogsResp,
				Index:  e.Index,
				Reason: ReasonError,
			}}
			return
		}
		r.stepC <- stepReq{event: Event{
			To:     e.From,
			Type:   GetLogsResp,
			Index:  e.Index,
			Logs:   logs,
			Reason: ReasonOk,
		}}
	})
	if err != nil {
		r.Error("submit get logs failed", zap.Error(err))
	}
}

// 根据副本的同步数据，来获取副本的需要裁剪的日志下标，如果不需要裁剪，则返回0
func (r *Raft) getTrunctLogIndex(e Event, leaderLastLogTerm uint32) (uint64, Reason) {

	// 如果副本的最新日志任期大于领导的最新日志任期，则不合法，副本最新日志任期不可能大于领导最新日志任期
	if e.LastLogTerm > r.node.lastTermStartIndex.Term {
		r.Error("log term is greater than leader term", zap.Uint32("lastLogTerm", e.LastLogTerm), zap.Uint32("term", r.node.cfg.Term))
		return 0, ReasonError
	}
	// 副本的最新日志任期为0，说明副本没有日志，不需要裁剪
	if e.LastLogTerm == 0 {
		return 0, ReasonOk
	}
	// 如果副本的最新日志任期等于当前领导的最新日志任期，则不需要裁剪
	if e.LastLogTerm == leaderLastLogTerm {
		return 0, ReasonOk
	}

	// 如果副本的最新日志任期小于当前领导的最新日志任期，则需要裁剪
	if e.LastLogTerm < leaderLastLogTerm {
		// 获取副本的最新日志任期+1的开始日志下标
		termStartIndex, err := r.opts.Storage.GetTermStartIndex(e.LastLogTerm + 1)
		if err != nil {
			r.Error("get term start index failed", zap.Error(err))
			return 0, ReasonError
		}
		if termStartIndex > 0 {
			return termStartIndex - 1, ReasonOk
		}
		return termStartIndex, ReasonOk
	}
	return 0, ReasonOk
}

func (r *Raft) advance() {
	select {
	case r.advanceC <- struct{}{}:
	default:
	}
}
