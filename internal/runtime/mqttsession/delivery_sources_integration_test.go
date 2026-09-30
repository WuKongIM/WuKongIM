//go:build integration

package mqttsession_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/stretchr/testify/require"
)

type notifiedDeliveryTask struct {
	deliveryTask
	hints atomic.Uint64
}

func (t *notifiedDeliveryTask) NotifyDelivery() { t.hints.Add(1) }

func TestDeliveriesSourceWakeReplacesInterestsAndInvalidatesTask(t *testing.T) {
	r := deliveryOwners(t, 2)
	s, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: r, Workers: 1, IdleInterval: time.Minute, MaxSourcesPerTask: 2})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { stopDeliveries(t, s) })
	events := make(chan string, 8)
	step := 0
	task := &notifiedDeliveryTask{deliveryTask: func(context.Context) (runtime.DeliveryWork, error) {
		step++
		switch step {
		case 1:
			events <- "initial"
			return runtime.DeliveryWork{Sources: []string{"2:a"}}, nil
		case 2:
			events <- "changed"
			return runtime.DeliveryWork{Sources: []string{"2:b"}}, nil
		default:
			events <- "done"
			return runtime.DeliveryWork{Done: true}, nil
		}
	}}
	o := deliveryOwner(t, r, "indexed")
	require.NoError(t, s.Register(o, task))
	require.Equal(t, "initial", deliveryEvent(t, events))
	require.Eventually(t, func() bool { return s.Snapshot().InProgress == 0 }, time.Second, time.Millisecond)
	require.NoError(t, s.WakeSource("2:unrelated"))
	require.Zero(t, task.hints.Load())
	require.NoError(t, s.WakeSource("2:a"))
	require.Equal(t, "changed", deliveryEvent(t, events))
	require.EqualValues(t, 1, task.hints.Load())
	require.Eventually(t, func() bool { return s.Snapshot().InProgress == 0 }, time.Second, time.Millisecond)
	require.NoError(t, s.WakeSource("2:a"))
	require.EqualValues(t, 1, task.hints.Load(), "old interests must be removed")
	require.NoError(t, s.Wake(o))
	require.Equal(t, "done", deliveryEvent(t, events))
	require.Eventually(t, func() bool { return s.Snapshot().Completed == 1 }, time.Second, time.Millisecond)
	require.NoError(t, s.WakeSource("2:b"))
	require.EqualValues(t, 2, task.hints.Load(), "completion must remove source interests")
	stopDeliveries(t, s)
	require.ErrorIs(t, s.WakeSource("2:b"), runtime.ErrDeliveriesStopped)
}

func TestDeliveriesSourceWakeDuringExecutionSurvivesIdle(t *testing.T) {
	r := deliveryOwners(t, 1)
	s, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: r, Workers: 1, IdleInterval: time.Minute})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { stopDeliveries(t, s) })
	events := make(chan int, 4)
	release := make(chan struct{})
	step := 0
	task := &notifiedDeliveryTask{deliveryTask: func(ctx context.Context) (runtime.DeliveryWork, error) {
		step++
		events <- step
		if step == 2 {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return runtime.DeliveryWork{Sources: []string{"2:a"}, Done: step == 3}, nil
	}}
	require.NoError(t, s.Register(deliveryOwner(t, r, "running"), task))
	require.Equal(t, 1, deliveryEvent(t, events))
	require.Eventually(t, func() bool { return s.Snapshot().InProgress == 0 }, time.Second, time.Millisecond)
	require.NoError(t, s.WakeSource("2:a"))
	require.Equal(t, 2, deliveryEvent(t, events))
	for range 100 {
		require.NoError(t, s.WakeSource("2:a"))
	}
	close(release)
	require.Equal(t, 3, deliveryEvent(t, events))
	require.Eventually(t, func() bool { return s.Snapshot().Completed == 1 }, time.Second, time.Millisecond)
	require.EqualValues(t, 3, s.Snapshot().Turns)
}

func TestDeliveriesRejectsUnboundedSourceInterests(t *testing.T) {
	r := deliveryOwners(t, 1)
	s, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: r, Workers: 1, MaxSourcesPerTask: 1, Retry: time.Minute})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { stopDeliveries(t, s) })
	task := &notifiedDeliveryTask{deliveryTask: func(context.Context) (runtime.DeliveryWork, error) {
		return runtime.DeliveryWork{Sources: []string{"2:a", "2:b"}}, nil
	}}
	require.NoError(t, s.Register(deliveryOwner(t, r, "oversized"), task))
	require.Eventually(t, func() bool { return s.Snapshot().Failures == 1 }, time.Second, time.Millisecond)
	before := task.hints.Load()
	require.NoError(t, s.WakeSource("2:a"))
	require.Equal(t, before, task.hints.Load())
	for _, n := range []int{-1, 1025} {
		_, err = runtime.NewDeliveries(runtime.DeliveryOptions{Owners: r, MaxSourcesPerTask: n})
		require.Error(t, err)
	}
}
