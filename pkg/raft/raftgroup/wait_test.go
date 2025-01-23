package raftgroup

import (
	"testing"
)

func TestApplyWait_didApply(t *testing.T) {
	aw := newWait()

	progress := aw.waitApply("key", 10)
	if progress == nil {
		t.Fatalf("expected non-nil progress")
	}

	aw.didApply("key", 5)
	select {
	case <-progress.waitC:
		t.Fatalf("expected progress to not be done")
	default:

	}

	aw.didApply("key", 10)
	select {
	case <-progress.waitC:
		// expected
	default:
		t.Fatalf("expected progress to be done")
	}
}
