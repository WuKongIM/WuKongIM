package replica

import (
	"container/heap"
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ExecutionPoolConfig configures shared replica execution workers.
type ExecutionPoolConfig struct {
	// Workers is the number of shared loop workers. Zero uses GOMAXPROCS.
	Workers int
	// MailboxSize is the default per-replica pooled loop queue size.
	MailboxSize int
	// TurnBudget is the default number of loop events a worker processes before yielding.
	TurnBudget int
	// Now returns the current wall clock time for future scheduler extensions.
	Now func() time.Time
	// Logger emits execution pool diagnostics.
	Logger wklog.Logger
}

// ExecutionPool bounds shared replica loop execution with a fixed worker set.
type ExecutionPool struct {
	cfg ExecutionPoolConfig

	ready    chan *pooledLoopDriver
	schedule chan executionTimer
	timerSeq uint64

	stopCh    chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	workers   sync.WaitGroup

	logger wklog.Logger
}

// NewExecutionPool creates a shared worker pool for pooled replica execution.
func NewExecutionPool(cfg ExecutionPoolConfig) (*ExecutionPool, error) {
	if cfg.Workers < 0 || cfg.MailboxSize < 0 || cfg.TurnBudget < 0 {
		return nil, channel.ErrInvalidConfig
	}
	if cfg.Workers == 0 {
		cfg.Workers = runtime.GOMAXPROCS(0)
	}
	if cfg.MailboxSize == 0 {
		cfg.MailboxSize = 1024
	}
	if cfg.TurnBudget == 0 {
		cfg.TurnBudget = 8
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
		cfg:      cfg,
		ready:    make(chan *pooledLoopDriver, readyCapacity),
		schedule: make(chan executionTimer, readyCapacity),
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
		logger:   cfg.Logger,
	}
	p.workers.Add(cfg.Workers)
	for i := 0; i < cfg.Workers; i++ {
		go p.runWorker()
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
