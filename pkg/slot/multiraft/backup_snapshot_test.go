package multiraft

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestCaptureHashSlotSnapshotReturnsAppliedBoundary(t *testing.T) {
	runtime := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, runtime, 88)
	future, err := runtime.Propose(context.Background(), slotID, proposalPayload(7, []byte("metadata-v1")))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	result, err := future.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := runtime.CaptureHashSlotSnapshot(ctx, slotID, 7)
	if err != nil {
		t.Fatalf("CaptureHashSlotSnapshot() error = %v", err)
	}
	defer snapshot.Reader.Close()
	body, err := io.ReadAll(snapshot.Reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if snapshot.AppliedIndex != result.Index || snapshot.CommitIndex != result.Index || snapshot.Term != result.Term || snapshot.HashSlot != 7 || snapshot.CapturedAtUnixMillis <= 0 || string(body) != "metadata-v1" {
		t.Fatalf("snapshot = %+v body=%q, want index=%d term=%d hashSlot=7 body=metadata-v1", snapshot, body, result.Index, result.Term)
	}
}
