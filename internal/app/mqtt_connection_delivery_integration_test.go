//go:build integration

package app

import (
	"context"
	"net"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/core"
	transport "github.com/WuKongIM/WuKongIM/pkg/gateway/transport/gnet"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Subscription preparation uses the real usecase, invoked by this fixture until
// shared protection is confirmed. Subsequent delivery has no manually supplied
// cursor, exchange, content, sink registration, enqueue or ACK invocation.
func TestMQTTConnectionDeliveryPahoSingleNodeCluster(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	channel := ch.ChannelID{ID: "mqtt-scheduled", Type: 2}
	waitSingleNodeClusterRouteLeader(t, node, channel.ID, cfg.NodeID)
	seedGroupSendPermission(t, node, channel, "alice")
	// Create the Channel runtime through ordinary IM send before preparing its
	// replay source. This earlier message must be excluded by StartAfter.
	_, err = a.Messages().Send(ctx, message.SendCommand{FromUID: "alice", DeviceFlag: 1, ChannelID: channel.ID, ChannelType: channel.Type, ClientMsgNo: "before-subscription", Payload: []byte("before"), Origin: message.SendOriginClient})
	require.NoError(t, err)
	require.NoError(t, node.UpsertDeviceMetadata(ctx, meta.Device{UID: "alice", DeviceFlag: 1, Token: "secret", DeviceLevel: 1}))
	owners, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: cfg.NodeID, BootID: "paho-scheduled", Capacity: 16, MaxOperations: 8, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, owners.Close(ctx))
	})
	sessions, err := sessioncase.New(sessioncase.Options{Store: node, Owners: owners, Isolation: owners, Tokens: user.New(user.Options{DeviceReader: mqttAcquisitionDeviceReader{node: node}}), Wills: mqttWillAuthorizer{messages: a.Messages()}, LeaseDuration: 30 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: 86400, QuotaMessages: 100, QuotaBytes: 1 << 20, WindowLimit: 16})
	require.NoError(t, err)
	authorization, err := newMQTTReceiveAuthorization(node)
	require.NoError(t, err)
	projection, err := newMQTTGroupProjection(node, owners, authorization, a.messageIDs)
	require.NoError(t, err)
	subscriptions, err := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: node, Owners: owners, Authorization: authorization, Projection: projection})
	require.NoError(t, err)
	replay, err := newMQTTReplayWorker(node, a.messageIDs, runtime.ReplayWorkerOptions{HashSlotCount: 256, Interval: 20 * time.Millisecond, PagesPerTurn: 32})
	require.NoError(t, err)
	require.NoError(t, replay.Start(ctx))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, replay.Stop(ctx))
	})
	key := contract.Key{Namespace: "main", ClientID: "scheduled"}
	prepared, err := sessions.Connect(ctx, sessioncase.ConnectCommand{Key: key, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 1, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }})
	require.NoError(t, err)
	topic, err := access.FormatTopic(access.Target{ChannelID: channel.ID, ChannelType: channel.Type})
	require.NoError(t, err)
	request := sessioncase.SubscriptionRequest{Topic: topic, TargetKind: meta.MQTTSubscriptionGroup, TargetID: channel.ID, RequestedQoS: 1, SubscriptionIdentifier: 41}
	var active meta.MQTTSubscription
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		active, err = subscriptions.Subscribe(ctx, prepared.Owner, request)
		require.NoError(c, err)
	}, 10*time.Second, 20*time.Millisecond)
	require.Equal(t, meta.MQTTSubscriptionActive, active.Stage)
	var last message.SendResult
	for _, body := range []string{"one", "two"} {
		last, err = a.Messages().Send(ctx, message.SendCommand{FromUID: "alice", DeviceFlag: 1, ChannelID: channel.ID, ChannelType: channel.Type, ClientMsgNo: "scheduled-" + body, Payload: []byte(body), Origin: message.SendOriginClient})
		require.NoError(t, err)
	}
	cursors, err := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: prepared.Owner.SessionGeneration, SubscriptionGeneration: active.Generation, Limit: 2})
	require.NoError(t, err)
	require.Len(t, cursors.DeliveryCursors, 1)
	source := meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "2:" + channel.ID, Generation: cursors.DeliveryCursors[0].Key.SourceGeneration}
	confirmation, err := newMQTTReplayCoordinator(node, a.messageIDs)
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) { require.NoError(c, confirmation.Confirm(ctx, source, last.MessageSeq)) }, 10*time.Second, 20*time.Millisecond)
	require.NoError(t, sessions.Disconnect(ctx, sessioncase.DisconnectCommand{Owner: prepared.Owner, Normal: true}))

	connections, err := runtime.NewConnections(runtime.ConnectionOptions{Owners: owners, Control: mqttConnectionControl{sessions: sessions}, Workers: 2, CallTimeout: time.Second, Retry: 20 * time.Millisecond})
	require.NoError(t, err)
	require.NoError(t, connections.Start(ctx))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, connections.Stop(ctx))
	})
	coordinator, err := newMQTTDeliveryCoordinator(node, owners, authorization, sessions, 128)
	require.NoError(t, err)
	// A one-minute idle poll makes sub-three-second ACK/close progress evidence
	// for entry wakes, rather than accidental periodic polling.
	deliveries, err := runtime.NewDeliveries(runtime.DeliveryOptions{Owners: owners, Workers: 2, IdleInterval: time.Minute, Retry: 20 * time.Millisecond})
	require.NoError(t, err)
	require.NoError(t, deliveries.Start(ctx))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		owners.StopAdmission()
		require.NoError(t, deliveries.Stop(ctx))
	})
	acks, err := newMQTTAcknowledgements(node, owners)
	require.NoError(t, err)
	publisher, err := access.NewPublisher(access.PublisherOptions{Owners: owners, Messages: a.Messages()})
	require.NoError(t, err)
	handler, err := access.NewHandler(access.HandlerOptions{Namespace: key.Namespace, Sessions: sessions, Connections: connections, Owners: owners, Publisher: publisher, Acknowledgements: acks, Deliveries: mqttConnectionDeliveries{coordinator: coordinator, scheduler: deliveries}})
	require.NoError(t, err)
	registry := core.NewRegistry()
	require.NoError(t, registry.RegisterTransport(transport.NewFactory()))
	require.NoError(t, registry.RegisterPacketProtocol(newMQTTProtocol(wire.Limits{})))
	server, err := core.NewServer(registry, &gt.Options{PacketHandler: handler, Listeners: []gt.ListenerOptions{{Name: "mqtt", Network: "tcp", Transport: "gnet", Protocol: "mqtt", Address: "127.0.0.1:0"}}})
	require.NoError(t, err)
	require.NoError(t, server.Start())
	t.Cleanup(func() { require.NoError(t, server.Stop()) })
	type peer struct {
		client   *paho.Client
		received chan *paho.Publish
	}
	dial := func() peer {
		conn, e := (&net.Dialer{}).DialContext(ctx, "tcp", server.ListenerAddr("mqtt"))
		require.NoError(t, e)
		received := make(chan *paho.Publish, 8)
		client := paho.NewClient(paho.ClientConfig{Conn: conn, EnableManualAcknowledgment: true, SendAcksInterval: time.Millisecond, OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(p paho.PublishReceived) (bool, error) {
			select {
			case received <- p.Packet:
			case <-ctx.Done():
			}
			return true, nil
		}}})
		t.Cleanup(func() {
			_ = conn.Close()
			select {
			case <-client.Done():
			case <-time.After(3 * time.Second):
				t.Error("Paho failed to stop")
			}
		})
		expiry, maximum := uint32(60), uint16(1)
		ack, e := client.Connect(ctx, &paho.Connect{ClientID: key.ClientID, Username: "alice", Password: []byte("secret"), UsernameFlag: true, PasswordFlag: true, Properties: &paho.ConnectProperties{SessionExpiryInterval: &expiry, ReceiveMaximum: &maximum, User: paho.UserProperties{{Key: "wk.device_flag", Value: "1"}}}})
		require.NoError(t, e)
		require.True(t, ack.SessionPresent)
		return peer{client: client, received: received}
	}
	read := func() *meta.MQTTSession {
		r, e := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: key.Namespace, ClientID: key.ClientID})
		require.NoError(t, e)
		require.NotNil(t, r.Session)
		return r.Session
	}
	await := func(p peer) *paho.Publish {
		deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return awaitMQTTGatewayPublication(t, deadline, p.received)
	}
	first := dial()
	original := await(first)
	require.Equal(t, "one", string(original.Payload))
	require.False(t, original.Duplicate())
	require.EqualValues(t, 41, *original.Properties.SubscriptionIdentifier)
	require.Eventually(t, func() bool { s := deliveries.Snapshot(); return s.Turns >= 2 && s.InProgress == 0 }, 3*time.Second, time.Millisecond)
	require.EqualValues(t, 2, read().PendingMessages)
	require.EqualValues(t, 1, read().OutboundInflight)
	require.Empty(t, first.received)
	second := dial()
	select {
	case <-first.client.Done():
	case <-ctx.Done():
		t.Fatal("takeover failed to close first peer")
	}
	resumed := await(second)
	require.True(t, resumed.Duplicate())
	require.Equal(t, original.PacketID, resumed.PacketID)
	require.Equal(t, original.Payload, resumed.Payload)
	require.Eventually(t, func() bool { s := deliveries.Snapshot(); return s.Completed >= 1 && s.InProgress == 0 }, 3*time.Second, time.Millisecond)
	require.NoError(t, second.client.Ack(resumed))
	next := await(second)
	require.Equal(t, "two", string(next.Payload))
	require.False(t, next.Duplicate())
	require.NoError(t, second.client.Ack(next))
	require.Eventually(t, func() bool {
		r := read()
		return r.PendingMessages == 0 && r.PendingBytes == 0 && r.OutboundInflight == 0
	}, 3*time.Second, time.Millisecond)
	require.NoError(t, second.client.Disconnect(&paho.Disconnect{}))
	require.Eventually(t, func() bool {
		return read().State == meta.MQTTSessionOffline && deliveries.Snapshot().Tracked == 0 && connections.Snapshot().Tracked == 0 && owners.Snapshot().Held == 0
	}, 3*time.Second, time.Millisecond)
	t.Log("mqtt_connection_delivery_evidence: client=Paho transport=gnet/TCP hash_slots=256 subscription_setup=real_usecase_direct source_protection=real replay=real source_discovery=automatic accounting=real delivery_registration=entry sender=real gateway_sink=real receive_maximum=1 takeover_same_packet_dup=true ack_wake=true close_wake=true idle_poll=1m pending_zero=true cleanup_joined=true product_listener=false")
}
