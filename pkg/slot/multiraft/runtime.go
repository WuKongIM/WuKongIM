package multiraft

import (
	"context"
	"sync"
	"time"
)

type Runtime struct {
	opts Options

	mu        sync.RWMutex
	closed    bool
	slots     map[SlotID]*slot
	scheduler *scheduler
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func New(opts Options) (*Runtime, error) {
	if opts.NodeID == 0 ||
		opts.TickInterval <= 0 ||
		opts.Workers <= 0 ||
		opts.Transport == nil ||
		opts.Raft.ElectionTick <= 0 ||
		opts.Raft.HeartbeatTick <= 0 ||
		opts.Raft.ElectionTick <= opts.Raft.HeartbeatTick {
		return nil, ErrInvalidOptions
	}

	rt := &Runtime{
		opts:      opts,
		slots:     make(map[SlotID]*slot),
		scheduler: newScheduler(),
		stopCh:    make(chan struct{}),
	}
	rt.start()
	return rt, nil
}

func (r *Runtime) start() {
	for i := 0; i < r.opts.Workers; i++ {
		r.wg.Add(1)
		go r.runWorker()
	}

	r.wg.Add(1)
	go r.runTicker()
}

func (r *Runtime) runWorker() {
	defer r.wg.Done()

	for {
		select {
		case <-r.stopCh:
			return
		case slotID := <-r.scheduler.ch:
			r.scheduler.begin(slotID)
			requeue := r.processSlot(slotID)
			if r.scheduler.done(slotID) || requeue {
				r.scheduler.requeue(slotID)
			}
		}
	}
}

func (r *Runtime) runTicker() {
	defer r.wg.Done()

	ticker := time.NewTicker(r.opts.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.mu.RLock()
			slots := make([]*slot, 0, len(r.slots))
			for _, g := range r.slots {
				slots = append(slots, g)
			}
			r.mu.RUnlock()
			for _, g := range slots {
				g.markTickPending()
				r.scheduler.enqueue(g.id)
			}
		}
	}
}

func (r *Runtime) processSlot(slotID SlotID) bool {
	r.mu.RLock()
	g := r.slots[slotID]
	r.mu.RUnlock()
	if g == nil {
		return false
	}
	if !g.beginProcessing() {
		return false
	}
	defer g.finishProcessing()

	g.processRequests()
	if !g.shouldProcess() {
		return false
	}
	g.processControls()
	if !g.shouldProcess() {
		return false
	}
	g.processTick()
	if !g.shouldProcess() {
		return false
	}
	if g.processReady(context.Background(), r.opts.Transport) {
		g.refreshStatus()
		return true
	}
	g.refreshStatus()
	return false
}
