//go:build integration

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

// This exercises real authentication, Session authority, message usecases and
// committed Channel storage. The transport writer is controlled; full product
// listener/process-level acceptance remains a separate gate.
func TestMQTTPublishSingleNodeClusterCommitRetryAndRevocation(t *testing.T) {
	runMQTTPublishCommitRetry(t, []byte("durable"), false)
}

func TestMQTTPublishEmptyBodySingleNodeCluster(t *testing.T) {
	runMQTTPublishCommitRetry(t, nil, false)
}

func TestMQTTPublishEmptyWebhookBodySingleNodeCluster(t *testing.T) {
	runMQTTPublishCommitRetry(t, nil, true)
}

func TestMQTTPublishEmptyWebhookReplacementSingleNodeCluster(t *testing.T) {
	runMQTTPublishCommitRetry(t, []byte("durable"), true)
}

func runMQTTPublishCommitRetry(t *testing.T, payload []byte, emptyWebhook bool) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	if emptyWebhook {
		hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"allow": true, "payload": []byte{}})
		}))
		t.Cleanup(hook.Close)
		cfg.Webhook.BeforeSend = BeforeSendWebhookConfig{Enabled: true, HTTPAddr: hook.URL, Timeout: time.Second}
	}
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	id := ch.ChannelID{ID: "mqtt-publish-entry", Type: 2}
	waitSingleNodeClusterRouteLeader(t, node, id.ID, cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	seedGroupSendPermission(t, node, id, "alice")
	now := time.Now()
	owners, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: cfg.NodeID, BootID: "publish-integration", Capacity: 2, MaxOperations: 2, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Second, Now: func() time.Time { return now }})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owners.Close(context.Background())) })
	require.NoError(t, node.UpsertDeviceMetadata(ctx, meta.Device{UID: "alice", DeviceFlag: 1, Token: "secret", DeviceLevel: 1}))
	sessions, err := sessioncase.New(sessioncase.Options{Store: node, Owners: owners, Isolation: owners, Tokens: user.New(user.Options{DeviceReader: mqttAcquisitionDeviceReader{node: node}}), Wills: mqttWillAuthorizer{messages: a.Messages()}, Now: func() time.Time { return now }, LeaseDuration: 30 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: 86400, QuotaMessages: 100, QuotaBytes: 1 << 20, WindowLimit: 16})
	require.NoError(t, err)
	connection, err := sessions.Connect(ctx, sessioncase.ConnectCommand{Key: contract.Key{Namespace: "main", ClientID: "SYSTEM"}, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 3600, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }})
	require.NoError(t, err)
	publisher, err := access.NewPublisher(access.PublisherOptions{Owners: owners, Messages: a.Messages(), Now: func() time.Time { return now }})
	require.NoError(t, err)
	var writes []any
	gateway := gt.Context{RequestContext: ctx, Session: session.New(session.Config{ID: 91, WritePacketFn: func(p any, _ session.OutboundMeta) error { writes = append(writes, p); return nil }}), CloseSessionFn: func(gt.CloseReason, error) { t.Error("unexpected close") }}
	topic, err := access.FormatTopic(access.Target{ChannelID: id.ID, ChannelType: id.Type})
	require.NoError(t, err)
	packet := &wire.Publish{Topic: topic, QoS: 1, PacketID: 10, Payload: payload, Properties: []wire.Property{{ID: wire.UserProperty, Text: "wk.client_msg_no", Value: "mqtt-first"}}}
	require.NoError(t, publisher.Publish(gateway, connection, packet))
	require.Equal(t, []any{&wire.Puback{PacketID: 10}}, writes)
	query := store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 1 << 20}
	first, err := node.ReadChannelCommitted(ctx, id, query)
	require.NoError(t, err)
	require.Len(t, first.Messages, 1)
	expectedPayload := payload
	if emptyWebhook {
		expectedPayload = nil
	}
	require.Equal(t, string(expectedPayload), string(first.Messages[0].Payload))
	require.Equal(t, "alice", first.Messages[0].FromUID)
	require.Equal(t, "mqtt-first", first.Messages[0].ClientMsgNo)
	original, err := publication.Decode(first.Messages[0].PublicationMetadata)
	require.NoError(t, err)
	require.Equal(t, publication.SourceMQTT, original.Source)
	require.Equal(t, byte(1), original.QoS)
	require.Equal(t, "SYSTEM", original.PublisherClientID)
	require.Equal(t, now.UnixMilli(), original.AcceptedAtMS)
	now = now.Add(time.Second)
	packet.PacketID, packet.Dup = 11, true
	require.NoError(t, publisher.Publish(gateway, connection, packet))
	again, err := node.ReadChannelCommitted(ctx, id, query)
	require.NoError(t, err)
	require.Equal(t, first.Messages, again.Messages)
	require.Equal(t, &wire.Puback{PacketID: 11}, writes[len(writes)-1])
	// Reuse a completed PID for a distinct application publication.
	packet.PacketID, packet.Dup = 10, false
	packet.Properties[0].Value = "mqtt-second"
	require.NoError(t, publisher.Publish(gateway, connection, packet))
	// QoS 0 still commits a normal IM message without a reply.
	packet.QoS, packet.PacketID = 0, 0
	packet.Properties[0].Value = "mqtt-zero"
	before := len(writes)
	require.NoError(t, publisher.Publish(gateway, connection, packet))
	require.Len(t, writes, before)
	rows, err := node.ReadChannelCommitted(ctx, id, query)
	require.NoError(t, err)
	require.Len(t, rows.Messages, 3)
	for _, row := range rows.Messages {
		require.Equal(t, string(expectedPayload), string(row.Payload))
	}
	require.Equal(t, uint64(3), rows.Messages[2].MessageSeq)
	zero, err := publication.Decode(rows.Messages[2].PublicationMetadata)
	require.NoError(t, err)
	require.Zero(t, zero.QoS)
	require.NoError(t, node.RemoveChannelSubscribers(ctx, id.ID, int64(id.Type), []string{"alice"}, 2))
	packet.QoS, packet.PacketID = 1, 12
	packet.Properties[0].Value = "denied-after-revoke"
	require.NoError(t, publisher.Publish(gateway, connection, packet))
	require.Equal(t, &wire.Puback{PacketID: 12, Reason: 0x87}, writes[len(writes)-1])
	rows, err = node.ReadChannelCommitted(ctx, id, query)
	require.NoError(t, err)
	require.Len(t, rows.Messages, 3)
	require.Zero(t, owners.Snapshot().Operations)
	t.Logf("mqtt_publish_evidence: hash_slots=256 authenticated=true commit_before_ack=true qos0_persisted=true cross_pid_retry_deduplicated=true reused_pid_new_message=true membership_revocation_denied=true empty_payload=%t real_webhook=%t", len(expectedPayload) == 0, emptyWebhook)
}
