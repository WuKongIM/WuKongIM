package workqueue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestQueueFacadesExposeCapacityAndNilLifecycleSafely(t *testing.T) {
	var nilPool *BoundedPool[int]
	if !errors.Is(nilPool.Submit(nil, 1), ErrClosed) || !errors.Is(nilPool.SubmitWait(nil, 1), ErrClosed) ||
		nilPool.Close(nil) != nil || nilPool.QueueDepth() != 0 || nilPool.Workers() != 0 ||
		nilPool.QueueCapacity() != 0 || nilPool.poolStats().Capacity != 0 {
		t.Fatal("nil bounded pool did not remain a closed zero-capacity facade")
	}
	pool, err := NewBoundedPool[int](BoundedPoolConfig{Workers: 2, QueueSize: 3}, func(context.Context, int) error { return nil })
	if err != nil {
		t.Fatalf("NewBoundedPool(): %v", err)
	}
	if pool.Workers() != 2 || pool.QueueCapacity() != 3 || pool.poolStats().Capacity != 2 || pool.poolStats().QueueCapacity != 3 {
		t.Fatalf("bounded pool metadata = workers %d capacity %d stats %+v", pool.Workers(), pool.QueueCapacity(), pool.poolStats())
	}
	if err := pool.Close(nil); err != nil {
		t.Fatalf("bounded pool Close(nil): %v", err)
	}

	var nilBatch *BoundedBatchPool[int]
	if !errors.Is(nilBatch.Submit(nil, 1), ErrClosed) || nilBatch.Close(nil) != nil ||
		nilBatch.QueueDepth() != 0 || nilBatch.Workers() != 0 || nilBatch.QueueCapacity() != 0 ||
		!nilBatch.Closed() || nilBatch.poolStats().Capacity != 0 {
		t.Fatal("nil batch pool did not remain a closed zero-capacity facade")
	}
	batch, err := NewBoundedBatchPool[int](BoundedBatchPoolConfig[int]{Workers: 2, QueueSize: 3}, func(context.Context, []int) error { return nil })
	if err != nil {
		t.Fatalf("NewBoundedBatchPool(): %v", err)
	}
	if batch.Workers() != 2 || batch.QueueCapacity() != 3 || batch.Closed() || batch.poolStats().Capacity != 2 || batch.poolStats().QueueCapacity != 3 {
		t.Fatalf("batch pool metadata = workers %d capacity %d closed %v stats %+v", batch.Workers(), batch.QueueCapacity(), batch.Closed(), batch.poolStats())
	}
	if err := batch.Close(nil); err != nil || !batch.Closed() {
		t.Fatalf("batch Close(nil) = %v, closed=%v", err, batch.Closed())
	}

	var nilQueue *BoundedWorkerQueue[int]
	if !errors.Is(nilQueue.Submit(nil, 1), ErrClosed) || !errors.Is(nilQueue.SubmitWait(nil, 1), ErrClosed) ||
		nilQueue.Close(nil) != nil || nilQueue.QueueDepth() != 0 || nilQueue.Workers() != 0 ||
		nilQueue.QueueCapacity() != 0 || !nilQueue.Closed() || nilQueue.poolStats().Capacity != 0 {
		t.Fatal("nil worker queue did not remain a closed zero-capacity facade")
	}
	queue, err := NewBoundedWorkerQueue[int](BoundedWorkerQueueConfig{Workers: 2, QueueSize: 3}, func(context.Context, int) error { return nil })
	if err != nil {
		t.Fatalf("NewBoundedWorkerQueue(): %v", err)
	}
	if queue.Workers() != 2 || queue.QueueCapacity() != 3 || queue.Closed() || queue.poolStats().Capacity != 2 || queue.poolStats().QueueCapacity != 3 {
		t.Fatalf("worker queue metadata = workers %d capacity %d closed %v stats %+v", queue.Workers(), queue.QueueCapacity(), queue.Closed(), queue.poolStats())
	}
	if err := queue.Close(nil); err != nil || !queue.Closed() {
		t.Fatalf("worker queue Close(nil) = %v, closed=%v", err, queue.Closed())
	}

	var nilMailbox *ShardedMailbox[int]
	if !errors.Is(nilMailbox.Submit(nil, "key", 1), ErrClosed) || !errors.Is(nilMailbox.SubmitHash(nil, 1, 1), ErrClosed) ||
		nilMailbox.Close(nil) != nil || nilMailbox.QueueDepth() != 0 || nilMailbox.poolStats().Capacity != 0 {
		t.Fatal("nil mailbox did not remain a closed zero-capacity facade")
	}
}

func TestConstructorsRejectUnboundedOrUnexecutableConfigurations(t *testing.T) {
	poolHandler := func(context.Context, int) error { return nil }
	mailboxHandler := func(context.Context, MailboxBatch[int]) error { return nil }
	for name, create := range map[string]func() error{
		"pool workers": func() error {
			_, err := NewBoundedPool[int](BoundedPoolConfig{Workers: 0, QueueSize: 1}, poolHandler)
			return err
		},
		"pool queue": func() error {
			_, err := NewBoundedPool[int](BoundedPoolConfig{Workers: 1, QueueSize: 0}, poolHandler)
			return err
		},
		"pool handler": func() error {
			_, err := NewBoundedPool[int](BoundedPoolConfig{Workers: 1, QueueSize: 1}, nil)
			return err
		},
		"worker workers": func() error {
			_, err := NewBoundedWorkerQueue[int](BoundedWorkerQueueConfig{Workers: 0, QueueSize: 1}, poolHandler)
			return err
		},
		"worker queue": func() error {
			_, err := NewBoundedWorkerQueue[int](BoundedWorkerQueueConfig{Workers: 1, QueueSize: 0}, poolHandler)
			return err
		},
		"worker handler": func() error {
			_, err := NewBoundedWorkerQueue[int](BoundedWorkerQueueConfig{Workers: 1, QueueSize: 1}, nil)
			return err
		},
		"mailbox shards": func() error {
			_, err := NewShardedMailbox[int](ShardedMailboxConfig{Shards: 0, Workers: 1, QueueSizePerShard: 1}, mailboxHandler)
			return err
		},
		"mailbox workers": func() error {
			_, err := NewShardedMailbox[int](ShardedMailboxConfig{Shards: 1, Workers: 0, QueueSizePerShard: 1}, mailboxHandler)
			return err
		},
		"mailbox queue": func() error {
			_, err := NewShardedMailbox[int](ShardedMailboxConfig{Shards: 1, Workers: 1, QueueSizePerShard: 0}, mailboxHandler)
			return err
		},
		"mailbox handler": func() error {
			_, err := NewShardedMailbox[int](ShardedMailboxConfig{Shards: 1, Workers: 1, QueueSizePerShard: 1}, nil)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := create()
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("constructor error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestAdmissionErrorClassificationIsStableForCallersAndMetrics(t *testing.T) {
	custom := errors.New("custom")
	for _, test := range []struct {
		err  error
		want string
	}{
		{err: nil, want: resultOK},
		{err: ErrFull, want: resultFull},
		{err: ErrClosed, want: resultClosed},
		{err: context.Canceled, want: resultCanceled},
		{err: context.DeadlineExceeded, want: resultTimeout},
		{err: custom, want: resultError},
	} {
		if got := errorResult(test.err); got != test.want {
			t.Fatalf("errorResult(%v) = %q, want %q", test.err, got, test.want)
		}
	}
	if nonNegativeDuration(-time.Second) != 0 || nonNegativeDuration(time.Second) != time.Second {
		t.Fatal("nonNegativeDuration() did not clamp only negative values")
	}
}

func TestWorkerQueueHonorsPreCanceledAndBoundedNonblockingAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := &BoundedWorkerQueue[int]{
		cfg:   BoundedWorkerQueueConfig{Workers: 1, QueueSize: 1},
		queue: make(chan int, 1), slots: make(chan struct{}, 1), stop: make(chan struct{}),
		ctx: ctx, cancel: cancel,
	}
	queue.slots <- struct{}{}
	canceled, cancelSubmit := context.WithCancel(context.Background())
	cancelSubmit()
	if err := queue.Submit(canceled, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Submit() error = %v", err)
	}
	if err := queue.Submit(nil, 1); err != nil {
		t.Fatalf("first Submit(nil): %v", err)
	}
	if err := queue.Submit(context.Background(), 2); !errors.Is(err, ErrFull) {
		t.Fatalf("full Submit() error = %v", err)
	}
	queue.mu.Lock()
	queue.closed = true
	if err := queue.enqueueWithSlotLocked(9); !errors.Is(err, ErrClosed) {
		queue.mu.Unlock()
		t.Fatalf("closed enqueueWithSlotLocked() error = %v", err)
	}
	queue.mu.Unlock()
	if err := queue.SubmitWait(context.Background(), 3); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed SubmitWait() error = %v", err)
	}
	queue.mu.Lock()
	queue.closed = false
	if err := queue.enqueueWithSlotLocked(9); !errors.Is(err, ErrFull) {
		queue.mu.Unlock()
		t.Fatalf("full enqueueWithSlotLocked() error = %v", err)
	}
	queue.closed = true
	queue.mu.Unlock()
	if stats := queue.poolStats(); stats.QueueDepth != 1 || stats.RejectedTotal != 1 {
		t.Fatalf("worker queue stats = %+v", stats)
	}
	queue.releaseSlot()
	queue.releaseSlot()
}

func TestBatchCancellationReleasesEveryAcceptedSlotExactlyOnce(t *testing.T) {
	var mu sync.Mutex
	canceled := make([]int, 0, 2)
	pool := &BoundedBatchPool[int]{
		cfg: BoundedBatchPoolConfig[int]{
			QueueSize: 2,
			CancelAccepted: func(item int, err error) {
				if !errors.Is(err, ErrClosed) {
					t.Errorf("CancelAccepted(%d) error = %v", item, err)
				}
				mu.Lock()
				canceled = append(canceled, item)
				mu.Unlock()
			},
		},
		queue: make(chan boundedBatchPoolTask[int], 2), slots: make(chan struct{}, 2),
	}
	pool.queue <- boundedBatchPoolTask[int]{item: 1}
	pool.queue <- boundedBatchPoolTask[int]{item: 2}
	pool.slots <- struct{}{}
	pool.slots <- struct{}{}
	pool.cancelQueued()
	mu.Lock()
	got := append([]int(nil), canceled...)
	mu.Unlock()
	if len(got) != 2 || got[0] != 1 || got[1] != 2 || pool.QueueDepth() != 0 || len(pool.queue) != 0 {
		t.Fatalf("cancellation = items %v depth %d queued %d", got, pool.QueueDepth(), len(pool.queue))
	}
	pool.cancelTasks(nil)
	pool.releaseSlots(1)
}

func TestBatchCollectionHonorsFirstItemPolicyAndQueueCapacity(t *testing.T) {
	pool := &BoundedBatchPool[int]{
		cfg: BoundedBatchPoolConfig[int]{
			QueueSize: 3,
			Policy: func(first int) BatchOptions {
				return BatchOptions{MaxItems: first, MaxWait: 0}
			},
		},
		queue: make(chan boundedBatchPoolTask[int], 3),
	}
	pool.queue <- boundedBatchPoolTask[int]{item: 2}
	pool.queue <- boundedBatchPoolTask[int]{item: 3}
	batch := pool.collectBatch(boundedBatchPoolTask[int]{item: 10})
	if len(batch) != 3 || batch[0].item != 10 || batch[1].item != 2 || batch[2].item != 3 {
		t.Fatalf("collectBatch() = %+v", batch)
	}
	single := pool.collectBatch(boundedBatchPoolTask[int]{item: 1})
	if len(single) != 1 || single[0].item != 1 {
		t.Fatalf("single collectBatch() = %+v", single)
	}
	empty := []boundedBatchPoolTask[int]{}
	pool.extendBatchReady(&empty)
	pool.extendBatchReady(&single)
}

type mailboxBoundaryObserver struct {
	mu           sync.Mutex
	observations []ShardedMailboxObservation
}

func (o *mailboxBoundaryObserver) ObserveShardedMailbox(observation ShardedMailboxObservation) {
	o.mu.Lock()
	o.observations = append(o.observations, observation)
	o.mu.Unlock()
}

func TestMailboxRejectsCanceledClosedMissingAndFullShardAdmission(t *testing.T) {
	observer := &mailboxBoundaryObserver{}
	ctx, cancel := context.WithCancel(context.Background())
	mailbox := &ShardedMailbox[int]{
		cfg: ShardedMailboxConfig{Shards: 1, Workers: 1, QueueSizePerShard: 1, Observer: observer},
		ctx: ctx, cancel: cancel, closedCh: make(chan struct{}),
	}
	canceled, cancelSubmit := context.WithCancel(context.Background())
	cancelSubmit()
	if err := mailbox.SubmitHash(canceled, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled mailbox admission = %v", err)
	}
	if err := mailbox.SubmitHash(nil, 0, 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("empty-shard mailbox admission = %v", err)
	}
	mailbox.shards = []*mailboxShard[int]{nil}
	if err := mailbox.SubmitHash(context.Background(), 0, 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil-shard mailbox admission = %v", err)
	}
	shard := &mailboxShard[int]{parent: mailbox, id: 0, queue: make(chan mailboxItem[int], 1), closed: true}
	mailbox.shards[0] = shard
	if err := mailbox.SubmitHash(context.Background(), 0, 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed-shard mailbox admission = %v", err)
	}
	shard.closed = false
	shard.scheduled = true
	shard.queue <- mailboxItem[int]{value: 1}
	if err := mailbox.SubmitHash(context.Background(), 0, 2); !errors.Is(err, ErrFull) {
		t.Fatalf("full-shard mailbox admission = %v", err)
	}
	if stats := mailbox.poolStats(); stats.QueueDepth != 1 || stats.QueueCapacity != 1 || stats.RejectedTotal != 1 {
		t.Fatalf("mailbox stats = %+v", stats)
	}
	mailbox.invokeShard(nil)
	(*ShardedMailbox[int])(nil).invokeShard(shard)
}

func TestMailboxShardCollectionPreservesOrderWithoutWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	parent := &ShardedMailbox[int]{
		cfg: ShardedMailboxConfig{BatchMaxItems: 3, BatchMaxWait: 0},
		ctx: ctx, cancel: cancel, closedCh: make(chan struct{}),
	}
	shard := &mailboxShard[int]{parent: parent, id: 2, queue: make(chan mailboxItem[int], 3)}
	shard.queue <- mailboxItem[int]{value: 2}
	shard.queue <- mailboxItem[int]{value: 3}
	items, wait := shard.collectBatch(mailboxItem[int]{value: 1, enqueuedAt: time.Now()})
	if len(items) != 3 || items[0] != 1 || items[1] != 2 || items[2] != 3 || wait < 0 {
		t.Fatalf("collectBatch() = (%v, %v)", items, wait)
	}
	parent.cfg.BatchMaxItems = 0
	items, _ = shard.collectBatch(mailboxItem[int]{value: 4, enqueuedAt: time.Now()})
	if len(items) != 1 || items[0] != 4 {
		t.Fatalf("default single-item collectBatch() = %v", items)
	}

	parent.wg.Add(1)
	shard.scheduled = true
	shard.closed = true
	done := make(chan struct{})
	go func() {
		parent.wg.Wait()
		close(done)
	}()
	parent.finishShardDrain(shard)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("finishShardDrain() did not release shard ownership")
	}
	parent.finishShardDrain(nil)
}
