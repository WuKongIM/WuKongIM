package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeliveryRuntimeLifecycleProcessesTicksAndStops(t *testing.T) {
	clock := &manualDeliveryLifecycleClock{}
	runtime := &recordingDeliveryRuntimeMaintenance{}
	lifecycle := newDeliveryRuntimeLifecycle(deliveryRuntimeLifecycleConfig{
		Runtime:       runtime,
		TickInterval:  time.Millisecond,
		SweepInterval: time.Millisecond,
		After:         clock.After,
	})

	require.NoError(t, lifecycle.Start(context.Background()))
	clock.WaitForWaiters(t, 2)
	clock.Fire()
	require.Eventually(t, func() bool { return runtime.retryCallCount() > 0 }, time.Second, time.Millisecond)
	require.NoError(t, lifecycle.Stop())
}

type manualDeliveryLifecycleClock struct {
	mu      sync.Mutex
	waiters []chan time.Time
}

func (c *manualDeliveryLifecycleClock) After(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.waiters = append(c.waiters, ch)
	c.mu.Unlock()
	return ch
}

func (c *manualDeliveryLifecycleClock) WaitForWaiters(t *testing.T, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.waiters) >= n
	}, time.Second, time.Millisecond)
}

func (c *manualDeliveryLifecycleClock) Fire() {
	c.mu.Lock()
	waiters := append([]chan time.Time(nil), c.waiters...)
	c.waiters = nil
	c.mu.Unlock()
	for _, ch := range waiters {
		ch <- time.Now()
	}
}

type recordingDeliveryRuntimeMaintenance struct {
	mu         sync.Mutex
	retryCalls int
	sweepCalls int
}

func (r *recordingDeliveryRuntimeMaintenance) ProcessRetryTicks(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retryCalls++
	return nil
}

func (r *recordingDeliveryRuntimeMaintenance) SweepIdle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepCalls++
}

func (r *recordingDeliveryRuntimeMaintenance) InflightRouteCount() int {
	return 0
}

func (r *recordingDeliveryRuntimeMaintenance) AckBindingCount() int {
	return 0
}

func (r *recordingDeliveryRuntimeMaintenance) retryCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.retryCalls
}
