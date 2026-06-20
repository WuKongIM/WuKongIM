package multiraft

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"go.etcd.io/raft/v3/raftpb"
)

type applyTask struct {
	slot *slot
	// entries is the committed normal-entry span accepted after Ready persistence.
	entries []raftpb.Entry
	// appliedBefore is the durable applied index before this task's Ready.
	appliedBefore uint64
}

type applyQueue struct {
	slotID  SlotID
	tasks   []applyTask
	running bool
	closed  bool
}

type applyPipeline struct {
	mu     sync.Mutex
	cond   *sync.Cond
	wg     sync.WaitGroup
	closed bool
	queues map[SlotID]*applyQueue
	ready  []*applyQueue
}

func newApplyPipeline(workers int, goroutines *goroutine.Registry) *applyPipeline {
	if workers <= 0 {
		workers = 1
	}
	p := &applyPipeline{
		queues: make(map[SlotID]*applyQueue),
	}
	p.cond = sync.NewCond(&p.mu)
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		goroutine.SafeGo(goroutines, "slot", "raft_apply_worker", func() {
			defer p.wg.Done()
			p.runWorker()
		})
	}
	return p
}

func (p *applyPipeline) enqueue(task applyTask) error {
	if p == nil || task.slot == nil {
		return ErrSlotClosed
	}
	slotID := task.slot.id

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrRuntimeClosed
	}
	p.mu.Unlock()

	if err := task.slot.beginApply(); err != nil {
		return err
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		task.slot.finishApply()
		return ErrRuntimeClosed
	}

	q := p.queues[slotID]
	if q == nil {
		q = &applyQueue{slotID: slotID}
		p.queues[slotID] = q
	}
	if q.closed {
		p.mu.Unlock()
		task.slot.finishApply()
		return ErrSlotClosed
	}
	q.tasks = append(q.tasks, task)
	p.scheduleLocked(q)
	p.mu.Unlock()
	return nil
}

func (p *applyPipeline) scheduleLocked(q *applyQueue) {
	if q == nil || q.running || len(q.tasks) == 0 {
		return
	}
	q.running = true
	p.ready = append(p.ready, q)
	p.cond.Signal()
}

func (p *applyPipeline) runWorker() {
	for {
		p.mu.Lock()
		for len(p.ready) == 0 && !p.closed {
			p.cond.Wait()
		}
		if len(p.ready) == 0 && p.closed {
			p.mu.Unlock()
			return
		}
		q := p.ready[0]
		copy(p.ready, p.ready[1:])
		p.ready[len(p.ready)-1] = nil
		p.ready = p.ready[:len(p.ready)-1]
		p.mu.Unlock()

		p.runQueue(q)
	}
}

func (p *applyPipeline) runQueue(q *applyQueue) {
	for {
		task, ok := p.popTask(q)
		if !ok {
			p.mu.Lock()
			if len(q.tasks) == 0 {
				q.running = false
				if q.closed {
					delete(p.queues, q.slotID)
				}
				p.mu.Unlock()
				return
			}
			p.mu.Unlock()
			continue
		}
		task.slot.runApplyTask(context.Background(), task)
	}
}

func (p *applyPipeline) popTask(q *applyQueue) (applyTask, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if q == nil || len(q.tasks) == 0 {
		return applyTask{}, false
	}
	task := q.tasks[0]
	var zero applyTask
	q.tasks[0] = zero
	q.tasks = q.tasks[1:]
	return task, true
}

func (p *applyPipeline) close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *applyPipeline) closeSlot(slotID SlotID) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	q := p.queues[slotID]
	if q == nil {
		return
	}
	q.closed = true
	if !q.running && len(q.tasks) == 0 {
		delete(p.queues, slotID)
	}
}

func (g *slot) runApplyTask(ctx context.Context, task applyTask) {
	defer g.finishApply()
	if ctx == nil {
		ctx = context.Background()
	}
	if g.hasFatalErr() {
		return
	}

	lastApplied := task.appliedBefore
	resolutions := g.takeResolutionBuffer()
	defer func() {
		g.releaseResolutionBuffer(resolutions)
	}()

	batchSM, canBatch := g.stateMachine.(BatchStateMachine)
	resolutions, _ = g.applyCommittedEntries(ctx, task.entries, &lastApplied, resolutions, batchSM, canBatch)
	if g.hasFatalErr() {
		return
	}

	if lastApplied > task.appliedBefore {
		started := time.Now()
		err := g.storage.MarkApplied(ctx, lastApplied)
		g.observeResolutionFutures(resolutions, "meta_create_slot_mark_applied", err, time.Since(started))
		if err != nil {
			g.fail(err)
			return
		}
		g.setDurableAppliedIndex(lastApplied)
	}

	g.refreshDurableAppliedStatus()
	g.completeResolutions(resolutions)
	if g.compactor.shouldCompact(lastApplied) {
		if err := g.compactLog(ctx, lastApplied); err != nil {
			g.logCompactionWarning(err, lastApplied)
		} else {
			g.compactor.recordSnapshot(lastApplied)
		}
	}
}
