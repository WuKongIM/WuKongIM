package raftgroup

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/track"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	wt "github.com/WuKongIM/WuKongIM/pkg/wait"
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

	wait *wait

	fowardProposeWait wt.Wait // 转发提按给领导等待领导的回应
}

func New(opts *Options) *RaftGroup {
	rg := &RaftGroup{
		raftList:          newLinkedList(),
		stopper:           syncutil.NewStopper(),
		opts:              opts,
		advanceC:          make(chan struct{}, 1),
		Log:               wklog.NewWKLog(fmt.Sprintf("RaftGroup[%s]", opts.LogPrefix)),
		mq:                NewEventQueue(opts.ReceiveQueueLength, false, 0, 0),
		wait:              newWait(),
		fowardProposeWait: wt.New(),
	}
	var err error
	rg.goPool, err = ants.NewPool(opts.GoPoolSize, ants.WithNonblocking(true))
	if err != nil {
		rg.Panic("create go pool failed", zap.Error(err))
	}
	return rg
}

func (rg *RaftGroup) Options() *Options {
	return rg.opts
}

func (rg *RaftGroup) AddRaft(r IRaft) {
	rg.raftList.push(r)
	if rg.opts.Event != nil {
		rg.opts.Event.OnAddRaft(r)
	}
}

func (rg *RaftGroup) RemoveRaft(r IRaft) {
	rg.raftList.remove(r.Key())
	if rg.opts.Event != nil {
		rg.opts.Event.OnRemoveRaft(r)
	}
}

func (rg *RaftGroup) GetRaft(raftKey string) IRaft {
	return rg.raftList.get(raftKey)
}

func (rg *RaftGroup) GetRafts() []IRaft {

	return rg.raftList.all()
}

func (rg *RaftGroup) GetRaftCount() int {
	return rg.raftList.count()
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
func (rg *RaftGroup) AddEvent(raftKey string, e types.Event) {

	if e.Type == types.SendPropose {
		err := rg.goPool.Submit(func() {
			rg.handleSendPropose(raftKey, e)
		})
		if err != nil {
			rg.Error("submit go pool failed", zap.Error(err))
		}
		return
	} else if e.Type == types.SendProposeResp {
		rg.handleSendProposeResp(e)
		return
	}

	rg.mq.MustAdd(Event{
		RaftKey: raftKey,
		Event:   e,
	})
}

func (rg *RaftGroup) AddEventWait(raftKey string, e types.Event) error {
	waitC := make(chan error, 1)
	rg.mq.MustAdd(Event{
		RaftKey: raftKey,
		Event:   e,
		WaitC:   waitC,
	})
	rg.Advance()
	return <-waitC
}

func (rg *RaftGroup) TryAddEvent(raftKey string, e types.Event) {
	if e.Type == types.SendPropose {
		rg.handleSendPropose(raftKey, e)
		return
	} else if e.Type == types.SendProposeResp {
		rg.handleSendProposeResp(e)
		return
	}

	rg.mq.Add(Event{
		RaftKey: raftKey,
		Event:   e,
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
			rg.Error("raft not found", zap.String("raftKey", e.RaftKey), zap.String("event", e.Event.String()))
			if e.WaitC != nil {
				e.WaitC <- ErrRaftNotExist
			}
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
		case types.Destory: // 处理销毁请求
			rg.handleDestory(r)
			continue

			// 角色转换
		case types.LearnerToFollowerReq,
			types.LearnerToLeaderReq,
			types.FollowerToLeaderReq:
			rg.handleRoleChangeReq(r, e)
			continue
		}
		if e.To == 0 {
			rg.Foucs("none node event", zap.Any("event", e))
			continue
		}
		if e.To == types.LocalNode {
			err := r.Step(e)
			if err != nil {
				rg.Error("step error", zap.Error(err))
			}
			continue
		}
		rg.opts.Transport.Send(r.Key(), e)
	}
	return true
}
