package app

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/conversationactive"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type conversationActiveFlusher interface {
	// FlushActiveRows persists at most batchRows dirty active rows; zero means all dirty rows.
	FlushActiveRows(context.Context, int) (conversationactive.FlushResult, error)
}

type conversationActiveFlushWorkerOptions struct {
	// Authority owns the local conversation active cache flush.
	Authority conversationActiveFlusher
	// FlushInterval controls how often dirty active rows are persisted.
	FlushInterval time.Duration
	// FlushTimeout bounds one dirty active-row flush attempt.
	FlushTimeout time.Duration
	// BatchRows bounds dirty active rows flushed in one tick.
	BatchRows int
	// PressureSignals receives coalesced dirty-cache pressure wakeups.
	PressureSignals <-chan conversationactive.PressureSignal
	// PressureRetryMinDelay is the first delay before retrying a pressure flush
	// that made no clear progress while retryable rows remain.
	PressureRetryMinDelay time.Duration
	// PressureRetryMaxDelay caps exponential pressure retry backoff while
	// selected rows keep making no clear progress.
	PressureRetryMaxDelay time.Duration
	// Observer receives pressure wakeup wait observations from the app-owned worker.
	Observer conversationactive.Observer
	// Logger records periodic flush failures.
	Logger wklog.Logger
}

// conversationActiveFlushWorker periodically persists dirty conversation active rows.
type conversationActiveFlushWorker struct {
	opts conversationActiveFlushWorkerOptions

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newConversationActiveFlushWorker(opts conversationActiveFlushWorkerOptions) *conversationActiveFlushWorker {
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = time.Second
	}
	if opts.FlushTimeout <= 0 {
		opts.FlushTimeout = 5 * time.Second
	}
	if opts.BatchRows <= 0 {
		opts.BatchRows = 512
	}
	if opts.PressureRetryMinDelay <= 0 {
		opts.PressureRetryMinDelay = 25 * time.Millisecond
	}
	if opts.PressureRetryMaxDelay <= 0 {
		opts.PressureRetryMaxDelay = 250 * time.Millisecond
	}
	if opts.PressureRetryMaxDelay < opts.PressureRetryMinDelay {
		opts.PressureRetryMaxDelay = opts.PressureRetryMinDelay
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &conversationActiveFlushWorker{opts: opts}
}

// Start begins periodic dirty active-row flushing.
func (w *conversationActiveFlushWorker) Start(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	if w.cancel != nil {
		w.mu.Unlock()
		cancel()
		return nil
	}
	w.cancel = cancel
	w.wg.Add(1)
	w.mu.Unlock()

	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskAppConversationFlush, func() { w.tick(runCtx) })
	return nil
}

// Stop cancels periodic flushing and drains remaining dirty active rows in bounded batches.
func (w *conversationActiveFlushWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	cancel := w.cancel
	w.cancel = nil
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskAppConversationFlush, func() {
		w.wg.Wait()
		close(done)
	})
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return w.flushFinal(ctx)
}

func (w *conversationActiveFlushWorker) tick(ctx context.Context) {
	defer w.wg.Done()
	ticker := time.NewTicker(w.opts.FlushInterval)
	defer ticker.Stop()
	pressureSignals := w.opts.PressureSignals
	var pressureRetryTimer *time.Timer
	var pressureRetry <-chan time.Time
	pressureRetryDelay := w.opts.PressureRetryMinDelay
	stopPressureRetry := func(resetDelay bool) {
		if pressureRetryTimer != nil {
			if !pressureRetryTimer.Stop() {
				select {
				case <-pressureRetryTimer.C:
				default:
				}
			}
		}
		pressureRetry = nil
		if resetDelay {
			pressureRetryDelay = w.opts.PressureRetryMinDelay
		}
	}
	defer stopPressureRetry(false)
	schedulePressureRetry := func() {
		if pressureRetryTimer == nil {
			pressureRetryTimer = time.NewTimer(pressureRetryDelay)
		} else {
			stopPressureRetry(false)
			pressureRetryTimer.Reset(pressureRetryDelay)
		}
		pressureRetry = pressureRetryTimer.C
		pressureRetryDelay = nextConversationActivePressureRetryDelay(
			pressureRetryDelay,
			w.opts.PressureRetryMaxDelay,
		)
	}
	handlePressureResult := func(result conversationactive.FlushResult, err error) {
		if err == nil && result.Selected > 0 && result.Cleared == 0 && result.Requeued > 0 {
			schedulePressureRetry()
			return
		}
		stopPressureRetry(true)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := w.flushOnce(ctx, false)
			if pressureRetry != nil {
				handlePressureResult(result, err)
			}
		case <-pressureRetry:
			pressureRetry = nil
			result, err := w.flushOnce(ctx, false)
			handlePressureResult(result, err)
		case signal, ok := <-pressureSignals:
			if !ok {
				pressureSignals = nil
				continue
			}
			if w.opts.Observer != nil {
				wait := time.Since(signal.EnqueuedAt)
				if signal.EnqueuedAt.IsZero() || wait < 0 {
					wait = 0
				}
				w.opts.Observer.ObserveConversationActivePressure(conversationactive.PressureObservation{
					Event:              "signal_received",
					WakeupWaitDuration: wait,
				})
			}
			stopPressureRetry(true)
			result, err := w.flushOnce(ctx, false)
			handlePressureResult(result, err)
		}
	}
}

func nextConversationActivePressureRetryDelay(current, maximum time.Duration) time.Duration {
	if current <= 0 {
		return maximum
	}
	if current >= maximum || current > maximum/2 {
		return maximum
	}
	return current * 2
}

func (w *conversationActiveFlushWorker) flushFinal(ctx context.Context) error {
	for {
		result, err := w.flushOnce(ctx, true)
		if err != nil {
			return err
		}
		if result.Selected == 0 {
			return nil
		}
	}
}

func (w *conversationActiveFlushWorker) flushOnce(ctx context.Context, final bool) (conversationactive.FlushResult, error) {
	if w == nil || w.opts.Authority == nil {
		return conversationactive.FlushResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return conversationactive.FlushResult{}, err
	}
	flushCtx := ctx
	var cancel context.CancelFunc
	if w.opts.FlushTimeout > 0 {
		flushCtx, cancel = context.WithTimeout(ctx, w.opts.FlushTimeout)
		defer cancel()
	}
	result, err := w.opts.Authority.FlushActiveRows(flushCtx, w.opts.BatchRows)
	if err == nil {
		return result, nil
	}
	if !final && ctx.Err() != nil {
		return result, err
	}
	w.opts.Logger.Warn("conversation active flush failed",
		wklog.Event("internal.app.conversation_active_flush_failed"),
		wklog.Bool("final", final),
		wklog.Int("batchRows", w.opts.BatchRows),
		wklog.Error(err),
	)
	return result, err
}
