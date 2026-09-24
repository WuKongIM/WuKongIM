package mqtt_test

import (
	"context"
	"errors"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/stretchr/testify/require"
)

type handlerSubscriptions struct {
	subscribe   func(context.Context, contract.Owner, app.SubscriptionRequest) (meta.MQTTSubscription, error)
	unsubscribe func(context.Context, contract.Owner, string) (bool, error)
}

func (s *handlerSubscriptions) Subscribe(ctx context.Context, o contract.Owner, r app.SubscriptionRequest) (meta.MQTTSubscription, error) {
	return s.subscribe(ctx, o, r)
}
func (s *handlerSubscriptions) Unsubscribe(ctx context.Context, o contract.Owner, topic string) (bool, error) {
	return s.unsubscribe(ctx, o, topic)
}
func subscriptionRow(o contract.Owner, r app.SubscriptionRequest, now time.Time) meta.MQTTSubscription {
	return meta.MQTTSubscription{Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Topic: r.Topic, Generation: 1, Revision: 1, TargetKind: r.TargetKind, TargetID: r.TargetID, GrantedQoS: min(r.RequestedQoS, 1), Stage: meta.MQTTSubscriptionActive, OperationID: "test", UpdatedAtMS: now.UnixMilli(), NoLocal: r.NoLocal, RetainAsPublished: r.RetainAsPublished, RetainHandling: r.RetainHandling, SubscriptionIdentifier: r.SubscriptionIdentifier}
}
func subscriptionHandler(t *testing.T) (*handlerFixture, *handlerSubscriptions, *int) {
	t.Helper()
	f := newHandlerFixture(t)
	port := &handlerSubscriptions{subscribe: func(_ context.Context, o contract.Owner, r app.SubscriptionRequest) (meta.MQTTSubscription, error) {
		return subscriptionRow(o, r, f.now), nil
	}, unsubscribe: func(context.Context, contract.Owner, string) (bool, error) { return true, nil }}
	wakes := new(int)
	h, err := access.NewHandler(access.HandlerOptions{Namespace: "main", Sessions: f.sessions, Connections: f.connections, Owners: f.owners, Publisher: f.publisher, Acknowledgements: outboundAcks(func(context.Context, app.AcknowledgementCommand) (app.AcknowledgementResult, error) {
		return app.AcknowledgementResult{}, nil
	}), Subscriptions: port, Deliveries: handlerDeliveries{register: func(context.Context, app.Connection, app.DeliverySink) error { return nil }, wake: func(contract.Owner) error { *wakes++; return nil }}, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	f.h = h
	r := f.accept(t)
	var advertised uint32
	for _, p := range r.Reply.(*wire.Connack).Properties {
		if p.ID == wire.SubscriptionIdentifierAvailable {
			advertised = p.Number
		}
	}
	require.EqualValues(t, 1, advertised)
	require.NoError(t, f.h.OnSessionOpen(f.gateway))
	return f, port, wakes
}
func subscribePacket() *wire.Subscribe {
	return &wire.Subscribe{PacketID: 19, Properties: []wire.Property{{ID: wire.SubscriptionIdentifier, Number: 41}}, Subscriptions: []wire.Subscription{{Filter: "wk/v1/groups/Z3JvdXA/messages", QoS: 2, NoLocal: true, RetainAsPublished: true, RetainHandling: 2}}}
}
func TestSubscriptionEntryMapsOrderedReasonsOptionsAndOwner(t *testing.T) {
	f, port, wakes := subscriptionHandler(t)
	var requests []app.SubscriptionRequest
	port.subscribe = func(ctx context.Context, o contract.Owner, r app.SubscriptionRequest) (meta.MQTTSubscription, error) {
		require.Equal(t, f.connection.Owner, o)
		require.Equal(t, 1, f.owners.Snapshot().Operations)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
		requests = append(requests, r)
		if r.TargetID == "bob" {
			return meta.MQTTSubscription{}, app.ErrSubscriptionDenied
		}
		return subscriptionRow(o, r, f.now), nil
	}
	p := subscribePacket()
	p.Properties = append(p.Properties, wire.Property{ID: wire.UserProperty, Text: "app", Value: "a"}, wire.Property{ID: wire.UserProperty, Text: "app", Value: "b"})
	p.Subscriptions = append(p.Subscriptions, wire.Subscription{Filter: "wk/v1/groups/+/messages"}, wire.Subscription{Filter: "$share/team/wk/v1/groups/Z3JvdXA/messages"}, wire.Subscription{Filter: "wk/v1/groups/Z3JvdXA=/messages"}, wire.Subscription{Filter: "wk/v1/users/YWxpY2U/messages"}, wire.Subscription{Filter: "wk/v1/users/Ym9i/messages"})
	require.NoError(t, f.h.OnPacket(f.gateway, p))
	require.Equal(t, []any{&wire.Suback{PacketID: 19, Reasons: []byte{1, 0xa2, 0x9e, 0x8f, 0, 0x87}}}, f.writes)
	require.Len(t, requests, 3)
	require.Equal(t, app.SubscriptionRequest{Topic: p.Subscriptions[0].Filter, TargetID: "group", TargetKind: meta.MQTTSubscriptionGroup, RequestedQoS: 2, NoLocal: true, RetainAsPublished: true, RetainHandling: 2, SubscriptionIdentifier: 41}, requests[0])
	require.Equal(t, meta.MQTTSubscriptionUserInbox, requests[1].TargetKind)
	require.Equal(t, "alice", requests[1].TargetID)
	require.Equal(t, 1, *wakes)
	require.Zero(t, f.owners.Snapshot().Operations)
	require.False(t, f.closed)
}
func TestUnsubscriptionEntryMapsAbsentAndRetainsOwnerThroughReply(t *testing.T) {
	f, port, wakes := subscriptionHandler(t)
	var topics []string
	port.unsubscribe = func(_ context.Context, o contract.Owner, topic string) (bool, error) {
		require.Equal(t, f.connection.Owner, o)
		topics = append(topics, topic)
		return len(topics) == 1, nil
	}
	f.write = func(p any) error {
		require.Equal(t, 1, f.owners.Snapshot().Operations)
		require.Equal(t, &wire.Unsuback{PacketID: 20, Reasons: []byte{0, 0x11, 0x8f}}, p)
		require.Zero(t, *wakes)
		return nil
	}
	require.NoError(t, f.h.OnPacket(f.gateway, &wire.Unsubscribe{PacketID: 20, Filters: []string{"wk/v1/groups/Z3JvdXA/messages", "wk/v1/users/Ym9i/messages", "#"}}))
	require.Len(t, topics, 2)
	require.Equal(t, 1, *wakes)
	require.Zero(t, f.owners.Snapshot().Operations)
	require.False(t, f.closed)
}
func TestSubscriptionEntryRejectsMalformedBatchBeforeEffects(t *testing.T) {
	for _, mode := range []string{"nil", "zero-id", "empty", "oversized", "qos", "retain", "zero-identifier", "duplicate-identifier", "unknown-property", "reserved-property", "unsubscribe-identifier", "unsubscribe-empty", "unsubscribe-id", "unsubscribe-oversized"} {
		t.Run(mode, func(t *testing.T) {
			f, port, _ := subscriptionHandler(t)
			calls := 0
			port.subscribe = func(context.Context, contract.Owner, app.SubscriptionRequest) (meta.MQTTSubscription, error) {
				calls++
				return meta.MQTTSubscription{}, nil
			}
			port.unsubscribe = func(context.Context, contract.Owner, string) (bool, error) { calls++; return true, nil }
			p := subscribePacket()
			var packet any = p
			switch mode {
			case "nil":
				packet = (*wire.Subscribe)(nil)
			case "zero-id":
				p.PacketID = 0
			case "empty":
				p.Subscriptions = nil
			case "oversized":
				p.Subscriptions = make([]wire.Subscription, 129)
			case "qos":
				p.Subscriptions = append(p.Subscriptions, wire.Subscription{Filter: p.Subscriptions[0].Filter, QoS: 3})
			case "retain":
				p.Subscriptions[0].RetainHandling = 3
			case "zero-identifier":
				p.Properties[0].Number = 0
			case "duplicate-identifier":
				p.Properties = append(p.Properties, p.Properties[0])
			case "unknown-property":
				p.Properties = append(p.Properties, wire.Property{ID: wire.TopicAlias, Number: 1})
			case "reserved-property":
				p.Properties = append(p.Properties, wire.Property{ID: wire.UserProperty, Text: "wk.from_uid", Value: "mallory"})
			case "unsubscribe-identifier":
				packet = &wire.Unsubscribe{PacketID: 1, Properties: p.Properties, Filters: []string{p.Subscriptions[0].Filter}}
			case "unsubscribe-empty":
				packet = &wire.Unsubscribe{PacketID: 1}
			case "unsubscribe-id":
				packet = &wire.Unsubscribe{Filters: []string{p.Subscriptions[0].Filter}}
			case "unsubscribe-oversized":
				packet = &wire.Unsubscribe{PacketID: 1, Filters: make([]string, 129)}
			}
			require.Error(t, f.h.OnPacket(f.gateway, packet))
			require.Zero(t, calls)
			require.True(t, f.closed)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
func TestSubscriptionEntryDoesNotAcknowledgeUnconfirmedOrInvalidResults(t *testing.T) {
	for _, mode := range []string{"unknown", "unconfirmed-denial", "unconfirmed-limit", "pending", "wrong-owner", "wrong-options", "preparing", "fenced", "canceled", "panic", "write-failure", "later-filter-fails"} {
		t.Run(mode, func(t *testing.T) {
			f, port, wakes := subscriptionHandler(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.gateway.RequestContext = ctx
			calls := 0
			port.subscribe = func(_ context.Context, o contract.Owner, r app.SubscriptionRequest) (meta.MQTTSubscription, error) {
				calls++
				row := subscriptionRow(o, r, f.now)
				switch mode {
				case "unknown":
					return row, errors.New("private")
				case "unconfirmed-denial":
					return row, errors.Join(app.ErrSubscriptionDenied, app.ErrSubscriptionUnconfirmed)
				case "unconfirmed-limit":
					return row, errors.Join(app.ErrSubscriptionLimit, app.ErrSubscriptionUnconfirmed)
				case "pending":
					return row, app.ErrReplayPending
				case "wrong-owner":
					row.ClientID = "other"
				case "wrong-options":
					row.NoLocal = !row.NoLocal
				case "preparing":
					row.Stage = meta.MQTTSubscriptionPreparing
				case "fenced":
					require.NoError(t, f.owners.Fence(o))
				case "canceled":
					cancel()
				case "panic":
					panic("private")
				case "later-filter-fails":
					if calls > 1 {
						return row, errors.New("unknown")
					}
				}
				return row, nil
			}
			if mode == "write-failure" {
				f.write = func(any) error { return errors.New("private") }
			}
			p := subscribePacket()
			if mode == "later-filter-fails" {
				p.Subscriptions = append(p.Subscriptions, p.Subscriptions[0])
			}
			require.Error(t, f.h.OnPacket(f.gateway, p))
			require.True(t, f.closed)
			require.Equal(t, 1, *wakes) // Only close notification, never success.
			for _, w := range f.writes {
				_, ack := w.(*wire.Suback)
				require.False(t, ack)
			}
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
func TestSubscriptionEntryDefiniteLimitAndRevocationAreOrderedFailures(t *testing.T) {
	f, port, _ := subscriptionHandler(t)
	calls := 0
	port.subscribe = func(context.Context, contract.Owner, app.SubscriptionRequest) (meta.MQTTSubscription, error) {
		calls++
		if calls == 1 {
			return meta.MQTTSubscription{}, app.ErrSubscriptionLimit
		}
		return meta.MQTTSubscription{}, app.ErrSubscriptionRevoked
	}
	p := subscribePacket()
	p.Subscriptions = append(p.Subscriptions, p.Subscriptions[0])
	require.NoError(t, f.h.OnPacket(f.gateway, p))
	require.Equal(t, []any{&wire.Suback{PacketID: 19, Reasons: []byte{0x97, 0x87}}}, f.writes)
	require.False(t, f.closed)
}
func TestSubscriptionEntryFencedOwnerNeverCallsPort(t *testing.T) {
	f, port, _ := subscriptionHandler(t)
	port.subscribe = func(context.Context, contract.Owner, app.SubscriptionRequest) (meta.MQTTSubscription, error) {
		t.Fatal("called after fence")
		return meta.MQTTSubscription{}, nil
	}
	require.NoError(t, f.owners.Fence(f.connection.Owner))
	require.Error(t, f.h.OnPacket(f.gateway, subscribePacket()))
	require.True(t, f.closed)
}
func TestSubscriptionEntryRequiresDeliveryAndValidTimeout(t *testing.T) {
	f := newHandlerFixture(t)
	for _, timeout := range []time.Duration{0, -1, 2 * time.Minute} {
		o := access.HandlerOptions{Namespace: "main", Sessions: f.sessions, Connections: f.connections, Owners: f.owners, Publisher: f.publisher, Subscriptions: &handlerSubscriptions{}, SubscriptionTimeout: timeout}
		if timeout != 0 {
			o.Deliveries = handlerDeliveries{}
			o.Acknowledgements = outboundAcks(func(context.Context, app.AcknowledgementCommand) (app.AcknowledgementResult, error) {
				return app.AcknowledgementResult{}, nil
			})
		}
		_, err := access.NewHandler(o)
		require.ErrorIs(t, err, access.ErrHandlerInvalid)
	}
}
