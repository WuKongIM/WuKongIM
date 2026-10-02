//go:build integration

package mqttsession

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/stretchr/testify/require"
)

func TestOwnerSweeperJoinedStopAndFreshRestart(t *testing.T) {
	owners, now := ownerFixture(t, 2, 1)
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	_, err := owners.Reserve(ownerClaim("slow"), func(ctx context.Context) error {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release // Deliberately violate prompt cancellation to test joined Stop.
		return ctx.Err()
	})
	require.NoError(t, err)
	*now = now.Add(2 * time.Second)
	r := gr.New()
	w, err := NewOwnerSweeper(OwnerSweeperOptions{Owners: owners, Registry: r, Interval: time.Minute, TurnTimeout: 5 * time.Second})
	require.NoError(t, err)
	start, cancelStart := context.WithCancel(context.Background())
	require.NoError(t, w.Start(start))
	require.NoError(t, w.Start(start))
	<-entered
	cancelStart()
	select {
	case <-cancelled:
		t.Fatal("startup context owns worker lifetime")
	default:
	}
	stop, cancelStop := context.WithCancel(context.Background())
	cancelStop()
	require.ErrorIs(t, w.Stop(stop), context.Canceled)
	<-cancelled
	require.ErrorIs(t, w.Start(context.Background()), ErrOwnerSweeperStopping)
	require.Equal(t, 1, owners.Snapshot().Held)
	close(release)
	joined, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	require.NoError(t, w.Stop(joined))
	require.NoError(t, r.Group(gr.ModuleMQTT).Wait(joined))
	require.Equal(t, int32(1), calls.Load())
	observed := make(chan OwnerSweepObservation, 1)
	w.opts.Observe = func(o OwnerSweepObservation) {
		select {
		case observed <- o:
		default:
		}
	}
	require.NoError(t, w.Start(context.Background()))
	o := <-observed
	require.Equal(t, 1, o.Owners.Closing, "Stop must not reset retained cleanup to zero")
	require.Zero(t, o.Visited, "retry deadline must remain intact across worker restart")
	require.NoError(t, w.Stop(joined))
	require.NoError(t, r.Group(gr.ModuleMQTT).Wait(joined))
	require.Equal(t, int64(2), r.Snapshot().TotalStarted)
	require.Zero(t, r.Snapshot().ManagedTotal)
	t.Log("mqtt_owner_sweep: joined_slow_callback=true retained_after_timeout=true overlapping_runs=0 fresh_runs=2")
}

func TestOwnerSweeperTimesOutDrainWithoutDiscardingOperations(t *testing.T) {
	owners, now := ownerFixture(t, 2, 1)
	owner, err := owners.Reserve(ownerClaim("draining"), func(context.Context) error { return nil })
	require.NoError(t, err)
	require.NoError(t, owners.Activate(owner, 1, now.Add(time.Second)))
	op, err := owners.Begin(context.Background(), owner)
	require.NoError(t, err)
	*now = now.Add(2 * time.Second)
	w, err := NewOwnerSweeper(OwnerSweeperOptions{Owners: owners, TurnTimeout: 5 * time.Millisecond})
	require.NoError(t, err)
	o := w.sweep(context.Background())
	require.Equal(t, 1, o.Visited)
	require.Equal(t, 1, o.Failures)
	require.Equal(t, 1, o.Owners.Operations)
	require.Equal(t, 1, o.Owners.Held)
	require.ErrorIs(t, op.Context().Err(), context.Canceled)
	op.Done()
	require.NoError(t, owners.Quiesce(context.Background(), owner))
	require.Zero(t, owners.Snapshot().Held)
}
