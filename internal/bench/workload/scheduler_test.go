package workload

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunScheduledMessagesBoundsInFlight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := make(chan int, 6)
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- runScheduledMessages(ctx, 6, 0, 3, func(ctx context.Context, offset int) error {
			started <- offset
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	first := []int{<-started, <-started, <-started}
	require.ElementsMatch(t, []int{0, 1, 2}, first)
	select {
	case extra := <-started:
		t.Fatalf("unexpected message %d started before a concurrency slot was released", extra)
	case <-time.After(25 * time.Millisecond):
	}

	release <- struct{}{}
	require.Equal(t, 3, <-started)
	close(release)

	require.NoError(t, <-done)
}

func TestRunScheduledMessagesByKeySerializesSameKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := make(chan int, 4)
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- runScheduledMessagesByKey(ctx, 4, 0, 4, func(int) string {
			return "sender-1"
		}, func(ctx context.Context, offset int) error {
			started <- offset
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	first := <-started
	select {
	case extra := <-started:
		t.Fatalf("unexpected same-sender message %d started before the previous sendack finished", extra)
	case <-time.After(25 * time.Millisecond):
	}

	release <- struct{}{}
	second := <-started
	require.NotEqual(t, first, second)
	close(release)

	require.NoError(t, <-done)
}

func TestRunScheduledMessagesStopsSchedulingWhenWindowExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := make(chan int, 4)
	err := runScheduledMessages(ctx, 4, 5*time.Millisecond, 1, func(ctx context.Context, offset int) error {
		started <- offset
		time.Sleep(40 * time.Millisecond)
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, len(started))
	require.Equal(t, 0, <-started)
}
