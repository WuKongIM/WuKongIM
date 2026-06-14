package client

import (
	"context"
	"sync"
)

// sendBatchWaiter collects SENDACK outcomes for one SendBatch call.
type sendBatchWaiter struct {
	// mu protects all mutable fields below.
	mu sync.Mutex
	// results stores outcomes by input order.
	results []SendResult
	// errs stores per-message send errors by input order.
	errs []error
	// completed marks which input indexes have reached a terminal outcome.
	completed []bool
	// done closes when the batch is ready to return.
	done chan struct{}
	// remaining tracks unresolved messages.
	remaining int
	// scan is the first index whose input-order outcome has not been observed.
	scan int
	// terminalErr is the first input-order error that should end the wait.
	terminalErr error
	// closed prevents double-closing done.
	closed bool
	// returned prevents late completions from mutating the returned result slice.
	returned bool
}

func newSendBatchWaiter(n int) *sendBatchWaiter {
	return &sendBatchWaiter{
		results:   make([]SendResult, n),
		errs:      make([]error, n),
		completed: make([]bool, n),
		done:      make(chan struct{}),
		remaining: n,
	}
}

func (w *sendBatchWaiter) complete(index int, out sendOutcome) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if index < 0 || index >= len(w.completed) || w.completed[index] {
		return
	}
	if w.returned || (w.closed && w.terminalErr != nil) {
		w.completed[index] = true
		w.remaining--
		return
	}
	w.results[index] = out.result
	w.errs[index] = out.err
	w.completed[index] = true
	w.remaining--

	if w.returned || w.closed {
		return
	}
	for w.scan < len(w.completed) && w.completed[w.scan] {
		if w.errs[w.scan] != nil {
			w.terminalErr = w.errs[w.scan]
			w.closed = true
			close(w.done)
			return
		}
		w.scan++
	}
	if w.remaining == 0 {
		w.closed = true
		close(w.done)
	}
}

func (w *sendBatchWaiter) wait(ctx context.Context) ([]SendResult, error) {
	if w == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-w.done:
		w.mu.Lock()
		w.returned = true
		results := w.results
		err := w.terminalErr
		w.mu.Unlock()
		return results, err
	case <-ctx.Done():
		w.mu.Lock()
		w.returned = true
		results := w.results
		w.mu.Unlock()
		return results, ctx.Err()
	}
}
