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

func ownerFixture(t *testing.T, capacity, operations int) (*Owners, *time.Time) {
	t.Helper()
	now := time.Now()
	r, err := NewOwners(OwnerOptions{NodeID: 3, BootID: "boot", Capacity: capacity, MaxOperations: operations, PendingTimeout: time.Second, MaxLease: 10 * time.Second, CloseRetry: time.Second, Now: func() time.Time { return now }})
	require.NoError(t, err)
	return r, &now
}
func ownerClaim(client string) Claim {
	return Claim{Key: contract.Key{Namespace: "main", ClientID: client}, UID: "alice", SessionGeneration: 1, OwnerGeneration: 1}
}

func TestOwnerAdmissionRequiresLiveExactCommittedActivation(t *testing.T) {
	r, now := ownerFixture(t, 3, 1)
	owner, err := r.Reserve(ownerClaim("client"), func(context.Context) error { return nil })
	require.NoError(t, err)
	_, err = r.Begin(context.Background(), owner)
	require.ErrorIs(t, err, ErrOwnerFenced)
	require.NoError(t, r.Activate(owner, 1, now.Add(5*time.Second)))
	require.NoError(t, r.Activate(owner, 1, now.Add(5*time.Second)))
	require.Error(t, r.Activate(owner, 1, now.Add(6*time.Second)))
	op, err := r.Begin(context.Background(), owner)
	require.NoError(t, err)
	_, err = r.Begin(context.Background(), owner)
	require.ErrorIs(t, err, ErrOwnerLimit)
	op.Done()
	op.Done()
	for _, change := range []func(*contract.Owner){func(o *contract.Owner) { o.Key.ClientID = "other" }, func(o *contract.Owner) { o.SessionGeneration++ }, func(o *contract.Owner) { o.OwnerGeneration++ }, func(o *contract.Owner) { o.NodeID++ }, func(o *contract.Owner) { o.BootID = "old" }} {
		bad := owner
		change(&bad)
		_, err = r.Begin(context.Background(), bad)
		require.Error(t, err)
		require.Error(t, r.Quiesce(context.Background(), bad))
	}
	require.Error(t, r.Renew(owner, 1, now.Add(7*time.Second)))
	require.NoError(t, r.Renew(owner, 2, now.Add(7*time.Second)))
	*now = now.Add(7 * time.Second)
	_, err = r.Begin(context.Background(), owner)
	require.ErrorIs(t, err, ErrOwnerFenced)
	require.ErrorIs(t, r.Renew(owner, 3, now.Add(time.Second)), ErrOwnerFenced)
	require.NoError(t, r.Quiesce(context.Background(), owner))
	require.NoError(t, r.Quiesce(context.Background(), owner), "retired issued identity stays closed without retaining an unbounded tombstone")
	future := owner
	future.ConnectionID++
	require.Error(t, r.Quiesce(context.Background(), future))
}

func TestOwnerQuiescenceWaitsForTransportAndAdmittedWork(t *testing.T) {
	r, now := ownerFixture(t, 2, 2)
	closing, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	owner, err := r.Reserve(ownerClaim("client"), func(context.Context) error { calls.Add(1); close(closing); <-release; return nil })
	require.NoError(t, err)
	require.NoError(t, r.Activate(owner, 1, now.Add(time.Second)))
	op, err := r.Begin(context.Background(), owner)
	require.NoError(t, err)
	finished := make(chan error, 2)
	go func() { finished <- r.Quiesce(context.Background(), owner) }()
	<-closing
	require.ErrorIs(t, op.Context().Err(), context.Canceled)
	_, err = r.Begin(context.Background(), owner)
	require.ErrorIs(t, err, ErrOwnerFenced)
	go func() { finished <- r.Quiesce(context.Background(), owner) }()
	close(release)
	select {
	case err := <-finished:
		t.Fatalf("quiesced before operation drain: %v", err)
	default:
	}
	op.Done()
	require.NoError(t, <-finished)
	require.NoError(t, <-finished)
	require.Equal(t, int32(1), calls.Load())
	require.Zero(t, r.Snapshot().Held)
	newOwner, err := r.Reserve(ownerClaim("client"), func(context.Context) error { return nil })
	require.NoError(t, err)
	require.Greater(t, newOwner.ConnectionID, owner.ConnectionID)
	require.NoError(t, r.Activate(newOwner, 2, now.Add(time.Second)))
	require.NoError(t, r.Quiesce(context.Background(), owner))
	live, err := r.Begin(context.Background(), newOwner)
	require.NoError(t, err)
	live.Done()
	require.NoError(t, r.Close(context.Background()))
}

func TestOwnerFailedCloseStaysFencedAndConsumesCapacity(t *testing.T) {
	for _, mode := range []string{"error", "panic", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			r, now := ownerFixture(t, 1, 1)
			entered := make(chan struct{})
			var calls atomic.Int32
			owner, err := r.Reserve(ownerClaim("client"), func(ctx context.Context) error {
				if calls.Add(1) > 1 {
					return nil
				}
				close(entered)
				switch mode {
				case "error":
					return errors.New("close failed")
				case "panic":
					panic("sensitive callback diagnostic")
				default:
					<-ctx.Done()
					return ctx.Err()
				}
			})
			require.NoError(t, err)
			require.NoError(t, r.Activate(owner, 1, now.Add(time.Second)))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan error, 1)
			go func() { finished <- r.Quiesce(ctx, owner) }()
			<-entered
			cancel()
			err = <-finished
			require.Error(t, err)
			require.NotContains(t, err.Error(), "sensitive")
			_, err = r.Begin(context.Background(), owner)
			require.ErrorIs(t, err, ErrOwnerFenced)
			_, err = r.Reserve(ownerClaim("other"), func(context.Context) error { return nil })
			require.ErrorIs(t, err, ErrOwnerLimit)
			require.Equal(t, 1, r.Snapshot().Closing)
			require.NoError(t, r.Quiesce(context.Background(), owner))
			require.Zero(t, r.Snapshot().Held)
		})
	}
}

func TestOwnerExpiryIsBoundedAndRenewalDoesNotGrowDeadlineQueue(t *testing.T) {
	r, now := ownerFixture(t, 4, 2)
	var closed atomic.Int32
	closeFn := func(context.Context) error { closed.Add(1); return nil }
	first, err := r.Reserve(ownerClaim("same"), closeFn)
	require.NoError(t, err)
	second, err := r.Reserve(ownerClaim("same"), closeFn)
	require.NoError(t, err)
	require.NoError(t, r.Activate(first, 1, now.Add(2*time.Second)))
	for revision := uint64(2); revision <= 100; revision++ {
		require.NoError(t, r.Renew(first, revision, now.Add(3*time.Second)))
	}
	require.Equal(t, 2, r.Snapshot().Deadlines)
	*now = now.Add(1500 * time.Millisecond)
	n, err := r.Sweep(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoError(t, r.Quiesce(context.Background(), second))
	require.Equal(t, int32(1), closed.Load())
	op, err := r.Begin(context.Background(), first)
	require.NoError(t, err)
	op.Done()
	*now = now.Add(2 * time.Second)
	n, err = r.Sweep(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Zero(t, r.Snapshot().Held)
	require.Zero(t, r.Snapshot().Deadlines)
	_, err = r.Sweep(context.Background(), 257)
	require.Error(t, err)
	_, err = r.Reserve(ownerClaim("x"), closeFn)
	require.NoError(t, err)
	require.NoError(t, r.Close(context.Background()))
	_, err = r.Reserve(ownerClaim("y"), closeFn)
	require.ErrorIs(t, err, ErrOwnerStopped)
}

func TestOwnerCancellationDoesNotFinishAnOperation(t *testing.T) {
	r, now := ownerFixture(t, 1, 1)
	owner, err := r.Reserve(ownerClaim("client"), func(context.Context) error { return nil })
	require.NoError(t, err)
	require.NoError(t, r.Activate(owner, 1, now.Add(time.Second)))
	ctx, cancel := context.WithCancel(context.Background())
	op, err := r.Begin(ctx, owner)
	require.NoError(t, err)
	cancel()
	<-op.Context().Done()
	require.Equal(t, 1, r.Snapshot().Operations)
	blocked, stop := context.WithCancel(context.Background())
	stop()
	require.Error(t, r.Close(blocked))
	_, err = r.Begin(context.Background(), owner)
	require.ErrorIs(t, err, ErrOwnerStopped)
	op.Done()
	require.NoError(t, r.Close(context.Background()))
	require.Zero(t, r.Snapshot().Held)
}
