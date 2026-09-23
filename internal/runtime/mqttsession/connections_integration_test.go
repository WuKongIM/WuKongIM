//go:build integration

package mqttsession

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/stretchr/testify/require"
)

type connectionControl struct {
	renew      func(context.Context, contract.Owner) error
	disconnect func(context.Context, DisconnectIntent) error
}

func (c connectionControl) Renew(ctx context.Context, o contract.Owner) error {
	if c.renew != nil {
		return c.renew(ctx, o)
	}
	return errors.New("unavailable")
}
func (c connectionControl) Disconnect(ctx context.Context, i DisconnectIntent) error {
	if c.disconnect != nil {
		return c.disconnect(ctx, i)
	}
	return nil
}
func supervisorOwners(t *testing.T, capacity int) *Owners {
	t.Helper()
	r, err := NewOwners(OwnerOptions{NodeID: 1, BootID: "supervisor", Capacity: capacity, MaxOperations: 4, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Millisecond})
	require.NoError(t, err)
	return r
}
func supervisedOwner(t *testing.T, r *Owners, client string, duration time.Duration) contract.Owner {
	t.Helper()
	o, err := r.Reserve(ownerClaim(client), func(context.Context) error { return nil })
	require.NoError(t, err)
	require.NoError(t, r.Activate(o, 1, time.Now().Add(duration)))
	return o
}
func stopSupervisor(t *testing.T, s *Connections) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, s.Stop(ctx))
}
func TestConnectionsRenewInstalledLeaseAndRetireClosedOwner(t *testing.T) {
	r := supervisorOwners(t, 4)
	o := supervisedOwner(t, r, "a", 200*time.Millisecond)
	var renewals atomic.Uint64
	closed := make(chan DisconnectIntent, 4)
	c := connectionControl{renew: func(ctx context.Context, o contract.Owner) error {
		return r.Renew(o, renewals.Add(1)+1, time.Now().Add(200*time.Millisecond))
	}, disconnect: func(ctx context.Context, i DisconnectIntent) error { closed <- i; return nil }}
	s, err := NewConnections(ConnectionOptions{Owners: r, Control: c, Workers: 2, Retry: 10 * time.Millisecond})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { stopSupervisor(t, s) })
	require.NoError(t, s.Register(o))
	require.NoError(t, s.Register(o))
	require.Eventually(t, func() bool { return renewals.Load() >= 3 }, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, 1, s.Snapshot().Tracked)
	op, err := r.Begin(context.Background(), o)
	require.NoError(t, err)
	at := time.Now()
	expiry := uint32(20)
	require.NoError(t, s.Disconnect(DisconnectIntent{Owner: o, Normal: true, SessionExpirySec: &expiry, ObservedAt: at}))
	expiry = 99
	require.NoError(t, s.Disconnect(DisconnectIntent{Owner: o, ObservedAt: time.Now()}))
	require.Error(t, op.Context().Err())
	require.Equal(t, 1, r.Snapshot().Operations, "acceptance must not wait for its caller")
	op.Done()
	var intent DisconnectIntent
	select {
	case intent = <-closed:
	case <-time.After(time.Second):
		t.Fatal("disconnect missing")
	}
	require.True(t, intent.Normal)
	require.Equal(t, at, intent.ObservedAt)
	require.Equal(t, uint32(20), *intent.SessionExpirySec)
	require.Eventually(t, func() bool { return s.Snapshot().Tracked == 0 }, time.Second, time.Millisecond)
	require.Zero(t, r.Snapshot().Held)
}
func TestConnectionsBoundConcurrencyCapacityAndJoinedStop(t *testing.T) {
	r := supervisorOwners(t, 4)
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var active, maximum atomic.Int32
	c := connectionControl{disconnect: func(ctx context.Context, i DisconnectIntent) error {
		n := active.Add(1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		return nil
	}}
	s, err := NewConnections(ConnectionOptions{Owners: r, Control: c, Capacity: 3, Workers: 2, CallTimeout: time.Second})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, s.Register(supervisedOwner(t, r, id, time.Second)))
	}
	fourth := supervisedOwner(t, r, "d", time.Second)
	require.ErrorIs(t, s.Register(fourth), ErrConnectionsLimit)
	require.NoError(t, r.Quiesce(context.Background(), fourth))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, s.Stop(ctx), context.DeadlineExceeded)
	<-entered
	<-entered
	require.LessOrEqual(t, maximum.Load(), int32(2))
	require.Greater(t, s.Snapshot().Tracked, 0)
	require.ErrorIs(t, s.Start(context.Background()), ErrConnectionsStopped)
	_, err = r.Begin(context.Background(), fourth)
	require.ErrorIs(t, err, ErrOwnerStopped)
	close(release)
	stopSupervisor(t, s)
	require.Zero(t, active.Load())
	require.Zero(t, s.Snapshot().Tracked)
}
func TestConnectionsRetryExactIntentAndRejectFakeRenewal(t *testing.T) {
	r := supervisorOwners(t, 2)
	o := supervisedOwner(t, r, "a", 150*time.Millisecond)
	attempts := make(chan DisconnectIntent, 8)
	var calls atomic.Int32
	c := connectionControl{renew: func(context.Context, contract.Owner) error { return nil }, disconnect: func(ctx context.Context, i DisconnectIntent) error {
		attempts <- i
		if calls.Add(1) == 1 {
			panic("secret callback data")
		}
		return nil
	}}
	s, err := NewConnections(ConnectionOptions{Owners: r, Control: c, Workers: 1, Retry: 10 * time.Millisecond})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { stopSupervisor(t, s) })
	require.NoError(t, s.Register(o))
	var first, second DisconnectIntent
	select {
	case first = <-attempts:
	case <-time.After(time.Second):
		t.Fatal("fake renewal accepted")
	}
	select {
	case second = <-attempts:
	case <-time.After(time.Second):
		t.Fatal("failed intent lost")
	}
	require.False(t, first.Normal)
	require.Equal(t, first, second)
	require.Eventually(t, func() bool { return s.Snapshot().Tracked == 0 }, time.Second, time.Millisecond)
	require.GreaterOrEqual(t, s.Snapshot().Failures, uint64(2))
}
func TestConnectionsDoNotForgetLateSuccessOrUncertainIsolation(t *testing.T) {
	r := supervisorOwners(t, 2)
	o := supervisedOwner(t, r, "a", time.Second)
	op, err := r.Begin(context.Background(), o)
	require.NoError(t, err)
	require.NoError(t, op.MarkUncertain())
	op.Done()
	// A fenced owner cannot newly register. The uncertainty case below is created
	// after registration so queued lifecycle success cannot erase its barrier.
	s, err := NewConnections(ConnectionOptions{Owners: r, Control: connectionControl{}})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	require.Error(t, s.Register(o))
	// Keep this registry isolated: its unresolved owner intentionally cannot stop.
	require.NoError(t, s.Stop(context.Background()))
	require.ErrorIs(t, r.Quiesce(context.Background(), o), ErrOwnerUnknown)

	r2 := supervisorOwners(t, 1)
	o2 := supervisedOwner(t, r2, "b", time.Second)
	var calls atomic.Int32
	c := connectionControl{disconnect: func(ctx context.Context, i DisconnectIntent) error {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return nil
		}
		return nil
	}}
	s2, err := NewConnections(ConnectionOptions{Owners: r2, Control: c, Workers: 1, CallTimeout: 10 * time.Millisecond, Retry: 20 * time.Millisecond})
	require.NoError(t, err)
	require.NoError(t, s2.Start(context.Background()))
	require.NoError(t, s2.Register(o2))
	require.NoError(t, s2.Disconnect(DisconnectIntent{Owner: o2, ObservedAt: time.Now()}))
	require.Eventually(t, func() bool { return calls.Load() >= 2 && s2.Snapshot().Tracked == 0 }, time.Second, time.Millisecond)
	require.GreaterOrEqual(t, s2.Snapshot().Failures, uint64(1))
	stopSupervisor(t, s2)
}

func TestConnectionsStopDoesNotHideLiveOwnersBehindFailedCleanup(t *testing.T) {
	r := supervisorOwners(t, 2)
	a := supervisedOwner(t, r, "blocked", 30*time.Second)
	b := supervisedOwner(t, r, "live", 30*time.Second)
	attempted := make(chan struct{}, 4)
	liveClosed := make(chan struct{}, 1)
	var recoverFirst atomic.Bool
	c := connectionControl{disconnect: func(ctx context.Context, i DisconnectIntent) error {
		if i.Owner == a && !recoverFirst.Load() {
			attempted <- struct{}{}
			return errors.New("unavailable")
		}
		if i.Owner == b {
			liveClosed <- struct{}{}
		}
		return nil
	}}
	s, err := NewConnections(ConnectionOptions{Owners: r, Control: c, Workers: 2, Retry: time.Second})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	require.NoError(t, s.Register(a))
	require.NoError(t, s.Register(b))
	t.Cleanup(func() { recoverFirst.Store(true); stopSupervisor(t, s) })
	require.NoError(t, s.Disconnect(DisconnectIntent{Owner: a, ObservedAt: time.Now()}))
	<-attempted
	require.Eventually(t, func() bool { return s.Snapshot().Failures > 0 }, time.Second, time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, s.Stop(ctx), context.DeadlineExceeded)
	select {
	case <-liveClosed:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("failed retry at heap head hid a live owner during Stop")
	}
}
