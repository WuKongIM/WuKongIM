package core

import (
	"sync"
	"sync/atomic"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	defaultObserverQueueSize      = 1024
	maxObserverCoalescedStateKeys = 8192
	observerDrainStoppedBit       = uint64(1) << 63
	observerDrainActiveMask       = observerDrainStoppedBit - 1
)

type observerStateKey struct {
	name      string
	sourceID  uint64
	priority  Priority
	serviceID uint16
	nodeID    NodeID
}

// ObserverDrain isolates transport hot paths from observer callback latency.
type ObserverDrain struct {
	target     Observer
	events     chan Event
	stateReady chan struct{}
	done       chan struct{}

	stateMu       sync.Mutex
	latestState   map[observerStateKey]Event
	stateRevision map[observerStateKey]uint64

	stopOnce sync.Once
	// admission stores the stopped bit and the number of in-flight ObserveTransport calls.
	admission atomic.Uint64
	// admissionsDrained wakes Stop after the last admitted observation finishes.
	admissionsDrained chan struct{}
	wg                sync.WaitGroup
}

// NewObserverDrain wraps target with a bounded non-blocking event drain owned by taskID.
func NewObserverDrain(target Observer, taskID goruntimeregistry.TaskID) *ObserverDrain {
	if target == nil {
		return nil
	}
	d := &ObserverDrain{
		target:            target,
		events:            make(chan Event, defaultObserverQueueSize),
		stateReady:        make(chan struct{}, 1),
		done:              make(chan struct{}),
		latestState:       make(map[observerStateKey]Event),
		stateRevision:     make(map[observerStateKey]uint64),
		admissionsDrained: make(chan struct{}, 1),
	}
	d.wg.Add(1)
	goruntimeregistry.SafeGo(nil, taskID, d.run)
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
	if key, ok := observerStateEventKey(event); ok && d.coalesceState(key, event) {
		return
	}
	select {
	case d.events <- event:
	default:
	}
}

func observerStateEventKey(event Event) (observerStateKey, bool) {
	switch event.Name {
	case "pending_rpc", "peer_pool", "scheduler_queue", "service_queue", "service_inflight", "controller_raft_queue":
		return observerStateKey{
			name:      event.Name,
			sourceID:  event.SourceID,
			priority:  event.Priority,
			serviceID: event.ServiceID,
			nodeID:    event.NodeID,
		}, true
	default:
		return observerStateKey{}, false
	}
}

// coalesceState preserves the newest absolute observation for a bounded set of
// transport sources while the ordinary lossy event queue is saturated.
func (d *ObserverDrain) coalesceState(key observerStateKey, event Event) bool {
	d.stateMu.Lock()
	lastRevision, exists := d.stateRevision[key]
	if !exists && len(d.stateRevision) >= maxObserverCoalescedStateKeys {
		d.stateMu.Unlock()
		return false
	}
	if event.Revision > 0 && lastRevision > 0 && event.Revision <= lastRevision {
		d.stateMu.Unlock()
		return true
	}
	if event.Revision > 0 {
		d.stateRevision[key] = event.Revision
	} else if !exists {
		d.stateRevision[key] = 0
	}
	d.latestState[key] = event
	d.stateMu.Unlock()
	select {
	case d.stateReady <- struct{}{}:
	default:
	}
	return true
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
		case <-d.stateReady:
			d.drainLatestState()
		case <-d.done:
			d.drain()
			d.drainLatestState()
			return
		}
	}
}

func (d *ObserverDrain) drainLatestState() {
	for {
		d.stateMu.Lock()
		if len(d.latestState) == 0 {
			d.stateMu.Unlock()
			return
		}
		states := make([]Event, 0, len(d.latestState))
		for key, event := range d.latestState {
			states = append(states, event)
			delete(d.latestState, key)
		}
		d.stateMu.Unlock()
		for _, event := range states {
			d.target.ObserveTransport(event)
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
