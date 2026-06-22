package pluginhook

import (
	"context"
	"errors"
	"sync"
	"time"

	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	defaultQueueSize        = 1024
	defaultWorkers          = 1
	defaultInvokeTimeout    = time.Second
	enqueueAdmissionTimeout = 2 * time.Millisecond
	enqueueResultAccepted   = "accepted"
	enqueueResultFull       = "full"
	enqueueResultClosed     = "closed"
	invokeResultOK          = "ok"
	invokeResultError       = "error"
	invokeResultTimeout     = "timeout"
	invokeResultPanic       = "panic"
)

var (
	errNilUsecase    = errors.New("pluginhook: persist-after usecase is required")
	ErrWorkerClosing = errors.New("pluginhook: worker is closing")
)

type workerState uint8

const (
	workerStateClosed workerState = iota
	workerStateOpen
	workerStateClosing
)

// PersistAfterUsecase runs one PersistAfterCommitted plugin hook invocation.
type PersistAfterUsecase interface {
	PersistAfterCommitted(context.Context, pluginevents.PersistAfterCommitted) error
}

// Observer records enqueue and invoke outcomes for plugin hook runtime metrics.
type Observer interface {
	ObservePersistAfterEnqueue(result string, wait time.Duration)
	ObservePersistAfterInvoke(result string, d time.Duration)
}

// Options configures the bounded plugin hook worker.
type Options struct {
	// Usecase handles one accepted PersistAfterCommitted event.
	Usecase PersistAfterUsecase
	// QueueSize bounds accepted events waiting for workers.
	QueueSize int
	// Workers controls the number of worker goroutines.
	Workers int
	// Timeout bounds one hook invocation duration.
	Timeout time.Duration
	// Observer receives enqueue and invoke observations.
	Observer Observer
	// Logger receives concise runtime warnings.
	Logger wklog.Logger
}

// workerRun owns one lifecycle generation so close/drain never races with restart.
type workerRun struct {
	ctx        context.Context
	cancel     context.CancelFunc
	queue      chan pluginevents.PersistAfterCommitted
	acceptDone chan struct{}
	done       chan struct{}
	finalized  chan struct{}
}

// Worker provides fail-open bounded admission for async plugin hook side effects.
type Worker struct {
	usecase   PersistAfterUsecase
	queueSize int
	workers   int
	timeout   time.Duration
	observer  Observer
	logger    wklog.Logger

	mu    sync.Mutex
	state workerState
	run   *workerRun
}

// NewWorker constructs a bounded plugin hook worker with normalized defaults.
func NewWorker(opts Options) *Worker {
	queueSize := opts.QueueSize
	if queueSize <= 0 {
		queueSize = defaultQueueSize
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultInvokeTimeout
	}
	logger := opts.Logger
	if logger == nil {
		logger = wklog.NewNop()
	}
	return &Worker{
		usecase:   opts.Usecase,
		queueSize: queueSize,
		workers:   workers,
		timeout:   timeout,
		observer:  opts.Observer,
		logger:    logger,
		state:     workerStateClosed,
	}
}

// Start launches worker goroutines. Repeated Start calls while open are idempotent.
func (w *Worker) Start(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if w.usecase == nil {
		return errNilUsecase
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	switch w.state {
	case workerStateOpen:
		return nil
	case workerStateClosing:
		return ErrWorkerClosing
	}

	runCtx, cancel := context.WithCancel(context.Background())
	run := &workerRun{
		ctx:        runCtx,
		cancel:     cancel,
		queue:      make(chan pluginevents.PersistAfterCommitted, w.queueSize),
		acceptDone: make(chan struct{}),
		done:       make(chan struct{}),
		finalized:  make(chan struct{}),
	}
	w.run = run
	w.state = workerStateOpen

	var runWG sync.WaitGroup
	runWG.Add(w.workers)
	for i := 0; i < w.workers; i++ {
		go func() {
			defer runWG.Done()
			w.runWorker(run)
		}()
	}
	go func() {
		runWG.Wait()
		close(run.done)
	}()
	return nil
}

// Stop cancels workers and waits for the current run to finalize.
func (w *Worker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	run, err := w.beginStop()
	if err != nil || run == nil {
		return err
	}

	select {
	case <-run.finalized:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// EnqueuePersistAfter clones the event and attempts bounded admission without failing the caller.
func (w *Worker) EnqueuePersistAfter(_ context.Context, event pluginevents.PersistAfterCommitted) {
	if w == nil {
		return
	}
	startedAt := time.Now()
	result := enqueueResultAccepted
	defer func() {
		w.observeEnqueue(result, time.Since(startedAt))
	}()

	run, cloned, state := w.tryEnqueueFast(event)
	switch state {
	case enqueueResultClosed:
		result = enqueueResultClosed
		return
	case enqueueResultAccepted:
		return
	}

	timer := time.NewTimer(enqueueAdmissionTimeout)
	defer timer.Stop()

	select {
	case run.queue <- cloned:
		if !w.runStillOpen(run) {
			result = enqueueResultClosed
		}
		return
	case <-run.acceptDone:
		result = enqueueResultClosed
		return
	case <-timer.C:
		result = enqueueResultFull
		return
	}
}

func (w *Worker) tryEnqueueFast(event pluginevents.PersistAfterCommitted) (*workerRun, pluginevents.PersistAfterCommitted, string) {
	cloned := event.Clone()
	run, accepted, open := w.tryEnqueueCloned(nil, cloned)
	if !open {
		return nil, cloned, enqueueResultClosed
	}
	if accepted {
		return nil, cloned, enqueueResultAccepted
	}
	return run, cloned, enqueueResultFull
}

func (w *Worker) tryEnqueueCloned(expected *workerRun, cloned pluginevents.PersistAfterCommitted) (*workerRun, bool, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.state != workerStateOpen || w.run == nil {
		return nil, false, false
	}
	run := w.run
	if expected != nil && run != expected {
		return nil, false, false
	}
	select {
	case run.queue <- cloned:
		return nil, true, true
	default:
		return run, false, true
	}
}

func (w *Worker) beginStop() (*workerRun, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	switch w.state {
	case workerStateClosed:
		return nil, nil
	case workerStateOpen:
		run := w.run
		w.state = workerStateClosing
		if run != nil {
			run.cancel()
			close(run.acceptDone)
			go w.finalizeRun(run)
		}
		return run, nil
	case workerStateClosing:
		return w.run, nil
	default:
		return nil, nil
	}
}

func (w *Worker) finalizeRun(run *workerRun) {
	<-run.done
	w.discardQueued(run.queue)

	w.mu.Lock()
	if w.run == run {
		w.run = nil
		w.state = workerStateClosed
	}
	w.mu.Unlock()

	close(run.finalized)
}

func (w *Worker) runWorker(run *workerRun) {
	for {
		select {
		case <-run.ctx.Done():
			return
		case event := <-run.queue:
			if run.ctx.Err() != nil {
				return
			}
			w.invoke(run.ctx, event)
		}
	}
}

func (w *Worker) runStillOpen(run *workerRun) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state == workerStateOpen && w.run == run
}

func (w *Worker) invoke(runCtx context.Context, event pluginevents.PersistAfterCommitted) {
	startedAt := time.Now()
	result := invokeResultOK
	defer func() {
		if recovered := recover(); recovered != nil {
			result = invokeResultPanic
			w.logger.Warn("pluginhook persist-after panic")
		}
		w.observeInvoke(result, time.Since(startedAt))
	}()

	invokeCtx, cancel := context.WithTimeout(runCtx, w.timeout)
	defer cancel()

	if err := w.usecase.PersistAfterCommitted(invokeCtx, event); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(invokeCtx.Err(), context.DeadlineExceeded) {
			result = invokeResultTimeout
			w.logger.Warn("pluginhook persist-after timeout")
			return
		}
		if errors.Is(err, context.Canceled) && runCtx.Err() != nil {
			result = invokeResultTimeout
			return
		}
		result = invokeResultError
		w.logger.Warn("pluginhook persist-after failed")
	}
}

func (w *Worker) discardQueued(queue <-chan pluginevents.PersistAfterCommitted) {
	for {
		select {
		case <-queue:
		default:
			return
		}
	}
}

func (w *Worker) observeEnqueue(result string, wait time.Duration) {
	if w == nil || w.observer == nil {
		return
	}
	w.observer.ObservePersistAfterEnqueue(result, wait)
}

func (w *Worker) observeInvoke(result string, d time.Duration) {
	if w == nil || w.observer == nil {
		return
	}
	w.observer.ObservePersistAfterInvoke(result, d)
}
