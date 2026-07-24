package workqueue

import (
	"context"
	"errors"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/panjf2000/ants/v2"
)

// MailboxBatch is one ordered shard-local batch delivered to a mailbox handler.
type MailboxBatch[T any] struct {
	// Shard is the shard index that owns this batch.
	Shard int
	// Items are delivered in shard admission order. The slice is valid only
	// during the handler call; handlers must copy it before retaining it.
	Items []T
}

// ShardedMailboxHandler processes one ordered shard-local item batch.
type ShardedMailboxHandler[T any] func(context.Context, MailboxBatch[T]) error

// ShardedMailboxConfig defines shard, queue, worker, and batch limits.
type ShardedMailboxConfig struct {
	// Name is a stable low-cardinality name used in observations.
	Name string
	// Goroutines receives lifecycle and pool ownership observations.
	Goroutines *goruntimeregistry.Registry
	// Task is the fixed module/task owner of this mailbox and its auxiliary goroutines.
	Task goruntimeregistry.TaskID
	// Shards is the number of hash partitions.
	Shards int
	// Workers is the maximum number of concurrently draining shards.
	Workers int
	// QueueSizePerShard bounds queued items inside each shard.
	QueueSizePerShard int
	// BatchMaxItems caps one handler batch. Values <= 0 use 1.
	BatchMaxItems int
	// BatchMaxWait bounds how long a shard waits for adjacent batch peers.
	BatchMaxWait time.Duration
	// ReleaseTimeout bounds graceful ants pool release after Close.
	ReleaseTimeout time.Duration
	// Observer receives low-level mailbox events. It must be concurrency-safe and non-blocking.
	Observer ShardedMailboxObserver
}

// ShardedMailbox owns bounded shard mailboxes and scheduled shard drains.
type ShardedMailbox[T any] struct {
	cfg     ShardedMailboxConfig
	handler ShardedMailboxHandler[T]
	shards  []*mailboxShard[T]

	ctx      context.Context
	cancel   context.CancelFunc
	closedCh chan struct{}
	pool     *ants.PoolWithFuncGeneric[*mailboxShard[T]]

	closed   atomic.Bool
	running  atomic.Int64
	rejected atomic.Int64

	closeOnce      sync.Once
	closeErr       error
	wg             sync.WaitGroup
	unregisterPool func()
}

type mailboxShard[T any] struct {
	parent *ShardedMailbox[T]
	id     int
	queue  chan mailboxItem[T]

	mu        sync.Mutex
	scheduled bool
	closed    bool
	batch     []T
}

type mailboxItem[T any] struct {
	value      T
	enqueuedAt time.Time
}

// NewShardedMailbox starts a bounded sharded mailbox.
func NewShardedMailbox[T any](cfg ShardedMailboxConfig, handler ShardedMailboxHandler[T]) (*ShardedMailbox[T], error) {
	if cfg.Shards <= 0 || cfg.Workers <= 0 || cfg.QueueSizePerShard <= 0 || handler == nil {
		return nil, ErrInvalidConfig
	}
	if cfg.BatchMaxItems <= 0 {
		cfg.BatchMaxItems = 1
	}
	if cfg.ReleaseTimeout <= 0 {
		cfg.ReleaseTimeout = defaultReleaseTimeout
	}
	cfg.Goroutines, cfg.Task = normalizeOwnership(cfg.Goroutines, cfg.Task)
	ctx, cancel := context.WithCancel(context.Background())
	m := &ShardedMailbox[T]{
		cfg:      cfg,
		handler:  handler,
		shards:   make([]*mailboxShard[T], cfg.Shards),
		ctx:      ctx,
		cancel:   cancel,
		closedCh: make(chan struct{}),
	}
	for i := range m.shards {
		m.shards[i] = &mailboxShard[T]{
			parent: m,
			id:     i,
			queue:  make(chan mailboxItem[T], cfg.QueueSizePerShard),
		}
	}
	pool, err := ants.NewPoolWithFuncGeneric[*mailboxShard[T]](
		cfg.Workers,
		m.drainScheduledShard,
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
		ants.WithPanicHandler(poolPanicHandler(cfg.Goroutines, cfg.Task)),
	)
	if err != nil {
		cancel()
		return nil, err
	}
	m.pool = pool
	unregister, err := cfg.Goroutines.RegisterPool(cfg.Task, m.poolStats)
	if err != nil {
		pool.Release()
		cancel()
		return nil, err
	}
	m.unregisterPool = unregister
	m.observeCapacity()
	return m, nil
}

// Submit admits item into the shard selected by key.
func (m *ShardedMailbox[T]) Submit(ctx context.Context, key string, item T) error {
	return m.SubmitHash(ctx, hashString(key), item)
}

// SubmitHash admits item into the shard selected by hash.
func (m *ShardedMailbox[T]) SubmitHash(ctx context.Context, hash uint64, item T) error {
	if m == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		m.observeAdmission(-1, contextResult(err))
		return err
	}
	if m.closed.Load() || len(m.shards) == 0 {
		m.observeAdmission(-1, resultClosed)
		return ErrClosed
	}
	shard := m.shards[int(hash%uint64(len(m.shards)))]
	if shard == nil {
		m.observeAdmission(-1, resultClosed)
		return ErrClosed
	}
	itemEnvelope := mailboxItem[T]{value: item, enqueuedAt: time.Now()}
	shouldSchedule := false

	shard.mu.Lock()
	if shard.closed || m.closed.Load() {
		shard.mu.Unlock()
		m.observeAdmission(shard.id, resultClosed)
		return ErrClosed
	}
	if len(shard.queue) >= cap(shard.queue) {
		shard.mu.Unlock()
		m.rejected.Add(1)
		m.observeAdmission(shard.id, resultFull)
		return ErrFull
	}
	shard.queue <- itemEnvelope
	if !shard.scheduled {
		shard.scheduled = true
		shouldSchedule = true
		m.wg.Add(1)
	}
	depth := len(shard.queue)
	shard.mu.Unlock()

	m.observeAdmission(shard.id, resultOK)
	m.observeDepth(shard.id, depth)
	if shouldSchedule {
		m.invokeShard(shard)
	}
	return nil
}

// Close closes admission and drains already accepted shard work until ctx expires.
func (m *ShardedMailbox[T]) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.closeOnce.Do(func() {
		m.closed.Store(true)
		close(m.closedCh)
		for _, shard := range m.shards {
			shard.mu.Lock()
			shard.closed = true
			shard.mu.Unlock()
		}
		done := make(chan struct{})
		goruntimeregistry.SafeGo(m.cfg.Goroutines, m.cfg.Task, func() {
			m.wg.Wait()
			close(done)
		})
		select {
		case <-done:
			m.closeErr = nil
		case <-ctx.Done():
			m.cancel()
			m.closeErr = ctx.Err()
		}
		if m.pool != nil {
			releaseOwnedPool(
				m.cfg.Goroutines,
				m.cfg.Task,
				func() error { return m.pool.ReleaseTimeout(m.cfg.ReleaseTimeout) },
				m.pool.Running,
				m.unregisterPool,
			)
		}
		m.cancel()
	})
	return m.closeErr
}

func (m *ShardedMailbox[T]) poolStats() goruntimeregistry.PoolStats {
	if m == nil {
		return goruntimeregistry.PoolStats{}
	}
	stats := goruntimeregistry.PoolStats{
		BusyTasks:     m.running.Load(),
		Capacity:      int64(m.cfg.Workers),
		QueueDepth:    int64(m.QueueDepth()),
		QueueCapacity: int64(m.cfg.Shards) * int64(m.cfg.QueueSizePerShard),
		RejectedTotal: m.rejected.Load(),
	}
	if m.pool != nil {
		stats.Goroutines = poolGoroutines(m.pool.Running(), m.pool.IsClosed())
	}
	return stats
}

// QueueDepth returns the current queued item count across all shards.
func (m *ShardedMailbox[T]) QueueDepth() int {
	if m == nil {
		return 0
	}
	total := 0
	for _, shard := range m.shards {
		if shard != nil {
			total += len(shard.queue)
		}
	}
	return total
}

func (m *ShardedMailbox[T]) invokeShard(shard *mailboxShard[T]) {
	if m == nil || shard == nil {
		return
	}
	err := m.pool.Invoke(shard)
	switch {
	case err == nil:
		return
	case errors.Is(err, ants.ErrPoolOverload):
		timer := time.NewTimer(executorRetryDelay)
		goruntimeregistry.SafeGo(m.cfg.Goroutines, m.cfg.Task, func() {
			defer timer.Stop()
			select {
			case <-timer.C:
				m.invokeShard(shard)
			case <-m.ctx.Done():
				m.finishShardDrain(shard)
			}
		})
	case errors.Is(err, ants.ErrPoolClosed):
		m.finishShardDrain(shard)
	default:
		m.finishShardDrain(shard)
	}
}

func (m *ShardedMailbox[T]) drainScheduledShard(shard *mailboxShard[T]) {
	if m == nil || shard == nil {
		return
	}
	running := int(m.running.Add(1))
	m.observeWorker(shard, running)
	defer func() {
		running := int(m.running.Add(-1))
		m.observeWorker(shard, running)
		m.finishShardDrain(shard)
	}()
	for {
		first, ok := shard.nextItem()
		if !ok {
			return
		}
		items, wait := shard.collectBatch(first)
		if len(items) == 0 {
			continue
		}
		m.runBatchHandler(shard, items, wait)
	}
}

func (m *ShardedMailbox[T]) runBatchHandler(shard *mailboxShard[T], items []T, wait time.Duration) {
	started := time.Now()
	var err error
	defer func() {
		if v := recover(); v != nil {
			m.observeBatch(shard, len(items), wait, time.Since(started), resultPanic, nil)
			panic(v)
		}
		m.observeBatch(shard, len(items), wait, time.Since(started), errorResult(err), err)
	}()
	err = m.handler(m.ctx, MailboxBatch[T]{Shard: shard.id, Items: items})
}

func (m *ShardedMailbox[T]) finishShardDrain(shard *mailboxShard[T]) {
	if shard == nil {
		return
	}
	shard.mu.Lock()
	shard.scheduled = false
	needsSchedule := len(shard.queue) > 0 && !shard.closed && !shard.parent.closed.Load()
	if needsSchedule {
		shard.scheduled = true
	}
	shard.mu.Unlock()
	if needsSchedule {
		shard.parent.invokeShard(shard)
		return
	}
	shard.parent.wg.Done()
}

func (s *mailboxShard[T]) nextItem() (mailboxItem[T], bool) {
	for {
		select {
		case item := <-s.queue:
			s.parent.observeDepth(s.id, len(s.queue))
			return item, true
		default:
		}
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.mu.Unlock()
			return mailboxItem[T]{}, false
		}
		s.mu.Unlock()
	}
}

func (s *mailboxShard[T]) collectBatch(first mailboxItem[T]) ([]T, time.Duration) {
	maxItems := s.parent.cfg.BatchMaxItems
	if maxItems <= 0 {
		maxItems = 1
	}
	if cap(s.batch) < maxItems {
		s.batch = make([]T, 0, maxItems)
	}
	items := s.batch[:0]
	items = append(items, first.value)
	if maxItems <= 1 {
		s.batch = items
		return items, time.Since(first.enqueuedAt)
	}
	deadline := time.Now().Add(s.parent.cfg.BatchMaxWait)
	for len(items) < maxItems {
		select {
		case next := <-s.queue:
			items = append(items, next.value)
			s.parent.observeDepth(s.id, len(s.queue))
			continue
		default:
		}
		if s.parent.cfg.BatchMaxWait <= 0 {
			break
		}
		if s.parent.closed.Load() {
			return items, time.Since(first.enqueuedAt)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		timer := time.NewTimer(remaining)
		select {
		case next := <-s.queue:
			timer.Stop()
			items = append(items, next.value)
			s.parent.observeDepth(s.id, len(s.queue))
		case <-timer.C:
			return items, time.Since(first.enqueuedAt)
		case <-s.parent.closedCh:
			timer.Stop()
			return items, time.Since(first.enqueuedAt)
		case <-s.parent.ctx.Done():
			timer.Stop()
			return items, time.Since(first.enqueuedAt)
		}
	}
	s.batch = items
	return items, time.Since(first.enqueuedAt)
}

func (m *ShardedMailbox[T]) observeCapacity() {
	if m == nil || m.cfg.Observer == nil {
		return
	}
	for _, shard := range m.shards {
		m.observe(ShardedMailboxObservation{
			Name:          m.cfg.Name,
			Shard:         shard.id,
			Kind:          observationCapacity,
			QueueCapacity: m.cfg.QueueSizePerShard,
			Workers:       m.cfg.Workers,
		})
	}
}

func (m *ShardedMailbox[T]) observeAdmission(shard int, result string) {
	m.observe(ShardedMailboxObservation{
		Name:          m.cfg.Name,
		Shard:         shard,
		Kind:          observationAdmission,
		Result:        result,
		QueueCapacity: m.cfg.QueueSizePerShard,
		Workers:       m.cfg.Workers,
	})
}

func (m *ShardedMailbox[T]) observeDepth(shard int, depth int) {
	m.observe(ShardedMailboxObservation{
		Name:          m.cfg.Name,
		Shard:         shard,
		Kind:          observationDepth,
		QueueDepth:    depth,
		QueueCapacity: m.cfg.QueueSizePerShard,
		Workers:       m.cfg.Workers,
	})
}

func (m *ShardedMailbox[T]) observeWorker(shard *mailboxShard[T], running int) {
	waiting := 0
	if m.pool != nil {
		waiting = m.pool.Waiting()
	}
	shardID := -1
	depth := 0
	if shard != nil {
		shardID = shard.id
		depth = len(shard.queue)
	}
	m.observe(ShardedMailboxObservation{
		Name:          m.cfg.Name,
		Shard:         shardID,
		Kind:          observationWorker,
		QueueDepth:    depth,
		QueueCapacity: m.cfg.QueueSizePerShard,
		Running:       running,
		Workers:       m.cfg.Workers,
		Waiting:       waiting,
	})
}

func (m *ShardedMailbox[T]) observeBatch(shard *mailboxShard[T], batchSize int, wait time.Duration, duration time.Duration, result string, err error) {
	shardID := -1
	depth := 0
	if shard != nil {
		shardID = shard.id
		depth = len(shard.queue)
	}
	m.observe(ShardedMailboxObservation{
		Name:          m.cfg.Name,
		Shard:         shardID,
		Kind:          observationBatch,
		Result:        result,
		QueueDepth:    depth,
		QueueCapacity: m.cfg.QueueSizePerShard,
		Running:       int(m.running.Load()),
		Workers:       m.cfg.Workers,
		BatchSize:     batchSize,
		Wait:          nonNegativeDuration(wait),
		Duration:      nonNegativeDuration(duration),
		Err:           err,
	})
}

func (m *ShardedMailbox[T]) observe(obs ShardedMailboxObservation) {
	if m == nil || m.cfg.Observer == nil {
		return
	}
	m.cfg.Observer.ObserveShardedMailbox(obs)
}

func hashString(value string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum64()
}
