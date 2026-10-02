//go:build e2e

package interop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedMQTTAndWKInterop(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) {
			var options []suite.Option
			mqttAddrs := make([]string, count)
			for i := range count {
				mqttAddrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
				options = append(options, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{
					"WK_CLUSTER_HASH_SLOT_COUNT": "256",
					"WK_GATEWAY_TOKEN_AUTH_ON":   "true",
					"WK_MQTT_ENABLE":             "true",
					"WK_MQTT_LISTEN_ADDR":        mqttAddrs[i],
				}))
			}
			s := suite.New(t)
			var first, last *suite.StartedNode
			if count == 1 {
				first = s.StartSingleNodeCluster(options...)
				last = first
			} else {
				cluster := s.StartThreeNodeCluster(append(options, suite.WithManagerHTTP())...)
				ready, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				require.NoError(t, cluster.WaitClusterReady(ready), cluster.DumpDiagnostics())
				_, err := cluster.WaitSlotLeadersStable(ready, time.Second)
				cancel()
				require.NoError(t, err, cluster.DumpDiagnostics())
				first, last = cluster.MustNode(1), cluster.MustNode(3)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for _, uid := range []string{"alice", "bob"} {
				_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{
					"uid": uid, "token": uid + "-fixture-token", "device_flag": 1, "device_level": 1,
				}, nil)
				require.NoError(t, err, "register fixture credential")
			}
			wk, err := suite.NewWKProtoClient()
			require.NoError(t, err)
			t.Cleanup(func() { _ = wk.Close() })
			_, err = wk.ConnectAuthenticatedContext(ctx, first.GatewayAddr(), "alice", "alice-wk", "alice-fixture-token", frame.WEB)
			require.NoError(t, err, first.DumpDiagnostics())
			denied, authErr := suite.ConnectMQTT(ctx, mqttAddrs[count-1], "bob", "wrong-fixture-token", "rejected-client", true, 0)
			if denied != nil {
				_ = denied.Close()
			}
			require.Error(t, authErr, "invalid token authenticated")
			bob, err := suite.ConnectMQTT(ctx, mqttAddrs[count-1], "bob", "bob-fixture-token", "bob-mqtt", true, 0)
			require.NoError(t, err, last.DumpDiagnostics())
			t.Cleanup(func() { _ = bob.Close() })
			require.NotNil(t, bob.Connack.Properties)
			require.False(t, bob.Connack.SessionPresent)
			require.NotNil(t, bob.Connack.Properties.MaximumQoS)
			require.Equal(t, byte(1), *bob.Connack.Properties.MaximumQoS)
			require.False(t, bob.Connack.Properties.RetainAvailable)
			require.False(t, bob.Connack.Properties.WildcardSubAvailable)
			require.False(t, bob.Connack.Properties.SharedSubAvailable)
			sub, err := bob.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
			require.NoError(t, err)
			require.Equal(t, []byte{1}, sub.Reasons)
			require.NoError(t, wk.SendFrame(&frame.SendPacket{ChannelID: "bob", ChannelType: frame.ChannelTypePerson, ClientSeq: 1, ClientMsgNo: "wk-to-mqtt", Payload: []byte("wk bytes")}))
			ack, err := wk.ReadSendAck()
			require.NoError(t, err)
			require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
			received, err := bob.Receive(ctx)
			require.NoError(t, err)
			require.Equal(t, []byte("wk bytes"), received.Payload)
			require.Equal(t, strconv.FormatInt(ack.MessageID, 10), received.Properties.User.Get("wk.message_id"))
			require.Equal(t, strconv.FormatUint(ack.MessageSeq, 10), received.Properties.User.Get("wk.message_seq"))
			published, err := bob.Client.Publish(ctx, &paho.Publish{Topic: "wk/v1/users/YWxpY2U/messages", QoS: 1, Payload: []byte("mqtt bytes"), Properties: &paho.PublishProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: "mqtt-to-wk"}}}})
			require.NoError(t, err)
			require.Less(t, published.ReasonCode, byte(0x80))
			recv, err := wk.ReadRecv()
			require.NoError(t, err)
			require.Equal(t, []byte("mqtt bytes"), recv.Payload)
			require.Equal(t, "bob", recv.FromUID)
			require.NoError(t, wk.RecvAck(recv.MessageID, recv.MessageSeq))
			var coexisting []*suite.MQTTClient
			for _, id := range []string{"alice-mqtt-1", "alice-mqtt-2"} {
				client, err := suite.ConnectMQTT(ctx, mqttAddrs[count-1], "alice", "alice-fixture-token", id, true, 0)
				require.NoError(t, err, "same UID/device credential must not invoke WK master conflict policy")
				t.Cleanup(func() { _ = client.Close() })
				coexisting = append(coexisting, client)
			}
			for _, client := range coexisting {
				_, err := client.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/YWxpY2U/messages", QoS: 1}}})
				require.NoError(t, err, "later ClientID displaced an earlier connection")
			}
			require.NoError(t, wk.SendFrame(&frame.PingPacket{}), "MQTT login displaced WK connection")
			reportDir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
			if reportDir == "" {
				reportDir = first.Spec.RootDir
			}
			require.NoError(t, os.MkdirAll(reportDir, 0o755))
			report, err := json.MarshalIndent(map[string]any{"scenario": "mqtt-wk-interop", "nodes": count, "hash_slots": 256, "passed": true, "wk_message_id": strconv.FormatInt(ack.MessageID, 10), "mqtt_message_id": strconv.FormatInt(recv.MessageID, 10)}, "", "  ")
			require.NoError(t, err)
			path := filepath.Join(reportDir, fmt.Sprintf("mqtt-interop-%d.json", count))
			require.NoError(t, os.WriteFile(path, append(report, '\n'), 0o600))
			t.Logf("result artifact: %s", path)
		})
	}
}
