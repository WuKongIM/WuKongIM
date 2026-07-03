package cluster

import (
	"context"
	"time"
)

type Retryable func(error) bool

type Retry struct {
	Interval    time.Duration
	MaxWait     time.Duration
	IsRetryable Retryable
}

func (r Retry) Do(ctx context.Context, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}

	retryCtx := ctx
	var cancel context.CancelFunc
	if r.MaxWait > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			retryCtx, cancel = context.WithTimeout(ctx, r.MaxWait)
			defer cancel()
		}
	}

	var lastErr error
	for {
		err := fn(retryCtx)
		if err == nil {
			return nil
		}
		if r.IsRetryable == nil || !r.IsRetryable(err) {
			return err
		}
		lastErr = err

		if retryCtx.Err() != nil {
			return lastErr
		}
		if r.Interval <= 0 {
			continue
		}

		timer := time.NewTimer(r.Interval)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			return lastErr
		case <-timer.C:
		}
	}
}
