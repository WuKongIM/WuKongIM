package mqtt_test

import (
	"context"
	"errors"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/stretchr/testify/require"
)

type handlerDeliveries struct {
	register func(context.Context, sessioncase.Connection, sessioncase.DeliverySink) error
	wake     func(contract.Owner) error
}

func (d handlerDeliveries) Register(ctx context.Context, c sessioncase.Connection, sink sessioncase.DeliverySink) error {
	return d.register(ctx, c, sink)
}
func (d handlerDeliveries) Wake(o contract.Owner) error {
	if d.wake != nil {
		return d.wake(o)
	}
	return nil
}
func installHandlerDeliveries(t *testing.T, f *handlerFixture, d handlerDeliveries) {
	t.Helper()
	h, err := access.NewHandler(access.HandlerOptions{Namespace: "main", Sessions: f.sessions, Connections: f.connections, Owners: f.owners, Publisher: f.publisher, Deliveries: d, Acknowledgements: outboundAcks(func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
		return sessioncase.AcknowledgementResult{Changed: true}, nil
	}), Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	f.h = h
}

func TestHandlerDeliveryRegistersAfterHandshakeAndWakesAfterNormalFence(t *testing.T) {
	_, sample := outboundFixture(t, 1, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
		return sessioncase.AcknowledgementResult{}, nil
	})
	f := newHandlerFixture(t)
	registered, woken := 0, 0
	installHandlerDeliveries(t, f, handlerDeliveries{register: func(ctx context.Context, c sessioncase.Connection, sink sessioncase.DeliverySink) error {
		registered++
		require.Equal(t, f.connection, c)
		require.Zero(t, f.owners.Snapshot().Operations)
		deadline, bounded := ctx.Deadline()
		require.True(t, bounded)
		require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
		bound, _, err := f.h.BindDelivery(f.gateway) // Reentry must not deadlock.
		require.NoError(t, err)
		require.Equal(t, c, bound)
		sample.Owner = c.Owner
		sample.Publication.Message.ServerTimestampMS = f.now.UnixMilli()
		got, err := sink.Enqueue(ctx, sessioncase.PreparedDelivery{Owner: c.Owner, QoS: 1, Topic: sample.Exchange.Topic, Exchange: sample.Exchange, Publication: sample.Publication}, false)
		require.NoError(t, err)
		require.Equal(t, sessioncase.DeliveryQueued, got)
		return nil
	}, wake: func(o contract.Owner) error {
		woken++
		require.Equal(t, f.connection.Owner, o)
		require.Len(t, f.connections.intents, 1)
		require.True(t, f.connections.intents[0].Normal)
		_, err := f.owners.Begin(context.Background(), o)
		require.ErrorIs(t, err, runtime.ErrOwnerFenced)
		return errors.New("lost hint")
	}})
	r := f.accept(t)
	require.Zero(t, registered)
	require.NoError(t, r.CheckReply())
	require.NoError(t, f.h.OnSessionOpen(f.gateway))
	require.Equal(t, 1, registered)
	require.Len(t, f.writes, 1)
	require.NoError(t, f.h.OnPacket(f.gateway, &wire.Disconnect{}))
	require.NoError(t, f.h.OnSessionClose(f.gateway))
	require.Equal(t, 1, woken)
	require.Error(t, f.h.OnSessionOpen(f.gateway))
	require.Equal(t, 1, registered)
}

func TestHandlerDeliveryNeverRegistersFailedHandshake(t *testing.T) {
	for _, mode := range []string{"rollback", "cancelled", "fenced"} {
		t.Run(mode, func(t *testing.T) {
			f := newHandlerFixture(t)
			calls := 0
			installHandlerDeliveries(t, f, handlerDeliveries{register: func(context.Context, sessioncase.Connection, sessioncase.DeliverySink) error { calls++; return nil }})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.gateway.RequestContext = ctx
			r := f.accept(t)
			switch mode {
			case "rollback":
				r.Rollback(nil)
			case "cancelled":
				cancel()
			case "fenced":
				require.NoError(t, f.owners.Fence(f.connection.Owner))
			}
			require.Error(t, f.h.OnSessionOpen(f.gateway))
			require.Zero(t, calls)
			require.Len(t, f.connections.intents, 1)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestHandlerDeliveryRegistrationFailurePreservesCleanup(t *testing.T) {
	for _, mode := range []string{"error", "panic", "cancel", "close", "fence"} {
		t.Run(mode, func(t *testing.T) {
			f := newHandlerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.gateway.RequestContext = ctx
			calls, wakes := 0, 0
			installHandlerDeliveries(t, f, handlerDeliveries{register: func(_ context.Context, _ sessioncase.Connection, sink sessioncase.DeliverySink) error {
				calls++
				switch mode {
				case "error":
					return errors.New("private dependency details")
				case "panic":
					panic("private dependency details")
				case "cancel":
					cancel()
				case "close":
					require.NoError(t, sink.Close(context.Background(), 0))
				case "fence":
					require.NoError(t, f.owners.Fence(f.connection.Owner))
				}
				return nil
			}, wake: func(o contract.Owner) error {
				wakes++
				require.Equal(t, f.connection.Owner, o)
				require.Len(t, f.connections.intents, 1)
				panic("private wake details")
			}})
			f.accept(t)
			err := f.h.OnSessionOpen(f.gateway)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private")
			require.Equal(t, 1, calls)
			require.Equal(t, 1, wakes)
			require.Len(t, f.connections.intents, 1)
			require.False(t, f.connections.intents[0].Normal)
			require.Zero(t, f.owners.Snapshot().Operations)
			_, err = f.owners.Begin(context.Background(), f.connection.Owner)
			require.ErrorIs(t, err, runtime.ErrOwnerFenced)
			require.Error(t, f.h.OnSessionOpen(f.gateway))
			require.Equal(t, 1, calls)
		})
	}
}

func TestHandlerDeliveryACKWakeSeesReleasedCreditAndIgnoresHintFailure(t *testing.T) {
	for _, mode := range []string{"error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			var f *handlerFixture
			var d access.OutboundDelivery
			committed, wakes := false, 0
			deliveries := handlerDeliveries{register: func(context.Context, sessioncase.Connection, sessioncase.DeliverySink) error { return nil }, wake: func(o contract.Owner) error {
				// Cleanup wakes happen after the assertions and are not ACK hints.
				if len(f.connections.intents) > 0 {
					return nil
				}
				wakes++
				require.True(t, committed)
				require.Equal(t, f.connection.Owner, o)
				require.NoError(t, f.h.SendQoS1(f.gateway, nextOutbound(d)))
				if mode == "panic" {
					panic("private hint")
				}
				return errors.New("lost hint")
			}}
			f, d = outboundFixtureWithOptions(t, 1, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
				require.Zero(t, wakes)
				committed = true
				return sessioncase.AcknowledgementResult{Changed: true}, nil
			}, func(o *access.HandlerOptions) { o.Deliveries = deliveries })
			require.NoError(t, f.h.SendQoS1(f.gateway, d))
			require.NoError(t, f.h.OnPacket(f.gateway, &wire.Puback{PacketID: 777}))
			require.Zero(t, wakes)
			require.NoError(t, f.h.OnPacket(f.gateway, &wire.Puback{PacketID: d.Exchange.PacketID, Reason: 0x87}))
			require.Equal(t, 1, wakes)
			require.NoError(t, f.h.OnPacket(f.gateway, &wire.Puback{PacketID: d.Exchange.PacketID}))
			require.Equal(t, 1, wakes)
			require.Len(t, f.writes, 2)
			require.False(t, f.closed)
		})
	}
}

func TestHandlerDeliveryRequiresAcknowledgements(t *testing.T) {
	f := newHandlerFixture(t)
	_, err := access.NewHandler(access.HandlerOptions{Namespace: "main", Sessions: f.sessions, Connections: f.connections, Owners: f.owners, Publisher: f.publisher, Deliveries: handlerDeliveries{}})
	require.ErrorIs(t, err, access.ErrHandlerInvalid)
}

func TestHandlerDeliveryScheduledWorkMayOccupyOwnerDuringOpen(t *testing.T) {
	f := newHandlerFixture(t)
	var admitted *runtime.Operation
	installHandlerDeliveries(t, f, handlerDeliveries{register: func(ctx context.Context, c sessioncase.Connection, _ sessioncase.DeliverySink) error {
		var err error
		admitted, err = f.owners.Begin(context.Background(), c.Owner)
		return err
	}})
	f.accept(t)
	err := f.h.OnSessionOpen(f.gateway)
	if admitted != nil {
		defer admitted.Done()
	}
	require.NoError(t, err)
	require.NoError(t, admitted.Check())
	require.Empty(t, f.connections.intents)
}
