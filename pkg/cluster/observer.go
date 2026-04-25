package cluster

import (
	"context"
	"time"
)

type observerLoop struct {
	interval time.Duration
	tick     func(context.Context)
	stop     chan struct{}
	done     chan struct{}
	cancel   context.CancelFunc
}

func newObserverLoop(interval time.Duration, tick func(context.Context)) *observerLoop {
	return &observerLoop{
		interval: interval,
		tick:     tick,
	}
}

func (l *observerLoop) Start(parent context.Context) {
	if l == nil || l.tick == nil || l.stop != nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}

	ctx, cancel := context.WithCancel(parent)
	l.cancel = cancel
	l.stop = make(chan struct{})
	l.done = make(chan struct{})

	go func() {
		defer close(l.done)

		ticker := time.NewTicker(l.interval)
		defer ticker.Stop()

		for {
			l.tick(ctx)
			select {
			case <-l.stop:
				return
			case <-ticker.C:
			}
		}
	}()
}

func (l *observerLoop) Stop() {
	if l == nil || l.stop == nil {
		return
	}
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	close(l.stop)
	if l.done != nil {
		<-l.done
	}
	l.stop = nil
	l.done = nil
}

type signalLoop struct {
	signal <-chan struct{}
	tick   func(context.Context)
	stop   chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
}

func newSignalLoop(signal <-chan struct{}, tick func(context.Context)) *signalLoop {
	return &signalLoop{
		signal: signal,
		tick:   tick,
	}
}

func (l *signalLoop) Start(parent context.Context) {
	if l == nil || l.tick == nil || l.signal == nil || l.stop != nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}

	ctx, cancel := context.WithCancel(parent)
	l.cancel = cancel
	l.stop = make(chan struct{})
	l.done = make(chan struct{})

	go func() {
		defer close(l.done)
		for {
			select {
			case <-l.stop:
				return
			case <-ctx.Done():
				return
			case _, ok := <-l.signal:
				if !ok {
					return
				}
				l.tick(ctx)
			}
		}
	}()
}

func (l *signalLoop) Stop() {
	if l == nil || l.stop == nil {
		return
	}
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	close(l.stop)
	if l.done != nil {
		<-l.done
	}
	l.stop = nil
	l.done = nil
}
