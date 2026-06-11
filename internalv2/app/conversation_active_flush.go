package app

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/conversationactive"
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
	// BatchRows bounds dirty active rows flushed in one tick.
	BatchRows int
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
	if opts.BatchRows <= 0 {
		opts.BatchRows = 512
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

	go w.tick(runCtx)
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
	go func() {
		w.wg.Wait()
		close(done)
	}()
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = w.flushOnce(ctx, false)
		}
	}
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
	result, err := w.opts.Authority.FlushActiveRows(ctx, w.opts.BatchRows)
	if err == nil {
		return result, nil
	}
	if !final && ctx.Err() != nil {
		return result, err
	}
	w.opts.Logger.Warn("conversation active flush failed",
		wklog.Event("internalv2.app.conversation_active_flush_failed"),
		wklog.Bool("final", final),
		wklog.Int("batchRows", w.opts.BatchRows),
		wklog.Error(err),
	)
	return result, err
}
