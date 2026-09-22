package conn

import (
	"context"
	"sync"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// inboundQueueContext retains the ordinary request context while allowing its
// single queue owner to observe the connection's explicit cancellation path.
// Only requests without a caller deadline use this hook: budget timer contexts
// retain the standard context.AfterFunc path. The owner is never pooled.
type inboundQueueContext struct {
	// Context preserves standard deadline, cancellation and value propagation.
	context.Context
	// cancelContext is invoked before scheduling the queue cancellation callback.
	cancelContext context.CancelFunc
	// mu arbitrates detaching the queue callback against cancellation. Callbacks
	// always run asynchronously and outside mu, like context.AfterFunc.
	mu sync.Mutex
	// queueCancel is cleared by whichever operation claims or detaches it.
	queueCancel func()
	// watchInstalled prevents a stale stop function from detaching a later watch.
	watchInstalled bool
}

// WatchQueueCancellation installs the one queue owner's callback. Additional
// registrations use the standard implementation without replacing that owner.
// A successful stop prevents invocation; false does not wait for its completion.
func (r *inboundQueueContext) WatchQueueCancellation(f func()) func() bool {
	r.mu.Lock()
	if r.watchInstalled {
		r.mu.Unlock()
		return context.AfterFunc(r.Context, f)
	}
	r.watchInstalled = true
	if r.Err() != nil {
		r.mu.Unlock()
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskTransportRPCService, f)
		return func() bool { return false }
	}
	r.queueCancel = f
	r.mu.Unlock()
	return r.stopQueueCancellation
}

func (r *inboundQueueContext) stopQueueCancellation() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := r.queueCancel != nil
	r.queueCancel = nil
	return pending
}

// cancel is used by explicit cancel frames, connection close and request finish.
// Close cancels the parent first; the table walk still invokes this method so
// listeners installed before parent cancellation cannot be left behind.
func (r *inboundQueueContext) cancel() {
	r.cancelContext()
	r.mu.Lock()
	f := r.queueCancel
	r.queueCancel = nil
	r.mu.Unlock()
	if f != nil {
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskTransportRPCService, f)
	}
}
