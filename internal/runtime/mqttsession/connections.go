package mqttsession

import (
	"container/heap"
	"context"
	"errors"
	"sync"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

var (
	ErrConnectionsInvalid  = errors.New("mqttsession: invalid connection supervisor request")
	ErrConnectionsStopped  = errors.New("mqttsession: connection supervisor admission closed")
	ErrConnectionsLimit    = errors.New("mqttsession: connection supervisor capacity reached")
	ErrConnectionsEvidence = errors.New("mqttsession: renewal did not install a new live lease")
	ErrConnectionsCallback = errors.New("mqttsession: connection lifecycle callback failed")
)

// DisconnectIntent is a trusted node-local observation, never a client timestamp
// or cross-node proof. Once accepted, its first values survive cleanup retries.
type DisconnectIntent struct {
	Owner            contract.Owner
	Normal           bool
	SessionExpirySec *uint32
	ObservedAt       time.Time
}

// ConnectionControl adapts Session usecases at the composition root. Renew must
// install a newer committed Owners lease before success. Disconnect must preserve
// ObservedAt and map a replaced durable owner to ErrOwnerFenced; no other failure
// is terminal. The supervisor joins every call, even when cancellation is slow;
// uncertain metadata writes remain subject to Session revision/owner fencing.
type ConnectionControl interface {
	Renew(context.Context, contract.Owner) error
	Disconnect(context.Context, DisconnectIntent) error
}

type ConnectionOptions struct {
	// Owners is dedicated to this supervisor's single registry/boot lifetime.
	Owners   *Owners
	Control  ConnectionControl
	Registry *gr.Registry
	// Capacity bounds all retained records, including working and failed cleanup.
	// Zero selects the Owners reservation capacity; the maximum is one million.
	Capacity int
	// Workers defaults to 16 and cannot exceed 128; queued jobs use one cohort.
	Workers int
	// CallTimeout defaults to one second, with an upper bound of five seconds.
	CallTimeout time.Duration
	// Retry defaults to 250ms, bounded between one millisecond and one minute.
	Retry time.Duration
}

// Connections schedules renewal and exact disconnect without per-owner workers
// or timers. Stop is terminal for this supervisor and its Owners registry. It
// drains registered owners only; app must separately close unregistered owners.
type Connections struct {
	options           ConnectionOptions
	mu                sync.Mutex
	started, stopping bool
	stopAt            time.Time
	entries           map[contract.Owner]*connectionEntry
	due               connectionHeap
	wake              chan struct{}
	done              chan struct{}
	queue             *workqueue.BoundedWorkerQueue[*connectionEntry]
	stats             ConnectionSnapshot
}

type connectionEntry struct {
	owner contract.Owner
	due   time.Time
	index int
	// intent is immutable after first acceptance; index -1 means queued/running.
	intent *DisconnectIntent
}

// ConnectionSnapshot contains constant-time aggregate diagnostics only.
type ConnectionSnapshot struct {
	Tracked, Scheduled, InProgress int
	Renewed, Retired, Failures     uint64
	Stopping                       bool
}

func NewConnections(o ConnectionOptions) (*Connections, error) {
	if o.Owners == nil || o.Control == nil {
		return nil, ErrConnectionsInvalid
	}
	if o.Capacity == 0 {
		o.Capacity = o.Owners.opts.Capacity
	}
	if o.Workers == 0 {
		o.Workers = 16
	}
	if o.CallTimeout == 0 {
		o.CallTimeout = time.Second
	}
	if o.Retry == 0 {
		o.Retry = 250 * time.Millisecond
	}
	if o.Capacity < 1 || o.Capacity > 1_000_000 || o.Workers < 1 || o.Workers > 128 || o.CallTimeout <= 0 || o.CallTimeout > 5*time.Second || o.Retry < time.Millisecond || o.Retry > time.Minute {
		return nil, ErrConnectionsInvalid
	}
	return &Connections{options: o, entries: make(map[contract.Owner]*connectionEntry), wake: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

// Start checks the startup context without inheriting its lifetime. The one
// scheduler and fixed worker cohort are joined only by terminal Stop.
func (s *Connections) Start(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrConnectionsInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return ErrConnectionsStopped
	}
	if s.started {
		return nil
	}
	q, err := workqueue.NewBoundedWorkerQueue(workqueue.BoundedWorkerQueueConfig{Name: "mqtt_connections", Goroutines: s.options.Registry, Task: gr.TaskMQTTConnectionWorker, Workers: s.options.Workers, QueueSize: s.options.Workers}, s.execute)
	if err != nil {
		return err
	}
	s.queue = q
	s.started = true
	gr.SafeGo(s.options.Registry, gr.TaskMQTTConnectionScheduler, s.run)
	return nil
}

// Register retains one scheduling record only after exact local activation.
// Repeated registration never postpones renewal or erases accepted disconnect.
func (s *Connections) Register(owner contract.Owner) error {
	if s == nil {
		return ErrConnectionsInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopping {
		return ErrConnectionsStopped
	}
	if _, ok := s.entries[owner]; ok {
		return nil
	}
	if len(s.entries) >= s.options.Capacity {
		return ErrConnectionsLimit
	}
	_, until, err := s.options.Owners.liveLease(owner)
	if err != nil {
		return err
	}
	now := s.options.Owners.opts.Now()
	e := &connectionEntry{owner: owner, index: -1, due: now.Add(until.Sub(now) / 2)}
	s.entries[owner] = e
	heap.Push(&s.due, e)
	s.signal()
	return nil
}

// Disconnect never waits for physical close, admitted operations or metadata.
// Every registration reserves its own intent/scheduling record, so a full worker
// queue cannot lose a close notification or overwrite the first normal intent.
func (s *Connections) Disconnect(i DisconnectIntent) error {
	if s == nil {
		return ErrConnectionsInvalid
	}
	now := s.options.Owners.opts.Now()
	if i.Owner.Validate() != nil || i.ObservedAt.IsZero() || i.ObservedAt == i.ObservedAt.Round(0) || i.ObservedAt.After(now) {
		return ErrConnectionsInvalid
	}
	s.mu.Lock()
	e := s.entries[i.Owner]
	if e == nil {
		s.mu.Unlock()
		return ErrOwnerUnknown
	}
	if e.intent == nil {
		if i.SessionExpirySec != nil {
			value := *i.SessionExpirySec
			i.SessionExpirySec = &value
		}
		e.intent = &i
		if e.index >= 0 {
			e.due = now
			heap.Fix(&s.due, e.index)
		}
	}
	s.mu.Unlock()
	_ = s.options.Owners.Fence(i.Owner)
	s.signal()
	return nil
}

// Stop immediately fences all owner admission, preserves earlier close intent
// and drains registered work. A timeout leaves this same run alive; dependencies
// must stay available until a later Stop successfully joins it.
func (s *Connections) Stop(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrConnectionsInvalid
	}
	s.mu.Lock()
	if !s.stopping {
		s.stopping = true
		s.stopAt = s.options.Owners.opts.Now()
		s.options.Owners.StopAdmission()
		// A single shutdown pass promotes every waiting live connection. Without
		// this pass, an earlier failed cleanup retry could hide future renewals
		// forever. Active jobs convert on completion; steady-state never scans.
		for _, e := range s.due {
			if e.intent == nil {
				s.shutdownIntentLocked(e)
				e.due = s.stopAt
			}
		}
		heap.Init(&s.due)
		if !s.started {
			close(s.done)
		}
	}
	done := s.done
	s.mu.Unlock()
	s.signal()
	return awaitOwner(ctx, done)
}

func (s *Connections) Snapshot() ConnectionSnapshot {
	if s == nil {
		return ConnectionSnapshot{Stopping: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.stats
	out.Tracked = len(s.entries)
	out.Scheduled = len(s.due)
	out.InProgress = out.Tracked - out.Scheduled
	out.Stopping = s.stopping
	return out
}
func (s *Connections) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Connections) run() {
	defer close(s.done)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.stopping && len(s.entries) == 0 {
			s.mu.Unlock()
			_ = s.queue.Close(context.Background())
			return
		}
		now := s.options.Owners.opts.Now()
		wait := time.Hour
		var e *connectionEntry
		if len(s.due) > 0 {
			head := s.due[0]
			// Stop expedites live connections once; retry deadlines still bound failures.
			if s.stopping && head.intent == nil || !head.due.After(now) {
				e = heap.Pop(&s.due).(*connectionEntry)
				s.shutdownIntentLocked(e)
			} else {
				wait = head.due.Sub(now)
			}
		}
		s.mu.Unlock()
		if e != nil {
			// Only this scheduler submits; queued jobs stay within one worker cohort.
			if err := s.queue.SubmitWait(context.Background(), e); err != nil {
				panic("mqttsession: connection work queue closed before scheduler")
			}
			continue
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
		select {
		case <-s.wake:
		case <-timer.C:
		}
	}
}

func (s *Connections) shutdownIntentLocked(e *connectionEntry) {
	if s.stopping && e.intent == nil {
		e.intent = &DisconnectIntent{Owner: e.owner, ObservedAt: s.stopAt}
	}
}

func (s *Connections) execute(_ context.Context, e *connectionEntry) error {
	s.mu.Lock()
	s.shutdownIntentLocked(e)
	intent := e.intent
	s.mu.Unlock()
	var err error
	if intent != nil {
		_ = s.options.Owners.Fence(e.owner)
		// Quiescence is independent of the Slot row. A stale row or a nil usecase
		// response cannot discard unresolved local work or incomplete physical close.
		err = s.call(func(ctx context.Context) error {
			if err := s.options.Owners.Quiesce(ctx, e.owner); err != nil {
				return err
			}
			copyIntent := *intent
			if intent.SessionExpirySec != nil {
				v := *intent.SessionExpirySec
				copyIntent.SessionExpirySec = &v
			}
			err := s.options.Control.Disconnect(ctx, copyIntent)
			if errors.Is(err, ErrOwnerFenced) {
				return nil
			}
			return err
		})
	} else {
		var before uint64
		before, _, err = s.options.Owners.liveLease(e.owner)
		if err == nil {
			err = s.call(func(ctx context.Context) error { return s.options.Control.Renew(ctx, e.owner) })
			if err == nil {
				var revision uint64
				revision, _, err = s.options.Owners.liveLease(e.owner)
				if err == nil && revision <= before {
					err = ErrConnectionsEvidence
				}
			}
		}
	}
	now := s.options.Owners.opts.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.stats.Failures++
	}
	if intent != nil && err == nil {
		delete(s.entries, e.owner)
		s.stats.Retired++
		s.signal()
		return nil
	}
	if intent == nil && err == nil {
		s.stats.Renewed++
	}
	s.shutdownIntentLocked(e)
	if e.intent != nil {
		// An intent accepted during renewal executes next, without waiting for the
		// future renewal time. A failed close preserves its retry delay.
		e.due = now
		if intent != nil {
			e.due = now.Add(s.options.Retry)
		}
	} else {
		_, installed, leaseErr := s.options.Owners.liveLease(e.owner)
		if leaseErr != nil || errors.Is(err, ErrConnectionsEvidence) {
			e.intent = &DisconnectIntent{Owner: e.owner, ObservedAt: now}
			e.due = now
			_ = s.options.Owners.Fence(e.owner)
		} else if err != nil {
			e.due = now.Add(s.options.Retry)
			if installed.Before(e.due) {
				e.due = installed
			}
		} else {
			e.due = now.Add(installed.Sub(now) / 2)
		}
	}
	heap.Push(&s.due, e)
	s.signal()
	return nil
}

func (s *Connections) call(fn func(context.Context) error) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.options.CallTimeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = ErrConnectionsCallback
		}
	}()
	err = fn(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// liveLease reads the actual installed local receipt. It is scheduling evidence,
// not a replacement for Session authority or remote isolation proof.
func (m *Owners) liveLease(owner contract.Owner) (uint64, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return 0, time.Time{}, ErrOwnerStopped
	}
	e, err := m.entryLocked(owner)
	if err != nil {
		return 0, time.Time{}, err
	}
	if e == nil || e.stage != ownerActive {
		return 0, time.Time{}, ErrOwnerFenced
	}
	if !m.opts.Now().Before(e.leaseUntil) {
		m.fenceLocked(e)
		return 0, time.Time{}, ErrOwnerFenced
	}
	return e.revision, e.leaseUntil, nil
}

type connectionHeap []*connectionEntry

func (h connectionHeap) Len() int { return len(h) }
func (h connectionHeap) Less(i, j int) bool {
	if h[i].due.Equal(h[j].due) {
		return h[i].owner.ConnectionID < h[j].owner.ConnectionID
	}
	return h[i].due.Before(h[j].due)
}
func (h connectionHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *connectionHeap) Push(v any) {
	e := v.(*connectionEntry)
	e.index = len(*h)
	*h = append(*h, e)
}
func (h *connectionHeap) Pop() any {
	a := *h
	e := a[len(a)-1]
	a[len(a)-1] = nil
	*h = a[:len(a)-1]
	e.index = -1
	return e
}
