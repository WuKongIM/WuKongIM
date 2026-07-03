package delivery

import (
	"context"
	"errors"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

const defaultManagerAsyncWorkers = 1
const defaultManagerAsyncQueueSize = 1024

// ErrManagerClosed reports that the manager is not accepting async work.
var ErrManagerClosed = errors.New("internalv2/runtime/delivery: manager closed")

type managerState uint8

const (
	managerStateClosed managerState = iota
	managerStateOpen
	managerStateStopped
)

type managerCommand struct {
	envelope Envelope
}

// managerAsync owns bounded async admission for Manager.
type managerAsync struct {
	// manager is the parent runtime facade used by later execution slices.
	manager *Manager
	// queueSize bounds accepted commands that have not entered the executor.
	queueSize int
	// workers is the fixed number of async manager workers.
	workers int
	// observer receives admission decisions and later terminal outcomes.
	observer ManagerObserver

	mu sync.Mutex
	// state gates admission and lifecycle transitions.
	state managerState
	// queue owns bounded admission and direct worker execution while started.
	queue *workqueue.BoundedWorkerQueue[managerCommand]
}

func newManagerAsync(manager *Manager, queueSize, workers int, observer ManagerObserver) *managerAsync {
	if queueSize <= 0 {
		queueSize = defaultManagerAsyncQueueSize
	}
	if workers <= 0 {
		workers = defaultManagerAsyncWorkers
	}
	return &managerAsync{
		manager:   manager,
		queueSize: queueSize,
		workers:   workers,
		observer:  observer,
		state:     managerStateClosed,
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
	case managerStateStopped:
		return ErrManagerClosed
	}

	queue, err := workqueue.NewBoundedWorkerQueue[managerCommand](workqueue.BoundedWorkerQueueConfig{
		Name:      "delivery_manager",
		Workers:   a.workers,
		QueueSize: a.queueSize,
	}, a.runCommand)
	if err != nil {
		return err
	}
	a.queue = queue
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
	case managerStateStopped:
		a.mu.Unlock()
		return nil
	}
	queue := a.queue
	a.queue = nil
	a.state = managerStateStopped
	a.mu.Unlock()

	if queue == nil {
		return nil
	}
	return queue.Close(ctx)
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

	a.mu.Lock()
	queue := a.queue
	if a.state != managerStateOpen || queue == nil {
		depth := 0
		if queue != nil {
			depth = queue.QueueDepth()
		}
		a.mu.Unlock()
		a.observeAdmission(DeliveryResultError, depth)
		return ErrManagerClosed
	}
	a.mu.Unlock()

	err := queue.SubmitWait(ctx, cmd)
	depth := queue.QueueDepth()
	if err == nil {
		a.observeAdmission(DeliveryResultOK, depth)
		return nil
	}
	if errors.Is(err, workqueue.ErrClosed) {
		a.observeAdmission(DeliveryResultError, depth)
		return ErrManagerClosed
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, workqueue.ErrFull) {
		a.observeAdmission(DeliveryResultOverflow, depth)
		return err
	}
	a.observeAdmission(DeliveryResultError, depth)
	return err
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

func (a *managerAsync) runCommand(_ context.Context, cmd managerCommand) error {
	err := a.manager.runEnvelope(context.Background(), cmd.envelope)
	result := deliveryResultForError(err)
	class := DeliveryErrorClass(err)
	a.observeTerminal(result, class)
	return err
}

func (a *managerAsync) observeTerminal(result, class string) {
	if a.observer == nil {
		return
	}
	a.observer.ObserveManagerTerminal(ManagerTerminalEvent{
		Result:     result,
		ErrorClass: class,
		QueueDepth: a.queueDepth(),
	})
}

func (a *managerAsync) queueDepth() int {
	a.mu.Lock()
	queue := a.queue
	a.mu.Unlock()
	if queue == nil {
		return 0
	}
	return queue.QueueDepth()
}
