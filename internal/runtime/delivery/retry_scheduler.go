package delivery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	defaultRetryCapacity    = 1024
	defaultRetryMaxAttempts = 3
	defaultRetryWorkers     = 1

	// DeliveryRetryEventEnqueue reports a retry queue enqueue decision.
	DeliveryRetryEventEnqueue = "enqueue"
	// DeliveryRetryEventAttempt reports a background retry attempt result.
	DeliveryRetryEventAttempt = "attempt"
	// DeliveryRetryEventDrop reports retry work dropped by scheduler policy.
	DeliveryRetryEventDrop = "drop"
)

// ErrRetryQueueFull reports that retryable fanout work could not enter the bounded queue.
var ErrRetryQueueFull = errors.New("internal/runtime/delivery: retry queue full")

// RetryObserver receives bounded retry scheduler observations.
type RetryObserver interface {
	ObserveRetry(RetryEvent)
}

// RetryEvent describes one retry scheduler queue or attempt event.
type RetryEvent struct {
	// Event is the retry scheduler stage, such as enqueue, attempt, or drop.
	Event string
	// Result is the bounded outcome label for the event.
	Result string
	// ErrorClass is the normalized delivery error class when an error was present.
	ErrorClass string
	// Attempt is the one-based attempt number for the fanout task.
	Attempt int
	// QueueDepth is the current retry queue depth after the event.
	QueueDepth int
}

// RetryScheduler wraps a FanoutTaskRunner with bounded in-memory retry scheduling.
type RetryScheduler struct {
	runner      FanoutTaskRunner
	queue       chan FanoutTask
	maxAttempts int
	backoff     time.Duration
	workers     int
	observer    RetryObserver
	goroutines  *goroutine.Registry

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// RetrySchedulerOptions configures a bounded in-memory retry scheduler.
type RetrySchedulerOptions struct {
	// Runner executes fanout tasks for both first attempts and background retries.
	Runner FanoutTaskRunner
	// Capacity bounds retryable fanout tasks waiting for a background attempt.
	Capacity int
	// MaxAttempts caps total attempts for one task, including the initial attempt.
	MaxAttempts int
	// Backoff waits before each background retry attempt; zero retries immediately.
	Backoff time.Duration
	// Workers controls retry concurrency; values <= 0 use the default.
	Workers int
	// Observer receives bounded retry scheduler observations.
	Observer RetryObserver
	// Goroutines receives lifecycle ownership observations.
	Goroutines *goroutine.Registry
}

// NewRetryScheduler creates a bounded in-memory retry scheduler.
func NewRetryScheduler(opts RetrySchedulerOptions) *RetryScheduler {
	capacity := opts.Capacity
	if capacity <= 0 {
		capacity = defaultRetryCapacity
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultRetryMaxAttempts
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultRetryWorkers
	}
	return &RetryScheduler{
		runner:      opts.Runner,
		queue:       make(chan FanoutTask, capacity),
		maxAttempts: maxAttempts,
		backoff:     opts.Backoff,
		workers:     workers,
		observer:    opts.Observer,
		goroutines:  opts.Goroutines,
	}
}

// RunTask executes the first attempt and schedules retryable failures.
func (s *RetryScheduler) RunTask(ctx context.Context, task FanoutTask) error {
	if s == nil || s.runner == nil {
		return nil
	}
	task = normalizeRetryTask(task)
	err := s.runner.RunTask(ctx, task)
	if err == nil {
		return nil
	}
	if !IsRetryableDeliveryError(err) {
		return err
	}
	return s.scheduleRetry(task, err)
}

// Start starts background retry workers.
func (s *RetryScheduler) Start(context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(s.workers)
	for i := 0; i < s.workers; i++ {
		goroutine.SafeGo(s.goroutines, goroutine.TaskDeliveryRetryWorker, func() {
			defer wg.Done()
			s.runWorker(ctx)
		})
	}
	goroutine.SafeGo(s.goroutines, goroutine.TaskDeliveryRetryDoneWait, func() {
		wg.Wait()
		close(done)
	})
	s.cancel = cancel
	s.done = done
	s.started = true
	return nil
}

// Stop stops background retry workers after draining queued retry tasks.
func (s *RetryScheduler) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.started = false
	s.mu.Unlock()

	cancel()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// QueueDepth returns the current retry queue depth for tests and diagnostics.
func (s *RetryScheduler) QueueDepth() int {
	if s == nil {
		return 0
	}
	return len(s.queue)
}

func (s *RetryScheduler) runWorker(ctx context.Context) {
	for {
		select {
		case task := <-s.queue:
			s.retryTask(ctx, task, true)
		case <-ctx.Done():
			s.drain()
			return
		}
	}
}

func (s *RetryScheduler) drain() {
	for {
		select {
		case task := <-s.queue:
			s.retryTask(context.Background(), task, false)
		default:
			return
		}
	}
}

func (s *RetryScheduler) retryTask(ctx context.Context, task FanoutTask, wait bool) {
	if s == nil || s.runner == nil {
		return
	}
	if wait && s.backoff > 0 {
		timer := time.NewTimer(s.backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}
	err := s.runner.RunTask(context.Background(), task)
	if err == nil {
		s.observeRetry(DeliveryRetryEventAttempt, DeliveryResultOK, DeliveryErrorClassNone, task.Attempt)
		return
	}
	result := deliveryResultForError(err)
	class := DeliveryErrorClass(err)
	s.observeRetry(DeliveryRetryEventAttempt, result, class, task.Attempt)
	if IsRetryableDeliveryError(err) {
		if scheduleErr := s.scheduleRetry(task, err); scheduleErr != nil {
			s.observeRetry(DeliveryRetryEventDrop, DeliveryResultDropped, DeliveryErrorClass(scheduleErr), task.Attempt)
		}
		return
	}
	s.observeRetry(DeliveryRetryEventDrop, DeliveryResultError, class, task.Attempt)
}

func (s *RetryScheduler) scheduleRetry(task FanoutTask, cause error) error {
	next := nextRetryTask(task, cause)
	if next.Attempt > s.maxAttempts {
		s.observeRetry(DeliveryRetryEventDrop, DeliveryResultMaxAttempts, DeliveryErrorClass(cause), task.Attempt)
		return cause
	}
	select {
	case s.queue <- next:
		s.observeRetry(DeliveryRetryEventEnqueue, DeliveryResultOK, DeliveryErrorClassNone, next.Attempt)
		return nil
	default:
		s.observeRetry(DeliveryRetryEventEnqueue, DeliveryResultOverflow, DeliveryErrorClass(cause), next.Attempt)
		return fmt.Errorf("%w: %w", ErrRetryQueueFull, cause)
	}
}

func (s *RetryScheduler) observeRetry(event, result, class string, attempt int) {
	if s == nil || s.observer == nil {
		return
	}
	s.observer.ObserveRetry(RetryEvent{
		Event:      event,
		Result:     result,
		ErrorClass: class,
		Attempt:    attempt,
		QueueDepth: len(s.queue),
	})
}

func normalizeRetryTask(task FanoutTask) FanoutTask {
	task = cloneFanoutTask(task)
	if task.Attempt <= 0 {
		task.Attempt = 1
	}
	return task
}

func nextRetryTask(task FanoutTask, cause error) FanoutTask {
	next := cloneFanoutTask(task)
	if next.Attempt <= 0 {
		next.Attempt = 1
	}
	next.Attempt++
	if errors.Is(cause, ErrRetryablePushRoutes) {
		var pushErr *RetryablePushRoutesError
		if errors.As(cause, &pushErr) {
			if uids := retryRouteUIDs(pushErr.Routes()); len(uids) > 0 {
				next.Envelope.MessageScopedUIDs = uids
				next.Cursor = ""
			}
		}
	}
	return next
}

func cloneFanoutTask(task FanoutTask) FanoutTask {
	task.Envelope = cloneEnvelope(task.Envelope)
	return task
}

func retryRouteUIDs(routes []Route) []string {
	uids := make([]string, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.UID == "" {
			continue
		}
		if _, ok := seen[route.UID]; ok {
			continue
		}
		seen[route.UID] = struct{}{}
		uids = append(uids, route.UID)
	}
	return uids
}
