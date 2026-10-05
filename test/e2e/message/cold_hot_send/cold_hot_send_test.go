//go:build e2e

package cold_hot_send

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// TestColdCreationDoesNotHoldHotSENDACK covers fresh group initialization and
// following SENDs, which share the nightly workload's group latency domain.
func TestColdCreationDoesNotHoldHotSENDACK(t *testing.T) {
	started := time.Now().UTC()
	var observations []map[string]any
	defer func() {
		path := os.Getenv("WK_E2E_COLD_HOT_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-cold-hot-send.json")
		}
		body, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "hash_slots": 256, "observations": observations}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, append(body, '\n'), 0644))
	}()
	for _, nodes := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", nodes), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			opts := []suite.Option{suite.WithManagerHTTP()}
			for nodeID := 1; nodeID <= nodes; nodeID++ {
				opts = append(opts, suite.WithNodeConfigOverrides(uint64(nodeID), map[string]string{
					"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12",
					"WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS": "1",
				}))
			}
			harness := suite.New(t)
			var node *suite.StartedNode
			if nodes == 1 {
				node = harness.StartSingleNodeCluster(opts...)
			} else {
				cluster := harness.StartThreeNodeCluster(opts...)
				require.NoError(t, cluster.WaitClusterReady(ctx))
				_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
				require.NoError(t, err)
				node = cluster.MustNode(1)
			}
			uid := "cold-hot-sender"
			client, err := suite.NewWKProtoClientWithTimeout(5 * time.Second)
			require.NoError(t, err)
			require.NoError(t, client.Connect(node.GatewayAddr(), uid, "cold-hot-device"))
			defer client.Close()
			hot := "cold-hot-warm-group"
			create := func(channel string) {
				t.Helper()
				require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{"channel_id": channel, "channel_type": 2, "subscribers": []string{uid}}))
			}
			send := func(channel, no string) {
				t.Helper()
				require.NoError(t, client.SendFrame(&frame.SendPacket{ChannelID: channel, ChannelType: 2, ClientMsgNo: no, Payload: []byte(no)}))
			}
			expected := map[string][]string{hot: {"warm-control"}}
			create(hot)
			send(hot, "warm-control")
			ack, err := client.ReadSendAck()
			require.NoError(t, err)
			require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
			for wave := 0; wave < 3; wave++ {
				cold := fmt.Sprintf("cold-hot-new-group-%d", wave)
				create(cold)
				coldNo, followNo, hotNo := fmt.Sprintf("cold-%d", wave), fmt.Sprintf("follow-%d", wave), fmt.Sprintf("hot-%d", wave)
				send(cold, coldNo)
				send(cold, followNo)
				send(hot, hotNo)
				seen := map[string]bool{}
				seqs := map[string]uint64{}
				for i := 0; i < 3; i++ {
					ack, timing, err := client.ReadSendAckWithTiming()
					require.NoError(t, err)
					require.Contains(t, []string{coldNo, followNo, hotNo}, ack.ClientMsgNo)
					require.False(t, seen[ack.ClientMsgNo])
					seen[ack.ClientMsgNo], seqs[ack.ClientMsgNo] = true, ack.MessageSeq
					require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
					require.Positive(t, ack.MessageID)
					require.False(t, timing.PendingStartedAt.IsZero())
					require.False(t, timing.ObservedAt.IsZero())
					elapsed := timing.ObservedAt.Sub(timing.PendingStartedAt)
					observations = append(observations, map[string]any{"nodes": nodes, "wave": wave, "client_msg_no": ack.ClientMsgNo, "elapsed_us": elapsed.Microseconds(), "seq": ack.MessageSeq})
					require.Less(t, elapsed, 400*time.Millisecond, "group initialization must leave budget for SEND and its followers")
				}
				require.Less(t, seqs[coldNo], seqs[followNo])
				expected[cold] = []string{coldNo, followNo}
				expected[hot] = append(expected[hot], hotNo)
			}
			for channel, want := range expected {
				var history struct {
					Items []struct {
						ClientMsgNo string `json:"client_msg_no"`
					} `json:"items"`
					HasMore bool `json:"has_more"`
				}
				endpoint := "http://" + node.Spec.ManagerAddr + "/manager/messages?channel_id=" + url.QueryEscape(channel) + "&channel_type=2&limit=100"
				require.Eventually(t, func() bool {
					_, err := suite.GetJSON(ctx, endpoint, &history)
					return err == nil && len(history.Items) >= len(want)
				}, 10*time.Second, 100*time.Millisecond)
				require.False(t, history.HasMore)
				var actual []string
				for _, item := range history.Items {
					actual = append(actual, item.ClientMsgNo)
				}
				require.ElementsMatch(t, want, actual)
				observations = append(observations, map[string]any{"nodes": nodes, "channel": channel, "exact_history": actual})
			}
		})
	}
}
