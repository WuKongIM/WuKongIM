package mqtt_test

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

type outboundAcks func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error)

func (f outboundAcks) Acknowledge(ctx context.Context, q sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
	return f(ctx, q)
}

func outboundFixture(t *testing.T, maximum uint16, ack outboundAcks, resumed ...bool) (*handlerFixture, access.OutboundDelivery) {
	t.Helper()
	f := newHandlerFixture(t)
	if len(resumed) > 0 {
		f.connection.SessionPresent = resumed[0]
	}
	var err error
	f.h, err = access.NewHandler(access.HandlerOptions{Namespace: "main", Sessions: f.sessions, Connections: f.connections, Owners: f.owners, Publisher: f.publisher, Acknowledgements: ack, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	p := handlerConnect()
	p.Properties = append(p.Properties, wire.Property{ID: wire.ReceiveMaximum, Number: uint32(maximum)})
	r, err := f.h.OnConnect(f.gateway, p)
	require.NoError(t, err)
	require.True(t, r.Accepted)
	for k, v := range r.SessionValues {
		f.gateway.Session.SetValue(k, v)
	}
	require.NoError(t, f.h.OnSessionOpen(f.gateway))
	var hash [32]byte
	hash[0] = 1
	m := ch.Message{MessageID: 100, MessageSeq: 7, ChannelID: "group", ChannelType: 2, FromUID: "bob", ClientMsgNo: "original", Payload: []byte("body"), ServerTimestampMS: f.now.UnixMilli()}
	e := meta.MQTTInflight{Key: meta.MQTTDeliveryCursorKey{Namespace: "main", ClientID: "SYSTEM", SessionGeneration: 2, SubscriptionGeneration: 1, SourceKind: meta.MQTTSourceChannel, SourceID: "2:group", SourceGeneration: "source"}, Direction: meta.MQTTOutbound, PacketID: 9, DeliveryOrder: 1, Publication: meta.MQTTInflightPublication{Position: 7, MessageID: 100, MessageSeq: 7, ContentVersion: 1, ContentHash: hex.EncodeToString(hash[:]), Bytes: 4, SubscriptionIdentifier: 33}, QoS: 1, Stage: meta.MQTTInflightAwaitPUBACK, Topic: "wk/v1/groups/Z3JvdXA/messages", UpdatedAtMS: f.now.UnixMilli()}
	return f, access.OutboundDelivery{Owner: f.connection.Owner, Exchange: e, Publication: ch.MQTTReplayPublication{Message: m, ContentVersion: 1, ContentHash: hash, AccountedBytes: 4}}
}
func nextOutbound(d access.OutboundDelivery) access.OutboundDelivery {
	d.Exchange.PacketID++
	d.Exchange.DeliveryOrder++
	return d
}

func TestOutboundBindsExactExchangeAndReturnsReceiveCreditAfterACK(t *testing.T) {
	var calls []sessioncase.AcknowledgementCommand
	f, d := outboundFixture(t, 1, func(_ context.Context, q sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
		calls = append(calls, q)
		return sessioncase.AcknowledgementResult{Changed: true}, nil
	})
	require.NoError(t, f.h.SendQoS1(f.gateway, d))
	require.ErrorIs(t, f.h.SendQoS1(f.gateway, d), access.ErrOutboundInvalid)
	next := nextOutbound(d)
	require.ErrorIs(t, f.h.SendQoS1(f.gateway, next), access.ErrOutboundBusy)
	require.NoError(t, f.h.OnPacket(f.gateway, &wire.Pingreq{}))
	require.NoError(t, f.h.OnPacket(f.gateway, &wire.Puback{PacketID: 1000}))
	require.Empty(t, calls)
	require.ErrorIs(t, f.h.SendQoS1(f.gateway, next), access.ErrOutboundBusy)
	require.NoError(t, f.h.OnPacket(f.gateway, &wire.Puback{PacketID: d.Exchange.PacketID, Reason: 0x87}))
	require.Equal(t, []sessioncase.AcknowledgementCommand{{Owner: d.Owner, Key: d.Exchange.Key, PacketID: d.Exchange.PacketID, DeliveryOrder: d.Exchange.DeliveryOrder}}, calls)
	require.NoError(t, f.h.OnPacket(f.gateway, &wire.Puback{PacketID: d.Exchange.PacketID}))
	require.Len(t, calls, 1)
	next.Exchange.PacketID = d.Exchange.PacketID // a new order may reuse the released identifier
	require.NoError(t, f.h.SendQoS1(f.gateway, next))
	require.NoError(t, f.h.OnPacket(f.gateway, &wire.Puback{PacketID: next.Exchange.PacketID}))
	require.Equal(t, next.Exchange.DeliveryOrder, calls[1].DeliveryOrder)
	require.Zero(t, f.owners.Snapshot().Operations)
	require.False(t, f.closed)
}

func TestOutboundMappingPreservesContentAndExpiry(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "remaining", true: "expired-begun"}[expired], func(t *testing.T) {
			f, d := outboundFixture(t, 2, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
				return sessioncase.AcknowledgementResult{Absent: true}, nil
			}, true)
			metadata := publication.Metadata{Source: publication.SourceMQTT, QoS: 1, PublisherNamespace: "main", PublisherClientID: "origin", OriginalTopic: d.Exchange.Topic, AcceptedAtMS: f.now.Add(-1500 * time.Millisecond).UnixMilli(), Properties: []publication.Property{{Kind: publication.UserProperty, Text: "app", Value: "first"}, {Kind: publication.CorrelationData, Binary: []byte{1, 2}}, {Kind: publication.UserProperty, Text: "app", Value: "second"}, {Kind: publication.MessageExpiry, Number: 3}}}
			var err error
			d.Publication.Message.PublicationMetadata, err = publication.Encode(metadata)
			require.NoError(t, err)
			d.Publication.AccountedBytes = uint64(len(d.Publication.Message.Payload) + len(d.Publication.Message.PublicationMetadata))
			d.Exchange.Publication.Bytes = d.Publication.AccountedBytes
			d.Redelivery = true
			if expired {
				f.now = f.now.Add(4 * time.Second)
			}
			require.NoError(t, f.h.SendQoS1(f.gateway, d))
			p := f.writes[0].(*wire.Publish)
			require.True(t, p.Dup)
			require.False(t, p.Retain)
			require.Equal(t, byte(1), p.QoS)
			require.Equal(t, d.Exchange.Topic, p.Topic)
			wantExpiry := uint32(2)
			if expired {
				wantExpiry = 0
			}
			require.Equal(t, []wire.Property{{ID: wire.UserProperty, Text: "app", Value: "first"}, {ID: wire.CorrelationData, Data: []byte{1, 2}}, {ID: wire.UserProperty, Text: "app", Value: "second"}, {ID: wire.MessageExpiryInterval, Number: wantExpiry}, {ID: wire.UserProperty, Text: "wk.message_id", Value: "100"}, {ID: wire.UserProperty, Text: "wk.message_seq", Value: "7"}, {ID: wire.UserProperty, Text: "wk.from_uid", Value: "bob"}, {ID: wire.UserProperty, Text: "wk.channel_id", Value: "group"}, {ID: wire.UserProperty, Text: "wk.channel_type", Value: "2"}, {ID: wire.UserProperty, Text: "wk.client_msg_no", Value: "original"}, {ID: wire.SubscriptionIdentifier, Number: 33}}, p.Properties)
			d.Publication.Message.Payload[0] = 'X'
			clear(d.Publication.Message.PublicationMetadata)
			require.Equal(t, "body", string(p.Payload))
			require.Equal(t, []byte{1, 2}, p.Properties[1].Data)
			require.NoError(t, f.h.OnPacket(f.gateway, &wire.Puback{PacketID: 9}))
			require.Error(t, f.h.SendQoS1(f.gateway, nextOutbound(d)))
		})
	}
}

func TestOutboundRejectsForeignOrUnusableEvidenceBeforeWrite(t *testing.T) {
	for _, mode := range []string{"owner", "session", "source", "message", "sequence", "position", "hash", "version", "bytes", "internal", "overlay", "topic", "qos", "stage", "packet", "reserved", "metadata", "fenced", "cancelled", "expired", "closed", "clock", "nil-ack"} {
		t.Run(mode, func(t *testing.T) {
			f, d := outboundFixture(t, 2, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
				t.Fatal("unexpected ACK")
				return sessioncase.AcknowledgementResult{}, nil
			})
			switch mode {
			case "owner":
				d.Owner.OwnerGeneration++
			case "session":
				d.Exchange.Key.SessionGeneration++
			case "source":
				d.Exchange.Key.SourceID = "2:foreign"
			case "message":
				d.Publication.Message.MessageID++
			case "sequence":
				d.Publication.Message.MessageSeq++
			case "position":
				d.Exchange.Publication.Position++
			case "hash":
				d.Publication.ContentHash[0]++
			case "version":
				d.Publication.ContentVersion++
			case "bytes":
				d.Publication.AccountedBytes++
			case "internal":
				d.Publication.Internal = true
			case "overlay":
				d.Publication.Message.Version = 1
			case "topic":
				d.Exchange.Topic = "wk/v1/groups/b3RoZXI/messages"
			case "qos":
				d.Exchange.QoS = 0
			case "stage":
				d.Exchange.Stage = 0
			case "packet":
				d.Exchange.PacketID = 0
			case "reserved":
				d.Publication.Message.PublicationMetadata, _ = publication.Encode(publication.Metadata{Source: publication.SourceMQTT, QoS: 1, AcceptedAtMS: f.now.UnixMilli(), PublisherNamespace: "main", PublisherClientID: "origin", OriginalTopic: d.Exchange.Topic, Properties: []publication.Property{{Kind: publication.UserProperty, Text: "wk.message_id", Value: "forged"}}})
				d.Publication.AccountedBytes = uint64(4 + len(d.Publication.Message.PublicationMetadata))
				d.Exchange.Publication.Bytes = d.Publication.AccountedBytes
			case "metadata":
				d.Publication.Message.PublicationMetadata = []byte{99}
			case "fenced":
				require.NoError(t, f.owners.Fence(d.Owner))
			case "cancelled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				f.gateway.RequestContext = ctx
			case "expired":
				f.now = f.now.Add(time.Minute)
			case "closed":
				require.NoError(t, f.h.OnSessionClose(f.gateway))
			case "clock":
				f.now = f.now.Add(-time.Second)
			case "nil-ack":
				var err error
				f.h, err = access.NewHandler(access.HandlerOptions{Namespace: "main", Sessions: f.sessions, Connections: f.connections, Owners: f.owners, Publisher: f.publisher, Now: func() time.Time { return f.now }})
				require.NoError(t, err)
			}
			require.Error(t, f.h.SendQoS1(f.gateway, d))
			require.Empty(t, f.writes)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}

func TestOutboundACKFailureClosesWithoutReleasingOrRetrying(t *testing.T) {
	for _, mode := range []string{"error", "panic", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			f, d := outboundFixture(t, 1, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
				calls++
				switch mode {
				case "panic":
					panic("secret")
				case "malformed":
					return sessioncase.AcknowledgementResult{Changed: true, Absent: true}, nil
				}
				return sessioncase.AcknowledgementResult{}, errors.New("secret")
			})
			require.NoError(t, f.h.SendQoS1(f.gateway, d))
			err := f.h.OnPacket(f.gateway, &wire.Puback{PacketID: 9})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
			require.True(t, f.closed)
			require.Equal(t, 1, calls)
			require.Zero(t, f.owners.Snapshot().Operations)
			require.Error(t, f.h.SendQoS1(f.gateway, nextOutbound(d)))
			require.Len(t, f.writes, 1)
		})
	}
}

func TestOutboundSendGateAndFastACKDoNotHoldConnectionLock(t *testing.T) {
	var ackCalls int
	f, d := outboundFixture(t, 2, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
		ackCalls++
		return sessioncase.AcknowledgementResult{Changed: true}, nil
	})
	// The callback deliberately reenters before the enqueue returns.
	f.write = func(p any) error {
		require.Equal(t, 1, f.owners.Snapshot().Operations)
		require.ErrorIs(t, f.h.SendQoS1(f.gateway, nextOutbound(d)), access.ErrOutboundBusy)
		require.NoError(t, f.h.OnPacket(f.gateway, &wire.Puback{PacketID: 9}))
		return nil
	}
	require.NoError(t, f.h.SendQoS1(f.gateway, d))
	require.Equal(t, 1, ackCalls)
	require.Zero(t, f.owners.Snapshot().Operations)
}

func TestOutboundWriteFailureAndPanicFenceOwner(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panics], func(t *testing.T) {
			f, d := outboundFixture(t, 1, func(context.Context, sessioncase.AcknowledgementCommand) (sessioncase.AcknowledgementResult, error) {
				return sessioncase.AcknowledgementResult{}, nil
			})
			f.write = func(any) error {
				if panics {
					panic("secret")
				}
				return errors.New("secret")
			}
			err := f.h.SendQoS1(f.gateway, d)
			require.Error(t, err)
			require.False(t, strings.Contains(err.Error(), "secret"))
			require.True(t, f.closed)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
