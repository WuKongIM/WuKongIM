//go:build e2e

package will

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

func TestWillPublicationAndNormalCancellation(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) {
			var opts []suite.Option
			addrs := make([]string, count)
			for i := range count {
				addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
				opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i]}))
			}
			s := suite.New(t)
			var first *suite.StartedNode
			if count == 1 {
				first = s.StartSingleNodeCluster(opts...)
			} else {
				cluster := s.StartThreeNodeCluster(append(opts, suite.WithManagerHTTP())...)
				ready, done := context.WithTimeout(context.Background(), 30*time.Second)
				require.NoError(t, cluster.WaitClusterReady(ready), cluster.DumpDiagnostics())
				_, err := cluster.WaitSlotLeadersStable(ready, time.Second)
				done()
				require.NoError(t, err, cluster.DumpDiagnostics())
				first = cluster.MustNode(1)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			for _, uid := range []string{"alice", "bob"} {
				_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-fixture-token", "device_flag": 1, "device_level": 1}, nil)
				require.NoError(t, err)
			}
			bob, err := suite.ConnectMQTT(ctx, addrs[0], "bob", "bob-fixture-token", "will-recipient", true, 0)
			require.NoError(t, err)
			t.Cleanup(func() { _ = bob.Close() })
			sub, err := bob.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
			require.NoError(t, err)
			require.Equal(t, []byte{1}, sub.Reasons)
			delay := uint32(2)
			connect := func(id, body string) *suite.MQTTClient {
				client, e := suite.ConnectMQTT(ctx, addrs[count-1], "alice", "alice-fixture-token", id, true, 60, suite.MQTTConnectOptions{Will: &paho.WillMessage{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Payload: []byte(body)}, WillProperties: &paho.WillProperties{WillDelayInterval: &delay, User: paho.UserProperties{{Key: "wk.client_msg_no", Value: id}}}})
				require.NoError(t, e)
				t.Cleanup(func() { _ = client.Abort() })
				return client
			}
			normal := connect("normal-close", "must-not-publish")
			require.NoError(t, normal.Close())
			abnormal := connect("abnormal-close", "expected-will")
			closedAt := time.Now()
			require.NoError(t, abnormal.Abort())
			received, e := bob.Receive(ctx)
			require.NoError(t, e, first.DumpDiagnostics())
			require.Equal(t, []byte("expected-will"), received.Payload, "normal disconnect published or Will was changed")
			require.GreaterOrEqual(t, time.Since(closedAt), 2*time.Second, "Will Delay was bypassed")
			require.NotNil(t, received.Properties)
			messageID := received.Properties.User.Get("wk.message_id")
			require.NotEmpty(t, messageID)
			require.Equal(t, "alice", received.Properties.User.Get("wk.from_uid"))
			// Observe multiple complete recovery scans after the due time. This bounded
			// absence check is not a proof of arbitrary crash-window duplicate safety.
			quiet, done := context.WithTimeout(ctx, 6*time.Second)
			unexpected, e := bob.Receive(quiet)
			done()
			require.ErrorIs(t, e, context.DeadlineExceeded, "unexpected publication: %v", unexpected)
			reportDir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
			if reportDir == "" {
				reportDir = first.Spec.RootDir
			}
			require.NoError(t, os.MkdirAll(reportDir, 0755))
			report, e := json.MarshalIndent(map[string]any{"scenario": "mqtt-will", "nodes": count, "hash_slots": 256, "passed": true, "message_id": messageID, "abnormal_close_published": true, "normal_close_cancelled": true, "delay_seconds": delay}, "", "  ")
			require.NoError(t, e)
			path := filepath.Join(reportDir, fmt.Sprintf("mqtt-will-%d.json", count))
			require.NoError(t, os.WriteFile(path, append(report, '\n'), 0600))
			t.Logf("result artifact: %s", path)
		})
	}
}
