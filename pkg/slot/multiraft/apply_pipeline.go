package multiraft

import (
	"context"
	"sync"
	"sync/atomic"
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
	// queueDepth is the number of pending tasks for this Slot after enqueue.
	queueDepth int
	// storageWork returns the original upstream apply responses to the owner.
	storageWork *storageWork
	// batchTerm and batchLastIndex cache immutable eligibility before the shared
	// pipeline lock; a zero term keeps the original one-task behavior.
	batchTerm      uint64
	batchLastIndex uint64
}

type applyQueue struct {
	slotID SlotID
	tasks  []applyTask
	// taskHead is the next task offset, avoiding front-slice capacity loss.
	taskHead int
	running  bool
	closed   bool
	// enqueueReservations pins this queue while an accepted Slot apply is
	// crossing the Slot-lock to pipeline-lock handoff.
	enqueueReservations int
}

type applyPipeline struct {
	mu     sync.Mutex
	cond   *sync.Cond
	wg     sync.WaitGroup
	closed bool
	queues map[SlotID]*applyQueue
	ready  []*applyQueue
	// readyHead is the next ready queue offset, avoiding O(n) front removal.
	readyHead int
	// observer receives low-cardinality async apply pipeline observations.
	observer ApplyPipelineObserver
}

type applyPipelineHook struct {
	fn func(slotID SlotID)
}

var applyPipelineAfterBeginApplyHook atomic.Pointer[applyPipelineHook]
var applyPipelineBeforeRetireQueueHook atomic.Pointer[applyPipelineHook]

func runApplyPipelineAfterBeginApplyHook(slotID SlotID) {
	hook := applyPipelineAfterBeginApplyHook.Load()
	if hook != nil && hook.fn != nil {
		hook.fn(slotID)
	}
}

func runApplyPipelineBeforeRetireQueueHook(slotID SlotID) {
	hook := applyPipelineBeforeRetireQueueHook.Load()
	if hook != nil && hook.fn != nil {
		hook.fn(slotID)
	}
}

func setApplyPipelineAfterBeginApplyHook(fn func(slotID SlotID)) func() {
	prev := applyPipelineAfterBeginApplyHook.Swap(newApplyPipelineHook(fn))
	return func() {
		applyPipelineAfterBeginApplyHook.Store(prev)
	}
}

func setApplyPipelineBeforeRetireQueueHook(fn func(slotID SlotID)) func() {
	prev := applyPipelineBeforeRetireQueueHook.Swap(newApplyPipelineHook(fn))
	return func() {
		applyPipelineBeforeRetireQueueHook.Store(prev)
	}
}

func newApplyPipelineHook(fn func(slotID SlotID)) *applyPipelineHook {
	if fn == nil {
		return nil
	}
	return &applyPipelineHook{fn: fn}
}

func newApplyPipeline(workers int, goroutines *goroutine.Registry, observer SchedulerObserver) *applyPipeline {
	if workers <= 0 {
		workers = 1
	}
	p := &applyPipeline{
		queues: make(map[SlotID]*applyQueue),
	}
	if applyObserver, ok := observer.(ApplyPipelineObserver); ok {
		p.observer = applyObserver
	}
	p.cond = sync.NewCond(&p.mu)
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		goroutine.SafeGo(goroutines, goroutine.TaskSlotRaftApplyWorker, func() {
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
	if _, ok := task.slot.stateMachine.(BatchStateMachine); ok {
		task.batchLastIndex, task.batchTerm, _ = ordinaryApplyBoundary(task)
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrRuntimeClosed
	}
	q := p.queues[slotID]
	if q == nil {
		q = &applyQueue{slotID: slotID}
		p.queues[slotID] = q
	}
	if q.closed {
		p.mu.Unlock()
		return ErrSlotClosed
	}
	q.enqueueReservations++
	p.mu.Unlock()

	if err := task.slot.beginApply(); err != nil {
		p.mu.Lock()
		q.enqueueReservations--
		p.deleteQueueIfIdleLocked(slotID, q)
		p.mu.Unlock()
		return err
	}
	runApplyPipelineAfterBeginApplyHook(slotID)

	p.mu.Lock()
	q.enqueueReservations--
	if p.closed {
		p.deleteQueueIfIdleLocked(slotID, q)
		p.mu.Unlock()
		task.slot.finishApply()
		return ErrRuntimeClosed
	}
	if p.queues[slotID] != q || q.closed {
		p.deleteQueueIfIdleLocked(slotID, q)
		p.mu.Unlock()
		task.slot.finishApply()
		return ErrSlotClosed
	}
	task.queueDepth = q.taskLenLocked() + 1
	q.pushTaskLocked(task)
	p.scheduleLocked(q)
	p.mu.Unlock()
	p.observeApplyQueue(task)
	return nil
}

func (p *applyPipeline) scheduleLocked(q *applyQueue) {
	if q == nil || q.running || q.taskLenLocked() == 0 {
		return
	}
	q.running = true
	p.pushReadyLocked(q)
	p.cond.Signal()
}

func (p *applyPipeline) runWorker() {
	for {
		p.mu.Lock()
		for p.readyLenLocked() == 0 && !p.closed {
			p.cond.Wait()
		}
		if p.readyLenLocked() == 0 && p.closed {
			p.mu.Unlock()
			return
		}
		q, _ := p.popReadyLocked()
		p.mu.Unlock()

		p.runQueue(q)
	}
}

func (p *applyPipeline) runQueue(q *applyQueue) {
	for {
		var tasks [storageSlotOwners]applyTask
		n := p.popApplyBatch(q, &tasks)
		if n == 0 {
			p.mu.Lock()
			if q.taskLenLocked() == 0 {
				q.running = false
				p.mu.Unlock()
				runApplyPipelineBeforeRetireQueueHook(q.slotID)
				p.mu.Lock()
				p.deleteQueueIfIdleLocked(q.slotID, q)
				p.mu.Unlock()
				return
			}
			p.mu.Unlock()
			continue
		}
		started := time.Now()
		var observed [storageSlotOwners]applyTask
		for i := 0; i < n; i++ {
			observed[i] = applyTask{slot: tasks[i].slot, queueDepth: tasks[i].queueDepth}
		}
		tasks[0].slot.runApplyBatch(context.Background(), tasks[:n])
		for i := 0; i < n; i++ {
			p.observeApplyTask(observed[i], time.Since(started))
		}
	}
}

func (p *applyPipeline) pushReadyLocked(q *applyQueue) {
	p.ready = append(p.ready, q)
}

func (p *applyPipeline) popReadyLocked() (*applyQueue, bool) {
	if p.readyHead >= len(p.ready) {
		return nil, false
	}
	q := p.ready[p.readyHead]
	p.ready[p.readyHead] = nil
	p.readyHead++
	if p.readyHead > 64 && p.readyHead*2 >= len(p.ready) {
		remaining := copy(p.ready, p.ready[p.readyHead:])
		for i := remaining; i < len(p.ready); i++ {
			p.ready[i] = nil
		}
		p.ready = p.ready[:remaining]
		p.readyHead = 0
	}
	return q, true
}

func (p *applyPipeline) readyLenLocked() int {
	return len(p.ready) - p.readyHead
}

func (q *applyQueue) pushTaskLocked(task applyTask) {
	q.tasks = append(q.tasks, task)
}

func (q *applyQueue) popTaskLocked() (applyTask, bool) {
	if q == nil || q.taskHead >= len(q.tasks) {
		return applyTask{}, false
	}
	task := q.tasks[q.taskHead]
	var zero applyTask
	q.tasks[q.taskHead] = zero
	q.taskHead++
	if q.taskHead > 64 && q.taskHead*2 >= len(q.tasks) {
		remaining := copy(q.tasks, q.tasks[q.taskHead:])
		for i := remaining; i < len(q.tasks); i++ {
			q.tasks[i] = zero
		}
		q.tasks = q.tasks[:remaining]
		q.taskHead = 0
	}
	return task, true
}

func (q *applyQueue) taskLenLocked() int {
	if q == nil {
		return 0
	}
	return len(q.tasks) - q.taskHead
}

func (p *applyPipeline) deleteQueueIfIdleLocked(slotID SlotID, q *applyQueue) {
	if q == nil ||
		p.queues[slotID] != q ||
		q.running ||
		q.taskLenLocked() > 0 ||
		q.enqueueReservations > 0 {
		return
	}
	delete(p.queues, slotID)
	p.cond.Broadcast()
}

func (p *applyPipeline) observeApplyQueue(task applyTask) {
	if p == nil || p.observer == nil || task.slot == nil {
		return
	}
	p.observer.ObserveSlotApplyQueue(task.slot.id, task.queueDepth)
}

func (p *applyPipeline) observeApplyTask(task applyTask, d time.Duration) {
	if p == nil || p.observer == nil || task.slot == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	p.observer.ObserveSlotApplyTask(task.slot.id, d)
}

// popApplyBatch freezes only an already-queued ordinary prefix. The original
// Ready leases still own its memory and completion records; no collection wait,
// new capacity or concurrent same-Slot apply is introduced.
func (p *applyPipeline) popApplyBatch(q *applyQueue, tasks *[storageSlotOwners]applyTask) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	first, ok := q.popTaskLocked()
	if !ok {
		return 0
	}
	tasks[0] = first
	if first.batchTerm == 0 {
		return 1
	}
	last, term := first.batchLastIndex, first.batchTerm
	n := 1
	for n < len(tasks) && q.taskLenLocked() > 0 {
		next := q.tasks[q.taskHead]
		if next.batchTerm == 0 || next.slot != first.slot || next.batchTerm != term ||
			next.storageWork.acceptedTerm != first.storageWork.acceptedTerm ||
			last == ^uint64(0) || next.entries[0].Index != last+1 {
			break
		}
		tasks[n], _ = q.popTaskLocked()
		n++
		last = next.batchLastIndex
	}
	return n
}

// ordinaryApplyBoundary excludes configuration, snapshots, gaps and mixed terms.
// Synchronous/unowned embeddings retain their original one-task behavior.
func ordinaryApplyBoundary(task applyTask) (uint64, uint64, bool) {
	w := task.storageWork
	if task.slot == nil || w == nil || w.slot != task.slot || w.lease == nil || w.lease.slot != task.slot || w.acceptedTerm == 0 ||
		!w.hasApply || w.append.Snapshot != nil || len(task.entries) == 0 {
		return 0, 0, false
	}
	first := task.entries[0]
	if first.Index == 0 || first.Term == 0 || first.Term > w.acceptedTerm || task.appliedBefore >= first.Index {
		return 0, 0, false
	}
	previous := first.Index
	for i, e := range task.entries {
		if e.Type != raftpb.EntryNormal || e.Term != first.Term || (i > 0 && (previous == ^uint64(0) || e.Index != previous+1)) {
			return 0, 0, false
		}
		previous = e.Index
	}
	return previous, first.Term, true
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

func (p *applyPipeline) closeSlot(slotID SlotID) *applyQueue {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	q := p.queues[slotID]
	if q == nil {
		return nil
	}
	q.closed = true
	if !q.running && q.taskLenLocked() == 0 && q.enqueueReservations == 0 {
		delete(p.queues, slotID)
		p.cond.Broadcast()
	}
	return q
}

func (p *applyPipeline) waitQueueRetired(slotID SlotID, q *applyQueue) {
	if p == nil || q == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.queues[slotID] == q {
		p.cond.Wait()
	}
}

// runApplyBatch applies one frozen prefix and publishes each original Ready
// completion only after its shared durable boundary or terminal failure.
func (g *slot) runApplyBatch(ctx context.Context, tasks []applyTask) {
	appliedBefore := tasks[0].appliedBefore
	var groups [storageSlotOwners][]raftpb.Entry
	for i := range tasks {
		groups[i] = tasks[i].entries
	}
	defer func() {
		clear(groups[:])
		for i := range tasks {
			work := tasks[i].storageWork
			tasks[i] = applyTask{}
			if work != nil {
				g.publishStorageCompletion(storageCompletion{work: work, part: storageApplyPart, err: g.currentErr()})
			} else {
				g.finishApply()
			}
		}
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	if g.hasFatalErr() {
		return
	}

	lastApplied := appliedBefore
	resolutions := g.takeResolutionBuffer()
	defer func() {
		g.releaseResolutionBuffer(resolutions)
	}()

	batchSM, canBatch := g.stateMachine.(BatchStateMachine)
	resolutions, _ = g.applyCommittedEntries(ctx, groups[:len(tasks)], &lastApplied, resolutions, batchSM, canBatch)
	if g.hasFatalErr() {
		return
	}

	if lastApplied > appliedBefore {
		started := time.Now()
		err := g.markApplied(ctx, lastApplied)
		g.observeResolutionFutures(resolutions, "meta_create_slot_mark_applied", err, time.Since(started))
		if err != nil {
			g.fail(err)
			return
		}
		if err := g.persistConfigAppliedIndex(ctx, lastApplied); err != nil {
			g.fail(err)
			return
		}
		g.setDurableAppliedIndex(lastApplied)
	}

	g.refreshDurableAppliedStatus()
	g.completeResolutions(resolutions)
	g.mu.Lock()
	g.compactionPending = true
	g.mu.Unlock()
}
