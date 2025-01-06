package raftgroup

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/track"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

type RaftGroup struct {
	raftList *linkedList
	stopper  *syncutil.Stopper
	opts     *Options
	advanceC chan struct{}

	tmpRafts []IRaft
	stopped  bool

	goPool *ants.Pool
	wklog.Log

	mq *EventQueue
}

func New(opts *Options) *RaftGroup {
	rg := &RaftGroup{
		raftList: newLinkedList(),
		stopper:  syncutil.NewStopper(),
		opts:     opts,
		advanceC: make(chan struct{}, 1),
		Log:      wklog.NewWKLog("RaftGroup"),
		mq:       NewEventQueue(opts.ReceiveQueueLength, false, 0, 0),
	}
	var err error
	rg.goPool, err = ants.NewPool(opts.GoPoolSize)
	if err != nil {
		rg.Panic("create go pool failed", zap.Error(err))
	}
	return rg
}

func (rg *RaftGroup) Options() *Options {
	return rg.opts
}

func (rg *RaftGroup) Propose(raftKey string, id uint64, data []byte) (*types.ProposeResp, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), rg.opts.ProposeTimeout)
	defer cancel()
	rg.Info("propose", zap.String("raftKey", raftKey), zap.Uint64("id", id), zap.ByteString("data", data))
	return rg.ProposeTimeout(timeoutCtx, raftKey, id, data)
}

func (rg *RaftGroup) ProposeTimeout(ctx context.Context, raftKey string, id uint64, data []byte) (*types.ProposeResp, error) {
	raft := rg.raftList.get(raftKey)
	if raft == nil {
		return nil, fmt.Errorf("raft not found, key:%s", raftKey)
	}

	if !raft.IsLeader() {
		return nil, fmt.Errorf("raft not leader, key:%s", raftKey)
	}

	raft.Lock()
	defer raft.Unlock()

	lastLogIndex := raft.LastLogIndex()

	logIndex := lastLogIndex + 1

	waitC := make(chan error, 1)

	// 轨迹记录
	record := track.Record{
		PreStart: time.Now(),
	}
	record.Add(track.PositionStart)

	// 添加提案事件
	rg.mq.MustAdd(Event{
		RaftKey: raftKey,
		Event: types.Event{
			Type: types.Propose,
			Logs: []types.Log{
				{
					Id:     id,
					Term:   raft.LastTerm(),
					Index:  logIndex,
					Data:   data,
					Record: record,
				},
			},
		},
		WaitC: waitC,
	})
	rg.Advance()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("propose timeout")
	case err := <-waitC:
		if err != nil {
			return nil, err
		}
		return &types.ProposeResp{
			Id:    id,
			Index: logIndex,
		}, nil
	}
}

// ProposeBatchTimeout 批量提案
func (rg *RaftGroup) ProposeBatchTimeout(ctx context.Context, raftKey string, reqs []types.ProposeReq) ([]*types.ProposeResp, error) {
	raft := rg.raftList.get(raftKey)
	if raft == nil {
		return nil, fmt.Errorf("raft not found, key:%s", raftKey)
	}

	if !raft.IsLeader() {
		return nil, fmt.Errorf("raft not leader, key:%s", raftKey)
	}

	raft.Lock()
	defer raft.Unlock()

	lastLogIndex := raft.LastLogIndex()
	logs := make([]types.Log, 0, len(reqs))
	for i, req := range reqs {
		logIndex := lastLogIndex + 1 + uint64(i)
		logs = append(logs, types.Log{
			Id:    req.Id,
			Term:  raft.LastTerm(),
			Index: logIndex,
			Data:  req.Data,
		})
	}

	waitC := make(chan error, 1)
	rg.mq.MustAdd(Event{
		RaftKey: raftKey,
		Event: types.Event{
			Type: types.Propose,
			Logs: logs,
		},
		WaitC: waitC,
	})

	rg.Advance()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("propose timeout")
	case err := <-waitC:
		if err != nil {
			return nil, err
		}
		resps := make([]*types.ProposeResp, 0, len(reqs))
		for i, req := range reqs {
			logIndex := lastLogIndex + 1 + uint64(i)
			resps = append(resps, &types.ProposeResp{
				Id:    req.Id,
				Index: logIndex,
			})
		}
		return resps, nil
	}
}

func (rg *RaftGroup) AddRaft(r IRaft) {
	rg.raftList.push(r)
}

func (rg *RaftGroup) RemoveRaft(r IRaft) {
	rg.raftList.remove(r.Key())
}

func (rg *RaftGroup) IsLeader(raftKey string) bool {
	raft := rg.raftList.get(raftKey)
	if raft == nil {
		return false
	}
	return raft.IsLeader()
}

func (rg *RaftGroup) Start() error {
	rg.stopper.RunWorker(rg.loopEvent)
	return nil
}

func (rg *RaftGroup) Stop() {
	rg.stopper.Stop()
}

func (rg *RaftGroup) Advance() {
	select {
	case rg.advanceC <- struct{}{}:
	default:
	}
}

// AddEvent 添加事件
func (rg *RaftGroup) AddEvent(raftKey string, event types.Event) {
	rg.mq.MustAdd(Event{
		RaftKey: raftKey,
		Event:   event,
	})
}

func (rg *RaftGroup) TryAddEvent(raftKey string, event types.Event) {
	rg.mq.Add(Event{
		RaftKey: raftKey,
		Event:   event,
	})
}

func (rg *RaftGroup) loopEvent() {
	tk := time.NewTicker(rg.opts.TickInterval)
	for !rg.stopped {
		rg.readyEvents()
		select {
		case <-tk.C:
			rg.ticks()
		case <-rg.advanceC:
		case <-rg.stopper.ShouldStop():
			return
		}
	}
}

func (rg *RaftGroup) readyEvents() {
	hasEvent := false
	// 处理收到的事件
	has := rg.handleReceivedEvents()
	if has {
		hasEvent = true
	}
	// 处理产生的事件
	has = rg.handleReadys()
	if has {
		hasEvent = true
	}

	if hasEvent {
		rg.Advance()
	}
}

// 处理收到的事件
func (rg *RaftGroup) handleReceivedEvents() bool {
	events := rg.mq.Get()
	if len(events) == 0 {
		return false
	}
	hasEvent := false
	for _, e := range events {
		raft := rg.raftList.get(e.RaftKey)
		if raft == nil {
			continue
		}

		// 轨迹记录
		if e.Type == types.Propose {
			for i := range e.Logs {
				e.Logs[i].Record.Add(track.PositionPropose)
			}
		}
		hasEvent = true
		err := raft.Step(e.Event)
		if err != nil {
			rg.Error("step failed", zap.Error(err), zap.String("handleKey", raft.Key()))
		}
		if e.WaitC != nil {
			e.WaitC <- err
		}
	}
	return hasEvent
}

func (rg *RaftGroup) handleReadys() bool {
	rg.raftList.readHandlers(&rg.tmpRafts)
	hasEvent := false
	for _, r := range rg.tmpRafts {
		if r.HasReady() {
			has := rg.handleReady(r)
			if has {
				hasEvent = true
			}
		}
	}
	rg.tmpRafts = rg.tmpRafts[:0]
	return hasEvent
}

func (rg *RaftGroup) ticks() {
	rg.raftList.readHandlers(&rg.tmpRafts)
	for _, r := range rg.tmpRafts {
		r.Tick()
	}
	rg.tmpRafts = rg.tmpRafts[:0]
}

func (rg *RaftGroup) handleReady(r IRaft) bool {
	events := r.Ready()
	if len(events) == 0 {
		return false
	}
	for _, e := range events {
		switch e.Type {
		case types.StoreReq: // 处理存储请求
			rg.handleStoreReq(r, e)
			continue
		case types.GetLogsReq: // 处理获取日志请求
			rg.handleGetLogsReq(r, e)
			continue
		case types.TruncateReq: // 处理截断请求
			rg.handleTruncateReq(r, e)
		case types.ApplyReq: // 处理应用请求
			rg.handleApplyReq(r, e)
			continue
		}
		if e.To == 0 {
			rg.Error("none node event", zap.Any("event", e))
			continue
		}
		if e.To == types.LocalNode {
			err := r.Step(e)
			if err != nil {
				rg.Error("step error", zap.Error(err))
			}
			continue
		}
		rg.opts.Transport.Send(r, e)
	}
	return true
}
