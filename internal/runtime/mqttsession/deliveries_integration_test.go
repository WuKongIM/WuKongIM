//go:build integration

package mqttsession_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/stretchr/testify/require"
)

func deliveryOwners(t *testing.T, capacity int) *runtime.Owners {
	t.Helper()
	r, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: 1, BootID: "delivery-test", Capacity: capacity, MaxOperations: 4, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Close(context.Background())) })
	return r
}

func TestDeliveriesFailuresBackOffDespiteWakeFlood(t *testing.T) {
	for _, fault := range []string{"error", "panic", "late_done"} {
		t.Run(fault, func(t *testing.T) {
			r := deliveryOwners(t, 1)
			s, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: r, Workers: 1, Retry: 100 * time.Millisecond, TurnTimeout: 10 * time.Millisecond, IdleInterval: time.Minute})
			require.NoError(t, err)
			require.NoError(t, s.Start(context.Background()))
			t.Cleanup(func() { stopDeliveries(t, s) })
			o := deliveryOwner(t, r, "failing")
			events := make(chan time.Time, 2)
			attempt := 0
			require.NoError(t, s.Register(o, deliveryTask(func(ctx context.Context) (runtime.DeliveryWork, error) {
				attempt++
				if attempt == 1 {
					events <- time.Now()
					switch fault {
					case "panic":
						panic("private callback detail")
					case "late_done":
						<-ctx.Done()
						return runtime.DeliveryWork{Done: true}, nil
					default:
						return runtime.DeliveryWork{Again: true}, errors.New("private callback detail")
					}
				}
				events <- time.Now()
				return runtime.DeliveryWork{Done: true}, nil
			})))
			first := deliveryEvent(t, events)
			require.Eventually(t, func() bool { return s.Snapshot().Failures == 1 }, time.Second, time.Millisecond)
			for i := 0; i < 1000; i++ {
				if err := s.Wake(o); errors.Is(err, runtime.ErrOwnerUnknown) {
					break
				} else {
					require.NoError(t, err)
				}
			}
			second := deliveryEvent(t, events)
			require.GreaterOrEqual(t, second.Sub(first), 100*time.Millisecond)
			require.Eventually(t, func() bool { return s.Snapshot().Completed == 1 }, time.Second, time.Millisecond)
			require.EqualValues(t, 2, s.Snapshot().Turns)
		})
	}
}

func deliveryOwner(t *testing.T, r *runtime.Owners, id string) contract.Owner {
	t.Helper()
	o, err := r.Reserve(runtime.Claim{Key: contract.Key{Namespace: "main", ClientID: id}, UID: "alice", SessionGeneration: 1, OwnerGeneration: 1}, func(context.Context) error { return nil })
	require.NoError(t, err)
	require.NoError(t, r.Activate(o, 1, time.Now().Add(50*time.Second)))
	return o
}

func stopDeliveries(t *testing.T, s *runtime.Deliveries) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, s.Stop(ctx))
}

func deliveryEvent[T any](t *testing.T, events <-chan T) T {
	t.Helper()
	select {
	case v := <-events:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("delivery task event missing")
		var zero T
		return zero
	}
}

func TestDeliveriesHotOwnerYieldsToOtherDueOwners(t *testing.T) {
	r := deliveryOwners(t, 3)
	s, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: r, Workers: 1})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { stopDeliveries(t, s) })
	first := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	events := make(chan string, 8)
	calls := 0
	hot := deliveryOwner(t, r, "hot")
	require.NoError(t, s.Register(hot, deliveryTask(func(ctx context.Context) (runtime.DeliveryWork, error) {
		calls++
		events <- "hot"
		if calls == 1 {
			close(first)
			select {
			case <-release:
			case <-ctx.Done():
				return runtime.DeliveryWork{}, ctx.Err()
			}
		}
		return runtime.DeliveryWork{Again: calls < 2, Done: calls == 2}, nil
	})))
	deliveryEvent(t, first)
	require.Equal(t, "hot", deliveryEvent(t, events))
	for _, id := range []string{"second", "third"} {
		require.NoError(t, s.Register(deliveryOwner(t, r, id), deliveryTask(func(context.Context) (runtime.DeliveryWork, error) {
			events <- id
			return runtime.DeliveryWork{Done: true}, nil
		})))
	}
	// A large wake burst is one pending hint, not queued work for the hot owner.
	for i := 0; i < 1000; i++ {
		require.NoError(t, s.Wake(hot))
	}
	release <- struct{}{}
	require.Equal(t, "second", deliveryEvent(t, events))
	require.Equal(t, "third", deliveryEvent(t, events))
	require.Equal(t, "hot", deliveryEvent(t, events))
	require.Eventually(t, func() bool { return s.Snapshot().Tracked == 0 }, time.Second, time.Millisecond)
	require.EqualValues(t, 3, s.Snapshot().Completed)
	require.EqualValues(t, 4, s.Snapshot().Turns)
}

func TestDeliveriesBoundWorkersAndKeepSlowStopJoined(t *testing.T) {
	r := deliveryOwners(t, 4)
	s, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: r, Workers: 2, Capacity: 3})
	require.NoError(t, err)
	startup, cancelStartup := context.WithCancel(context.Background())
	require.NoError(t, s.Start(startup))
	cancelStartup() // Start's request lifetime cannot cancel the running cohort.
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { stopDeliveries(t, s) })
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	entered, canceled := make(chan struct{}, 4), make(chan struct{}, 4)
	var calls, active atomic.Int32
	task := deliveryTask(func(ctx context.Context) (runtime.DeliveryWork, error) {
		calls.Add(1)
		active.Add(1)
		defer active.Add(-1)
		entered <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		<-release // Deliberately delayed cancellation must still be joined.
		return runtime.DeliveryWork{Done: true}, nil
	})
	first := deliveryOwner(t, r, "first")
	require.NoError(t, s.Register(first, task))
	require.ErrorIs(t, s.Register(first, deliveryTask(func(context.Context) (runtime.DeliveryWork, error) {
		panic("replacement must never execute")
	})), runtime.ErrDeliveriesRegistered)
	require.NoError(t, s.Register(deliveryOwner(t, r, "second"), task))
	deliveryEvent(t, entered)
	deliveryEvent(t, entered)
	require.NoError(t, s.Register(deliveryOwner(t, r, "queued"), task))
	require.ErrorIs(t, s.Register(deliveryOwner(t, r, "overflow"), task), runtime.ErrDeliveriesLimit)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, s.Stop(ctx), context.DeadlineExceeded)
	deliveryEvent(t, canceled)
	deliveryEvent(t, canceled)
	require.EqualValues(t, 2, active.Load())
	require.Equal(t, 3, s.Snapshot().Tracked)
	require.ErrorIs(t, s.Start(context.Background()), runtime.ErrDeliveriesStopped)
	require.ErrorIs(t, s.Wake(first), runtime.ErrDeliveriesStopped)
	require.ErrorIs(t, s.Register(first, task), runtime.ErrDeliveriesStopped)
	once.Do(func() { close(release) })
	stopDeliveries(t, s)
	require.Zero(t, s.Snapshot().Tracked)
	require.Zero(t, active.Load())
	require.EqualValues(t, 2, calls.Load(), "queued work cannot begin business calls after Stop")
	// Joined scheduling alone did not close or isolate the physical owner.
	op, err := r.Begin(context.Background(), first)
	require.NoError(t, err)
	op.Done()
}

func TestDeliveriesWakeDuringIdleTurnAndFencedCleanupSurvive(t *testing.T) {
	r := deliveryOwners(t, 1)
	s, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: r, Workers: 1, IdleInterval: time.Minute})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { stopDeliveries(t, s) })
	o := deliveryOwner(t, r, "ending")
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	completed := make(chan struct{})
	attempt := 0
	require.NoError(t, s.Register(o, deliveryTask(func(ctx context.Context) (runtime.DeliveryWork, error) {
		attempt++
		if attempt == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return runtime.DeliveryWork{}, ctx.Err()
			}
			return runtime.DeliveryWork{}, nil
		}
		close(completed)
		return runtime.DeliveryWork{Done: true}, nil
	})))
	deliveryEvent(t, entered)
	require.NoError(t, r.Fence(o))
	for i := 0; i < 100; i++ {
		require.NoError(t, s.Wake(o))
	}
	release <- struct{}{}
	deliveryEvent(t, completed) // One-minute idle interval would lose this wake.
	require.Eventually(t, func() bool { return s.Snapshot().Completed == 1 }, time.Second, time.Millisecond)
}

func TestDeliveriesIdlePollingRecoversMissingWake(t *testing.T) {
	r := deliveryOwners(t, 1)
	s, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: r, Workers: 1, IdleInterval: 20 * time.Millisecond})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { stopDeliveries(t, s) })
	events := make(chan time.Time, 2)
	attempt := 0
	require.NoError(t, s.Register(deliveryOwner(t, r, "idle"), deliveryTask(func(context.Context) (runtime.DeliveryWork, error) {
		attempt++
		events <- time.Now()
		return runtime.DeliveryWork{Done: attempt == 2}, nil
	})))
	first, second := deliveryEvent(t, events), deliveryEvent(t, events)
	require.GreaterOrEqual(t, second.Sub(first), 20*time.Millisecond)
}

func TestDeliveriesRequireExactActivationAndNeverOverlapOneOwner(t *testing.T) {
	r := deliveryOwners(t, 1)
	s, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: r, Workers: 4})
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	t.Cleanup(func() { stopDeliveries(t, s) })
	o, err := r.Reserve(runtime.Claim{Key: contract.Key{Namespace: "main", ClientID: "one"}, UID: "alice", SessionGeneration: 1, OwnerGeneration: 1}, func(context.Context) error { return nil })
	require.NoError(t, err)
	entered, release := make(chan struct{}, 8), make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	task := deliveryTask(func(ctx context.Context) (runtime.DeliveryWork, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return runtime.DeliveryWork{}, ctx.Err()
		}
		return runtime.DeliveryWork{Done: true}, nil
	})
	require.ErrorIs(t, s.Register(o, task), runtime.ErrOwnerFenced)
	require.NoError(t, r.Activate(o, 1, time.Now().Add(50*time.Second)))
	for _, alter := range []func(*contract.Owner){
		func(o *contract.Owner) { o.Key.ClientID = "foreign" },
		func(o *contract.Owner) { o.OwnerGeneration++ },
		func(o *contract.Owner) { o.BootID = "foreign" },
	} {
		foreign := o
		alter(&foreign)
		require.Error(t, s.Register(foreign, task))
	}
	require.NoError(t, s.Register(o, task))
	deliveryEvent(t, entered)
	var wg sync.WaitGroup
	faults := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				if err := s.Wake(o); err != nil {
					faults <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(faults)
	for err := range faults {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, calls.Load())
	require.Equal(t, 1, s.Snapshot().Tracked)
	release <- struct{}{}
	require.Eventually(t, func() bool { return s.Snapshot().Completed == 1 }, time.Second, time.Millisecond)
	require.EqualValues(t, 1, calls.Load(), "terminal completion wins over pending hints")
}
