package mqtt_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func preparedQoS0(d access.OutboundDelivery) sessioncase.PreparedDelivery {
	return sessioncase.PreparedDelivery{Owner: d.Owner, Topic: d.Exchange.Topic, SubscriptionIdentifier: d.Exchange.Publication.SubscriptionIdentifier, Publication: d.Publication}
}
func TestOutboundQoS0UsesNoACKCreditAndSharesSendGate(t *testing.T) {
	acks := 0
	f, d := outboundFixture(t, 1, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
		acks++
		return sessioncase.AcknowledgementResult{Changed: true}, nil
	})
	require.NoError(t, f.h.SendQoS1(f.gateway, d))
	q := preparedQoS0(d)
	f.write = func(v any) error {
		require.Equal(t, 1, f.owners.Snapshot().Operations)
		p := v.(*wire.Publish)
		require.Zero(t, p.QoS)
		require.Zero(t, p.PacketID)
		require.False(t, p.Dup)
		require.ErrorIs(t, f.h.SendQoS0(f.gateway, q), access.ErrOutboundBusy)
		require.ErrorIs(t, f.h.SendQoS1(f.gateway, nextOutbound(d)), access.ErrOutboundBusy)
		f.writes = append(f.writes, p)
		return nil
	}
	require.NoError(t, f.h.SendQoS0(f.gateway, q))
	require.Zero(t, acks)
	require.Len(t, f.writes, 2)
	require.ErrorIs(t, f.h.SendQoS1(f.gateway, nextOutbound(d)), access.ErrOutboundBusy)
	require.NoError(t, f.h.OnPacket(f.gateway, &wire.Puback{PacketID: d.Exchange.PacketID}))
	require.Equal(t, 1, acks)
	f.write = nil
	require.NoError(t, f.h.SendQoS1(f.gateway, nextOutbound(d)))
	require.Zero(t, f.owners.Snapshot().Operations)
}
func TestOutboundQoS0MapsOriginalAndDowngradedPublication(t *testing.T) {
	for _, qos := range []uint8{0, 1} {
		t.Run(string(rune('0'+qos)), func(t *testing.T) {
			f, d := outboundFixture(t, 1, nil)
			md := publication.Metadata{Source: publication.SourceMQTT, QoS: qos, PublisherNamespace: "main", PublisherClientID: "origin", OriginalTopic: d.Exchange.Topic, AcceptedAtMS: f.now.Add(-1500 * time.Millisecond).UnixMilli(), Properties: []publication.Property{{Kind: publication.UserProperty, Text: "app", Value: "one"}, {Kind: publication.CorrelationData, Binary: []byte{1, 2}}, {Kind: publication.UserProperty, Text: "app", Value: "two"}, {Kind: publication.MessageExpiry, Number: 3}}}
			var err error
			d.Publication.Message.PublicationMetadata, err = publication.Encode(md)
			require.NoError(t, err)
			d.Publication.AccountedBytes = uint64(4 + len(d.Publication.Message.PublicationMetadata))
			q := preparedQoS0(d)
			require.NoError(t, f.h.SendQoS0(f.gateway, q))
			p := f.writes[0].(*wire.Publish)
			require.Equal(t, []wire.Property{{ID: wire.UserProperty, Text: "app", Value: "one"}, {ID: wire.CorrelationData, Data: []byte{1, 2}}, {ID: wire.UserProperty, Text: "app", Value: "two"}, {ID: wire.MessageExpiryInterval, Number: 2}}, p.Properties[:4])
			require.Equal(t, wire.Property{ID: wire.SubscriptionIdentifier, Number: 33}, p.Properties[len(p.Properties)-1])
			q.Publication.Message.Payload[0] = 'X'
			clear(q.Publication.Message.PublicationMetadata)
			require.Equal(t, "body", string(p.Payload))
			require.Equal(t, []byte{1, 2}, p.Properties[1].Data)
			require.Zero(t, f.owners.Snapshot().Operations)
			require.False(t, f.closed)
		})
	}
}
func TestOutboundExpiryUsesOriginalNativeAndWillClocks(t *testing.T) {
	for _, mode := range []string{"native", "will", "earlier_native", "earlier_metadata"} {
		t.Run(mode, func(t *testing.T) {
			f, d := outboundFixture(t, 1, nil)
			d.Publication.Message.ServerTimestampMS = f.now.Add(-1500 * time.Millisecond).UnixMilli()
			d.Publication.Message.Expire = 3
			if mode != "native" {
				source := publication.SourceMQTT
				accepted := f.now.Add(-1500 * time.Millisecond).UnixMilli()
				expiry := uint32(10)
				if mode == "will" {
					source = publication.SourceWill
					accepted = 0
					d.Publication.Message.Expire = 0
					expiry = 3
				}
				if mode == "earlier_metadata" {
					d.Publication.Message.Expire = 10
					expiry = 3
				}
				var err error
				d.Publication.Message.PublicationMetadata, err = publication.Encode(publication.Metadata{Source: source, QoS: 1, AcceptedAtMS: accepted, PublisherNamespace: "main", PublisherClientID: "origin", OriginalTopic: d.Exchange.Topic, Properties: []publication.Property{{Kind: publication.MessageExpiry, Number: expiry}}})
				require.NoError(t, err)
				d.Publication.AccountedBytes = uint64(4 + len(d.Publication.Message.PublicationMetadata))
			}
			q := preparedQoS0(d)
			require.NoError(t, f.h.SendQoS0(f.gateway, q))
			p := f.writes[0].(*wire.Publish)
			count := 0
			for _, prop := range p.Properties {
				if prop.ID == wire.MessageExpiryInterval {
					count++
					require.EqualValues(t, 2, prop.Number)
				}
			}
			require.Equal(t, 1, count)
			f.now = f.now.Add(2 * time.Second)
			require.ErrorIs(t, f.h.SendQoS0(f.gateway, q), access.ErrOutboundExpired)
			require.Len(t, f.writes, 1)
			require.False(t, f.closed)
		})
	}
}
func TestOutboundQoS0RejectsInvalidCandidateBeforeWrite(t *testing.T) {
	for _, mode := range []string{"owner", "qos", "exchange", "topic", "message", "sequence", "internal", "hash", "content_version", "bytes", "overlay", "metadata", "reserved", "identifier", "future_clock", "overflow", "fenced", "cancel", "lease", "closed"} {
		t.Run(mode, func(t *testing.T) {
			f, d := outboundFixture(t, 1, nil)
			q := preparedQoS0(d)
			switch mode {
			case "owner":
				q.Owner.OwnerGeneration++
			case "qos":
				q.QoS = 1
			case "exchange":
				q.Exchange = d.Exchange
			case "topic":
				q.Topic = "wk/v1/groups/b3RoZXI/messages"
			case "message":
				q.Publication.Message.MessageID = 0
			case "sequence":
				q.Publication.Message.MessageSeq = 0
			case "internal":
				q.Publication.Internal = true
			case "hash":
				q.Publication.ContentHash = [32]byte{}
			case "content_version":
				q.Publication.ContentVersion++
			case "bytes":
				q.Publication.AccountedBytes++
			case "overlay":
				q.Publication.Message.Version = 1
			case "metadata":
				q.Publication.Message.PublicationMetadata = []byte{99}
			case "reserved":
				q.Publication.Message.PublicationMetadata, _ = publication.Encode(publication.Metadata{Source: publication.SourceMQTT, QoS: 0, AcceptedAtMS: f.now.UnixMilli(), PublisherNamespace: "main", PublisherClientID: "origin", OriginalTopic: q.Topic, Properties: []publication.Property{{Kind: publication.UserProperty, Text: "wk.message_id", Value: "forged"}}})
				q.Publication.AccountedBytes = uint64(4 + len(q.Publication.Message.PublicationMetadata))
			case "identifier":
				q.SubscriptionIdentifier = math.MaxUint32
			case "future_clock":
				q.Publication.Message.ServerTimestampMS = f.now.Add(time.Second).UnixMilli()
			case "overflow":
				q.Publication.Message.ServerTimestampMS = math.MaxInt64
				q.Publication.Message.Expire = 1
			case "fenced":
				require.NoError(t, f.owners.Fence(q.Owner))
			case "cancel":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				f.gateway.RequestContext = ctx
			case "lease":
				f.now = f.now.Add(time.Minute)
			case "closed":
				require.NoError(t, f.h.OnSessionClose(f.gateway))
			}
			require.Error(t, f.h.SendQoS0(f.gateway, q))
			require.Empty(t, f.writes)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
func TestOutboundQoS0EnqueueFailureFencesWithoutRetry(t *testing.T) {
	for _, mode := range []string{"error", "panic", "cancel", "fence"} {
		t.Run(mode, func(t *testing.T) {
			f, d := outboundFixture(t, 1, nil)
			q := preparedQoS0(d)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.gateway.RequestContext = ctx
			calls := 0
			f.write = func(any) error {
				calls++
				switch mode {
				case "error":
					return errors.New("secret")
				case "panic":
					panic("secret")
				case "cancel":
					cancel()
				case "fence":
					require.NoError(t, f.owners.Fence(q.Owner))
				}
				return nil
			}
			err := f.h.SendQoS0(f.gateway, q)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
			require.True(t, f.closed)
			require.Equal(t, 1, calls)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
func TestOutboundFreshConnectionRejectsRedelivery(t *testing.T) {
	f, d := outboundFixture(t, 1, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
		return sessioncase.AcknowledgementResult{}, nil
	})
	d.Redelivery = true
	require.ErrorIs(t, f.h.SendQoS1(f.gateway, d), access.ErrOutboundInvalid)
	require.Empty(t, f.writes)
}
func TestOutboundBegunNativeExpiryIsPreserved(t *testing.T) {
	f, d := outboundFixture(t, 1, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
		return sessioncase.AcknowledgementResult{}, nil
	})
	d.Publication.Message.Expire = 1
	d.Publication.Message.ServerTimestampMS = f.now.Add(-time.Second).UnixMilli()
	require.NoError(t, f.h.SendQoS1(f.gateway, d))
	p := f.writes[0].(*wire.Publish)
	require.Contains(t, p.Properties, wire.Property{ID: wire.MessageExpiryInterval})
	require.Equal(t, meta.MQTTInflightAwaitPUBACK, d.Exchange.Stage)
}
