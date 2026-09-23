//go:build integration

package app

import (
	"context"
	"net"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/core"
	adapter "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/mqtt"
	transport "github.com/WuKongIM/WuKongIM/pkg/gateway/transport/gnet"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

// A real gateway and independent MQTT client exercise this internal composition.
// Product configuration and process-level acceptance remain separate required gates.
func TestMQTTGatewayPahoSingleNodeCluster(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	channel := ch.ChannelID{ID: "mqtt-gateway", Type: 2}
	waitSingleNodeClusterRouteLeader(t, node, channel.ID, cfg.NodeID)
	seedGroupSendPermission(t, node, channel, "alice")
	require.NoError(t, node.UpsertDeviceMetadata(ctx, meta.Device{UID: "alice", DeviceFlag: 1, Token: "secret", DeviceLevel: 1}))
	owners, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: cfg.NodeID, BootID: "paho-gateway", Capacity: 16, MaxOperations: 4, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, owners.Close(ctx))
	})
	sessions, err := sessioncase.New(sessioncase.Options{Store: node, Owners: owners, Isolation: owners, Tokens: user.New(user.Options{DeviceReader: mqttAcquisitionDeviceReader{node: node}}), Wills: mqttWillAuthorizer{messages: a.Messages()}, LeaseDuration: 10 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: 86400, QuotaMessages: 100, QuotaBytes: 1 << 20, WindowLimit: 16})
	require.NoError(t, err)
	connections, err := runtime.NewConnections(runtime.ConnectionOptions{Owners: owners, Control: mqttConnectionControl{sessions: sessions}, Workers: 2, CallTimeout: time.Second, Retry: 20 * time.Millisecond})
	require.NoError(t, err)
	require.NoError(t, connections.Start(ctx))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, connections.Stop(ctx))
	})
	publisher, err := access.NewPublisher(access.PublisherOptions{Owners: owners, Messages: a.Messages()})
	require.NoError(t, err)
	handler, err := access.NewHandler(access.HandlerOptions{Namespace: "main", Sessions: sessions, Connections: connections, Owners: owners, Publisher: publisher})
	require.NoError(t, err)
	registry := core.NewRegistry()
	require.NoError(t, registry.RegisterTransport(transport.NewFactory()))
	require.NoError(t, registry.RegisterPacketProtocol(adapter.New(wire.Limits{})))
	server, err := core.NewServer(registry, &gt.Options{PacketHandler: handler, Listeners: []gt.ListenerOptions{{Name: "mqtt", Network: "tcp", Transport: "gnet", Protocol: "mqtt", Address: "127.0.0.1:0"}}})
	require.NoError(t, err)
	require.NoError(t, server.Start())
	t.Cleanup(func() { require.NoError(t, server.Stop()) })
	topic, err := access.FormatTopic(access.Target{ChannelID: channel.ID, ChannelType: 2})
	require.NoError(t, err)
	dial := func(id, token string, will bool, max uint32) (*paho.Client, net.Conn, *paho.Connack, error) {
		conn, e := (&net.Dialer{}).DialContext(ctx, "tcp", server.ListenerAddr("mqtt"))
		require.NoError(t, e)
		client := paho.NewClient(paho.ClientConfig{Conn: conn})
		t.Cleanup(func() {
			_ = conn.Close()
			select {
			case <-client.Done():
			case <-time.After(3 * time.Second):
				t.Error("Paho failed to stop")
			}
		})
		expiry := uint32(60)
		packet := &paho.Connect{ClientID: id, Username: "alice", Password: []byte(token), UsernameFlag: true, PasswordFlag: true, Properties: &paho.ConnectProperties{SessionExpiryInterval: &expiry, User: paho.UserProperties{{Key: "wk.device_flag", Value: "1"}}}}
		if max > 0 {
			packet.Properties.MaximumPacketSize = &max
		}
		if will {
			packet.WillMessage = &paho.WillMessage{Topic: topic, QoS: 1, Payload: []byte("gone")}
			delay := uint32(30)
			packet.WillProperties = &paho.WillProperties{WillDelayInterval: &delay, User: paho.UserProperties{{Key: "wk.client_msg_no", Value: "will"}}}
		}
		ack, e := client.Connect(ctx, packet)
		return client, conn, ack, e
	}
	read := func(id string) *meta.MQTTSession {
		r, e := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: "main", ClientID: id})
		require.NoError(t, e)
		require.NotNil(t, r.Session)
		return r.Session
	}
	offline := func(id string) {
		require.Eventually(t, func() bool {
			r, e := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: "main", ClientID: id})
			return e == nil && r.Session != nil && r.Session.State == meta.MQTTSessionOffline
		}, 3*time.Second, 10*time.Millisecond)
	}
	first, _, ack, err := dial("paho", "secret", false, 0)
	require.NoError(t, err)
	require.False(t, ack.SessionPresent)
	require.Equal(t, byte(1), *ack.Properties.MaximumQoS)
	require.False(t, ack.Properties.RetainAvailable)
	require.False(t, ack.Properties.WildcardSubAvailable)
	require.False(t, ack.Properties.SharedSubAvailable)
	_, _, denied, err := dial("paho", "wrong", false, 0)
	require.Error(t, err)
	require.NotNil(t, denied)
	require.Equal(t, byte(0x87), denied.ReasonCode)
	receipt, err := first.Publish(ctx, &paho.Publish{Topic: topic, QoS: 1, Payload: []byte("committed"), Properties: &paho.PublishProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: "first"}}}})
	require.NoError(t, err)
	require.Zero(t, receipt.ReasonCode)
	rows, err := node.ReadChannelCommitted(ctx, channel, store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Len(t, rows.Messages, 1)
	require.Equal(t, "alice", rows.Messages[0].FromUID)
	second, _, ack, err := dial("paho", "secret", false, 0)
	require.NoError(t, err)
	require.True(t, ack.SessionPresent)
	select {
	case <-first.Done():
	case <-ctx.Done():
		t.Fatal("takeover did not physically close previous client")
	}
	require.NoError(t, second.Disconnect(&paho.Disconnect{}))
	offline("paho")
	resumed, _, ack, err := dial("paho", "secret", false, 0)
	require.NoError(t, err)
	require.True(t, ack.SessionPresent)
	require.NoError(t, resumed.Disconnect(&paho.Disconnect{}))
	offline("paho")
	for _, normal := range []bool{true, false} {
		id := "will-abnormal"
		if normal {
			id = "will-normal"
		}
		client, conn, _, e := dial(id, "secret", true, 0)
		require.NoError(t, e)
		row := read(id)
		if normal {
			require.NoError(t, client.Disconnect(&paho.Disconnect{}))
		} else {
			require.NoError(t, conn.Close())
		}
		offline(id)
		r, e := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadWill, WillKey: meta.MQTTWillKey{Namespace: "main", ClientID: id, SessionGeneration: row.Generation, WillGeneration: row.WillGeneration}})
		require.NoError(t, e)
		require.Len(t, r.Wills, 1)
		if normal {
			require.Equal(t, meta.MQTTWillCancelled, r.Wills[0].Stage)
		} else {
			require.Equal(t, meta.MQTTWillWaiting, r.Wills[0].Stage)
		}
	}
	_, _, _, err = dial("small-connack", "secret", false, 5)
	require.Error(t, err)
	offline("small-connack")
	require.Eventually(t, func() bool { return connections.Snapshot().Tracked == 0 && owners.Snapshot().Held == 0 }, 3*time.Second, 10*time.Millisecond)
	t.Log("mqtt_gateway_evidence: client=Paho transport=gnet/TCP hash_slots=256 auth=true invalid_token_no_eviction=true committed_ack=true takeover=true resume=true will_decisions=true connack_rollback=true cleanup_joined=true")
}
