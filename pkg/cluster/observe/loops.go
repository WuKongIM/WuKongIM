package observe

import (
	"context"
	"sync"
	"time"
)

// Loop runs a function periodically until stopped.
type Loop struct {
	interval time.Duration
	fn       func(context.Context) error
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewLoop creates a Loop.
func NewLoop(interval time.Duration, fn func(context.Context) error) *Loop {
	if interval <= 0 {
		interval = time.Second
	}
	return &Loop{interval: interval, fn: fn}
}

// Start starts the loop.
func (l *Loop) Start(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	l.cancel = cancel
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		ticker := time.NewTicker(l.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if l.fn != nil {
					_ = l.fn(ctx)
				}
			}
		}
	}()
}

// Stop stops the loop and waits for it to exit.
func (l *Loop) Stop() {
	if l == nil {
		return
	}
	if l.cancel != nil {
		l.cancel()
	}
	l.wg.Wait()
}
