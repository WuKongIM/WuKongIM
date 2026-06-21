package multiraft

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

type Runtime struct {
	opts Options

	mu        sync.RWMutex
	closed    bool
	slots     map[SlotID]*slot
	scheduler *scheduler
	apply     *applyPipeline
	stopCh    chan struct{}
	wg        sync.WaitGroup
	inflight  atomic.Int64
	tickSlots []*slot
}

func New(opts Options) (*Runtime, error) {
	opts.Raft = NormalizeRaftOptions(opts.Raft)
	if opts.NodeID == 0 ||
		opts.TickInterval <= 0 ||
		opts.Workers <= 0 ||
		opts.Transport == nil ||
		opts.Raft.ElectionTick <= 0 ||
		opts.Raft.HeartbeatTick <= 0 ||
		opts.Raft.ElectionTick <= opts.Raft.HeartbeatTick {
		return nil, ErrInvalidOptions
	}
	if err := ValidateRaftOptions(opts.Raft); err != nil {
		return nil, err
	}

	rt := &Runtime{
		opts:      opts,
		slots:     make(map[SlotID]*slot),
		scheduler: newScheduler(opts.Observer),
		apply:     newApplyPipeline(opts.Workers, opts.Goroutines, opts.Observer),
		stopCh:    make(chan struct{}),
	}
	if opts.Observer != nil {
		opts.Observer.SetSchedulerWorkers(opts.Workers)
	}
	rt.start()
	return rt, nil
}

func (r *Runtime) start() {
	for i := 0; i < r.opts.Workers; i++ {
		r.wg.Add(1)
		goroutine.SafeGo(r.opts.Goroutines, "slot", "raft_worker", func() {
			defer r.wg.Done()
			r.runWorker()
		})
	}

	r.wg.Add(1)
	goroutine.SafeGo(r.opts.Goroutines, "slot", "raft_ticker", func() {
		defer r.wg.Done()
		r.runTicker()
	})
}

func (r *Runtime) runWorker() {
	for {
		select {
		case <-r.stopCh:
			return
		case slotID := <-r.scheduler.ch:
			r.scheduler.begin(slotID)
			r.observeSchedulerInflight(int(r.inflight.Add(1)))
			started := time.Now()
			requeue := r.processSlot(slotID)
			r.observeSchedulerTask("process_slot", time.Since(started))
			r.observeSchedulerInflight(int(r.inflight.Add(-1)))
			if r.scheduler.done(slotID) || requeue {
				r.scheduler.requeue(slotID)
			}
		}
	}
}

func (r *Runtime) runTicker() {
	ticker := time.NewTicker(r.opts.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.enqueueTickForOpenSlots()
		}
	}
}

func (r *Runtime) enqueueTickForOpenSlots() {
	slots := r.tickSlots[:0]
	r.mu.RLock()
	for _, g := range r.slots {
		slots = append(slots, g)
	}
	r.mu.RUnlock()
	for _, g := range slots {
		g.markTickPending()
		r.scheduler.enqueue(g.id)
	}
	clear(slots)
	r.tickSlots = slots[:0]
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

	worked := g.processRequests()
	if !g.shouldProcess() {
		return false
	}
	worked = g.processControls(context.Background()) || worked
	if !g.shouldProcess() {
		return false
	}
	worked = g.processTick() || worked
	if !g.shouldProcess() {
		return false
	}
	readyProcessed, requeue := g.processReady(context.Background(), r.opts.Transport)
	if requeue {
		return true
	}
	if readyProcessed {
		return false
	}
	if worked {
		g.refreshStatus()
	}
	return false
}

func (r *Runtime) observeSchedulerInflight(inflight int) {
	if r != nil && r.opts.Observer != nil {
		r.opts.Observer.SetSchedulerInflight(inflight)
	}
}

func (r *Runtime) observeSchedulerTask(task string, d time.Duration) {
	if d < 0 {
		d = 0
	}
	if r != nil && r.opts.Observer != nil {
		r.opts.Observer.ObserveSchedulerTask(task, d)
	}
}
