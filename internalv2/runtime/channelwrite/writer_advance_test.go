package channelwrite

import (
	"context"
	"testing"
	"time"
)

func TestWriterAdvanceAppendsInOrder(t *testing.T) {
	appender := &orderedAppender{}
	rt := newWriterRuntime(t, writerRuntimeConfig{appender: appender})
	defer rt.stop()

	target := benchmarkAuthorityTarget("order-1")
	const batches = 50
	futures := make([]*Future, batches)
	for i := 0; i < batches; i++ {
		f, err := rt.submit(target, []SendBatchItem{benchmarkSendItem("order-1")})
		if err != nil {
			t.Fatalf("submit %d error = %v", i, err)
		}
		futures[i] = f
	}
	for i, f := range futures {
		res, err := f.Wait(context.Background())
		if err != nil {
			t.Fatalf("wait %d error = %v", i, err)
		}
		if len(res) != 1 || res[0].Err != nil || res[0].Result.Reason != ReasonSuccess {
			t.Fatalf("batch %d result = %#v, want success", i, res)
		}
	}
	if !appender.seqsMonotonic() {
		t.Fatalf("append message seqs not monotonic: %v", appender.seqs())
	}
}

func TestWriterAdvanceCompletesWithinDeadline(t *testing.T) {
	rt := newWriterRuntime(t, writerRuntimeConfig{appender: &orderedAppender{}})
	defer rt.stop()
	target := benchmarkAuthorityTarget("deadline-1")
	f, err := rt.submit(target, []SendBatchItem{benchmarkSendItem("deadline-1")})
	if err != nil {
		t.Fatalf("submit error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := f.Wait(ctx); err != nil {
		t.Fatalf("wait error = %v", err)
	}
}
