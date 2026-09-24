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
	ErrDeliveriesInvalid    = errors.New("mqttsession: invalid delivery scheduling request")
	ErrDeliveriesStopped    = errors.New("mqttsession: delivery scheduling admission closed")
	ErrDeliveriesLimit      = errors.New("mqttsession: delivery scheduling capacity reached")
	ErrDeliveriesRegistered = errors.New("mqttsession: delivery task already registered")
	ErrDeliveriesCallback   = errors.New("mqttsession: delivery task failed")
)

// DeliveryWork contains scheduling hints, never durable delivery evidence.
// Again yields to other due Owners before another turn. Done releases the task
// only when its usecase no longer needs this connection's volatile continuation.
type DeliveryWork struct{ Again, Done bool }

// DeliveryTask is bound to one exact Owner and retains no publication queue.
// Turn owns authority checks and bounded usecase work, including terminal cleanup
// after fencing. It must honor ctx and join every effect before returning. Mere
// owner fencing must not discard a pending exact-owner End continuation.
type DeliveryTask interface {
	Turn(context.Context) (DeliveryWork, error)
}

type DeliveryOptions struct {
	// Owners belongs to this node's single MQTT registry/boot lifetime.
	Owners   *Owners
	Registry *gr.Registry
	// Capacity includes scheduled, queued and executing tasks. Zero uses Owners.
	Capacity int
	// Workers defaults to 16, with at most 128 and one cohort of queued turns.
	Workers int
	// TurnTimeout bounds a joined turn, default five seconds, maximum one minute.
	TurnTimeout time.Duration
	// IdleInterval polls for missed hints, default one second, maximum one minute.
	IdleInterval time.Duration
	// Retry bounds repeated failures, default 250ms, between 1ms and one minute.
	Retry time.Duration
}

// Deliveries serializes each connection's turns in a bounded worker cohort.
// One indexed entry and coalesced wake bit per Owner replace per-session tasks.
// App must fence admission before Stop and keep dependencies alive until it joins.
type Deliveries struct {
	options           DeliveryOptions
	mu                sync.Mutex
	started, stopping bool
	entries           map[contract.Owner]*deliveryEntry
	due               deliveryHeap
	wake              chan struct{}
	done              chan struct{}
	ctx               context.Context
	cancel            context.CancelFunc
	queue             *workqueue.BoundedWorkerQueue[*deliveryEntry]
	stats             DeliverySnapshot
}

type deliveryEntry struct {
	owner contract.Owner
	task  DeliveryTask
	due   time.Time
	index int // -1 while queued or executing; the record remains capacity-charged.
	woken bool
	// notBefore prevents notification floods from defeating failure backoff.
	notBefore time.Time
}

// DeliverySnapshot exposes constant-time aggregate diagnostics without identities.
type DeliverySnapshot struct {
	Tracked, Scheduled, InProgress int
	Turns, Completed, Failures     uint64
	Stopping                       bool
}

func NewDeliveries(o DeliveryOptions) (*Deliveries, error) {
	if o.Owners == nil {
		return nil, ErrDeliveriesInvalid
	}
	if o.Capacity == 0 {
		o.Capacity = o.Owners.opts.Capacity
	}
	if o.Workers == 0 {
		o.Workers = 16
	}
	if o.TurnTimeout == 0 {
		o.TurnTimeout = 5 * time.Second
	}
	if o.IdleInterval == 0 {
		o.IdleInterval = time.Second
	}
	if o.Retry == 0 {
		o.Retry = 250 * time.Millisecond
	}
	if o.Capacity < 1 || o.Capacity > 1_000_000 || o.Workers < 1 || o.Workers > 128 || o.TurnTimeout <= 0 || o.TurnTimeout > time.Minute || o.IdleInterval < time.Millisecond || o.IdleInterval > time.Minute || o.Retry < time.Millisecond || o.Retry > time.Minute {
		return nil, ErrDeliveriesInvalid
	}
	return &Deliveries{options: o, entries: make(map[contract.Owner]*deliveryEntry), wake: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

// Start checks its caller but owns a separate lifetime, joined by terminal Stop.
func (s *Deliveries) Start(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrDeliveriesInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return ErrDeliveriesStopped
	}
	if s.started {
		return nil
	}
	q, err := workqueue.NewBoundedWorkerQueue(workqueue.BoundedWorkerQueueConfig{Name: "mqtt_deliveries", Goroutines: s.options.Registry, Task: gr.TaskMQTTDeliveryWorker, Workers: s.options.Workers, QueueSize: s.options.Workers}, s.execute)
	if err != nil {
		return err
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.queue, s.started = q, true
	gr.SafeGo(s.options.Registry, gr.TaskMQTTDeliveryScheduler, s.run)
	return nil
}

// Register accepts exactly one task after local activation. Duplicate calls fail
// without replacing the existing task, even when it is executing or ending.
func (s *Deliveries) Register(owner contract.Owner, task DeliveryTask) error {
	if s == nil || task == nil || owner.Validate() != nil {
		return ErrDeliveriesInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopping {
		return ErrDeliveriesStopped
	}
	if _, exists := s.entries[owner]; exists {
		return ErrDeliveriesRegistered
	}
	if len(s.entries) >= s.options.Capacity {
		return ErrDeliveriesLimit
	}
	if _, _, err := s.options.Owners.liveLease(owner); err != nil {
		return err
	}
	e := &deliveryEntry{owner: owner, task: task, due: time.Now(), index: -1}
	s.entries[owner] = e
	heap.Push(&s.due, e)
	s.signal()
	return nil
}

// Wake coalesces one exact-owner hint without allocating queued work. A hint
// received during execution survives an idle result from that same turn.
func (s *Deliveries) Wake(owner contract.Owner) error {
	if s == nil || owner.Validate() != nil {
		return ErrDeliveriesInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopping {
		return ErrDeliveriesStopped
	}
	e := s.entries[owner]
	if e == nil {
		return ErrOwnerUnknown
	}
	if e.index < 0 {
		e.woken = true
	} else {
		now := time.Now()
		if e.notBefore.After(now) {
			now = e.notBefore
		}
		if e.due.After(now) {
			e.due = now
			heap.Fix(&s.due, e.index)
		}
	}
	s.signal()
	return nil
}

// Stop cancels and joins this exact run without claiming physical closure or
// owner isolation. A timeout retains all records and permits another Stop join.
func (s *Deliveries) Stop(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrDeliveriesInvalid
	}
	s.mu.Lock()
	if !s.stopping {
		s.stopping = true
		if s.started {
			s.cancel()
		} else {
			close(s.done)
		}
	}
	done := s.done
	s.mu.Unlock()
	return awaitOwner(ctx, done)
}

func (s *Deliveries) Snapshot() DeliverySnapshot {
	if s == nil {
		return DeliverySnapshot{Stopping: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.stats
	out.Tracked, out.Scheduled = len(s.entries), len(s.due)
	out.InProgress = out.Tracked - out.Scheduled
	out.Stopping = s.stopping
	return out
}

func (s *Deliveries) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Deliveries) run() {
	defer close(s.done)
	defer func() {
		// Only the scheduler closes this queue. Background join is intentional:
		// caller timeout cannot turn an unjoined callback into completed Stop.
		_ = s.queue.Close(context.Background())
		s.mu.Lock()
		clear(s.entries)
		s.due = nil
		s.mu.Unlock()
	}()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			return
		}
		now, wait := time.Now(), time.Hour
		var e *deliveryEntry
		if len(s.due) > 0 {
			if !s.due[0].due.After(now) {
				e = heap.Pop(&s.due).(*deliveryEntry)
				e.woken = false
			} else {
				wait = s.due[0].due.Sub(now)
			}
		}
		s.mu.Unlock()
		if e != nil {
			if err := s.queue.SubmitWait(s.ctx, e); err != nil {
				return // Stop cancellation interrupts a full worker queue.
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
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-timer.C:
		}
	}
}

func (s *Deliveries) execute(_ context.Context, e *deliveryEntry) error {
	ctx, cancel := context.WithTimeout(s.ctx, s.options.TurnTimeout)
	defer cancel()
	if ctx.Err() != nil {
		return nil
	}
	out, err := callDeliveryTask(ctx, e.task)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.Turns++
	if err != nil {
		s.stats.Failures++
	}
	if s.stopping {
		return nil
	}
	if out.Done && err == nil {
		delete(s.entries, e.owner)
		s.stats.Completed++
		return nil
	}
	now := time.Now()
	e.notBefore = time.Time{}
	e.due = now.Add(s.options.IdleInterval)
	if err != nil {
		e.notBefore = now.Add(s.options.Retry)
		e.due = e.notBefore
	} else if out.Again || e.woken {
		e.due = now
	}
	heap.Push(&s.due, e)
	s.signal()
	return nil
}

// callDeliveryTask joins even a slow cancellation and redacts callback panics.
// A late result cannot claim terminal completion or immediate retry authority.
func callDeliveryTask(ctx context.Context, task DeliveryTask) (out DeliveryWork, err error) {
	defer func() {
		if recover() != nil {
			out, err = DeliveryWork{}, ErrDeliveriesCallback
		}
		if ctx.Err() != nil {
			out, err = DeliveryWork{}, ctx.Err()
		}
	}()
	return task.Turn(ctx)
}

type deliveryHeap []*deliveryEntry

func (h deliveryHeap) Len() int { return len(h) }
func (h deliveryHeap) Less(i, j int) bool {
	if h[i].due.Equal(h[j].due) {
		return h[i].owner.ConnectionID < h[j].owner.ConnectionID
	}
	return h[i].due.Before(h[j].due)
}
func (h deliveryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *deliveryHeap) Push(v any) {
	e := v.(*deliveryEntry)
	e.index = len(*h)
	*h = append(*h, e)
}
func (h *deliveryHeap) Pop() any {
	a := *h
	e := a[len(a)-1]
	a[len(a)-1] = nil
	*h = a[:len(a)-1]
	e.index = -1
	return e
}
