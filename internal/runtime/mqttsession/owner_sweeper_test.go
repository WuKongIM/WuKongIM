package mqttsession

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOwnerSweeperValidatesBounds(t *testing.T) {
	owners, _ := ownerFixture(t, 1, 1)
	for _, change := range []func(*OwnerSweeperOptions){
		func(o *OwnerSweeperOptions) { o.Owners = nil },
		func(o *OwnerSweeperOptions) { o.Limit = -1 },
		func(o *OwnerSweeperOptions) { o.Limit = 257 },
		func(o *OwnerSweeperOptions) { o.Interval = -1 },
		func(o *OwnerSweeperOptions) { o.Interval = time.Minute + 1 },
		func(o *OwnerSweeperOptions) { o.TurnTimeout = -1 },
		func(o *OwnerSweeperOptions) { o.TurnTimeout = 5*time.Second + 1 },
	} {
		o := OwnerSweeperOptions{Owners: owners}
		change(&o)
		_, err := NewOwnerSweeper(o)
		require.ErrorIs(t, err, ErrOwnerSweeperInvalid)
	}
	w, err := NewOwnerSweeper(OwnerSweeperOptions{Owners: owners})
	require.NoError(t, err)
	require.Equal(t, 256, w.opts.Limit)
	require.Equal(t, 250*time.Millisecond, w.opts.Interval)
	require.Equal(t, 250*time.Millisecond, w.opts.TurnTimeout)
	require.ErrorIs(t, w.Start(nil), ErrOwnerSweeperInvalid)
	require.ErrorIs(t, w.Stop(nil), ErrOwnerSweeperInvalid)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, w.Start(ctx), context.Canceled)
	require.NoError(t, w.Stop(context.Background()))
	var absent *OwnerSweeper
	require.ErrorIs(t, absent.Start(context.Background()), ErrOwnerSweeperInvalid)
	require.ErrorIs(t, absent.Stop(context.Background()), ErrOwnerSweeperInvalid)
}

func TestOwnerSweeperBoundsDueWorkAndPreservesRenewal(t *testing.T) {
	owners, now := ownerFixture(t, 4, 1)
	closed := 0
	closeFn := func(context.Context) error { closed++; return nil }
	live, err := owners.Reserve(ownerClaim("renewed"), closeFn)
	require.NoError(t, err)
	require.NoError(t, owners.Activate(live, 1, now.Add(time.Second)))
	require.NoError(t, owners.Renew(live, 2, now.Add(5*time.Second)))
	for _, id := range []string{"pending-a", "pending-b", "expired-active"} {
		o, err := owners.Reserve(ownerClaim(id), closeFn)
		require.NoError(t, err)
		if id == "expired-active" {
			require.NoError(t, owners.Activate(o, 1, now.Add(time.Second)))
		}
	}
	*now = now.Add(2 * time.Second)
	w, err := NewOwnerSweeper(OwnerSweeperOptions{Owners: owners, Limit: 2})
	require.NoError(t, err)
	o := w.sweep(context.Background())
	require.Equal(t, 2, o.Visited)
	require.Zero(t, o.Failures)
	require.Equal(t, 2, o.Owners.Held)
	o = w.sweep(context.Background())
	require.Equal(t, 1, o.Visited)
	require.Equal(t, 1, o.Owners.Held)
	require.Equal(t, 3, closed)
	op, err := owners.Begin(context.Background(), live)
	require.NoError(t, err)
	op.Done()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Zero(t, w.sweep(ctx).Visited)
	require.Equal(t, 3, closed)
	require.NoError(t, owners.Close(context.Background()))
}

func TestOwnerSweeperYieldsFailedCloseAndRetainsUncertainBarrier(t *testing.T) {
	for _, mode := range []string{"error", "panic", "uncertain"} {
		t.Run(mode, func(t *testing.T) {
			owners, now := ownerFixture(t, 2, 1)
			attempts := 0
			bad, err := owners.Reserve(ownerClaim("bad"), func(context.Context) error {
				attempts++
				if attempts == 1 {
					if mode == "error" {
						return errors.New("private diagnostic")
					}
					if mode == "panic" {
						panic("private diagnostic")
					}
				}
				return nil
			})
			require.NoError(t, err)
			if mode == "uncertain" {
				require.NoError(t, owners.Activate(bad, 1, now.Add(time.Second)))
				op, err := owners.Begin(context.Background(), bad)
				require.NoError(t, err)
				require.NoError(t, op.MarkUncertain())
				op.Done()
			}
			_, err = owners.Reserve(ownerClaim("good"), func(context.Context) error { return nil })
			require.NoError(t, err)
			*now = now.Add(2 * time.Second)
			w, err := NewOwnerSweeper(OwnerSweeperOptions{Owners: owners})
			require.NoError(t, err)
			o := w.sweep(context.Background())
			require.Equal(t, 1, o.Visited)
			require.Equal(t, 1, o.Failures)
			require.Equal(t, 2, o.Owners.Held)
			o = w.sweep(context.Background())
			require.Equal(t, 1, o.Visited, "failed owner must yield to another due owner")
			require.Zero(t, o.Failures)
			require.Equal(t, 1, o.Owners.Held)
			*now = now.Add(2 * time.Second)
			o = w.sweep(context.Background())
			if mode == "uncertain" {
				require.Equal(t, 1, o.Failures)
				require.Equal(t, 1, o.Owners.Uncertain)
				require.Equal(t, 1, o.Owners.Held)
				require.Equal(t, 1, attempts, "physical closure must not repeat after success")
				require.ErrorIs(t, owners.Quiesce(context.Background(), bad), ErrOwnerUnknown)
			} else {
				require.Zero(t, o.Failures)
				require.Zero(t, o.Owners.Held)
				require.Equal(t, 2, attempts)
			}
		})
	}
}
