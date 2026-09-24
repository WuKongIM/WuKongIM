//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

func verifyMQTTSubscriptionPackets(t *testing.T, ctx context.Context, a *App, client *paho.Client, received <-chan *paho.Publish, deliveries *runtime.Deliveries, read func() *meta.MQTTSession) {
	t.Helper()
	node := a.cluster.(*cluster.Node)
	channel := ch.ChannelID{ID: "mqtt-wire-empty", Type: 2}
	denied := ch.ChannelID{ID: "mqtt-wire-denied", Type: 2}
	seedGroupSendPermission(t, node, channel, "alice")
	seedGroupSendPermission(t, node, denied, "bob")
	_, err := node.GetChannelRuntimeMetaFresh(ctx, channel.ID, int64(channel.Type))
	require.ErrorIs(t, err, meta.ErrNotFound)
	topic, err := access.FormatTopic(access.Target{ChannelID: channel.ID, ChannelType: channel.Type})
	require.NoError(t, err)
	deniedTopic, err := access.FormatTopic(access.Target{ChannelID: denied.ID, ChannelType: denied.Type})
	require.NoError(t, err)
	identifier := 51
	reply, err := client.Subscribe(ctx, &paho.Subscribe{Properties: &paho.SubscribeProperties{SubscriptionIdentifier: &identifier}, Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 2, NoLocal: true, RetainAsPublished: true, RetainHandling: 2}, {Topic: "wk/v1/groups/not-canonical=/messages", QoS: 1}, {Topic: deniedTopic, QoS: 1}}})
	require.Error(t, err) // Paho reports mixed failures while returning the full SUBACK.
	require.NotNil(t, reply)
	require.Equal(t, []byte{1, 0x8f, 0x87}, reply.Reasons)
	state := read()
	sub, err := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: state.Namespace, ClientID: state.ClientID, SessionGeneration: state.Generation, Topic: topic})
	require.NoError(t, err)
	require.Len(t, sub.Subscriptions, 1)
	row := sub.Subscriptions[0]
	require.Equal(t, meta.MQTTSubscriptionActive, row.Stage)
	require.True(t, row.NoLocal)
	require.True(t, row.RetainAsPublished)
	require.EqualValues(t, 2, row.RetainHandling)
	require.EqualValues(t, 51, row.SubscriptionIdentifier)
	_, err = node.GetChannelRuntimeMetaFresh(ctx, denied.ID, int64(denied.Type))
	require.ErrorIs(t, err, meta.ErrNotFound)
	_, err = a.Messages().Send(ctx, message.SendCommand{FromUID: "alice", DeviceFlag: 1, ChannelID: channel.ID, ChannelType: channel.Type, ClientMsgNo: "wire-subscribed", Payload: []byte("wire-subscribed"), Origin: message.SendOriginClient})
	require.NoError(t, err)
	receiveCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	publication := awaitMQTTGatewayPublication(t, receiveCtx, received)
	require.Equal(t, topic, publication.Topic)
	require.Equal(t, "wire-subscribed", string(publication.Payload))
	require.EqualValues(t, 51, *publication.Properties.SubscriptionIdentifier)
	require.EqualValues(t, 1, read().OutboundInflight)
	// Real removal leaves the already begun exchange bound to this connection.
	removed, err := client.Unsubscribe(ctx, &paho.Unsubscribe{Topics: []string{topic, deniedTopic}})
	require.NoError(t, err)
	require.Equal(t, []byte{0, 0x11}, removed.Reasons)
	sub, err = node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: state.Namespace, ClientID: state.ClientID, SessionGeneration: state.Generation, Topic: topic})
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSubscriptionRemoved, sub.Subscriptions[0].Stage)
	require.EqualValues(t, 1, read().OutboundInflight)
	require.NoError(t, client.Ack(publication))
	require.Eventually(t, func() bool {
		r := read()
		return r.PendingMessages == 0 && r.PendingBytes == 0 && r.OutboundInflight == 0
	}, 3*time.Second, time.Millisecond)
	_, err = a.Messages().Send(ctx, message.SendCommand{FromUID: "alice", DeviceFlag: 1, ChannelID: channel.ID, ChannelType: channel.Type, ClientMsgNo: "after-unsubscribe", Payload: []byte("after-unsubscribe"), Origin: message.SendOriginClient})
	require.NoError(t, err)
	turns := deliveries.Snapshot().Turns
	require.Eventually(t, func() bool { return deliveries.Snapshot().Turns >= turns+3 }, 3*time.Second, time.Millisecond)
	require.Empty(t, received)
	t.Log("mqtt_subscription_entry_evidence: client=Paho transport=gnet/TCP hash_slots=256 subscribe=wire unsubscribe=wire empty_group=true mixed_suback_reasons=true granted_qos=1 identifier=51 options_persisted=true denied_no_runtime=true native_delivery=true puback_after_unsubscribe=true no_delivery_after_unsubscribe=true source_protection=real replay=real product_listener=false")
}
