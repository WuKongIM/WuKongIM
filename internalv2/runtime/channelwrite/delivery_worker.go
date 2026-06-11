package channelwrite

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	defaultRecipientDeliveryQueueSize = 1024
	defaultRecipientDeliveryWorkers   = 1
)

// ErrRecipientDeliveryWorkerClosed reports that the recipient delivery worker is not accepting batches.
var ErrRecipientDeliveryWorkerClosed = errors.New("internalv2/channelwrite: recipient delivery worker closed")

type recipientDeliveryWorkerState uint8

const (
	recipientDeliveryWorkerClosed recipientDeliveryWorkerState = iota
	recipientDeliveryWorkerOpen
	recipientDeliveryWorkerClosing
)

// RecipientDeliveryWorkerOptions configures the bounded recipient delivery worker.
type RecipientDeliveryWorkerOptions struct {
	// Processor runs one accepted recipient-authority delivery batch.
	Processor *RecipientProcessor
	// QueueSize bounds accepted recipient batches waiting for workers.
	QueueSize int
	// Workers controls the number of delivery worker goroutines.
	Workers int
	// Observer receives terminal processing failures.
	Observer AppendObserver
}

// RecipientDeliveryWorker owns bounded async delivery admission for recipient-authority batches.
type RecipientDeliveryWorker struct {
	processor *RecipientProcessor
	queue     chan recipientDeliveryCommand
	slots     chan struct{}
	workers   int
	observer  AppendObserver

	mu sync.Mutex
	// state gates admission and lifecycle transitions.
	state recipientDeliveryWorkerState
	// acceptDone closes when the current lifecycle stops accepting enqueue waiters.
	acceptDone chan struct{}
	// done closes after all workers exit.
	done chan struct{}
}

type recipientDeliveryCommand struct {
	target RecipientAuthorityTarget
	batch  RecipientBatch
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
	slots := make(chan struct{}, queueSize)
	for i := 0; i < queueSize; i++ {
		slots <- struct{}{}
	}
	return &RecipientDeliveryWorker{
		processor: opts.Processor,
		queue:     make(chan recipientDeliveryCommand, queueSize),
		slots:     slots,
		workers:   workers,
		observer:  opts.Observer,
		state:     recipientDeliveryWorkerClosed,
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
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(w.workers)
	for i := 0; i < w.workers; i++ {
		go func() {
			defer wg.Done()
			w.runWorker(acceptDone)
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	w.acceptDone = acceptDone
	w.done = done
	w.state = recipientDeliveryWorkerOpen
	w.observeQueue()
	return nil
}

// Stop closes admission and drains already accepted delivery batches.
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
	done := w.done
	w.state = recipientDeliveryWorkerClosing
	close(acceptDone)
	w.mu.Unlock()
	return w.waitClosed(ctx, done)
}

// EnqueueRecipientBatch admits one recipient-authority batch for asynchronous delivery processing.
func (w *RecipientDeliveryWorker) EnqueueRecipientBatch(ctx context.Context, target RecipientAuthorityTarget, batch RecipientBatch) error {
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
	cmd := recipientDeliveryCommand{target: target, batch: batch.Clone()}

	for {
		w.mu.Lock()
		if w.state != recipientDeliveryWorkerOpen {
			w.mu.Unlock()
			admissionResult = recipientDeliveryResultClosed
			return ErrRecipientDeliveryWorkerClosed
		}
		queue := w.queue
		slots := w.slots
		acceptDone := w.acceptDone
		select {
		case <-acceptDone:
			w.mu.Unlock()
			admissionResult = recipientDeliveryResultClosed
			return ErrRecipientDeliveryWorkerClosed
		default:
		}
		select {
		case <-slots:
			if err := w.enqueueWithSlotLocked(queue, acceptDone, cmd); err != nil {
				w.mu.Unlock()
				w.releaseSlot()
				if errors.Is(err, ErrRecipientDeliveryWorkerClosed) {
					admissionResult = recipientDeliveryResultClosed
					return err
				}
				continue
			}
			w.mu.Unlock()
			w.observeQueue()
			return nil
		default:
		}
		w.mu.Unlock()

		select {
		case <-slots:
			w.mu.Lock()
			if w.state != recipientDeliveryWorkerOpen || w.acceptDone != acceptDone {
				w.mu.Unlock()
				w.releaseSlot()
				admissionResult = recipientDeliveryResultClosed
				return ErrRecipientDeliveryWorkerClosed
			}
			if err := w.enqueueWithSlotLocked(w.queue, acceptDone, cmd); err != nil {
				w.mu.Unlock()
				w.releaseSlot()
				if errors.Is(err, ErrRecipientDeliveryWorkerClosed) {
					admissionResult = recipientDeliveryResultClosed
					return err
				}
				continue
			}
			w.mu.Unlock()
			w.observeQueue()
			return nil
		case <-acceptDone:
			admissionResult = recipientDeliveryResultClosed
			return ErrRecipientDeliveryWorkerClosed
		case <-ctx.Done():
			admissionResult = recipientDeliveryAdmissionResultFromError(ctx.Err())
			return ctx.Err()
		}
	}
}

func (w *RecipientDeliveryWorker) enqueueWithSlotLocked(queue chan recipientDeliveryCommand, acceptDone <-chan struct{}, cmd recipientDeliveryCommand) error {
	select {
	case <-acceptDone:
		return ErrRecipientDeliveryWorkerClosed
	default:
	}
	select {
	case queue <- cmd:
		return nil
	default:
		return errRecipientDeliverySlotMismatch
	}
}

var errRecipientDeliverySlotMismatch = errors.New("internalv2/channelwrite: recipient delivery slot mismatch")

func (w *RecipientDeliveryWorker) runWorker(acceptDone <-chan struct{}) {
	for {
		select {
		case cmd := <-w.queue:
			w.releaseSlot()
			w.observeQueue()
			w.runCommand(cmd)
		case <-acceptDone:
			w.drain()
			return
		}
	}
}

func (w *RecipientDeliveryWorker) drain() {
	for {
		select {
		case cmd := <-w.queue:
			w.releaseSlot()
			w.observeQueue()
			w.runCommand(cmd)
		default:
			return
		}
	}
}

func (w *RecipientDeliveryWorker) runCommand(cmd recipientDeliveryCommand) {
	startedAt := time.Now()
	result := recipientDeliveryResultOK
	defer func() {
		if recovered := recover(); recovered != nil {
			result = recipientDeliveryResultPanic
			w.observeProcessingFailure(cmd, withPostCommitFailureDetail(effectPanicError(effectStagePostCommit, recovered), postCommitBatchDetail("panic", cmd.batch)))
		}
		w.observeProcess(RecipientDeliveryProcessObservation{
			Result:     result,
			Recipients: len(cmd.batch.Recipients),
			Duration:   positiveRecipientDeliveryDuration(time.Since(startedAt)),
		})
	}()
	if w.processor == nil {
		return
	}
	if err := w.processor.ProcessRecipientBatch(context.Background(), cmd.batch.Clone()); err != nil {
		result = recipientDeliveryResultError
		w.observeProcessingFailure(cmd, err)
	}
}

func (w *RecipientDeliveryWorker) observeQueue() {
	observeRecipientDeliveryQueue(w.observer, RecipientDeliveryQueueObservation{
		QueueDepth:    len(w.queue),
		QueueCapacity: cap(w.queue),
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

func (w *RecipientDeliveryWorker) observeProcessingFailure(cmd recipientDeliveryCommand, err error) {
	detail := postCommitFailureDetailFromError(err)
	if detail.Phase == "" {
		detail.Phase = "recipient_delivery"
	}
	targetDetail := postCommitTargetDetail(cmd.target)
	if detail.TargetHashSlot == 0 {
		detail.TargetHashSlot = targetDetail.TargetHashSlot
	}
	if detail.TargetSlotID == 0 {
		detail.TargetSlotID = targetDetail.TargetSlotID
	}
	if detail.TargetLeaderNodeID == 0 {
		detail.TargetLeaderNodeID = targetDetail.TargetLeaderNodeID
	}
	if detail.TargetRouteRevision == 0 {
		detail.TargetRouteRevision = targetDetail.TargetRouteRevision
	}
	if detail.TargetAuthorityEpoch == 0 {
		detail.TargetAuthorityEpoch = targetDetail.TargetAuthorityEpoch
	}
	observePostCommitFailure(w.observer, PostCommitFailureObservation{
		ChannelID:             cmd.batch.Event.ChannelID,
		ChannelType:           cmd.batch.Event.ChannelType,
		MessageID:             cmd.batch.Event.MessageID,
		MessageSeq:            cmd.batch.Event.MessageSeq,
		Result:                errorClass(err),
		Phase:                 detail.Phase,
		UID:                   detail.UID,
		UIDCount:              detail.UIDCount,
		RecipientCount:        detail.RecipientCount,
		TargetHashSlot:        detail.TargetHashSlot,
		TargetSlotID:          detail.TargetSlotID,
		TargetLeaderNodeID:    detail.TargetLeaderNodeID,
		TargetRouteRevision:   detail.TargetRouteRevision,
		TargetAuthorityEpoch:  detail.TargetAuthorityEpoch,
		DispatchTargetCount:   detail.DispatchTargetCount,
		DispatchBatchSize:     detail.DispatchBatchSize,
		DispatchOwnerNodeID:   detail.DispatchOwnerNodeID,
		DispatchOwnerRouteNum: detail.DispatchOwnerRouteNum,
		Err:                   err,
	})
}

func (w *RecipientDeliveryWorker) releaseSlot() {
	select {
	case w.slots <- struct{}{}:
	default:
	}
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
	w.mu.Lock()
	if w.state == recipientDeliveryWorkerClosing && w.done == done {
		w.state = recipientDeliveryWorkerClosed
		w.acceptDone = nil
		w.done = nil
	}
	w.mu.Unlock()
}

func (w *RecipientDeliveryWorker) finishClosedIfDoneLocked() bool {
	done := w.done
	if done == nil {
		w.state = recipientDeliveryWorkerClosed
		w.acceptDone = nil
		return true
	}
	select {
	case <-done:
		w.state = recipientDeliveryWorkerClosed
		w.acceptDone = nil
		w.done = nil
		return true
	default:
		return false
	}
}
