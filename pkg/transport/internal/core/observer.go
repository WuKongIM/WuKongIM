package core

import (
	"sync"
	"sync/atomic"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	defaultObserverQueueSize = 1024
	observerDrainStoppedBit  = uint64(1) << 63
	observerDrainActiveMask  = observerDrainStoppedBit - 1
)

// ObserverDrain isolates transport hot paths from observer callback latency.
type ObserverDrain struct {
	target Observer
	events chan Event
	done   chan struct{}

	stopOnce sync.Once
	// admission stores the stopped bit and the number of in-flight ObserveTransport calls.
	admission atomic.Uint64
	// admissionsDrained wakes Stop after the last admitted observation finishes.
	admissionsDrained chan struct{}
	wg                sync.WaitGroup
}

// NewObserverDrain wraps target with a bounded non-blocking event drain.
func NewObserverDrain(target Observer) *ObserverDrain {
	if target == nil {
		return nil
	}
	d := &ObserverDrain{
		target:            target,
		events:            make(chan Event, defaultObserverQueueSize),
		done:              make(chan struct{}),
		admissionsDrained: make(chan struct{}, 1),
	}
	d.wg.Add(1)
	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskTransportObserver, d.run)
	return d
}

// ObserveTransport enqueues an event for asynchronous delivery.
// Non-terminal events are dropped when the drain is full; terminal cleanup waits for admission.
func (d *ObserverDrain) ObserveTransport(event Event) {
	if d == nil {
		return
	}
	if !d.beginObservation() {
		return
	}
	defer d.finishObservation()
	if isTerminalCleanupEvent(event) {
		d.events <- event
		return
	}
	select {
	case d.events <- event:
	default:
	}
}

// beginObservation atomically rejects stopped drains or counts one in-flight observation.
func (d *ObserverDrain) beginObservation() bool {
	for {
		state := d.admission.Load()
		if state&observerDrainStoppedBit != 0 {
			return false
		}
		if d.admission.CompareAndSwap(state, state+1) {
			return true
		}
	}
}

// finishObservation releases one admission and wakes Stop when it was the last in-flight call.
func (d *ObserverDrain) finishObservation() {
	state := d.admission.Add(^uint64(0))
	if state == observerDrainStoppedBit {
		select {
		case d.admissionsDrained <- struct{}{}:
		default:
		}
	}
}

// stopAdmissions fences new observations and returns the number already in flight.
func (d *ObserverDrain) stopAdmissions() uint64 {
	for {
		state := d.admission.Load()
		if state&observerDrainStoppedBit != 0 {
			return state & observerDrainActiveMask
		}
		if d.admission.CompareAndSwap(state, state|observerDrainStoppedBit) {
			return state & observerDrainActiveMask
		}
	}
}

func isTerminalCleanupEvent(event Event) bool {
	switch event.Name {
	case "pending_rpc", "scheduler_queue":
		return event.Result == "closed" || event.Result == "stopped"
	default:
		return false
	}
}

// Stop stops accepting events, drains queued observations, and waits for the drain goroutine.
func (d *ObserverDrain) Stop() {
	if d == nil {
		return
	}
	d.stopOnce.Do(func() {
		if d.stopAdmissions() > 0 {
			<-d.admissionsDrained
		}
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
