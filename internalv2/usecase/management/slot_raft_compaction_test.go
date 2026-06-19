package management

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSlotRaftCompactLogReturnsNodeSlotResult(t *testing.T) {
	generatedAt := time.Date(2026, 6, 19, 11, 0, 0, 0, time.UTC)
	operator := &fakeSlotRaftOperator{
		results: map[slotRaftCompactTarget]SlotRaftCompactionResult{
			{nodeID: 2, slotID: 9}: {
				NodeID:              2,
				SlotID:              9,
				AppliedIndex:        50,
				BeforeSnapshotIndex: 40,
				AfterSnapshotIndex:  50,
				Compacted:           true,
			},
		},
	}
	app := New(Options{
		SlotRaft: operator,
		Now:      func() time.Time { return generatedAt },
	})

	got, err := app.CompactSlotRaftLog(context.Background(), 2, 9)

	if err != nil {
		t.Fatalf("CompactSlotRaftLog() error = %v", err)
	}
	if !got.GeneratedAt.Equal(generatedAt) || got.Total != 1 || got.Succeeded != 1 || got.Failed != 0 {
		t.Fatalf("summary = %#v, want one success at generated time", got)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items length = %d, want 1", len(got.Items))
	}
	item := got.Items[0]
	if item.NodeID != 2 || item.SlotID != 9 || !item.Success || !item.Compacted || item.AppliedIndex != 50 || item.BeforeSnapshotIndex != 40 || item.AfterSnapshotIndex != 50 {
		t.Fatalf("item = %#v, want compacted node 2 slot 9", item)
	}
	if len(operator.called) != 1 || operator.called[0] != (slotRaftCompactTarget{nodeID: 2, slotID: 9}) {
		t.Fatalf("called = %#v, want node 2 slot 9", operator.called)
	}
}

func TestSlotRaftCompactLogPreservesAttemptFailure(t *testing.T) {
	operator := &fakeSlotRaftOperator{
		errors: map[slotRaftCompactTarget]error{
			{nodeID: 2, slotID: 9}: errors.New("slot compact unavailable"),
		},
	}
	app := New(Options{SlotRaft: operator})

	got, err := app.CompactSlotRaftLog(context.Background(), 2, 9)

	if err != nil {
		t.Fatalf("CompactSlotRaftLog() error = %v", err)
	}
	if got.Succeeded != 0 || got.Failed != 1 || len(got.Items) != 1 {
		t.Fatalf("summary = %#v, want one preserved failure", got)
	}
	item := got.Items[0]
	if item.Success || item.NodeID != 2 || item.SlotID != 9 || item.Error != "slot compact unavailable" {
		t.Fatalf("item = %#v, want failed node 2 slot 9", item)
	}
}

func TestSlotRaftCompactLogRequiresOperatorAndPositiveIDs(t *testing.T) {
	app := New(Options{})

	if _, err := app.CompactSlotRaftLog(context.Background(), 0, 9); err == nil {
		t.Fatalf("CompactSlotRaftLog(node 0) error = nil, want invalid argument")
	}
	if _, err := app.CompactSlotRaftLog(context.Background(), 2, 0); err == nil {
		t.Fatalf("CompactSlotRaftLog(slot 0) error = nil, want invalid argument")
	}
	if _, err := app.CompactSlotRaftLog(context.Background(), 2, 9); !errors.Is(err, ErrSlotRaftOperatorUnavailable) {
		t.Fatalf("CompactSlotRaftLog() error = %v, want %v", err, ErrSlotRaftOperatorUnavailable)
	}
}

type slotRaftCompactTarget struct {
	nodeID uint64
	slotID uint32
}

type fakeSlotRaftOperator struct {
	called  []slotRaftCompactTarget
	results map[slotRaftCompactTarget]SlotRaftCompactionResult
	errors  map[slotRaftCompactTarget]error
}

func (f *fakeSlotRaftOperator) CompactSlotRaftLog(_ context.Context, nodeID uint64, slotID uint32) (SlotRaftCompactionResult, error) {
	target := slotRaftCompactTarget{nodeID: nodeID, slotID: slotID}
	f.called = append(f.called, target)
	if err := f.errors[target]; err != nil {
		return SlotRaftCompactionResult{NodeID: nodeID, SlotID: slotID, Error: err.Error()}, err
	}
	result := f.results[target]
	result.NodeID = nodeID
	result.SlotID = slotID
	return result, nil
}
