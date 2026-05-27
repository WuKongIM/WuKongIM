package reactor

import (
	"context"
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

// Result is the synchronous facade completion payload.
type Result struct {
	Append      ch.AppendResult
	AppendBatch ch.AppendBatchResult
	Pull        transport.PullResponse
	Err         error
}

// Future lets synchronous callers wait for reactor completion.
type Future struct {
	once sync.Once
	done chan struct{}
	mu   sync.Mutex
	res  Result
	// beforeComplete is an unexported test hook used to observe ordering before publish.
	beforeComplete func(Result)
}

// NewFuture creates an incomplete future.
func NewFuture() *Future {
	return &Future{done: make(chan struct{})}
}

// Complete publishes the first result and ignores later completions.
func (f *Future) Complete(result Result) {
	if f == nil {
		return
	}
	f.once.Do(func() {
		if f.beforeComplete != nil {
			f.beforeComplete(result)
		}
		f.mu.Lock()
		f.res = result
		f.mu.Unlock()
		close(f.done)
	})
}

// Done is closed when the future has a result.
func (f *Future) Done() <-chan struct{} {
	if f == nil {
		return nil
	}
	return f.done
}

// Result returns the completed result. Callers should wait for Done or Await first.
func (f *Future) Result() Result {
	if f == nil {
		return Result{Err: ch.ErrClosed}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.res
}

// Await waits for completion or context cancellation.
func (f *Future) Await(ctx context.Context) (Result, error) {
	if f == nil {
		return Result{}, ch.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-f.done:
		result := f.Result()
		return result, result.Err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}
