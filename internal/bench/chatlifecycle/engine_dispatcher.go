package chatlifecycle

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// SDK admission is bounded independently from the owner's command loop. A
// blocked local SDK call must not hold a worker tick, grant or completion turn.
const engineAdmissionWorkers = 16

type engineAdmission struct {
	intent   TrafficIntent
	attempt  uint8
	due      time.Time
	deadline time.Time
	next     *engineAdmission
}

type engineAdmissionLane struct {
	head, tail *engineAdmission
	readyNext  *engineAdmissionLane
	uid        string
	running    bool
}

// engineDispatcher owns a bounded FIFO per active sender and a fixed executor
// pool. It never registers attempts or counts sends; those remain owner work at
// actual execution. There is no goroutine, channel or retained history per UID.
type engineDispatcher struct {
	mu              sync.Mutex
	ready           *sync.Cond
	lanes           map[string]*engineAdmissionLane
	head, tail      *engineAdmissionLane
	count, capacity int
	workers         int
	closed          bool
	joined          sync.WaitGroup
	run             func(*engineAdmission)
	workerExited    func()
}

func newEngineDispatcher(capacity, workers int, run func(*engineAdmission), exited func()) *engineDispatcher {
	d := &engineDispatcher{capacity: capacity, workers: workers, run: run, workerExited: exited,
		lanes: make(map[string]*engineAdmissionLane)}
	d.ready = sync.NewCond(&d.mu)
	return d
}

func (d *engineDispatcher) start() {
	d.joined.Add(d.workers)
	for range d.workers {
		go d.loop()
	}
}

func (d *engineDispatcher) appendReady(lane *engineAdmissionLane) {
	if d.tail == nil {
		d.head = lane
	} else {
		d.tail.readyNext = lane
	}
	d.tail = lane
	d.ready.Signal()
}

// submit transfers existing work credit; it never waits for capacity.
func (d *engineDispatcher) submit(admission *engineAdmission) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.count == d.capacity {
		return false
	}
	uid := admission.intent.Logical.Sender
	lane := d.lanes[uid]
	if lane == nil {
		lane = &engineAdmissionLane{uid: uid}
		d.lanes[uid] = lane
	}
	empty := lane.head == nil
	if empty {
		lane.head = admission
	} else {
		lane.tail.next = admission
	}
	lane.tail = admission
	d.count++
	if empty && !lane.running {
		d.appendReady(lane)
	}
	return true
}

func (d *engineDispatcher) loop() {
	defer d.joined.Done()
	defer d.workerExited()
	for {
		d.mu.Lock()
		for d.head == nil && !d.closed {
			d.ready.Wait()
		}
		if d.head == nil {
			d.mu.Unlock()
			return
		}
		lane := d.head
		d.head = lane.readyNext
		if d.head == nil {
			d.tail = nil
		}
		lane.readyNext = nil
		lane.running = true
		admission := lane.head
		lane.head = admission.next
		if lane.head == nil {
			lane.tail = nil
		}
		admission.next = nil
		d.mu.Unlock()

		d.run(admission)

		d.mu.Lock()
		lane.running = false
		if lane.head != nil {
			d.appendReady(lane)
		} else {
			delete(d.lanes, lane.uid)
		}
		d.mu.Unlock()
	}
}

// closeAndWait joins every queued or executing ticket. The generation context
// has already been canceled, so queued tickets settle without touching the SDK.
// The owner loop must remain alive until this join completes.
func (d *engineDispatcher) closeAndWait() {
	d.mu.Lock()
	d.closed = true
	d.ready.Broadcast()
	d.mu.Unlock()
	d.joined.Wait()
}

// releaseCredit runs in the owner result transition, so newly admitted work
// sees executor capacity and retained-work credit change together.
func (d *engineDispatcher) releaseCredit() {
	d.mu.Lock()
	d.count--
	d.mu.Unlock()
}

type engineAdmissionStart struct {
	packet    *frame.SendPacket
	inflight  *engineInflight
	attemptAt time.Time
	deadline  time.Time
	err       error
}

// queueAttempt is owner-only. Future work retains its credit until execution;
// moving it here does not increment verifier Sent or start an attempt deadline.
func (e *Engine) queueAttempt(intent TrafficIntent, attempt uint8, due, deadline time.Time) error {
	// Initial work has released its heap credit; a retry arriving from the
	// separate retry heap must also reserve the shared retained-work bound.
	if e.futureCount() >= e.workCapacity || !e.dispatcher.submit(&engineAdmission{
		intent: intent, attempt: attempt, due: due, deadline: deadline,
	}) {
		err := e.recordRuntimeFailure(RuntimeFailureEngineQueueSaturated, uint64(e.workCapacity))
		if attempt == 0 {
			return errors.Join(err, e.releaseUnstartedSendLease(intent))
		}
		return e.abortHarness(e.inflight[intent.Logical.ClientMsgNo], err)
	}
	e.admissionQueued++
	e.admissionOutstanding++
	if attempt == 0 {
		e.admissionInitialQueued++
	}
	e.observeWorkPeak()
	return nil
}

// runAdmission crosses the owner only for state transitions. Neither an SDK
// call nor its result wait occupies Advance, stepMu or worker advanceMu.
func (e *Engine) runAdmission(admission *engineAdmission, commands chan<- engineCommand, ctx context.Context) {
	started := make(chan engineAdmissionStart, 1)
	commands <- engineCommand{run: func() {
		e.admissionQueued--
		if admission.attempt == 0 {
			e.admissionInitialQueued--
		}
		result := engineAdmissionStart{}
		switch {
		case ctx.Err() != nil:
			if admission.attempt == 0 {
				result.err = e.releaseUnstartedSendLease(admission.intent)
			}
		default:
			result = e.startSDKAttempt(admission.intent, admission.attempt, admission.due, admission.deadline)
		}
		started <- result
	}}
	result := <-started
	if result.packet != nil {
		sendErr := e.sessions.TrySend(ctx, admission.intent.Logical.Sender, result.packet)
		finished := make(chan struct{}, 1)
		commands <- engineCommand{run: func() {
			e.dispatcher.releaseCredit()
			e.admissionOutstanding--
			e.retainAdmissionError(e.finishSDKAttempt(ctx, admission.intent, admission.attempt, result, sendErr))
			finished <- struct{}{}
		}}
		<-finished
		return
	}
	finished := make(chan struct{}, 1)
	commands <- engineCommand{run: func() {
		e.dispatcher.releaseCredit()
		e.retainAdmissionError(result.err)
		e.admissionOutstanding--
		finished <- struct{}{}
	}}
	<-finished
}

// retainAdmissionError preserves the first asynchronous failure for the next
// worker advance without an unbounded error chain. Native evidence is retained
// by the original transition that produced the failure.
func (e *Engine) retainAdmissionError(err error) {
	if err != nil && e.admissionError == nil {
		e.admissionError = err
	}
}
