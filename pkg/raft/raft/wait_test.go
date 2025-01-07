package raft

import (
	"testing"
)

func TestApplyWait_didCommit(t *testing.T) {
	aw := newWait("test")

	progress := aw.waitCommit(10)
	if progress == nil {
		t.Fatalf("expected non-nil progress")
	}

	aw.didCommit(5)
	select {
	case <-progress.waitC:
		t.Fatalf("expected progress to not be done")
	default:

	}

	aw.didCommit(10)
	select {
	case <-progress.waitC:
		// expected
	default:
		t.Fatalf("expected progress to be done")
	}
}

func TestApplyWait_didApply(t *testing.T) {
	aw := newWait("test")

	progress := aw.waitApply(10)
	if progress == nil {
		t.Fatalf("expected non-nil progress")
	}

	aw.didApply(5)
	select {
	case <-progress.waitC:
		t.Fatalf("expected progress to not be done")
	default:

	}

	aw.didApply(10)
	select {
	case <-progress.waitC:
		// expected
	default:
		t.Fatalf("expected progress to be done")
	}
}
