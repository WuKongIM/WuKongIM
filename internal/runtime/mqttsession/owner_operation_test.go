package mqttsession

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOwnerOperationRetainsIdentityAndRechecksLiveLease(t *testing.T) {
	r, now := ownerFixture(t, 1, 1)
	claim := ownerClaim("client")
	o, err := r.Reserve(claim, func(context.Context) error { return nil })
	require.NoError(t, err)
	claim.UID = "mallory"
	require.NoError(t, r.Activate(o, 1, now.Add(time.Second)))
	op, err := r.Begin(context.Background(), o)
	require.NoError(t, err)
	require.Equal(t, "alice", op.UID())
	require.NoError(t, op.Check())
	*now = now.Add(time.Second)
	require.ErrorIs(t, op.Check(), ErrOwnerFenced)
	require.Error(t, op.Context().Err())
	require.Equal(t, 1, r.Snapshot().Operations)
	op.Done()
	require.Error(t, op.Check())
	require.Equal(t, "alice", op.UID())
	require.NoError(t, r.Quiesce(context.Background(), o))
	var absent *Operation
	require.Empty(t, absent.UID())
	require.Error(t, absent.Check())
}

func TestOwnerUncertainEffectCannotProduceQuiescenceProof(t *testing.T) {
	r, now := ownerFixture(t, 1, 1)
	physical := make(chan struct{})
	o, err := r.Reserve(ownerClaim("client"), func(context.Context) error { close(physical); return nil })
	require.NoError(t, err)
	require.NoError(t, r.Activate(o, 1, now.Add(time.Second)))
	op, err := r.Begin(context.Background(), o)
	require.NoError(t, err)
	result := make(chan error, 2)
	go func() { result <- r.Quiesce(context.Background(), o) }()
	<-physical
	require.NoError(t, op.MarkUncertain())
	require.NoError(t, op.MarkUncertain())
	go func() { result <- r.Quiesce(context.Background(), o) }()
	op.Done()
	require.ErrorIs(t, <-result, ErrOwnerUnknown)
	require.ErrorIs(t, <-result, ErrOwnerUnknown)
	require.Equal(t, 1, r.Snapshot().Uncertain)
	require.Equal(t, 1, r.Snapshot().Held)
	require.Zero(t, r.Snapshot().Operations)
	require.Error(t, op.MarkUncertain())
	_, err = r.Reserve(ownerClaim("other"), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrOwnerLimit)
	*now = now.Add(3 * time.Second)
	_, err = r.Sweep(context.Background(), 1)
	require.ErrorIs(t, err, ErrOwnerUnknown)
	require.ErrorIs(t, r.Close(context.Background()), ErrOwnerUnknown)
	require.Equal(t, 1, r.Snapshot().Held)
}
