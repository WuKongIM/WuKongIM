package multiraft

import (
	"sync"
	"testing"
	"time"
)

func TestApplyPipelineReadyQueuePopsFIFOWithoutShifting(t *testing.T) {
	p := &applyPipeline{}
	first := &applyQueue{slotID: 1}
	second := &applyQueue{slotID: 2}
	third := &applyQueue{slotID: 3}

	p.pushReadyLocked(first)
	p.pushReadyLocked(second)
	p.pushReadyLocked(third)

	got, ok := p.popReadyLocked()
	if !ok || got != first {
		t.Fatalf("first pop = %v ok=%v, want first queue", got, ok)
	}
	if p.readyHead != 1 {
		t.Fatalf("readyHead after one pop = %d, want 1", p.readyHead)
	}
	if len(p.ready) != 3 {
		t.Fatalf("ready queue length after one pop = %d, want 3", len(p.ready))
	}

	got, ok = p.popReadyLocked()
	if !ok || got != second {
		t.Fatalf("second pop = %v ok=%v, want second queue", got, ok)
	}
	got, ok = p.popReadyLocked()
	if !ok || got != third {
		t.Fatalf("third pop = %v ok=%v, want third queue", got, ok)
	}
	if _, ok := p.popReadyLocked(); ok {
		t.Fatal("pop from empty ready queue succeeded")
	}
}

func TestApplyQueuePopsTasksFIFOWithoutShifting(t *testing.T) {
	q := &applyQueue{slotID: 1}
	first := applyTask{appliedBefore: 1}
	second := applyTask{appliedBefore: 2}
	third := applyTask{appliedBefore: 3}

	q.pushTaskLocked(first)
	q.pushTaskLocked(second)
	q.pushTaskLocked(third)

	got, ok := q.popTaskLocked()
	if !ok || got.appliedBefore != first.appliedBefore {
		t.Fatalf("first pop = %+v ok=%v, want first task", got, ok)
	}
	if q.taskHead != 1 {
		t.Fatalf("taskHead after one pop = %d, want 1", q.taskHead)
	}
	if len(q.tasks) != 3 {
		t.Fatalf("task backing length after one pop = %d, want 3", len(q.tasks))
	}
	if got, ok = q.popTaskLocked(); !ok || got.appliedBefore != second.appliedBefore {
		t.Fatalf("second pop = %+v ok=%v, want second task", got, ok)
	}
	if got, ok = q.popTaskLocked(); !ok || got.appliedBefore != third.appliedBefore {
		t.Fatalf("third pop = %+v ok=%v, want third task", got, ok)
	}
	if _, ok := q.popTaskLocked(); ok {
		t.Fatal("pop from empty task queue succeeded")
	}
}

func TestApplyQueueRetirementPreservesInFlightEnqueue(t *testing.T) {
	const slotID SlotID = 3
	g := newTestSlotForDrain()
	g.id = slotID

	q := &applyQueue{
		slotID:  slotID,
		running: true,
	}
	p := &applyPipeline{
		queues: map[SlotID]*applyQueue{slotID: q},
	}
	p.cond = sync.NewCond(&p.mu)

	retireStarted := make(chan struct{})
	releaseRetire := make(chan struct{})
	enqueueStarted := make(chan struct{})
	releaseEnqueue := make(chan struct{})
	var retireOnce sync.Once
	var enqueueOnce sync.Once
	var releaseRetireOnce sync.Once
	var releaseEnqueueOnce sync.Once
	restoreRetireHook := setApplyPipelineBeforeRetireQueueHook(func(id SlotID) {
		if id != slotID {
			return
		}
		retireOnce.Do(func() { close(retireStarted) })
		<-releaseRetire
	})
	defer restoreRetireHook()
	restoreEnqueueHook := setApplyPipelineAfterBeginApplyHook(func(id SlotID) {
		if id != slotID {
			return
		}
		enqueueOnce.Do(func() { close(enqueueStarted) })
		<-releaseEnqueue
	})
	defer restoreEnqueueHook()
	defer releaseRetireOnce.Do(func() { close(releaseRetire) })
	defer releaseEnqueueOnce.Do(func() { close(releaseEnqueue) })

	retireDone := make(chan struct{})
	go func() {
		p.runQueue(q)
		close(retireDone)
	}()
	select {
	case <-retireStarted:
	case <-time.After(time.Second):
		t.Fatal("apply queue retirement did not start")
	}

	enqueueDone := make(chan error, 1)
	go func() {
		enqueueDone <- p.enqueue(applyTask{slot: g})
	}()
	select {
	case <-enqueueStarted:
	case <-time.After(time.Second):
		t.Fatal("apply queue enqueue handoff did not start")
	}

	releaseRetireOnce.Do(func() { close(releaseRetire) })
	select {
	case <-retireDone:
	case <-time.After(time.Second):
		t.Fatal("apply queue retirement did not finish")
	}
	releaseEnqueueOnce.Do(func() { close(releaseEnqueue) })

	select {
	case err := <-enqueueDone:
		if err != nil {
			t.Fatalf("enqueue() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("apply queue enqueue did not finish")
	}

	p.mu.Lock()
	gotQueue := p.queues[slotID]
	taskCount := q.taskLenLocked()
	p.mu.Unlock()
	if gotQueue != q {
		t.Fatalf("queues[%d] = %p, want original queue %p", slotID, gotQueue, q)
	}
	if taskCount != 1 {
		t.Fatalf("queued tasks = %d, want 1", taskCount)
	}
	g.finishApply()
}
