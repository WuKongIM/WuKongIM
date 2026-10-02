//go:build e2e

package subscribe_admission

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

type observation struct {
	History       string             `json:"history"`
	Round         int                `json:"round"`
	Ingress       int                `json:"ingress"`
	Phase         string             `json:"phase"`
	SubscribeMS   int64              `json:"subscribe_ms"`
	Passed        bool               `json:"passed"`
	ErrorKind     string             `json:"error_kind,omitempty"`
	ClosureCounts map[string]float64 `json:"closure_counts,omitempty"`
}

// TestFirstGroupSubscriptionAdmission exercises the first filter of every new
// Session, including cold groups that have never received a native message.
func TestFirstGroupSubscriptionAdmission(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) { admission(t, count) })
	}
}

func admission(t *testing.T, count int) {
	s := suite.New(t)
	addrs := make([]string, count)
	opts := []suite.Option{suite.WithManagerHTTP()}
	reportDir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if reportDir != "" {
		opts = append(opts, suite.WithNodeLogRootDir(filepath.Join(reportDir, fmt.Sprintf("%d-node-logs", count))), suite.WithWorkspaceRootDir(filepath.Join(reportDir, "workspaces")))
	}
	for i := range count {
		addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i]}))
	}
	var nodes []*suite.StartedNode
	if count == 1 {
		nodes = append(nodes, s.StartSingleNodeCluster(opts...))
	} else {
		cluster := s.StartThreeNodeCluster(opts...)
		ready, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		require.NoError(t, cluster.WaitClusterReady(ready), cluster.DumpDiagnostics())
		cancel()
		for i := range count {
			nodes = append(nodes, cluster.MustNode(uint64(i+1)))
		}
	}
	if reportDir == "" {
		reportDir = nodes[0].Spec.RootDir
	}
	var observations []observation
	defer func() {
		data, err := json.MarshalIndent(map[string]any{"scenario": "mqtt-first-subscribe-admission", "hash_slots": 256, "nodes": count, "rounds_per_history": 20, "selected_cases": len(observations), "session_expiry_sec": 86400, "clean_start": false, "passed": !t.Failed(), "observations": observations}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(reportDir, 0755))
		path := filepath.Join(reportDir, fmt.Sprintf("mqtt-subscribe-admission-%d.json", count))
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+nodes[0].APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-admission-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	for round := range 20 {
		for _, history := range []string{"empty", "existing"} {
			t.Run(fmt.Sprintf("%s/%02d", history, round), func(t *testing.T) {
				index := (count - 1 + round) % count
				o := observation{History: history, Round: round, Ingress: index + 1, Phase: "provision"}
				defer func() { o.Passed = !t.Failed(); observations = append(observations, o) }()
				group := fmt.Sprintf("admission-%s-%d", history, round)
				require.NoError(t, suite.PostChannel(ctx, nodes[0].APIAddr(), map[string]any{"channel_id": group, "channel_type": frame.ChannelTypeGroup, "subscribers": []string{"alice", "bob"}}))
				send := func(no string) suite.MessageSendResponse {
					ack, err := suite.PostMessageSendEventually(ctx, nodes[0].APIAddr(), map[string]any{"from_uid": "alice", "channel_id": group, "channel_type": frame.ChannelTypeGroup, "client_msg_no": no, "payload": base64.StdEncoding.EncodeToString([]byte(no))})
					require.NoError(t, err, nodes[0].DumpDiagnostics())
					require.Equal(t, uint8(frame.ReasonSuccess), ack.Reason)
					return ack
				}
				if history == "existing" {
					send(group + "-history")
				}
				o.Phase = "connect"
				c, err := suite.ConnectMQTT(ctx, addrs[index], "bob", "bob-admission-token", group+"-client", false, 86400, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
				require.NoError(t, err, nodes[index].DumpDiagnostics())
				defer func() { _ = c.Close() }()
				require.False(t, c.Connack.SessionPresent)
				o.Phase = "subscribe"
				topic := "wk/v1/groups/" + base64.RawURLEncoding.EncodeToString([]byte(group)) + "/messages"
				start := time.Now()
				ack, err := c.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 1}}})
				o.SubscribeMS = time.Since(start).Milliseconds()
				t.Logf("first subscribe history=%s round=%d ingress=%d ms=%d success=%t", history, round, index+1, o.SubscribeMS, err == nil)
				if err != nil {
					o.ErrorKind = "transport_or_protocol"
					if errors.Is(err, context.DeadlineExceeded) {
						o.ErrorKind = "deadline"
					}
					o.ClosureCounts = make(map[string]float64)
					for _, reason := range []string{"deadline", "canceled", "conflict", "pending", "fenced", "owner_limit", "unconfirmed", "unknown", "evidence"} {
						metric, stop := context.WithTimeout(context.Background(), time.Second)
						value, metricErr := suite.FetchMetricValue(metric, nodes[index].APIAddr(), "wukongim_mqtt_subscription_closures_total", map[string]string{"operation": "subscribe", "reason": reason})
						stop()
						if metricErr == nil {
							o.ClosureCounts[reason] = value
						}
					}
				}
				require.NoError(t, err, nodes[index].DumpDiagnostics())
				require.Equal(t, []byte{1}, ack.Reasons)
				o.Phase = "future-delivery"
				no := group + "-future"
				message := send(no)
				receive, stop := context.WithTimeout(ctx, 30*time.Second)
				p, err := c.Receive(receive)
				stop()
				require.NoError(t, err, nodes[index].DumpDiagnostics())
				require.Equal(t, byte(1), p.QoS)
				require.Equal(t, []byte(no), p.Payload)
				require.NotNil(t, p.Properties)
				require.Equal(t, no, p.Properties.User.Get("wk.client_msg_no"))
				require.Equal(t, strconv.FormatInt(message.MessageID, 10), p.Properties.User.Get("wk.message_id"))
				require.NoError(t, c.Client.Ack(p))
				o.Phase = "complete"
			})
		}
	}
}
