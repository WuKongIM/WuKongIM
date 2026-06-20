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
