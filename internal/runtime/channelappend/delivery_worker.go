package channelappend

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	defaultRecipientDeliveryQueueSize   = 1024
	defaultRecipientDeliveryWorkers     = 1
	defaultRecipientDeliveryPlanTimeout = 5 * time.Second
)

// ErrRecipientDeliveryWorkerClosed reports that the recipient delivery worker is not accepting plans.
var ErrRecipientDeliveryWorkerClosed = errors.New("internal/channelappend: recipient delivery worker closed")

type recipientDeliveryWorkerState uint8

const (
	recipientDeliveryWorkerClosed recipientDeliveryWorkerState = iota
	recipientDeliveryWorkerOpen
	recipientDeliveryWorkerClosing
)

// RecipientDeliveryWorkerOptions configures the bounded recipient delivery worker.
type RecipientDeliveryWorkerOptions struct {
	// Processor runs one accepted recipient delivery plan.
	Processor *RecipientProcessor
	// QueueSize bounds accepted recipient delivery plans waiting for workers.
	QueueSize int
	// Workers controls the number of delivery worker goroutines.
	Workers int
	// PlanTimeout bounds one accepted delivery plan. Values <= 0 use a bounded default.
	PlanTimeout time.Duration
	// Observer receives terminal processing failures.
	Observer AppendObserver
	// Goroutines receives lifecycle ownership observations.
	Goroutines *goroutine.Registry
}

// RecipientDeliveryWorker owns bounded async delivery admission for recipient delivery plans.
type RecipientDeliveryWorker struct {
	processor   *RecipientProcessor
	queue       chan recipientDeliveryCommand
	workers     int
	planTimeout time.Duration
	observer    AppendObserver
	goroutines  *goroutine.Registry

	mu sync.Mutex
	// state gates admission and lifecycle transitions.
	state recipientDeliveryWorkerState
	// acceptDone closes when the current lifecycle stops accepting enqueue waiters.
	acceptDone chan struct{}
	// stopReady closes after every sender admitted before Stop has left Enqueue.
	stopReady chan struct{}
	// runCancel cancels in-flight and queued delivery plans for the current lifecycle.
	runCancel context.CancelFunc
	// done closes after all workers exit.
	done chan struct{}
	// admissionSenders counts Enqueue calls that crossed the open-state gate.
	admissionSenders sync.WaitGroup

	// inflight covers commands currently executing inside runCommand.
	inflight atomic.Int64
	// observationMu preserves gauge update order while queue and worker state change concurrently.
	observationMu sync.Mutex
}

// WorkerCapacity returns the configured recipient delivery worker concurrency.
func (w *RecipientDeliveryWorker) WorkerCapacity() int {
	if w == nil {
		return 0
	}
	return w.workers
}

type recipientDeliveryCommand struct {
	plan RecipientDeliveryPlan
}

// NewRecipientDeliveryWorker creates a bounded async recipient delivery worker.
func NewRecipientDeliveryWorker(opts RecipientDeliveryWorkerOptions) *RecipientDeliveryWorker {
	queueSize := opts.QueueSize
	if queueSize <= 0 {
		queueSize = defaultRecipientDeliveryQueueSize
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultRecipientDeliveryWorkers
	}
	planTimeout := opts.PlanTimeout
	if planTimeout <= 0 {
		planTimeout = defaultRecipientDeliveryPlanTimeout
	}
	return &RecipientDeliveryWorker{
		processor:   opts.Processor,
		queue:       make(chan recipientDeliveryCommand, queueSize),
		workers:     workers,
		planTimeout: planTimeout,
		observer:    opts.Observer,
		goroutines:  opts.Goroutines,
		state:       recipientDeliveryWorkerClosed,
	}
}

// Start opens admission and launches delivery workers.
func (w *RecipientDeliveryWorker) Start(context.Context) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	switch w.state {
	case recipientDeliveryWorkerOpen:
		return nil
	case recipientDeliveryWorkerClosing:
		if !w.finishClosedIfDoneLocked() {
			return ErrRecipientDeliveryWorkerClosed
		}
	}

	acceptDone := make(chan struct{})
	stopReady := make(chan struct{})
	runCtx, runCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(w.workers)
	for i := 0; i < w.workers; i++ {
		goroutine.SafeGo(w.goroutines, goroutine.TaskChannelAppendDeliveryWorker, func() {
			defer wg.Done()
			w.runWorker(runCtx, stopReady)
		})
	}
	goroutine.SafeGo(w.goroutines, goroutine.TaskChannelAppendDeliveryDoneWait, func() {
		wg.Wait()
		close(done)
	})
	w.acceptDone = acceptDone
	w.stopReady = stopReady
	w.runCancel = runCancel
	w.done = done
	w.state = recipientDeliveryWorkerOpen
	w.observePressure()
	return nil
}

// Stop closes admission and drains already accepted delivery plans.
func (w *RecipientDeliveryWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	w.mu.Lock()
	switch w.state {
	case recipientDeliveryWorkerClosed:
		w.mu.Unlock()
		return nil
	case recipientDeliveryWorkerClosing:
		done := w.done
		w.mu.Unlock()
		return w.waitClosed(ctx, done)
	}
	acceptDone := w.acceptDone
	stopReady := w.stopReady
	runCancel := w.runCancel
	done := w.done
	w.state = recipientDeliveryWorkerClosing
	close(acceptDone)
	w.mu.Unlock()
	if runCancel != nil {
		runCancel()
	}
	goroutine.SafeGo(w.goroutines, goroutine.TaskChannelAppendDeliveryAdmissionWait, func() {
		w.admissionSenders.Wait()
		close(stopReady)
	})
	return w.waitClosed(ctx, done)
}

// EnqueueRecipientBatch admits one recipient-authority batch for asynchronous delivery processing.
func (w *RecipientDeliveryWorker) EnqueueRecipientBatch(ctx context.Context, target RecipientAuthorityTarget, batch RecipientBatch) error {
	return w.EnqueueRecipientDeliveryPlan(ctx, RecipientDeliveryPlan{
		Event: batch.Event,
		Targets: []RecipientTargetBatch{{
			Target:     target,
			Recipients: batch.Recipients,
		}},
	})
}

// EnqueueRecipientDeliveryPlan admits one bounded multi-target recipient plan
// for asynchronous delivery processing.
func (w *RecipientDeliveryWorker) EnqueueRecipientDeliveryPlan(ctx context.Context, plan RecipientDeliveryPlan) error {
	if w == nil {
		return nil
	}
	startedAt := time.Now()
	admissionResult := recipientDeliveryResultAccepted
	defer func() {
		w.observeAdmission(admissionResult, startedAt)
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		admissionResult = recipientDeliveryAdmissionResultFromError(err)
		return err
	}
	cmd := recipientDeliveryCommand{plan: plan}
	w.mu.Lock()
	if w.state != recipientDeliveryWorkerOpen {
		w.mu.Unlock()
		admissionResult = recipientDeliveryResultClosed
		return ErrRecipientDeliveryWorkerClosed
	}
	queue := w.queue
	acceptDone := w.acceptDone
	w.admissionSenders.Add(1)
	w.mu.Unlock()
	defer w.admissionSenders.Done()

	select {
	case queue <- cmd:
		w.observePressure()
		return nil
	case <-acceptDone:
		admissionResult = recipientDeliveryResultClosed
		return ErrRecipientDeliveryWorkerClosed
	case <-ctx.Done():
		admissionResult = recipientDeliveryAdmissionResultFromError(ctx.Err())
		return ctx.Err()
	}
}

func (w *RecipientDeliveryWorker) runWorker(runCtx context.Context, stopReady <-chan struct{}) {
	for {
		select {
		case cmd := <-w.queue:
			w.runCommand(runCtx, cmd)
		case <-stopReady:
			w.drain(runCtx)
			return
		}
	}
}

func (w *RecipientDeliveryWorker) drain(runCtx context.Context) {
	for {
		select {
		case cmd := <-w.queue:
			w.runCommand(runCtx, cmd)
		default:
			return
		}
	}
}

func (w *RecipientDeliveryWorker) runCommand(runCtx context.Context, cmd recipientDeliveryCommand) {
	w.inflight.Add(1)
	defer func() {
		w.inflight.Add(-1)
		w.observePressure()
	}()
	w.observePressure()

	startedAt := time.Now()
	result := recipientDeliveryResultOK
	defer func() {
		if recovered := recover(); recovered != nil {
			result = recipientDeliveryResultPanic
			target, batch := recipientDeliveryCommandTarget(cmd, 0)
			w.observeProcessingFailure(cmd, withPostCommitFailureDetail(effectPanicError(effectStagePostCommit, recovered), postCommitBatchDetail("panic", batch)), target)
		}
		w.observeProcess(RecipientDeliveryProcessObservation{
			Result:     result,
			Recipients: cmd.plan.RecipientCount(),
			Duration:   positiveRecipientDeliveryDuration(time.Since(startedAt)),
		})
	}()
	if w.processor == nil {
		return
	}
	planCtx, cancel := context.WithTimeout(runCtx, w.planTimeout)
	defer cancel()
	for i, err := range w.processor.ProcessRecipientDeliveryPlan(planCtx, cmd.plan) {
		if err == nil {
			continue
		}
		result = recipientDeliveryProcessResult(result, err)
		target, _ := recipientDeliveryCommandTarget(cmd, i)
		w.observeProcessingFailure(cmd, err, target)
	}
}

func recipientDeliveryProcessResult(current string, err error) string {
	if errors.Is(err, ErrEffectPanic) {
		return recipientDeliveryResultPanic
	}
	if current == recipientDeliveryResultPanic {
		return current
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return recipientDeliveryResultTimeout
	}
	if current == recipientDeliveryResultTimeout {
		return current
	}
	if errors.Is(err, context.Canceled) {
		return recipientDeliveryResultCanceled
	}
	if current == recipientDeliveryResultCanceled {
		return current
	}
	return recipientDeliveryResultError
}

func (w *RecipientDeliveryWorker) observePressure() {
	w.observationMu.Lock()
	defer w.observationMu.Unlock()
	observeRecipientDeliveryQueue(w.observer, RecipientDeliveryQueueObservation{
		QueueDepth:    len(w.queue),
		QueueCapacity: cap(w.queue),
	})
	observeRecipientDeliveryWorkerPressure(w.observer, RecipientDeliveryWorkerPressureObservation{
		Inflight: int(w.inflight.Load()),
		Capacity: w.workers,
	})
}

func (w *RecipientDeliveryWorker) observeAdmission(result string, startedAt time.Time) {
	observeRecipientDeliveryAdmission(w.observer, RecipientDeliveryAdmissionObservation{
		Result:        result,
		QueueDepth:    len(w.queue),
		QueueCapacity: cap(w.queue),
		Duration:      positiveRecipientDeliveryDuration(time.Since(startedAt)),
	})
}

func (w *RecipientDeliveryWorker) observeProcess(obs RecipientDeliveryProcessObservation) {
	observeRecipientDeliveryProcess(w.observer, obs)
}

func recipientDeliveryAdmissionResultFromError(err error) string {
	switch {
	case err == nil:
		return recipientDeliveryResultAccepted
	case errors.Is(err, ErrRecipientDeliveryWorkerClosed):
		return recipientDeliveryResultClosed
	case errors.Is(err, context.Canceled):
		return recipientDeliveryResultCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return recipientDeliveryResultTimeout
	default:
		return recipientDeliveryResultError
	}
}

func positiveRecipientDeliveryDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Nanosecond
	}
	return d
}

func (w *RecipientDeliveryWorker) observeProcessingFailure(cmd recipientDeliveryCommand, err error, target RecipientAuthorityTarget) {
	detail := postCommitFailureDetailFromError(err)
	if detail.Phase == "" {
		detail.Phase = "recipient_delivery"
	}
	detail = detail.withFallback(postCommitTargetDetail(target))
	observePostCommitFailure(w.observer, detail.toObservation(cmd.plan.Event, 0, errorClass(err), err))
}

func recipientDeliveryCommandTarget(cmd recipientDeliveryCommand, index int) (RecipientAuthorityTarget, RecipientBatch) {
	if index < 0 || index >= len(cmd.plan.Targets) {
		return RecipientAuthorityTarget{}, RecipientBatch{Event: cmd.plan.Event}
	}
	target := cmd.plan.Targets[index]
	return target.Target, RecipientBatch{Event: cmd.plan.Event, Recipients: target.Recipients}
}

func (w *RecipientDeliveryWorker) waitClosed(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		w.finishClosedForDone(done)
		return nil
	}
	select {
	case <-done:
		w.finishClosedForDone(done)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *RecipientDeliveryWorker) finishClosedForDone(done <-chan struct{}) {
	closed := false
	w.mu.Lock()
	if w.state == recipientDeliveryWorkerClosing && w.done == done {
		w.state = recipientDeliveryWorkerClosed
		w.acceptDone = nil
		w.stopReady = nil
		w.runCancel = nil
		w.done = nil
		closed = true
	}
	w.mu.Unlock()
	if closed {
		w.observePressure()
	}
}

func (w *RecipientDeliveryWorker) finishClosedIfDoneLocked() bool {
	done := w.done
	if done == nil {
		w.state = recipientDeliveryWorkerClosed
		w.acceptDone = nil
		w.stopReady = nil
		w.runCancel = nil
		return true
	}
	select {
	case <-done:
		w.state = recipientDeliveryWorkerClosed
		w.acceptDone = nil
		w.stopReady = nil
		w.runCancel = nil
		w.done = nil
		return true
	default:
		return false
	}
}
