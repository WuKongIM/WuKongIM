package rpctrace

import (
	"sync/atomic"
	"time"
)

// DispatchPoint is a closed local transport boundary, not a queue residence
// interval. Concurrent owners need not reach these points in numeric order.
type DispatchPoint uint8

const (
	DispatchEnqueueEnter DispatchPoint = iota
	DispatchEnqueueLocked
	DispatchEnqueueUnlocked
	DispatchEnqueueObserversDone
	DispatchEnqueueWakeDone
	DispatchDequeue
	DispatchDequeueObserversDone
	DispatchExecutorSubmit
	DispatchExecutorAccepted
	DispatchWorkerEnter
	DispatchWorkerBookkeepingDone
	DispatchHandlerEnter
	DispatchContextCancelCallback
	DispatchExpiryUnlink
	DispatchExpiryObserversDone
	DispatchErrorDeliver
	DispatchPointCount
)

// DispatchPath identifies the branch responsible for leaving dispatch. Handler
// means handler entry, not business success; the Sample.Result remains separate.
type DispatchPath string

const (
	DispatchPathHandler         DispatchPath = "handler"
	DispatchPathEnqueueRejected DispatchPath = "enqueue_rejected"
	DispatchPathContextExpiry   DispatchPath = "context_expiry"
	DispatchPathQueueExpiry     DispatchPath = "queue_expiry"
	DispatchPathDequeueRejected DispatchPath = "dequeue_rejected"
	DispatchPathSubmitRejected  DispatchPath = "submit_rejected"
	DispatchPathWorkerRejected  DispatchPath = "worker_rejected"
	DispatchPathStopped         DispatchPath = "stopped"
)

var dispatchPaths = [...]DispatchPath{"", DispatchPathHandler, DispatchPathEnqueueRejected, DispatchPathContextExpiry, DispatchPathQueueExpiry, DispatchPathDequeueRejected, DispatchPathSubmitRejected, DispatchPathWorkerRejected, DispatchPathStopped}

// DispatchSample is a detached fixed-size companion to one server Sample.
// KnownMask distinguishes observed offset zero from an unexecuted/unknown point.
// Offsets use the containing Trace's monotonic origin, not endpoint wall clocks.
// QueueDeadline is a planned effective FIFO deadline, never an actual interval;
// it may be negative or later than a healthy Sample.Duration. Missing points,
// including work racing with Finish, do not prove the corresponding work absent.
type DispatchSample struct {
	KnownMask          uint16                            `json:"known_mask"`
	Offsets            [DispatchPointCount]time.Duration `json:"offsets"`
	QueueDeadline      time.Duration                     `json:"queue_deadline"`
	QueueDeadlineKnown bool                              `json:"queue_deadline_known"`
	TerminalPath       DispatchPath                      `json:"terminal_path,omitempty"`
}

// DispatchRecorder owns only bounded atomic observation cells. Its nil methods
// return before reading a clock. Mark/End/SetQueueDeadline never acquire Trace.mu,
// allocate, call an observer, or wait; they may run under a transport queue lock.
// Finish copies the cells once; this live owner never backs a published sample.
type DispatchRecorder struct {
	// at is the immutable containing Trace's monotonic origin.
	at time.Time
	// points encodes offset+1 so a measured zero remains distinct from unknown.
	points   [DispatchPointCount]atomic.Int64
	deadline atomic.Int64
	// deadlineState is 0 absent, 1 publishing, 2 ready for a frozen copy.
	deadlineState atomic.Uint32
	// terminal claims one closed branch; terminalAt publishes its offset+1.
	terminal   atomic.Uint32
	terminalAt atomic.Int64
	// closed prevents new work; Finish also excludes in-flight marks past its cut.
	closed atomic.Bool
}

// StartDispatch admits one sidecar only for a bound, declared server observation.
// It must run before business queue locks; no unknown/client/drop path allocates.
func (t *Trace) StartDispatch() *DispatchRecorder {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.side != SideServer || t.unbound || t.operation == 0 {
		return nil
	}
	if t.dispatch == nil {
		t.dispatch = &DispatchRecorder{at: t.at}
	}
	return t.dispatch
}

// Mark retains the first actual visit to point, including a measured zero.
func (d *DispatchRecorder) Mark(point DispatchPoint) bool {
	if d == nil || point >= DispatchPointCount || d.closed.Load() || d.points[point].Load() != 0 {
		return false
	}
	return d.points[point].CompareAndSwap(0, int64(time.Since(d.at))+1)
}

// SetQueueDeadline records the actual admitted entry's effective planned bound.
// State 1 is an unpublished write; a racing Finish leaves that value unknown.
func (d *DispatchRecorder) SetQueueDeadline(deadline time.Time) bool {
	if d == nil || deadline.IsZero() || d.closed.Load() || !d.deadlineState.CompareAndSwap(0, 1) {
		return false
	}
	d.deadline.Store(int64(deadline.Sub(d.at)))
	d.deadlineState.Store(2)
	return true
}

// End records only the first responsible dispatch branch, not an RPC result.
func (d *DispatchRecorder) End(path DispatchPath) bool {
	if d == nil || d.closed.Load() {
		return false
	}
	var id uint32
	for i := 1; i < len(dispatchPaths); i++ {
		if dispatchPaths[i] == path {
			id = uint32(i)
			break
		}
	}
	if id == 0 {
		return false
	}
	at := int64(time.Since(d.at)) + 1
	if !d.terminal.CompareAndSwap(0, id) {
		return false
	}
	d.terminalAt.Store(at)
	return true
}

func (d *DispatchRecorder) finish(elapsed time.Duration) *DispatchSample {
	d.closed.Store(true)
	sample := &DispatchSample{}
	for point := DispatchPoint(0); point < DispatchPointCount; point++ {
		encoded := d.points[point].Load()
		if encoded > 0 && time.Duration(encoded-1) <= elapsed {
			sample.KnownMask |= 1 << point
			sample.Offsets[point] = time.Duration(encoded - 1)
		}
	}
	if d.deadlineState.Load() == 2 {
		sample.QueueDeadline = time.Duration(d.deadline.Load())
		sample.QueueDeadlineKnown = true
	}
	if at := d.terminalAt.Load(); at > 0 && time.Duration(at-1) <= elapsed {
		sample.TerminalPath = dispatchPaths[d.terminal.Load()]
	}
	return sample
}
