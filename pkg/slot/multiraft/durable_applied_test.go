package multiraft

import (
	"context"
	"testing"
)

type durableAppliedTestStateMachine struct {
	index uint64
}

func (*durableAppliedTestStateMachine) Apply(context.Context, Command) ([]byte, error) {
	return nil, nil
}
func (*durableAppliedTestStateMachine) Restore(context.Context, Snapshot) error { return nil }
func (*durableAppliedTestStateMachine) Snapshot(context.Context) (Snapshot, error) {
	return Snapshot{}, nil
}
func (s *durableAppliedTestStateMachine) DurableAppliedIndex(context.Context) (uint64, error) {
	return s.index, nil
}

type markAppliedCountingStorage struct {
	*internalFakeStorage
	markAppliedCalls int
}

func (s *markAppliedCountingStorage) MarkApplied(ctx context.Context, index uint64) error {
	s.markAppliedCalls++
	return s.internalFakeStorage.MarkApplied(ctx, index)
}

func TestSlotSkipsSecondAppliedWriteWhenStateMachineCommittedWatermark(t *testing.T) {
	storage := &markAppliedCountingStorage{internalFakeStorage: &internalFakeStorage{}}
	g := &slot{
		storage:      storage,
		stateMachine: &durableAppliedTestStateMachine{index: 7},
	}
	if err := g.markApplied(context.Background(), 7); err != nil {
		t.Fatalf("markApplied() error = %v", err)
	}
	if storage.markAppliedCalls != 0 {
		t.Fatalf("Storage.MarkApplied calls = %d, want 0", storage.markAppliedCalls)
	}

	g.stateMachine = &durableAppliedTestStateMachine{index: 6}
	if err := g.markApplied(context.Background(), 7); err != nil {
		t.Fatalf("markApplied(lagging state machine) error = %v", err)
	}
	if storage.markAppliedCalls != 1 {
		t.Fatalf("Storage.MarkApplied calls = %d, want 1 for lagging watermark", storage.markAppliedCalls)
	}
}
