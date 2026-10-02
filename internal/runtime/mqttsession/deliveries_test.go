package mqttsession_test

import (
	"context"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/stretchr/testify/require"
)

type deliveryTask func(context.Context) (runtime.DeliveryWork, error)

func (f deliveryTask) Turn(ctx context.Context) (runtime.DeliveryWork, error) { return f(ctx) }

func TestDeliveriesConfigurationAndStoppedAdmission(t *testing.T) {
	r, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: 1, BootID: "config", Capacity: 1, MaxOperations: 1, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Second})
	require.NoError(t, err)
	for _, change := range []func(*runtime.DeliveryOptions){
		func(o *runtime.DeliveryOptions) { o.Owners = nil },
		func(o *runtime.DeliveryOptions) { o.Capacity = -1 },
		func(o *runtime.DeliveryOptions) { o.Capacity = 1_000_001 },
		func(o *runtime.DeliveryOptions) { o.Workers = -1 },
		func(o *runtime.DeliveryOptions) { o.Workers = 129 },
		func(o *runtime.DeliveryOptions) { o.TurnTimeout = -1 },
		func(o *runtime.DeliveryOptions) { o.TurnTimeout = time.Minute + 1 },
		func(o *runtime.DeliveryOptions) { o.IdleInterval = time.Millisecond - 1 },
		func(o *runtime.DeliveryOptions) { o.IdleInterval = time.Minute + 1 },
		func(o *runtime.DeliveryOptions) { o.Retry = time.Millisecond - 1 },
		func(o *runtime.DeliveryOptions) { o.Retry = time.Minute + 1 },
	} {
		o := runtime.DeliveryOptions{Owners: r}
		change(&o)
		_, err := runtime.NewDeliveries(o)
		require.ErrorIs(t, err, runtime.ErrDeliveriesInvalid)
	}
	s, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: r})
	require.NoError(t, err)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, s.Start(canceled), context.Canceled)
	require.ErrorIs(t, s.Start(nil), runtime.ErrDeliveriesInvalid)
	require.ErrorIs(t, s.Stop(nil), runtime.ErrDeliveriesInvalid)
	require.ErrorIs(t, s.Wake(contract.Owner{}), runtime.ErrDeliveriesInvalid)
	require.NoError(t, s.Stop(context.Background()))
	require.NoError(t, s.Stop(context.Background()))
	require.ErrorIs(t, s.Start(context.Background()), runtime.ErrDeliveriesStopped)
	require.Zero(t, s.Snapshot().Tracked)
}
