package mqttsession

import (
	"context"
	"errors"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/stretchr/testify/require"
)

func TestRetirementRequiresTerminalProvedDrain(t *testing.T) {
	ctx := context.Background()
	r, now := ownerFixture(t, 2, 2)
	_, err := r.Retirement()
	require.ErrorIs(t, err, ErrOwnerUnknown)
	failClose := true
	o, err := r.Reserve(ownerClaim("a"), func(context.Context) error {
		if failClose {
			return ErrOwnerClose
		}
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, r.Activate(o, 1, now.Add(time.Second)))
	op, err := r.Begin(ctx, o)
	require.NoError(t, err)
	r.StopAdmission()
	_, err = r.Retirement()
	require.ErrorIs(t, err, ErrOwnerUnknown)
	require.ErrorIs(t, r.Close(ctx), ErrOwnerClose)
	_, err = r.Retirement()
	require.ErrorIs(t, err, ErrOwnerUnknown)
	failClose = false
	// A cancelled wait cannot manufacture successful operation completion.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, r.Close(cancelled), context.Canceled)
	_, err = r.Retirement()
	require.ErrorIs(t, err, ErrOwnerUnknown)
	op.Done()
	require.NoError(t, r.Close(ctx))
	proof, err := r.Retirement()
	require.NoError(t, err)
	node, boot, maxID := proof.Identity()
	require.Equal(t, o.NodeID, node)
	require.Equal(t, o.BootID, boot)
	require.Equal(t, o.ConnectionID, maxID)
	_, err = r.Reserve(ownerClaim("b"), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrOwnerStopped)
}

func TestRetirementRejectsUncertainEffects(t *testing.T) {
	r, now := ownerFixture(t, 1, 1)
	o, err := r.Reserve(ownerClaim("a"), func(context.Context) error { return nil })
	require.NoError(t, err)
	require.NoError(t, r.Activate(o, 1, now.Add(time.Second)))
	op, err := r.Begin(context.Background(), o)
	require.NoError(t, err)
	op.MarkUncertain()
	op.Done()
	require.ErrorIs(t, r.Close(context.Background()), ErrOwnerUnknown)
	_, err = r.Retirement()
	require.ErrorIs(t, err, ErrOwnerUnknown)
}

type retirementReaderFunc func(context.Context, contract.Owner) error

func (f retirementReaderFunc) Quiesce(ctx context.Context, o contract.Owner) error { return f(ctx, o) }

func TestRetirementIsolationNeverFallsBackForLiveBoot(t *testing.T) {
	r, _ := ownerFixture(t, 1, 1)
	calls := 0
	proofErr := errors.New("missing receipt")
	i := Isolation{Owners: r, Retired: retirementReaderFunc(func(context.Context, contract.Owner) error { calls++; return proofErr })}
	o, err := r.Reserve(ownerClaim("a"), func(context.Context) error { return ErrOwnerClose })
	require.NoError(t, err)
	ctx := context.Background()
	require.ErrorIs(t, i.Quiesce(ctx, o), ErrOwnerClose)
	o.ConnectionID++
	require.ErrorIs(t, i.Quiesce(ctx, o), ErrOwnerUnknown)
	o.BootID = "old"
	o.NodeID++
	require.ErrorIs(t, i.Quiesce(ctx, o), ErrOwnerUnknown)
	require.Zero(t, calls)
	o.NodeID--
	require.ErrorIs(t, i.Quiesce(ctx, o), proofErr)
	require.Equal(t, 1, calls)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, i.Quiesce(cancelled, o), context.Canceled)
	require.Equal(t, 1, calls)
	i.Retired = nil
	require.ErrorIs(t, i.Quiesce(ctx, o), ErrOwnerUnknown)
}
