package mqtt_test

import (
	"context"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/stretchr/testify/require"
)

func TestDeliverySinkPreservesGatewayCreditAndMapsDefiniteNonWrites(t *testing.T) {
	f, d := outboundFixture(t, 1, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
		return sessioncase.AcknowledgementResult{Changed: true}, nil
	}, true)
	c, sink, err := f.h.BindDelivery(f.gateway)
	require.NoError(t, err)
	require.Equal(t, f.connection, c)
	prepared := sessioncase.PreparedDelivery{Owner: d.Owner, QoS: 1, Topic: d.Exchange.Topic, Exchange: d.Exchange, Publication: d.Publication}
	got, err := sink.Enqueue(context.Background(), prepared, true)
	require.NoError(t, err)
	require.Equal(t, sessioncase.DeliveryQueued, got)
	require.True(t, f.writes[0].(*wire.Publish).Dup)
	next := prepared
	next.Exchange.PacketID++
	next.Exchange.DeliveryOrder++
	got, err = sink.Enqueue(context.Background(), next, false)
	require.NoError(t, err)
	require.Equal(t, sessioncase.DeliveryBusy, got)
	require.Len(t, f.writes, 1)
	qos0 := prepared
	qos0.QoS = 0
	qos0.Exchange = meta.MQTTInflight{}
	got, err = sink.Enqueue(context.Background(), qos0, false)
	require.NoError(t, err)
	require.Equal(t, sessioncase.DeliveryQueued, got)
	require.Zero(t, f.writes[1].(*wire.Publish).QoS)
	qos0.Publication.Message.Expire = 1
	f.now = f.now.Add(2 * time.Second)
	got, err = sink.Enqueue(context.Background(), qos0, false)
	require.NoError(t, err)
	require.Equal(t, sessioncase.DeliveryExpired, got)
	require.Len(t, f.writes, 2)
	require.NoError(t, f.h.OnPacket(f.gateway, &wire.Puback{PacketID: prepared.Exchange.PacketID}))
	got, err = sink.Enqueue(context.Background(), next, false)
	require.NoError(t, err)
	require.Equal(t, sessioncase.DeliveryQueued, got)
	require.NoError(t, sink.Close(context.Background(), meta.MQTTSessionRevoked))
	require.Equal(t, byte(0x87), f.writes[len(f.writes)-1].(*wire.Disconnect).Reason)
	require.True(t, f.closed)
	_, _, err = f.h.BindDelivery(f.gateway)
	require.Error(t, err)
}

func TestDeliverySinkRejectsUnopenedForeignCanceledAndInvalidQoS(t *testing.T) {
	before := newHandlerFixture(t)
	_, _, err := before.h.BindDelivery(before.gateway)
	require.Error(t, err)
	f, d := outboundFixture(t, 2, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
		return sessioncase.AcknowledgementResult{}, nil
	})
	_, sink, err := f.h.BindDelivery(f.gateway)
	require.NoError(t, err)
	p := sessioncase.PreparedDelivery{Owner: d.Owner, QoS: 1, Topic: d.Exchange.Topic, Exchange: d.Exchange, Publication: d.Publication}
	foreign := p
	foreign.Owner.OwnerGeneration++
	_, err = sink.Enqueue(context.Background(), foreign, false)
	require.Error(t, err)
	invalid := p
	invalid.QoS = 2
	_, err = sink.Enqueue(context.Background(), invalid, false)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = sink.Enqueue(ctx, p, false)
	require.Error(t, err)
	require.Empty(t, f.writes)
	require.ErrorIs(t, sink.Close(context.Background(), meta.MQTTSessionCleanStart), access.ErrOutboundInvalid)
}

func TestDeliverySinkOwnerPressureYieldsWithoutClosingOrConsumingOrder(t *testing.T) {
	for _, qos := range []uint8{0, 1} {
		t.Run(map[uint8]string{0: "qos0", 1: "qos1"}[qos], func(t *testing.T) {
			f, d := outboundFixture(t, 2, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
				return sessioncase.AcknowledgementResult{}, nil
			})
			_, sink, err := f.h.BindDelivery(f.gateway)
			require.NoError(t, err)
			p := sessioncase.PreparedDelivery{Owner: d.Owner, QoS: qos, Topic: d.Exchange.Topic, Exchange: d.Exchange, Publication: d.Publication}
			if qos == 0 {
				p.Exchange = meta.MQTTInflight{}
			}
			// This fixture has one operation slot; another admitted effect owns it.
			op, err := f.owners.Begin(context.Background(), d.Owner)
			require.NoError(t, err)
			defer op.Done()
			got, err := sink.Enqueue(context.Background(), p, false)
			require.NoError(t, err)
			require.Equal(t, sessioncase.DeliveryBusy, got)
			require.Empty(t, f.writes)
			require.False(t, f.closed)
			op.Done()
			got, err = sink.Enqueue(context.Background(), p, false)
			require.NoError(t, err)
			require.Equal(t, sessioncase.DeliveryQueued, got)
			require.Len(t, f.writes, 1)
		})
	}
}
