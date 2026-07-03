package replica

import (
	"container/heap"
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ExecutionPoolConfig configures shared replica execution workers.
type ExecutionPoolConfig struct {
	// Workers is the number of shared loop workers. Zero uses GOMAXPROCS.
	Workers int
	// AppendWorkers is the number of shared durable append workers. Zero uses Workers.
	AppendWorkers int
	// CheckpointWorkers is the number of shared checkpoint workers. Zero uses max(1, Workers/2).
	CheckpointWorkers int
	// MailboxSize is the default per-replica pooled loop queue size.
	MailboxSize int
	// TurnBudget is the default number of loop events a worker processes before yielding.
	TurnBudget int
	// EffectQueueSize bounds queued durable append and checkpoint effects.
	EffectQueueSize int
	// Now returns the current wall clock time for future scheduler extensions.
	Now func() time.Time
	// Observer receives low-cardinality pooled execution metrics.
	Observer ExecutionObserver
	// Logger emits execution pool diagnostics.
	Logger wklog.Logger
}

// ExecutionPool bounds shared replica loop execution with a fixed worker set.
type ExecutionPool struct {
	cfg ExecutionPoolConfig

	ready      chan *pooledLoopDriver
	schedule   chan executionTimer
	timerSeq   uint64
	append     chan pooledAppendEffect
	checkpoint chan pooledCheckpointEffect

	stopCh    chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	workers   sync.WaitGroup

	logger   wklog.Logger
	observer ExecutionObserver
}

// NewExecutionPool creates a shared worker pool for pooled replica execution.
func NewExecutionPool(cfg ExecutionPoolConfig) (*ExecutionPool, error) {
	if cfg.Workers < 0 || cfg.AppendWorkers < 0 || cfg.CheckpointWorkers < 0 ||
		cfg.MailboxSize < 0 || cfg.TurnBudget < 0 || cfg.EffectQueueSize < 0 {
		return nil, channel.ErrInvalidConfig
	}
	if cfg.Workers == 0 {
		cfg.Workers = runtime.GOMAXPROCS(0)
	}
	if cfg.AppendWorkers == 0 {
		// Increased from cfg.Workers to cfg.Workers * 2 to reduce append worker blocking
		cfg.AppendWorkers = cfg.Workers * 2
	}
	if cfg.CheckpointWorkers == 0 {
		// Increased from cfg.Workers / 2 to cfg.Workers to improve checkpoint throughput
		cfg.CheckpointWorkers = cfg.Workers
		if cfg.CheckpointWorkers == 0 {
			cfg.CheckpointWorkers = 1
		}
	}
	if cfg.MailboxSize == 0 {
		cfg.MailboxSize = 2048 // Increased from 1024 to 2048 to reduce mailbox pressure
	}
	if cfg.TurnBudget == 0 {
		cfg.TurnBudget = 16 // Increased from 8 to 16 to reduce context switching
	}
	if cfg.EffectQueueSize == 0 {
		cfg.EffectQueueSize = 2048 // Increased from 1024 to 2048 to reduce effect queue blocking
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = wklog.NewNop()
	}

	readyCapacity := cfg.MailboxSize
	if minimum := cfg.Workers * 64; readyCapacity < minimum {
		readyCapacity = minimum
	}
	if readyCapacity < 1024 {
		readyCapacity = 1024
	}
	p := &ExecutionPool{
		cfg:        cfg,
		ready:      make(chan *pooledLoopDriver, readyCapacity),
		schedule:   make(chan executionTimer, readyCapacity),
		append:     make(chan pooledAppendEffect, cfg.EffectQueueSize),
		checkpoint: make(chan pooledCheckpointEffect, cfg.EffectQueueSize),
		stopCh:     make(chan struct{}),
		done:       make(chan struct{}),
		logger:     cfg.Logger,
		observer:   cfg.Observer,
	}
	p.workers.Add(cfg.Workers)
	for i := 0; i < cfg.Workers; i++ {
		go p.runWorker()
	}
	p.workers.Add(cfg.AppendWorkers)
	for i := 0; i < cfg.AppendWorkers; i++ {
		go p.runAppendWorker()
	}
	p.workers.Add(cfg.CheckpointWorkers)
	for i := 0; i < cfg.CheckpointWorkers; i++ {
		go p.runCheckpointWorker()
	}
	p.workers.Add(1)
	go p.runScheduler()
	go func() {
		p.workers.Wait()
		close(p.done)
	}()
	return p, nil
}

// Close stops all shared execution workers.
func (p *ExecutionPool) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		close(p.stopCh)
	})
	<-p.done
	return nil
}

func (p *ExecutionPool) runWorker() {
	defer p.workers.Done()
	for {
		select {
		case driver := <-p.ready:
			if driver != nil {
				driver.drain()
			}
		case <-p.stopCh:
			return
		}
	}
}

func (p *ExecutionPool) runAppendWorker() {
	defer p.workers.Done()
	for {
		select {
		case effect := <-p.append:
			if effect.replica != nil {
				effect.replica.runAppendEffect(context.Background(), effect.effect)
			}
		case <-p.stopCh:
			return
		}
	}
}

func (p *ExecutionPool) runCheckpointWorker() {
	defer p.workers.Done()
	for {
		select {
		case effect := <-p.checkpoint:
			if effect.replica == nil {
				continue
			}
			err := effect.replica.storeCheckpointEffect(context.Background(), effect.effect)
			_ = effect.replica.submitLoopResult(context.Background(), machineCheckpointStoredEvent{
				EffectID:       effect.effect.EffectID,
				ChannelKey:     effect.effect.ChannelKey,
				Epoch:          effect.effect.Epoch,
				LeaderEpoch:    effect.effect.LeaderEpoch,
				RoleGeneration: effect.effect.RoleGeneration,
				Checkpoint:     effect.effect.Checkpoint,
				Err:            err,
			})
		case <-p.stopCh:
			return
		}
	}
}

func (p *ExecutionPool) submitAppendEffect(ctx context.Context, r *replica, effect appendLeaderBatchEffect) error {
	if p == nil {
		return channel.ErrNotReady
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case p.append <- pooledAppendEffect{replica: r, effect: effect}:
		p.observeEnqueue("ok")
		p.observeQueueDepth()
		return nil
	case <-p.stopCh:
		p.observeEnqueue("closed")
		return channel.ErrNotLeader
	case <-ctx.Done():
		p.observeEnqueue("canceled")
		return ctx.Err()
	default:
		p.observeEnqueue("queue_full")
		return channel.ErrNotReady
	}
}

func (p *ExecutionPool) submitCheckpointEffect(ctx context.Context, r *replica, effect storeCheckpointEffect) error {
	if p == nil {
		return channel.ErrNotReady
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case p.checkpoint <- pooledCheckpointEffect{replica: r, effect: effect}:
		p.observeEnqueue("ok")
		p.observeQueueDepth()
		return nil
	case <-p.stopCh:
		p.observeEnqueue("closed")
		return channel.ErrNotLeader
	case <-ctx.Done():
		p.observeEnqueue("canceled")
		return ctx.Err()
	default:
		p.observeEnqueue("queue_full")
		return channel.ErrNotReady
	}
}

func (p *ExecutionPool) observeEnqueue(result string) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveEnqueue(result)
}

func (p *ExecutionPool) observeQueueDepth() {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.SetQueueDepth(len(p.ready) + len(p.schedule) + len(p.append) + len(p.checkpoint))
}

func (p *ExecutionPool) observeMailboxWait(d time.Duration) {
	if p == nil || p.observer == nil {
		return
	}
	p.observer.ObserveMailboxWait(d)
}

func (p *ExecutionPool) mailboxSize(configured int) int {
	if configured > 0 {
		return configured
	}
	return p.cfg.MailboxSize
}

func (p *ExecutionPool) turnBudget(configured int) int {
	if configured > 0 {
		return configured
	}
	return p.cfg.TurnBudget
}

func (p *ExecutionPool) scheduleResult(driver *pooledLoopDriver, delay time.Duration, event machineEvent) {
	if p == nil || driver == nil {
		return
	}
	if delay <= 0 {
		delay = time.Millisecond
	}
	item := executionTimer{
		due:    p.cfg.Now().Add(delay),
		driver: driver,
		event:  event,
		seq:    p.nextTimerSeq(),
	}
	select {
	case p.schedule <- item:
	case <-p.stopCh:
	default:
		// If the shared timer queue is saturated, flush through the loop mailbox
		// immediately rather than spawning a per-channel timer goroutine.
		_ = driver.submitResult(context.Background(), event)
	}
}

func (p *ExecutionPool) nextTimerSeq() uint64 {
	return atomic.AddUint64(&p.timerSeq, 1)
}

func (p *ExecutionPool) runScheduler() {
	defer p.workers.Done()

	var timers executionTimerHeap
	heap.Init(&timers)
	timer := time.NewTimer(time.Hour)
	stopExecutionTimer(timer)
	for {
		var timerC <-chan time.Time
		if timers.Len() > 0 {
			delay := time.Until(timers[0].due)
			if delay < 0 {
				delay = 0
			}
			resetExecutionTimer(timer, delay)
			timerC = timer.C
		}

		select {
		case item := <-p.schedule:
			heap.Push(&timers, item)
		case <-timerC:
			now := p.cfg.Now()
			for timers.Len() > 0 && !timers[0].due.After(now) {
				item := heap.Pop(&timers).(executionTimer)
				if item.driver != nil {
					_ = item.driver.submitResult(context.Background(), item.event)
				}
			}
		case <-p.stopCh:
			stopExecutionTimer(timer)
			return
		}
	}
}

func resetExecutionTimer(timer *time.Timer, delay time.Duration) {
	stopExecutionTimer(timer)
	timer.Reset(delay)
}

func stopExecutionTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

type executionTimer struct {
	due    time.Time
	driver *pooledLoopDriver
	event  machineEvent
	seq    uint64
}

type pooledAppendEffect struct {
	replica *replica
	effect  appendLeaderBatchEffect
}

type pooledCheckpointEffect struct {
	replica *replica
	effect  storeCheckpointEffect
}

type executionTimerHeap []executionTimer

func (h executionTimerHeap) Len() int { return len(h) }

func (h executionTimerHeap) Less(i, j int) bool {
	if h[i].due.Equal(h[j].due) {
		return h[i].seq < h[j].seq
	}
	return h[i].due.Before(h[j].due)
}

func (h executionTimerHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *executionTimerHeap) Push(x any) {
	*h = append(*h, x.(executionTimer))
}

func (h *executionTimerHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
