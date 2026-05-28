package workload

import (
	"context"
	"sync"
	"time"
)

func runScheduledMessages(ctx context.Context, totalMessages int, interval time.Duration, maxConcurrency int, send func(context.Context, int) error) error {
	return runScheduledMessagesByKey(ctx, totalMessages, interval, maxConcurrency, nil, send)
}

// runScheduledMessagesByKey bounds global in-flight sends and serializes sends
// that share a key, which keeps one simulated client from pipelining sendacks.
func runScheduledMessagesByKey(ctx context.Context, totalMessages int, interval time.Duration, maxConcurrency int, keyForOffset func(int) string, send func(context.Context, int) error) error {
	if totalMessages <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	if maxConcurrency > totalMessages {
		maxConcurrency = totalMessages
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	recordError := func(err error) {
		if err == nil {
			return
		}
		select {
		case errCh <- err:
			cancel()
		default:
		}
	}
	firstError := func() error {
		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	}

	sem := make(chan struct{}, maxConcurrency)
	keyLimiter := newScheduledMessageKeyLimiter()
	var wg sync.WaitGroup
	startAt := time.Now()
	var stopAt time.Time
	if interval > 0 {
		stopAt = startAt.Add(interval * time.Duration(totalMessages))
	}
	for offset := 0; offset < totalMessages; offset++ {
		if err := firstError(); err != nil {
			wg.Wait()
			return err
		}
		if !stopAt.IsZero() {
			dueAt := startAt.Add(interval * time.Duration(offset))
			if wait := time.Until(dueAt); wait > 0 {
				if err := sleepContext(runCtx, wait); err != nil {
					cancel()
					wg.Wait()
					if first := firstError(); first != nil {
						return first
					}
					return err
				}
			}
			if !time.Now().Before(stopAt) {
				wg.Wait()
				if err := firstError(); err != nil {
					return err
				}
				return ctx.Err()
			}
			acquired, err := acquireScheduledMessageSlot(runCtx, sem, stopAt)
			if err != nil {
				wg.Wait()
				if first := firstError(); first != nil {
					return first
				}
				return err
			}
			if !acquired {
				wg.Wait()
				if err := firstError(); err != nil {
					return err
				}
				return ctx.Err()
			}
		} else {
			select {
			case sem <- struct{}{}:
			case <-runCtx.Done():
				wg.Wait()
				if err := firstError(); err != nil {
					return err
				}
				return runCtx.Err()
			}
		}
		wg.Add(1)
		offset := offset
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			releaseKey, err := keyLimiter.acquire(runCtx, keyForOffset, offset)
			if err != nil {
				recordError(err)
				return
			}
			defer releaseKey()
			recordError(send(runCtx, offset))
		}()
	}
	wg.Wait()
	if err := firstError(); err != nil {
		return err
	}
	return ctx.Err()
}

func acquireScheduledMessageSlot(ctx context.Context, sem chan<- struct{}, stopAt time.Time) (bool, error) {
	if stopAt.IsZero() {
		select {
		case sem <- struct{}{}:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	remaining := time.Until(stopAt)
	if remaining <= 0 {
		return false, nil
	}
	timer := time.NewTimer(remaining)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case sem <- struct{}{}:
		return true, nil
	case <-timer.C:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

type scheduledMessageKeyLimiter struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

func newScheduledMessageKeyLimiter() *scheduledMessageKeyLimiter {
	return &scheduledMessageKeyLimiter{slots: make(map[string]chan struct{})}
}

func (l *scheduledMessageKeyLimiter) acquire(ctx context.Context, keyForOffset func(int) string, offset int) (func(), error) {
	if keyForOffset == nil {
		return func() {}, nil
	}
	key := keyForOffset(offset)
	if key == "" {
		return func() {}, nil
	}
	l.mu.Lock()
	slot := l.slots[key]
	if slot == nil {
		slot = make(chan struct{}, 1)
		l.slots[key] = slot
	}
	l.mu.Unlock()

	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}
}
