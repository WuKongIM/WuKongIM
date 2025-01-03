package raftgroup

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/lni/goutils/syncutil"
)

type RaftGroup struct {
	raftList *linkedList
	stopper  *syncutil.Stopper
	opts     *Options
	advanceC chan struct{}

	tmpRafts []IRaft

	stopped bool
}

func New(opts *Options) *RaftGroup {
	return &RaftGroup{
		raftList: newLinkedList(),
		stopper:  syncutil.NewStopper(),
		opts:     opts,
		advanceC: make(chan struct{}, 1),
	}
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
		case types.StoreReq:
			// rg.handleStoreReq(r, e)
		}
	}
}
