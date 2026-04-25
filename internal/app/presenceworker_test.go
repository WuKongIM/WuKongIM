package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPresenceWorkerTriggersImmediateHeartbeatOnActiveSlotLeaderChange(t *testing.T) {
	var (
		mu     sync.Mutex
		leader uint64 = 1
	)

	heartbeater := &recordingPresenceHeartbeater{}
	worker := newPresenceWorker(heartbeater, time.Hour)
	worker.activeSlotIDs = func() []uint64 {
		return []uint64{1}
	}
	worker.leaderOf = func(slotID uint64) (uint64, error) {
		require.Equal(t, uint64(1), slotID)
		mu.Lock()
		defer mu.Unlock()
		return leader, nil
	}
	worker.leaderPollInterval = 10 * time.Millisecond

	require.NoError(t, worker.Start())
	t.Cleanup(func() {
		require.NoError(t, worker.Stop())
	})

	require.Never(t, func() bool {
		return heartbeater.Count() > 0
	}, 80*time.Millisecond, 10*time.Millisecond)

	mu.Lock()
	leader = 2
	mu.Unlock()

	require.Eventually(t, func() bool {
		return heartbeater.Count() > 0
	}, time.Second, 10*time.Millisecond)
}

type recordingPresenceHeartbeater struct {
	mu    sync.Mutex
	calls int
}

func (r *recordingPresenceHeartbeater) HeartbeatOnce(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return nil
}

func (r *recordingPresenceHeartbeater) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}
