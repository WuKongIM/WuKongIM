//go:build e2e

package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

func TestConsumerMaintenanceQuotasAndCompletion(t *testing.T) {
	for _, nodes := range []int{1, 3} {
		for _, scenario := range []string{"offline-messages", "offline-bytes", "full-window", "ack-completion"} {
			t.Run(fmt.Sprintf("%d-node-cluster/%s", nodes, scenario), func(t *testing.T) {
				runConsumerScenario(t, nodes, scenario)
			})
		}
	}
}

func runConsumerScenario(t *testing.T, count int, scenario string) {
	t.Helper()
	s := suite.New(t)
	var opts []suite.Option
	addrs := make([]string, count)
	for i := range count {
		addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		cfg := map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i], "WK_MQTT_QUOTA_MESSAGES": "2", "WK_MQTT_QUOTA_BYTES": "1048576"}
		if scenario == "offline-bytes" {
			cfg["WK_MQTT_QUOTA_MESSAGES"] = "1000"
			cfg["WK_MQTT_QUOTA_BYTES"] = "512"
		}
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), cfg))
	}
	var nodes []*suite.StartedNode
	if count == 1 {
		nodes = append(nodes, s.StartSingleNodeCluster(opts...))
	} else {
		cluster := s.StartThreeNodeCluster(append(opts, suite.WithManagerHTTP())...)
		ready, done := context.WithTimeout(context.Background(), 30*time.Second)
		require.NoError(t, cluster.WaitClusterReady(ready), cluster.DumpDiagnostics())
		_, err := cluster.WaitSlotLeadersStable(ready, time.Second)
		done()
		require.NoError(t, err, cluster.DumpDiagnostics())
		for i := range count {
			nodes = append(nodes, cluster.MustNode(uint64(i+1)))
		}
	}
	n := nodes[0]
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	for _, uid := range []string{"alice", "bob"} {
		_, e := suite.PostJSON(ctx, "http://"+n.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-quota-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, e)
	}
	connect := func(addr string) *suite.MQTTClient {
		c, e := suite.ConnectMQTT(ctx, addr, "bob", "bob-quota-token", "consumer-bob", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, e, n.DumpDiagnostics())
		t.Cleanup(func() { _ = c.Abort() })
		return c
	}
	const topic = "wk/v1/users/Ym9i/messages"
	subscribe := func(c *suite.MQTTClient) {
		ack, e := c.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 1}}})
		require.NoError(t, e)
		require.Equal(t, []byte{1}, ack.Reasons)
	}
	bob := connect(addrs[count-1])
	require.False(t, bob.Connack.SessionPresent)
	subscribe(bob)
	offline := strings.HasPrefix(scenario, "offline-")
	if offline {
		require.NoError(t, bob.Close())
	}
	alice, e := suite.NewWKProtoClient()
	require.NoError(t, e)
	t.Cleanup(func() { _ = alice.Close() })
	_, e = alice.ConnectAuthenticatedContext(ctx, n.GatewayAddr(), "alice", "quota-wk", "alice-quota-token", frame.WEB)
	require.NoError(t, e)
	send := func(seq int, payload []byte) {
		require.NoError(t, alice.SendFrame(&frame.SendPacket{ChannelID: "bob", ChannelType: frame.ChannelTypePerson, ClientSeq: uint64(seq), ClientMsgNo: fmt.Sprintf("quota-%s-%d", scenario, seq), Payload: payload}))
		ack, e := alice.ReadSendAck()
		require.NoError(t, e)
		require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	}
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, node := range nodes {
			call, done := context.WithTimeout(context.Background(), 2*time.Second)
			samples, err := suite.FetchMetricSamples(call, node.APIAddr())
			done()
			t.Logf("consumer diagnostics: node=%s metrics_error=%v", node.Spec.RootDir, err)
			for _, sample := range samples {
				if strings.HasPrefix(sample.Name, "wukongim_mqtt_consumer_") {
					t.Logf("%s %v = %g", sample.Name, sample.Labels, sample.Value)
				}
			}
		}
	})
	waitEvent := func(event string) {
		require.Eventually(t, func() bool {
			var total float64
			for _, node := range nodes {
				call, done := context.WithTimeout(ctx, 2*time.Second)
				value, err := suite.FetchMetricValue(call, node.APIAddr(), "wukongim_mqtt_consumer_events_total", map[string]string{"event": event})
				done()
				if err != nil {
					return false
				}
				total += value
			}
			return total >= 1
		}, 45*time.Second, 250*time.Millisecond, "background consumer event was not confirmed: %s", event)
	}
	messages, body := 3, []byte("pending")
	if scenario == "offline-bytes" {
		messages = 1
		body = make([]byte, 2048)
	}
	if scenario == "ack-completion" {
		messages = 1
	}
	for i := 1; i <= messages; i++ {
		send(i, body)
		if !offline && i == 1 {
			p, e := bob.Receive(ctx)
			require.NoError(t, e, n.DumpDiagnostics())
			require.Equal(t, body, p.Payload)
			if scenario == "ack-completion" {
				require.NoError(t, bob.Client.Ack(p))
			}
			// full-window deliberately retains the first exchange without PUBACK.
		}
	}
	report := map[string]any{"scenario": scenario, "nodes": count, "hash_slots": 256, "passed": true, "receive_maximum": 1}
	if scenario == "ack-completion" {
		waitEvent("projected")
		ack, e := bob.Client.Unsubscribe(ctx, &paho.Unsubscribe{Topics: []string{topic}})
		require.NoError(t, e)
		require.Equal(t, []byte{0}, ack.Reasons)
		waitEvent("removed")
		report["ack_progress_projected"], report["closed_source_removed"] = true, true
	} else {
		waitEvent("quota_end_confirmed")
		waitEvent("removed")
		if !offline {
			select {
			case <-bob.Client.Done():
			case <-ctx.Done():
				t.Fatal("quota-ended online owner did not close")
			}
		}
		// Reconnect through a different ingress in the three-node case.
		resumed := connect(addrs[0])
		require.False(t, resumed.Connack.SessionPresent, "quota was not enforced before reconnect")
		subscribe(resumed)
		send(messages+1, []byte("fresh"))
		p, e := resumed.Receive(ctx)
		require.NoError(t, e, n.DumpDiagnostics())
		require.Equal(t, []byte("fresh"), p.Payload)
		require.NoError(t, resumed.Client.Ack(p))
		report["ended_before_reconnect"], report["source_removed_before_reconnect"], report["resubscribe_delivers_fresh_message"] = true, true, true
		report["session_present"], report["offline"] = false, offline
	}
	dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if dir == "" {
		dir = n.Spec.RootDir
	}
	require.NoError(t, os.MkdirAll(dir, 0755))
	data, e := json.MarshalIndent(report, "", "  ")
	require.NoError(t, e)
	path := filepath.Join(dir, fmt.Sprintf("mqtt-consumer-%s-%d.json", scenario, count))
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
	t.Logf("result artifact: %s", path)
}
