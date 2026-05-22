package channelplane

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

type appendFuture struct {
	once sync.Once
	done chan struct{}
	res  channel.AppendBatchResult
	err  error
}

func newAppendFuture() *appendFuture {
	return &appendFuture{done: make(chan struct{})}
}

func (f *appendFuture) complete(res channel.AppendBatchResult, err error) {
	f.once.Do(func() {
		f.res = res
		f.err = err
		close(f.done)
	})
}

func (f *appendFuture) wait(ctx context.Context) (channel.AppendBatchResult, error) {
	select {
	case <-f.done:
		return f.res, f.err
	case <-ctx.Done():
		return channel.AppendBatchResult{}, ctx.Err()
	}
}
