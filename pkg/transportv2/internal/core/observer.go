package core

import "sync"

const defaultObserverQueueSize = 1024

// ObserverDrain isolates transport hot paths from observer callback latency.
type ObserverDrain struct {
	target Observer
	events chan Event
	done   chan struct{}

	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewObserverDrain wraps target with a bounded non-blocking event drain.
func NewObserverDrain(target Observer) *ObserverDrain {
	if target == nil {
		return nil
	}
	d := &ObserverDrain{
		target: target,
		events: make(chan Event, defaultObserverQueueSize),
		done:   make(chan struct{}),
	}
	d.wg.Add(1)
	go d.run()
	return d
}

// ObserveTransport enqueues an event for asynchronous delivery or drops it when the drain is full.
func (d *ObserverDrain) ObserveTransport(event Event) {
	if d == nil {
		return
	}
	select {
	case <-d.done:
		return
	default:
	}
	select {
	case d.events <- event:
	case <-d.done:
	default:
	}
}

// Stop stops accepting events, drains queued observations, and waits for the drain goroutine.
func (d *ObserverDrain) Stop() {
	if d == nil {
		return
	}
	d.stopOnce.Do(func() {
		close(d.done)
		d.wg.Wait()
	})
}

func (d *ObserverDrain) run() {
	defer d.wg.Done()
	for {
		select {
		case event := <-d.events:
			d.target.ObserveTransport(event)
		case <-d.done:
			d.drain()
			return
		}
	}
}

func (d *ObserverDrain) drain() {
	for {
		select {
		case event := <-d.events:
			d.target.ObserveTransport(event)
		default:
			return
		}
	}
}
