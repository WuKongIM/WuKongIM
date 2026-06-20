package multiraft

import "testing"

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
