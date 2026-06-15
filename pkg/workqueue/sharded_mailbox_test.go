package workqueue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestShardedMailboxBatchesAdjacentItems(t *testing.T) {
	batches := make(chan []int, 1)
	mailbox, err := NewShardedMailbox[int](ShardedMailboxConfig{
		Name:              "send",
		Shards:            1,
		Workers:           1,
		QueueSizePerShard: 4,
		BatchMaxItems:     2,
		BatchMaxWait:      50 * time.Millisecond,
	}, func(ctx context.Context, batch MailboxBatch[int]) error {
		batches <- append([]int(nil), batch.Items...)
		return nil
	})
	if err != nil {
		t.Fatalf("NewShardedMailbox() error = %v", err)
	}
	defer func() {
		_ = mailbox.Close(context.Background())
	}()

	if err := mailbox.Submit(context.Background(), "same", 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	if err := mailbox.Submit(context.Background(), "same", 2); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}

	select {
	case got := <-batches:
		if len(got) != 2 || got[0] != 1 || got[1] != 2 {
			t.Fatalf("batch = %#v, want [1 2]", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for mailbox batch")
	}
}

func TestShardedMailboxRejectsFullShard(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	mailbox, err := NewShardedMailbox[int](ShardedMailboxConfig{
		Name:              "send",
		Shards:            1,
		Workers:           1,
		QueueSizePerShard: 1,
		BatchMaxItems:     1,
	}, func(ctx context.Context, batch MailboxBatch[int]) error {
		if batch.Items[0] == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewShardedMailbox() error = %v", err)
	}
	defer func() {
		close(release)
		_ = mailbox.Close(context.Background())
	}()

	if err := mailbox.Submit(context.Background(), "same", 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	<-entered
	if err := mailbox.Submit(context.Background(), "same", 2); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}
	if err := mailbox.Submit(context.Background(), "same", 3); !errors.Is(err, ErrFull) {
		t.Fatalf("Submit(third) error = %v, want ErrFull", err)
	}
}

func TestShardedMailboxSerializesOneShardDrain(t *testing.T) {
	var running atomic.Int64
	var peak atomic.Int64
	var processed atomic.Int64
	done := make(chan struct{})
	mailbox, err := NewShardedMailbox[int](ShardedMailboxConfig{
		Name:              "send",
		Shards:            1,
		Workers:           4,
		QueueSizePerShard: 32,
		BatchMaxItems:     1,
	}, func(ctx context.Context, batch MailboxBatch[int]) error {
		current := running.Add(1)
		updatePeak(&peak, current)
		time.Sleep(time.Millisecond)
		running.Add(-1)
		if processed.Add(1) == 16 {
			close(done)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewShardedMailbox() error = %v", err)
	}
	defer func() {
		_ = mailbox.Close(context.Background())
	}()

	for i := 0; i < 16; i++ {
		if err := mailbox.Submit(context.Background(), "same", i); err != nil {
			t.Fatalf("Submit(%d) error = %v", i, err)
		}
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for mailbox drain")
	}
	if got := peak.Load(); got != 1 {
		t.Fatalf("same-shard concurrent handlers = %d, want 1", got)
	}
}

func updatePeak(peak *atomic.Int64, value int64) {
	for {
		current := peak.Load()
		if value <= current {
			return
		}
		if peak.CompareAndSwap(current, value) {
			return
		}
	}
}

func TestShardedMailboxCloseRejectsNewSubmissions(t *testing.T) {
	var mu sync.Mutex
	processed := make([]int, 0, 2)
	mailbox, err := NewShardedMailbox[int](ShardedMailboxConfig{
		Name:              "send",
		Shards:            1,
		Workers:           1,
		QueueSizePerShard: 2,
		BatchMaxItems:     2,
	}, func(ctx context.Context, batch MailboxBatch[int]) error {
		mu.Lock()
		processed = append(processed, batch.Items...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("NewShardedMailbox() error = %v", err)
	}

	if err := mailbox.Submit(context.Background(), "same", 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	if err := mailbox.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := mailbox.Submit(context.Background(), "same", 2); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit(after close) error = %v, want ErrClosed", err)
	}
	mu.Lock()
	got := append([]int(nil), processed...)
	mu.Unlock()
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("processed = %#v, want [1]", got)
	}
}

func TestShardedMailboxCloseFlushesPartialBatchWithoutWaitingForPeers(t *testing.T) {
	var processed atomic.Int64
	mailbox, err := NewShardedMailbox[int](ShardedMailboxConfig{
		Name:              "send",
		Shards:            1,
		Workers:           1,
		QueueSizePerShard: 4,
		BatchMaxItems:     2,
		BatchMaxWait:      200 * time.Millisecond,
	}, func(ctx context.Context, batch MailboxBatch[int]) error {
		processed.Add(int64(len(batch.Items)))
		return nil
	})
	if err != nil {
		t.Fatalf("NewShardedMailbox() error = %v", err)
	}

	if err := mailbox.Submit(context.Background(), "same", 1); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := mailbox.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v, want partial batch to flush before BatchMaxWait", err)
	}
	if got := processed.Load(); got != 1 {
		t.Fatalf("processed = %d, want 1 accepted item", got)
	}
}

func TestShardedMailboxBatchObservationReportsShardLocalDepth(t *testing.T) {
	observer := &recordingMailboxObserver{batchCh: make(chan ShardedMailboxObservation, 8)}
	shardOneEntered := make(chan struct{})
	releaseShardOne := make(chan struct{})
	var closeShardOneEntered sync.Once
	mailbox, err := NewShardedMailbox[int](ShardedMailboxConfig{
		Name:              "send",
		Shards:            2,
		Workers:           2,
		QueueSizePerShard: 4,
		BatchMaxItems:     1,
		Observer:          observer,
	}, func(ctx context.Context, batch MailboxBatch[int]) error {
		if batch.Shard == 1 {
			closeShardOneEntered.Do(func() { close(shardOneEntered) })
			<-releaseShardOne
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewShardedMailbox() error = %v", err)
	}
	defer func() {
		close(releaseShardOne)
		_ = mailbox.Close(context.Background())
	}()

	if err := mailbox.SubmitHash(context.Background(), 1, 10); err != nil {
		t.Fatalf("SubmitHash(shard 1 blocking) error = %v", err)
	}
	select {
	case <-shardOneEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for shard 1 handler")
	}
	if err := mailbox.SubmitHash(context.Background(), 1, 11); err != nil {
		t.Fatalf("SubmitHash(shard 1 queued) error = %v", err)
	}
	if err := mailbox.SubmitHash(context.Background(), 0, 20); err != nil {
		t.Fatalf("SubmitHash(shard 0) error = %v", err)
	}

	var got ShardedMailboxObservation
	for {
		select {
		case got = <-observer.batchCh:
			if got.Shard == 0 {
				if got.QueueDepth != 0 {
					t.Fatalf("shard 0 batch QueueDepth = %d, want shard-local depth 0", got.QueueDepth)
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for shard 0 batch observation")
		}
	}
}

func TestShardedMailboxObservesHandlerPanic(t *testing.T) {
	observer := &recordingMailboxObserver{batchCh: make(chan ShardedMailboxObservation, 8)}
	mailbox, err := NewShardedMailbox[int](ShardedMailboxConfig{
		Name:              "send",
		Shards:            1,
		Workers:           1,
		QueueSizePerShard: 4,
		BatchMaxItems:     1,
		Observer:          observer,
	}, func(ctx context.Context, batch MailboxBatch[int]) error {
		panic("mailbox handler panic")
	})
	if err != nil {
		t.Fatalf("NewShardedMailbox() error = %v", err)
	}
	defer func() {
		_ = mailbox.Close(context.Background())
	}()

	if err := mailbox.Submit(context.Background(), "same", 1); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	for {
		select {
		case got := <-observer.batchCh:
			if got.Result == resultPanic {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for panic batch observation")
		}
	}
}

type recordingMailboxObserver struct {
	batchCh chan ShardedMailboxObservation
}

func (o *recordingMailboxObserver) ObserveShardedMailbox(obs ShardedMailboxObservation) {
	if obs.Kind != observationBatch {
		return
	}
	select {
	case o.batchCh <- obs:
	default:
	}
}
