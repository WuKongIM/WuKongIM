package replica

import (
	"runtime"
	"sync"
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

	ready chan *pooledLoopDriver

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
		cfg:    cfg,
		ready:  make(chan *pooledLoopDriver, readyCapacity),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
		logger: cfg.Logger,
	}
	p.workers.Add(cfg.Workers)
	for i := 0; i < cfg.Workers; i++ {
		go p.runWorker()
	}
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
