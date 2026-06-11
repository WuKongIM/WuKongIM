package channelwrite

import (
	"context"
	"testing"
)

func TestFutureWaitReturnsSnapshotCopy(t *testing.T) {
	future := newFuture(1)
	future.complete([]SendBatchItemResult{{Result: SendResult{Reason: ReasonSuccess}}})

	got, err := future.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait error = %v", err)
	}
	if len(got) != 1 || got[0].Result.Reason != ReasonSuccess {
		t.Fatalf("Wait result = %#v, want success", got)
	}

	got[0].Result.Reason = ReasonSystemError
	again, err := future.Wait(context.Background())
	if err != nil {
		t.Fatalf("second Wait error = %v", err)
	}
	if again[0].Result.Reason != ReasonSuccess {
		t.Fatalf("returned slice aliases internal buffer")
	}
}
