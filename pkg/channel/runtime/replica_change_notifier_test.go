package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReplicaChangeNotifierNotifyWithoutWaitersDoesNotAllocate(t *testing.T) {
	notifier := newReplicaChangeNotifier()

	allocs := testing.AllocsPerRun(1000, func() {
		notifier.notify()
	})

	require.Zero(t, allocs)
}

func TestReplicaChangeNotifierWakesWaitersAndClearsWaiterCount(t *testing.T) {
	notifier := newReplicaChangeNotifier()
	version := notifier.snapshot()
	done := make(chan bool, 1)
	go func() {
		done <- notifier.wait(context.Background(), version)
	}()
	require.Eventually(t, func() bool {
		notifier.mu.Lock()
		defer notifier.mu.Unlock()
		return notifier.waiters == 1
	}, time.Second, time.Millisecond)

	notifier.notify()

	require.True(t, <-done)
	require.Eventually(t, func() bool {
		notifier.mu.Lock()
		defer notifier.mu.Unlock()
		return notifier.waiters == 0
	}, time.Second, time.Millisecond)
}
