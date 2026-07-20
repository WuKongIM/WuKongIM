package workload

import (
	"context"
	"errors"
	"fmt"
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

func TestRunScheduledMessagesByKeyLimitAllowsTwoInFlightPerKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := make(chan int, 3)
	release := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	done := make(chan error, 1)

	go func() {
		done <- runScheduledMessagesByKeyLimitWithStats(ctx, 3, 0, 3, 2, func(int) string {
			return "sender-1"
		}, func(ctx context.Context, offset int) error {
			started <- offset
			select {
			case <-release[offset]:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, nil)
	}()

	first := []int{<-started, <-started}
	require.ElementsMatch(t, []int{0, 1}, first)
	select {
	case extra := <-started:
		t.Fatalf("unexpected message %d started before a per-key slot was released", extra)
	case <-time.After(25 * time.Millisecond):
	}

	close(release[0])
	require.Equal(t, 2, <-started)
	close(release[1])
	close(release[2])

	require.NoError(t, <-done)
}

func TestRunScheduledMessagesByKeySkipsBusyKeyWhenDispatching(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := make(chan int, 4)
	release := make(chan struct{})
	done := make(chan error, 1)
	keys := []string{"sender-1", "sender-1", "sender-2", "sender-3"}

	go func() {
		done <- runScheduledMessagesByKey(ctx, 4, 0, 2, func(offset int) string {
			return keys[offset]
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

	first := []int{<-started, <-started}
	require.ElementsMatch(t, []int{0, 2}, first)
	select {
	case extra := <-started:
		t.Fatalf("unexpected message %d started while both dispatch slots were still busy", extra)
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-done)
}

func TestRunScheduledMessagesByKeyDropsBusyKeyWaitersAfterWindowExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := make(chan int, 3)
	release := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(release)
	}()

	err := runScheduledMessagesByKey(ctx, 3, 5*time.Millisecond, 3, func(int) string {
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

	require.NoError(t, err)
	close(started)
	require.Equal(t, []int{0}, drainStartedOffsets(started))
}

func TestRunScheduledMessagesByKeyStatsRecordWindowDropsAndBusyKeyStalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := make(chan int, 3)
	release := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(release)
	}()

	stats := &scheduledMessageStats{}
	err := runScheduledMessagesByKeyWithStats(ctx, 3, 5*time.Millisecond, 3, func(int) string {
		return "sender-1"
	}, func(ctx context.Context, offset int) error {
		started <- offset
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, stats)

	require.NoError(t, err)
	close(started)
	require.Equal(t, []int{0}, drainStartedOffsets(started))
	require.Equal(t, uint64(3), stats.Planned)
	require.Equal(t, uint64(3), stats.Enqueued)
	require.Equal(t, uint64(1), stats.Dispatched)
	require.Equal(t, uint64(2), stats.DroppedPendingWindowExpired)
	require.GreaterOrEqual(t, stats.BusyKeyStalls, uint64(1))
	require.GreaterOrEqual(t, stats.MaxPendingDepth, 1)
	require.Equal(t, 1, stats.MaxActive)
	require.Equal(t, 1, stats.MaxBusyKeys)
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

func TestRunScheduledMessagesCatchesUpAfterBlockedDispatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Keep a wide window after the blocked first dispatch. With only four
	// messages the old test left roughly 10ms between the first timer wakeup and
	// window expiry, so repository-wide parallel test load could measure the
	// scheduler after expiry and exercise the valid drop path instead.
	const totalMessages = 20
	started := make(chan int, totalMessages)
	err := runScheduledMessages(ctx, totalMessages, 10*time.Millisecond, 1, func(ctx context.Context, offset int) error {
		started <- offset
		if offset == 0 {
			select {
			case <-time.After(30 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})

	require.NoError(t, err)
	close(started)
	want := make([]int, totalMessages)
	for offset := range want {
		want[offset] = offset
	}
	require.ElementsMatch(t, want, drainStartedOffsets(started))
}

func TestRunScheduledMessagesByKeyDispatchesReadyKeysBehindLargeBusyPrefix(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		busyPrefix = 100000
		readyCount = 1000
	)
	releaseBusy := make(chan struct{})
	readyStarted := make(chan int, readyCount)
	done := make(chan error, 1)

	go func() {
		done <- runScheduledMessagesByKey(ctx, busyPrefix+readyCount, 0, readyCount+1, func(offset int) string {
			if offset < busyPrefix {
				return "busy-sender"
			}
			return fmt.Sprintf("ready-sender-%d", offset)
		}, func(ctx context.Context, offset int) error {
			if offset < busyPrefix {
				select {
				case <-releaseBusy:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			readyStarted <- offset
			return nil
		})
	}()

	require.Eventually(t, func() bool {
		return len(readyStarted) == readyCount
	}, 75*time.Millisecond, time.Millisecond)
	close(releaseBusy)
	cancel()
	select {
	case err := <-done:
		require.True(t, err == nil || errors.Is(err, context.Canceled), "unexpected scheduler error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop after cancellation")
	}
}

func drainStartedOffsets(started <-chan int) []int {
	offsets := make([]int, 0)
	for offset := range started {
		offsets = append(offsets, offset)
	}
	return offsets
}
