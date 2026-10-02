//go:build e2e

package reclamation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

func TestSessionReclamationPreservesNewLifetime(t *testing.T) {
	for _, count := range []int{1, 3} {
		for _, mode := range []string{"zero-expiry", "expiry", "clean-start"} {
			t.Run(fmt.Sprintf("%d-node-cluster/%s", count, mode), func(t *testing.T) {
				s := suite.New(t)
				addrs := make([]string, count)
				var opts []suite.Option
				for i := range count {
					addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
					opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i]}))
				}
				var nodes []*suite.StartedNode
				if count == 1 {
					nodes = append(nodes, s.StartSingleNodeCluster(opts...))
				} else {
					cluster := s.StartThreeNodeCluster(append(opts, suite.WithManagerHTTP())...)
					ready, done := context.WithTimeout(context.Background(), 30*time.Second)
					require.NoError(t, cluster.WaitClusterReady(ready), cluster.DumpDiagnostics())
					_, e := cluster.WaitSlotLeadersStable(ready, time.Second)
					done()
					require.NoError(t, e, cluster.DumpDiagnostics())
					for i := range count {
						nodes = append(nodes, cluster.MustNode(uint64(i+1)))
					}
				}
				first := nodes[0]
				ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
				defer cancel()
				for _, uid := range []string{"alice", "bob"} {
					_, e := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-reclamation-token", "device_flag": 1, "device_level": 1}, nil)
					require.NoError(t, e)
				}
				connect := func(addr string, clean bool, expiry uint32) *suite.MQTTClient {
					c, e := suite.ConnectMQTT(ctx, addr, "bob", "bob-reclamation-token", "reclamation-bob", clean, expiry)
					require.NoError(t, e, first.DumpDiagnostics())
					t.Cleanup(func() { _ = c.Abort() })
					return c
				}
				subscribe := func(c *suite.MQTTClient) {
					ack, e := c.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
					require.NoError(t, e)
					require.Equal(t, []byte{1}, ack.Reasons)
				}
				expiry := uint32(600)
				if mode == "zero-expiry" {
					expiry = 0
				}
				if mode == "expiry" {
					expiry = 1
				}
				old := connect(addrs[count-1], false, expiry)
				require.False(t, old.Connack.SessionPresent)
				subscribe(old)
				require.NoError(t, old.Close())
				var successor *suite.MQTTClient
				if mode == "clean-start" {
					successor = connect(addrs[0], true, 600)
					require.False(t, successor.Connack.SessionPresent)
					subscribe(successor)
				}
				var total, reclaimed float64
				require.Eventually(t, func() bool {
					total, reclaimed = 0, 0
					for _, node := range nodes {
						call, done := context.WithTimeout(ctx, 2*time.Second)
						value, e := suite.FetchMetricValue(call, node.APIAddr(), "wukongim_mqtt_consumer_events_total", map[string]string{"event": "qualification_removed"})
						done()
						if e != nil {
							return false
						}
						total += value
						call, done = context.WithTimeout(ctx, 2*time.Second)
						value, e = suite.FetchMetricValue(call, node.APIAddr(), "wukongim_mqtt_consumer_events_total", map[string]string{"event": "reclamation_confirmed"})
						done()
						if e != nil {
							return false
						}
						reclaimed += value
					}
					return total >= 1 && reclaimed >= 1
				}, 45*time.Second, 250*time.Millisecond, "old Session children and UID qualification were not reclaimed")
				if successor == nil {
					successor = connect(addrs[0], false, 600)
					require.False(t, successor.Connack.SessionPresent)
					subscribe(successor)
				}
				// This person Channel did not exist when the old qualification was retired.
				alice, e := suite.NewWKProtoClient()
				require.NoError(t, e)
				t.Cleanup(func() { _ = alice.Close() })
				_, e = alice.ConnectAuthenticatedContext(ctx, first.GatewayAddr(), "alice", "reclamation-wk", "alice-reclamation-token", frame.WEB)
				require.NoError(t, e)
				require.NoError(t, alice.SendFrame(&frame.SendPacket{ChannelID: "bob", ChannelType: frame.ChannelTypePerson, ClientSeq: 1, ClientMsgNo: "new-qualified-source", Payload: []byte("new-qualified-source")}))
				ack, e := alice.ReadSendAck()
				require.NoError(t, e)
				require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
				p, e := successor.Receive(ctx)
				require.NoError(t, e, first.DumpDiagnostics())
				require.Equal(t, []byte("new-qualified-source"), p.Payload)
				require.NoError(t, successor.Close())
				dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
				if dir == "" {
					dir = first.Spec.RootDir
				}
				require.NoError(t, os.MkdirAll(dir, 0755))
				data, e := json.MarshalIndent(map[string]any{"scenario": "session-reclamation", "mode": mode, "nodes": count, "hash_slots": 256, "passed": true, "qualification_removed_confirmations": total, "reclamation_confirmations": reclaimed, "cleanup_observed_via": "fixed public completion counter", "direct_table_inspection": false, "new_lifetime_preserved": true, "new_source_delivered": true}, "", "  ")
				require.NoError(t, e)
				path := filepath.Join(dir, fmt.Sprintf("mqtt-reclamation-%s-%d.json", mode, count))
				require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
				t.Logf("result artifact: %s", path)
			})
		}
	}
}
