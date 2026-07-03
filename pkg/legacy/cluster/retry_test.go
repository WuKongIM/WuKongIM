package cluster

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryDoRetriesUntilSuccess(t *testing.T) {
	retry := Retry{
		Interval: 5 * time.Millisecond,
		MaxWait:  50 * time.Millisecond,
		IsRetryable: func(err error) bool {
			return errors.Is(err, ErrNotLeader)
		},
	}

	attempts := 0
	err := retry.Do(context.Background(), func(context.Context) error {
		attempts++
		if attempts < 3 {
			return ErrNotLeader
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Retry.Do() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("Retry.Do() attempts = %d, want 3", attempts)
	}
}

func TestRetryDoStopsOnNonRetryableError(t *testing.T) {
	sentinel := errors.New("stop")
	retry := Retry{
		Interval: 5 * time.Millisecond,
		MaxWait:  50 * time.Millisecond,
		IsRetryable: func(err error) bool {
			return errors.Is(err, ErrNotLeader)
		},
	}

	attempts := 0
	err := retry.Do(context.Background(), func(context.Context) error {
		attempts++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Retry.Do() error = %v, want %v", err, sentinel)
	}
	if attempts != 1 {
		t.Fatalf("Retry.Do() attempts = %d, want 1", attempts)
	}
}

func TestRetryDoReturnsLastRetryableErrorWhenBudgetExpires(t *testing.T) {
	retry := Retry{
		Interval: 5 * time.Millisecond,
		MaxWait:  15 * time.Millisecond,
		IsRetryable: func(err error) bool {
			return errors.Is(err, ErrNotLeader)
		},
	}

	start := time.Now()
	err := retry.Do(context.Background(), func(context.Context) error {
		return ErrNotLeader
	})
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Retry.Do() error = %v, want %v", err, ErrNotLeader)
	}
	if time.Since(start) < retry.MaxWait {
		t.Fatalf("Retry.Do() elapsed = %v, want at least %v", time.Since(start), retry.MaxWait)
	}
}
