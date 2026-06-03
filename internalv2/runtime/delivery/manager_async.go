package delivery

import (
	"context"
	"errors"
	"sync"
)

const defaultManagerAsyncWorkers = 1
const defaultManagerAsyncQueueSize = 1024

// ErrManagerClosed reports that the manager is not accepting async work.
var ErrManagerClosed = errors.New("internalv2/runtime/delivery: manager closed")

type managerState uint8

const (
	managerStateClosed managerState = iota
	managerStateOpen
	managerStateClosing
)

type managerCommand struct {
	envelope Envelope
}

// managerAsync owns bounded async admission and worker lifecycle for Manager.
type managerAsync struct {
	// manager is the parent runtime facade used by later execution slices.
	manager *Manager
	// queue stores accepted committed-message commands until workers consume them.
	queue chan managerCommand
	// slots tracks free queue slots so submitters can re-check lifecycle state before enqueueing.
	slots chan struct{}
	// workers is the fixed number of async manager workers.
	workers int
	// observer receives admission decisions and later terminal outcomes.
	observer ManagerObserver

	mu sync.Mutex
	// state gates admission and lifecycle transitions.
	state managerState
	// cancel stops workers during Stop.
	cancel context.CancelFunc
	// acceptDone closes when the current manager lifecycle stops accepting waiters.
	acceptDone <-chan struct{}
	// done closes after all workers exit.
	done <-chan struct{}
}

func newManagerAsync(manager *Manager, queueSize, workers int, observer ManagerObserver) *managerAsync {
	if queueSize <= 0 {
		queueSize = defaultManagerAsyncQueueSize
	}
	if workers <= 0 {
		workers = defaultManagerAsyncWorkers
	}
	slots := make(chan struct{}, queueSize)
	for i := 0; i < queueSize; i++ {
		slots <- struct{}{}
	}
	return &managerAsync{
		manager:  manager,
		queue:    make(chan managerCommand, queueSize),
		slots:    slots,
		workers:  workers,
		observer: observer,
		state:    managerStateClosed,
	}
}

func (a *managerAsync) start(context.Context) error {
	if a == nil {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	switch a.state {
	case managerStateOpen:
		return nil
	case managerStateClosing:
		if !a.finishClosedIfDoneLocked() {
			return ErrManagerClosed
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(a.workers)
	for i := 0; i < a.workers; i++ {
		go func() {
			defer wg.Done()
			a.runWorker(ctx)
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	a.cancel = cancel
	a.acceptDone = ctx.Done()
	a.done = done
	a.state = managerStateOpen
	return nil
}

func (a *managerAsync) stop(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	a.mu.Lock()
	switch a.state {
	case managerStateClosed:
		a.mu.Unlock()
		return nil
	case managerStateClosing:
		done := a.done
		a.mu.Unlock()
		return a.waitClosed(ctx, done)
	}
	cancel := a.cancel
	done := a.done
	a.state = managerStateClosing
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return a.waitClosed(ctx, done)
}

func (a *managerAsync) waitClosed(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		a.finishClosedForDone(done)
		return nil
	}
	select {
	case <-done:
		a.finishClosedForDone(done)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *managerAsync) finishClosedForDone(done <-chan struct{}) {
	a.mu.Lock()
	if a.state == managerStateClosing && a.done == done {
		a.state = managerStateClosed
		a.cancel = nil
		a.acceptDone = nil
		a.done = nil
	}
	a.mu.Unlock()
}

func (a *managerAsync) finishClosedIfDoneLocked() bool {
	done := a.done
	if done == nil {
		a.state = managerStateClosed
		a.cancel = nil
		a.acceptDone = nil
		return true
	}
	select {
	case <-done:
		a.state = managerStateClosed
		a.cancel = nil
		a.acceptDone = nil
		a.done = nil
		return true
	default:
		return false
	}
}

func (a *managerAsync) submit(ctx context.Context, env Envelope) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	cmd := managerCommand{envelope: cloneEnvelope(env)}

	for {
		a.mu.Lock()
		if a.state != managerStateOpen {
			depth := len(a.queue)
			a.mu.Unlock()
			a.observeAdmission(DeliveryResultError, depth)
			return ErrManagerClosed
		}
		queue := a.queue
		slots := a.slots
		acceptDone := a.acceptDone
		select {
		case <-acceptDone:
			depth := len(queue)
			a.mu.Unlock()
			a.observeAdmission(DeliveryResultError, depth)
			return ErrManagerClosed
		default:
		}
		select {
		case <-slots:
			if err := a.enqueueWithSlotLocked(queue, acceptDone, cmd); err != nil {
				depth := len(queue)
				a.mu.Unlock()
				a.releaseSlot()
				if errors.Is(err, ErrManagerClosed) {
					a.observeAdmission(DeliveryResultError, depth)
					return err
				}
				continue
			}
			depth := len(queue)
			a.mu.Unlock()
			a.observeAdmission(DeliveryResultOK, depth)
			return nil
		default:
		}
		a.mu.Unlock()

		select {
		case <-slots:
			a.mu.Lock()
			if a.state != managerStateOpen || a.acceptDone != acceptDone {
				depth := len(a.queue)
				a.mu.Unlock()
				a.releaseSlot()
				a.observeAdmission(DeliveryResultError, depth)
				return ErrManagerClosed
			}
			if err := a.enqueueWithSlotLocked(a.queue, acceptDone, cmd); err != nil {
				depth := len(a.queue)
				a.mu.Unlock()
				a.releaseSlot()
				if errors.Is(err, ErrManagerClosed) {
					a.observeAdmission(DeliveryResultError, depth)
					return err
				}
				continue
			}
			depth := len(a.queue)
			a.mu.Unlock()
			a.observeAdmission(DeliveryResultOK, depth)
			return nil
		case <-acceptDone:
			a.observeAdmission(DeliveryResultError, len(queue))
			return ErrManagerClosed
		case <-ctx.Done():
			a.observeAdmission(DeliveryResultOverflow, len(queue))
			return ctx.Err()
		}
	}
}

// enqueueWithSlotLocked writes a command after the caller has acquired one queue slot.
// The caller must hold a.mu so Stop cannot close the lifecycle between validation and enqueue.
func (a *managerAsync) enqueueWithSlotLocked(queue chan managerCommand, acceptDone <-chan struct{}, cmd managerCommand) error {
	select {
	case <-acceptDone:
		return ErrManagerClosed
	default:
	}
	select {
	case queue <- cmd:
		return nil
	default:
		return errManagerSlotMismatch
	}
}

var errManagerSlotMismatch = errors.New("internalv2/runtime/delivery: manager slot mismatch")

// releaseSlot returns one queue slot after a worker removes an accepted command.
func (a *managerAsync) releaseSlot() {
	select {
	case a.slots <- struct{}{}:
	default:
	}
}

func (a *managerAsync) observeAdmission(result string, queueDepth int) {
	if a.observer == nil {
		return
	}
	a.observer.ObserveManagerAdmission(ManagerAdmissionEvent{
		Result:     result,
		QueueDepth: queueDepth,
	})
}

func (a *managerAsync) runWorker(ctx context.Context) {
	for {
		select {
		case cmd := <-a.queue:
			a.releaseSlot()
			a.runCommand(cmd)
		case <-ctx.Done():
			a.drain()
			return
		}
	}
}

func (a *managerAsync) drain() {
	for {
		select {
		case cmd := <-a.queue:
			a.releaseSlot()
			a.runCommand(cmd)
		default:
			return
		}
	}
}

func (a *managerAsync) runCommand(cmd managerCommand) {
	err := a.manager.runEnvelope(context.Background(), cmd.envelope)
	result := deliveryResultForError(err)
	class := DeliveryErrorClass(err)
	a.observeTerminal(result, class)
}

func (a *managerAsync) observeTerminal(result, class string) {
	if a.observer == nil {
		return
	}
	a.observer.ObserveManagerTerminal(ManagerTerminalEvent{
		Result:     result,
		ErrorClass: class,
		QueueDepth: len(a.queue),
	})
}
