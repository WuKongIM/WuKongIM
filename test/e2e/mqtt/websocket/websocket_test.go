//go:build e2e

package websocket

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/packets"
	ws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestMQTTWebSocketProductAccess(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) {
			s := suite.New(t)
			opts := []suite.Option{suite.WithMQTTWebSocketGateway(), suite.WithWebSocketGateway()}
			if dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR"); dir != "" {
				opts = append(opts, suite.WithWorkspaceRootDir(filepath.Join(dir, "workspaces")))
			}
			for i := range count {
				opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{
					"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true",
					"WK_MQTT_LISTEN_ADDR": suite.ReserveLoopbackPorts(t).GatewayAddr,
				}))
			}
			var first, last *suite.StartedNode
			var nodes []*suite.StartedNode
			if count == 1 {
				first = s.StartSingleNodeCluster(opts...)
				last = first
				nodes = []*suite.StartedNode{first}
			} else {
				cluster := s.StartThreeNodeCluster(append(opts, suite.WithManagerHTTP())...)
				ready, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				require.NoError(t, cluster.WaitClusterReady(ready), cluster.DumpDiagnostics())
				_, err := cluster.WaitSlotLeadersStable(ready, time.Second)
				cancel()
				require.NoError(t, err, cluster.DumpDiagnostics())
				first, last = cluster.MustNode(1), cluster.MustNode(3)
				nodes = []*suite.StartedNode{first, cluster.MustNode(2), last}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			report := map[string]any{"scenario": "mqtt-websocket", "nodes": count, "hash_slots": 256, "subprotocol": "mqtt"}
			binary, err := os.ReadFile(first.Process.BinaryPath)
			require.NoError(t, err)
			digest := sha256.Sum256(binary)
			report["binary_sha256"] = fmt.Sprintf("%x", digest)
			t.Cleanup(func() {
				report["passed"] = !t.Failed()
				dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
				if dir == "" {
					dir = first.Spec.RootDir
				}
				require.NoError(t, os.MkdirAll(dir, 0755))
				body, err := json.MarshalIndent(report, "", "  ")
				require.NoError(t, err)
				path := filepath.Join(dir, fmt.Sprintf("mqtt-websocket-%d.json", count))
				require.NoError(t, os.WriteFile(path, append(body, '\n'), 0600))
				t.Logf("result artifact: %s", path)
			})
			for _, uid := range []string{"alice", "bob"} {
				_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-fixture-token", "device_flag": 1, "device_level": 1}, nil)
				require.NoError(t, err, "fixture credential registration")
			}
			url := first.MQTTWebSocketURL()
			for _, case_ := range []struct {
				name, path string
				protocols  []string
				status     int
			}{
				{"wrong-path", "/other", []string{"mqtt"}, http.StatusNotFound},
				{"missing-subprotocol", "/mqtt", nil, http.StatusBadRequest},
				{"wrong-subprotocol", "/mqtt", []string{"mqttv3.1"}, http.StatusBadRequest},
				{"case-sensitive-subprotocol", "/mqtt", []string{"MQTT"}, http.StatusBadRequest},
			} {
				t.Run(case_.name, func(t *testing.T) {
					endpoint := "ws://" + first.Spec.MQTTWebSocketAddr + case_.path
					c, response, err := suite.DialMQTTWebSocket(ctx, endpoint, case_.protocols...)
					if c != nil {
						_ = c.Conn.Close()
					}
					if response != nil && response.Body != nil {
						defer response.Body.Close()
					}
					require.Error(t, err, "invalid MQTT WebSocket handshake accepted")
					require.NotNil(t, response)
					require.Equal(t, case_.status, response.StatusCode)
				})
			}
			report["handshake_rejections"] = 4
			alice, response, err := suite.DialMQTTWebSocket(ctx, url, "other", "mqtt")
			require.NoError(t, err, "MQTT WebSocket dial")
			defer alice.Conn.Close()
			require.Equal(t, "mqtt", alice.Conn.Subprotocol(), "MQTT subprotocol not selected")
			require.Equal(t, "mqtt", response.Header.Get("Sec-WebSocket-Protocol"))
			bob, _, err := suite.DialMQTTWebSocket(ctx, last.MQTTWebSocketURL(), "mqtt")
			require.NoError(t, err)
			defer bob.Conn.Close()
			// CONNECT spans independent binary WebSocket messages.
			connect := connectPacket("alice", "alice-fixture-token", "alice-ws", true, 0)
			body, err := suite.EncodeMQTTPackets(connect)
			require.NoError(t, err)
			for _, part := range [][]byte{body[:2], body[2:9], body[9:]} {
				require.NoError(t, alice.Conn.WriteMessage(ws.BinaryMessage, part))
			}
			assertConnack(t, ctx, alice, false)
			// 32-byte write buffering forces WebSocket continuation frames.
			require.NoError(t, bob.WriteFragmented(ctx, connectPacket("bob", "bob-fixture-token", "bob-ws", true, 0)))
			assertConnack(t, ctx, bob, false)
			for _, c := range []struct {
				client *suite.MQTTWebSocket
				topic  string
			}{{alice, "wk/v1/users/YWxpY2U/messages"}, {bob, "wk/v1/users/Ym9i/messages"}} {
				require.NoError(t, c.client.WritePackets(ctx, &packets.Subscribe{PacketID: 1, Properties: &packets.Properties{}, Subscriptions: []packets.SubOptions{{Topic: c.topic, QoS: 1}}}))
				response, err := c.client.ReadPacket(ctx)
				require.NoError(t, err)
				require.Equal(t, packets.SUBACK, response.Type)
				require.Equal(t, []byte{1}, response.Content.(*packets.Suback).Reasons)
			}
			ids := []string{}
			for i, c := range []struct {
				from, to       *suite.MQTTWebSocket
				topic, uid, no string
				split          bool
			}{
				{alice, bob, "wk/v1/users/Ym9i/messages", "alice", "ws-alice-bob", true},
				{bob, alice, "wk/v1/users/YWxpY2U/messages", "bob", "ws-bob-alice", false},
			} {
				payload := []byte(`{"type":1,"content":"MQTT WebSocket message"}`)
				publish := &packets.Publish{PacketID: uint16(i + 2), QoS: 1, Topic: c.topic, Payload: payload, Properties: &packets.Properties{User: []packets.User{{Key: "wk.client_msg_no", Value: c.no}}}}
				if c.split {
					body, err := suite.EncodeMQTTPackets(publish)
					require.NoError(t, err)
					require.NoError(t, c.from.Conn.WriteMessage(ws.BinaryMessage, body[:3]))
					require.NoError(t, c.from.Conn.WriteMessage(ws.BinaryMessage, body[3:]))
				} else {
					require.NoError(t, c.from.WriteFragmented(ctx, publish))
				}
				// A personal inbox projects both directions of its person Channel.
				// PUBACK and the sender's own projection can arrive in either order.
				var echo *packets.Publish
				acknowledged := false
				for reads := 0; reads < 3 && (!acknowledged || echo == nil); reads++ {
					packet, err := c.from.ReadPacket(ctx)
					require.NoError(t, err, first.DumpDiagnostics())
					switch packet.Type {
					case packets.PUBACK:
						require.False(t, acknowledged, "duplicate submit acknowledgement")
						require.Less(t, packet.Content.(*packets.Puback).ReasonCode, byte(0x80))
						require.Equal(t, publish.PacketID, packet.PacketID())
						acknowledged = true
					case packets.PUBLISH:
						require.Nil(t, echo, "unexpected duplicate sender projection")
						echo = packet.Content.(*packets.Publish)
						require.Equal(t, payload, echo.Payload)
						require.Equal(t, c.uid, suite.MQTTUserProperty(echo.Properties, "wk.from_uid"))
						require.Equal(t, c.no, suite.MQTTUserProperty(echo.Properties, "wk.client_msg_no"))
						require.NoError(t, c.from.WritePackets(ctx, &packets.Puback{PacketID: echo.PacketID, Properties: &packets.Properties{}}))
					default:
						t.Fatalf("unexpected sender packet type %d", packet.Type)
					}
				}
				require.True(t, acknowledged)
				require.NotNil(t, echo)
				delivered, err := c.to.ReadPacket(ctx)
				require.NoError(t, err, last.DumpDiagnostics())
				require.Equal(t, packets.PUBLISH, delivered.Type)
				p := delivered.Content.(*packets.Publish)
				require.Equal(t, payload, p.Payload)
				require.Equal(t, c.uid, suite.MQTTUserProperty(p.Properties, "wk.from_uid"))
				require.Equal(t, c.no, suite.MQTTUserProperty(p.Properties, "wk.client_msg_no"))
				id := suite.MQTTUserProperty(p.Properties, "wk.message_id")
				require.Regexp(t, `^[1-9][0-9]*$`, id)
				require.Equal(t, id, suite.MQTTUserProperty(echo.Properties, "wk.message_id"))
				ids = append(ids, id)
				require.NoError(t, c.to.WritePackets(ctx, &packets.Puback{PacketID: p.PacketID, Properties: &packets.Properties{}}))
			}
			require.NoError(t, alice.WritePackets(ctx, &packets.Pingreq{}, &packets.Pingreq{}))
			for range 2 {
				p, err := alice.ReadPacket(ctx)
				require.NoError(t, err)
				require.Equal(t, packets.PINGRESP, p.Type)
			}
			report["message_ids"] = ids
			report["split_connect"] = true
			report["split_publish"] = true
			report["continuation"] = true
			report["coalesced_packets"] = true
			report["binary_replies"] = true
			report["personal_sender_echo"] = true
			t.Run("failed-authentication", func(t *testing.T) {
				c, _, err := suite.DialMQTTWebSocket(ctx, url, "mqtt")
				require.NoError(t, err)
				defer c.Conn.Close()
				require.NoError(t, c.WritePackets(ctx, connectPacket("alice", "wrong-fixture-token", "auth-rejected", true, 0)))
				p, err := c.ReadPacket(ctx)
				require.NoError(t, err)
				require.Equal(t, packets.CONNACK, p.Type)
				require.GreaterOrEqual(t, p.Content.(*packets.Connack).ReasonCode, byte(0x80))
			})
			t.Run("text-data-rejected", func(t *testing.T) {
				c, _, err := suite.DialMQTTWebSocket(ctx, url, "mqtt")
				require.NoError(t, err)
				defer c.Conn.Close()
				require.NoError(t, c.WritePackets(ctx, connectPacket("alice", "alice-fixture-token", "text-rejected", true, 0)))
				assertConnack(t, ctx, c, false)
				// A valid ASCII MQTT PUBLISH proves rejection is based on WS opcode,
				// rather than invalid UTF-8 or malformed MQTT packet syntax.
				body, err := suite.EncodeMQTTPackets(&packets.Publish{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, PacketID: 1, Payload: []byte("text"), Properties: &packets.Properties{User: []packets.User{{Key: "wk.client_msg_no", Value: "text"}}}})
				require.NoError(t, err)
				require.True(t, utf8.Valid(body))
				require.NoError(t, c.Conn.WriteMessage(ws.TextMessage, body))
				require.NoError(t, c.Conn.SetReadDeadline(time.Now().Add(5*time.Second)))
				_, _, err = c.Conn.ReadMessage()
				require.Error(t, err, "server accepted text MQTT data")
				var timeout net.Error
				require.False(t, errors.As(err, &timeout) && timeout.Timeout(), "rejection was only an observation timeout")
			})
			t.Run("oversized-message-rejected", func(t *testing.T) {
				c, _, err := suite.DialMQTTWebSocket(ctx, url, "mqtt")
				require.NoError(t, err)
				defer c.Conn.Close()
				_ = c.Conn.WriteMessage(ws.BinaryMessage, make([]byte, (1<<20)+1))
				require.NoError(t, c.Conn.SetReadDeadline(time.Now().Add(5*time.Second)))
				_, _, err = c.Conn.ReadMessage()
				require.Error(t, err)
				var timeout net.Error
				require.False(t, errors.As(err, &timeout) && timeout.Timeout())
			})
			report["authentication_rejected"] = true
			report["text_rejected"] = true
			report["oversized_rejected"] = true
			for _, c := range []*suite.MQTTWebSocket{alice, bob} {
				require.NoError(t, c.WritePackets(ctx, &packets.Disconnect{ReasonCode: 0, Properties: &packets.Properties{}}))
				require.NoError(t, c.Conn.Close())
			}
			for _, node := range nodes {
				require.NoError(t, node.Stop(), "product process cleanup")
			}
			report["process_cleanup"] = true
		})
	}
}

func connectPacket(uid, token, clientID string, clean bool, expiry uint32) *packets.Connect {
	return &packets.Connect{ProtocolName: "MQTT", ProtocolVersion: 5, Username: uid, UsernameFlag: true, Password: []byte(token), PasswordFlag: true, ClientID: clientID, CleanStart: clean, KeepAlive: 30, Properties: &packets.Properties{SessionExpiryInterval: &expiry, User: []packets.User{{Key: "wk.device_flag", Value: "1"}}}}
}
func assertConnack(t *testing.T, ctx context.Context, c *suite.MQTTWebSocket, present bool) {
	t.Helper()
	p, err := c.ReadPacket(ctx)
	require.NoError(t, err, "MQTT CONNACK read")
	require.Equal(t, packets.CONNACK, p.Type)
	ack := p.Content.(*packets.Connack)
	require.Equal(t, byte(0), ack.ReasonCode)
	require.Equal(t, present, ack.SessionPresent)
}
