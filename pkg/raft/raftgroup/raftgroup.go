package raftgroup

import (
	"fmt"
	"time"

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

func (rg *RaftGroup) AddRaft(r IRaft) {
	rg.raftList.push(r)
}

func (rg *RaftGroup) RemoveRaft(r IRaft) {
	rg.raftList.remove(r.Key())
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
	// 处理收到的事件
	rg.handleReceivedEvents()
	// 处理产生的事件
	rg.handleReadys()
}

// 处理收到的事件
func (rg *RaftGroup) handleReceivedEvents() bool {
	events := rg.mq.Get()
	if len(events) == 0 {
		return false
	}
	for _, e := range events {
		raft := rg.raftList.get(e.RaftKey)
		if raft == nil {
			continue
		}
		err := raft.Step(e.Event)
		if err != nil {
			rg.Error("step failed", zap.Error(err), zap.String("handleKey", raft.Key()))
		}
	}

	return true
}

func (rg *RaftGroup) handleReadys() {
	rg.raftList.readHandlers(&rg.tmpRafts)
	for _, r := range rg.tmpRafts {
		if r.HasReady() {
			rg.handleReady(r)
		}
	}
	rg.tmpRafts = rg.tmpRafts[:0]
}

func (rg *RaftGroup) ticks() {
	rg.raftList.readHandlers(&rg.tmpRafts)
	for _, r := range rg.tmpRafts {
		r.Tick()
	}
	rg.tmpRafts = rg.tmpRafts[:0]
}

func (rg *RaftGroup) handleReady(r IRaft) {
	events := r.Ready()
	if len(events) == 0 {
		return
	}
	for _, e := range events {
		switch e.Type {
		case types.StoreReq: // 处理存储请求
			rg.handleStoreReq(r, e)
		case types.GetLogsReq: // 处理获取日志请求
			rg.handleGetLogsReq(r, e)
		}
		if e.To == 0 {
			fmt.Println("none node event--->", e)
			continue
		}
		if e.To == rg.opts.NodeId {
			err := r.Step(e)
			if err != nil {
				rg.Error("step error", zap.Error(err))
			}
			continue
		}
		rg.opts.Transport.Send(r, e)
	}
}
