package channelappend

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

func TestAdvancePostCommitReusesEffectWithoutBleed(t *testing.T) {
	group := New(Options{
		LocalNodeID:                1,
		AuthorityShardCount:        1,
		EffectPoolSize:             4,
		AdmissionCapacityPerShard:  4096,
		Appender:                   &orderedAppender{},
		MessageID:                  newBenchmarkMessageIDs(1),
		ConversationActiveAdmitter: benchmarkNoopActiveAdmitter{},
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := group.Stop(ctx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	target := benchmarkAuthorityTarget("reuse-1")
	const batches = 200
	futures := make([]*Future, batches)
	for i := 0; i < batches; i++ {
		f, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{benchmarkSendItem("reuse-1")})
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
			t.Fatalf("batch %d result = %#v, want one successful result", i, res)
		}
	}
}
