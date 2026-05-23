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
	Fetch       ch.FetchResult
	Pull        transport.PullResponse
	ApplyLEO    uint64
	Err         error
}

// Future lets synchronous callers wait for reactor completion.
type Future struct {
	once sync.Once
	ch   chan Result
	// beforeComplete is an unexported test hook used to observe ordering before publish.
	beforeComplete func(Result)
}

// NewFuture creates an incomplete future.
func NewFuture() *Future {
	return &Future{ch: make(chan Result, 1)}
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
		f.ch <- result
	})
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
	case result := <-f.ch:
		return result, result.Err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}
