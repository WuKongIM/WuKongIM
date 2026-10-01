//go:build e2e

package storage_capacity

import (
	"context"
	"encoding/base64"
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

// Public ingress receipts and two independent consumer completions qualify one
// shared body, maintenance at full capacity, forwarded publication and Will retry.
func TestIngressAndSharedConsumersUseOneStorageBudget(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) { runIngressCapacity(t, count) })
	}
}
func runIngressCapacity(t *testing.T, count int) {
	s := suite.New(t)
	addrs := make([]string, count)
	var opts []suite.Option
	if root := os.Getenv("WK_E2E_MQTT_CAPACITY_WORKSPACE"); root != "" {
		opts = append(opts, suite.WithWorkspaceRootDir(root))
	}
	for i := range count {
		addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true",
			"WK_MQTT_LISTEN_ADDR": addrs[i], "WK_METRICS_ENABLE": "true", "WK_MQTT_QUOTA_MESSAGES": "1000", "WK_MQTT_QUOTA_BYTES": "8388608",
		}), suite.WithNodeEnv(uint64(i+1), "WK_MQTT_STORAGE_NODE_BYTES=8192", fmt.Sprintf("WK_MQTT_STORAGE_CLUSTER_BYTES=%d", 8192*count)))
	}
	var nodes []*suite.StartedNode
	if count == 1 {
		nodes = []*suite.StartedNode{s.StartSingleNodeCluster(opts...)}
	} else {
		c := s.StartThreeNodeCluster(append(opts, suite.WithManagerHTTP())...)
		ready, done := context.WithTimeout(context.Background(), 30*time.Second)
		require.NoError(t, c.WaitClusterReady(ready), c.DumpDiagnostics())
		done()
		for i := range count {
			nodes = append(nodes, c.MustNode(uint64(i+1)))
		}
	}
	ctx, done := context.WithTimeout(context.Background(), 100*time.Second)
	defer done()
	first := nodes[0]
	last := nodes[count-1]
	report := map[string]any{"scenario": "all-ingress-shared-body", "nodes": count, "hash_slots": 256, "passed": false}
	t.Cleanup(func() {
		report["passed"] = !t.Failed()
		dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
		if dir == "" {
			dir = first.Spec.RootDir
		}
		require.NoError(t, os.MkdirAll(dir, 0755))
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		p := filepath.Join(dir, fmt.Sprintf("aggregate-storage-ingress-%d.json", count))
		require.NoError(t, os.WriteFile(p, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", p)
	})
	for _, uid := range []string{"alice", "bob", "carol"} {
		_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-ingress-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	topic := func(uid string) string {
		return "wk/v1/users/" + base64.RawURLEncoding.EncodeToString([]byte(uid)) + "/messages"
	}
	connect := func(uid, id string) *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addrs[count-1], uid, uid+"-ingress-token", id, false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, last.DumpDiagnostics())
		t.Cleanup(func() { _ = c.Abort() })
		return c
	}
	for _, q := range []struct{ uid, id string }{{"bob", "body-bob-1"}, {"bob", "body-bob-2"}, {"carol", "body-carol"}} {
		c := connect(q.uid, q.id)
		ack, err := c.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic(q.uid), QoS: 1}}})
		require.NoError(t, err)
		require.Equal(t, []byte{1}, ack.Reasons)
		require.NoError(t, c.Close())
	}
	producer := connect("alice", "ingress-producer")
	publish := func(uid, no string, body []byte) *paho.PublishResponse {
		ack, err := producer.Client.Publish(ctx, &paho.Publish{Topic: topic(uid), QoS: 1, Payload: body, Properties: &paho.PublishProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: no}}}})
		require.NotNil(t, ack, "MQTT did not return a public PUBACK: %v", err)
		return ack
	}
	body := make([]byte, 2048)
	require.Less(t, publish("bob", "ingress-funded", body).ReasonCode, byte(0x80))
	reserved := func() []float64 {
		values := make([]float64, len(nodes))
		for i, n := range nodes {
			v, err := suite.FetchMetricValue(ctx, n.APIAddr(), "wukongim_mqtt_storage_bytes", map[string]string{"state": "reserved"})
			require.NoError(t, err)
			values[i] = v
		}
		return values
	}
	before := reserved()
	report["shared_body_reserved"] = before
	for _, v := range before {
		require.Greater(t, v, float64(0))
		require.LessOrEqual(t, v, float64(8192))
	}
	// A duplicate succeeds within its original responsibility even at capacity.
	require.Less(t, publish("bob", "ingress-funded", body).ReasonCode, byte(0x80))
	require.Equal(t, before, reserved())
	wk, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = wk.Close() })
	_, err = wk.ConnectAuthenticatedContext(ctx, last.GatewayAddr(), "alice", "ingress-wk", "alice-ingress-token", frame.WEB)
	require.NoError(t, err)
	require.NoError(t, wk.SendFrame(&frame.SendPacket{ChannelID: "bob", ChannelType: frame.ChannelTypePerson, ClientSeq: 1, ClientMsgNo: "ingress-wk-denied", Payload: body}))
	wkack, err := wk.ReadSendAck()
	require.NoError(t, err)
	require.NotEqual(t, frame.ReasonSuccess, wkack.ReasonCode)
	report["wk_denied_reason"] = wkack.ReasonCode
	httpack, err := suite.PostMessageSend(ctx, last.APIAddr(), map[string]any{"from_uid": "alice", "channel_id": "carol", "channel_type": 1, "client_msg_no": "ingress-http-denied", "payload": body})
	require.Error(t, err, "HTTP accepted new debt: %+v", httpack)
	report["http_denied"] = true
	mqttack, publishErr := producer.Client.Publish(ctx, &paho.Publish{Topic: topic("carol"), QoS: 1, Payload: body, Properties: &paho.PublishProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: "ingress-mqtt-denied"}}}})
	if mqttack != nil {
		require.GreaterOrEqual(t, mqttack.ReasonCode, byte(0x80))
		report["mqtt_denied_reason"] = mqttack.ReasonCode
	} else {
		require.Error(t, publishErr)
		report["mqtt_retry_required"] = true
	}
	require.Equal(t, before, reserved(), "a rejected ingress left a partial original")
	// The exact negative proof must not strand the producer's owner generation.
	// Reconnect once with the same ClientID, without restarting any product node.
	producerRetry := connect("alice", "ingress-producer")
	require.NoError(t, producerRetry.Close())
	report["rejected_producer_reconnected"] = true
	// Each ACK belongs to its own Session. One completion cannot retire the
	// shared body while the other offline Session still requires it.
	b1 := connect("bob", "body-bob-1")
	p1, err := b1.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, body, p1.Payload)
	require.NoError(t, b1.Client.Ack(p1))
	wait, stop := context.WithTimeout(ctx, 3*time.Second)
	_, err = b1.Receive(wait)
	stop()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, before, reserved(), "the first subscriber ACK freed the second subscriber's body")
	b2 := connect("bob", "body-bob-2")
	p2, err := b2.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, body, p2.Payload)
	require.Equal(t, p1.Properties.User.Get("wk.message_id"), p2.Properties.User.Get("wk.message_id"))
	report["shared_message_id"] = p1.Properties.User.Get("wk.message_id")
	// A due Will uses the same original append boundary and must wait for space.
	delay := uint32(1)
	willBody := make([]byte, 2048)
	willBody[0] = 42
	will, err := suite.ConnectMQTT(ctx, addrs[count-1], "alice", "alice-ingress-token", "capacity-will", true, 60, suite.MQTTConnectOptions{Will: &paho.WillMessage{Topic: topic("carol"), QoS: 1, Payload: willBody}, WillProperties: &paho.WillProperties{WillDelayInterval: &delay, User: paho.UserProperties{{Key: "wk.client_msg_no", Value: "ingress-will"}}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = will.Abort() })
	require.NoError(t, will.Abort())
	carol := connect("carol", "body-carol")
	blocked, stop := context.WithTimeout(ctx, 4*time.Second)
	unexpected, err := carol.Receive(blocked)
	stop()
	require.ErrorIs(t, err, context.DeadlineExceeded, "full-budget Will published: %+v", unexpected)
	require.Equal(t, before, reserved())
	require.NoError(t, b2.Client.Ack(p2))
	delivered, err := carol.Receive(ctx)
	require.NoError(t, err, first.DumpDiagnostics())
	require.Equal(t, willBody, delivered.Payload)
	require.Equal(t, "alice", delivered.Properties.User.Get("wk.from_uid"))
	report["will_reopened_message_id"] = delivered.Properties.User.Get("wk.message_id")
	require.NoError(t, carol.Client.Ack(delivered))
	require.Eventually(t, func() bool {
		for _, v := range reserved() {
			if v != 0 {
				return false
			}
		}
		return true
	}, 30*time.Second, 200*time.Millisecond, "maintenance could not retire accepted content at full capacity")
	report["independent_acks_and_retirement"] = true
}
