//go:build e2e

package storage_partition

import (
	"context"
	"encoding/base64"
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

// Live TCP isolation must preserve accepted shared replay through recovery.
func TestPartitionRetainsAcceptedStorageAndReopensAfterRecovery(t *testing.T) {
	reportDir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if reportDir == "" {
		reportDir = t.TempDir()
	}
	phase := "startup"
	report := map[string]any{"scenario": "mqtt-storage-partition", "nodes": 3, "hash_slots": 256, "channel_replicas": 3, "node_bytes": 8192, "cluster_bytes": 24576, "resubscriptions": 0}
	// Register first: final receipts include cleanup failures.
	t.Cleanup(func() {
		report["passed"], report["last_phase"] = !t.Failed(), phase
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(reportDir, 0755))
		path := filepath.Join(reportDir, "mqtt-storage-partition-accepted.json")
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	mesh := suite.NewClusterTCPPartition(t)
	addresses := make([]string, 3)
	opts := []suite.Option{suite.WithManagerHTTP(), suite.WithClusterTCPPartition(mesh), suite.WithNodeLogRootDir(filepath.Join(reportDir, "accepted-logs"))}
	for i := range 3 {
		addresses[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_SLOT_REPLICA_N": "3", "WK_CLUSTER_CHANNEL_REPLICA_N": "3", "WK_GATEWAY_TOKEN_AUTH_ON": "true",
			"WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addresses[i], "WK_METRICS_ENABLE": "true", "WK_MQTT_STORAGE_NODE_BYTES": "8192", "WK_MQTT_STORAGE_CLUSTER_BYTES": "24576",
		}))
	}
	cluster := suite.New(t).StartThreeNodeCluster(opts...)
	ctx, done := context.WithTimeout(context.Background(), 3*time.Minute)
	defer done()
	ready := func() {
		call, cancel := context.WithTimeout(ctx, 35*time.Second)
		defer cancel()
		require.NoError(t, cluster.WaitClusterReady(call), cluster.DumpDiagnostics())
		_, err := cluster.WaitSlotLeadersStable(call, time.Second)
		require.NoError(t, err, cluster.DumpDiagnostics())
	}
	ready()
	first := cluster.MustNode(1)
	for _, uid := range []string{"alice", "bob", "carol"} {
		_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-partition-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	topic := func(uid string) string {
		return "wk/v1/users/" + base64.RawURLEncoding.EncodeToString([]byte(uid)) + "/messages"
	}
	connectTo := func(uid, id string) *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addresses[0], uid, uid+"-partition-token", id, false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() {
			join, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, c.AbortAndWait(join))
		})
		return c
	}
	connect := func(id string) *suite.MQTTClient { return connectTo("bob", id) }
	for _, q := range []struct{ uid, id string }{{"bob", "partition-bob-1"}, {"bob", "partition-bob-2"}, {"carol", "partition-carol"}} {
		c := connectTo(q.uid, q.id)
		ack, err := c.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic(q.uid), QoS: 1}}})
		require.NoError(t, err)
		require.Equal(t, []byte{1}, ack.Reasons)
		require.NoError(t, c.Close())
	}
	body := make([]byte, 2048)
	sendBody := func(uid, no string) map[string]any {
		return map[string]any{"from_uid": "alice", "channel_id": uid, "channel_type": 1, "client_msg_no": no, "payload": body}
	}
	accepted, err := suite.PostMessageSendEventually(ctx, first.APIAddr(), sendBody("bob", "partition-accepted"))
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), accepted.Reason)
	report["accepted"] = accepted
	snapshot := func() []float64 {
		out := make([]float64, 3)
		for i := range 3 {
			call, cancel := context.WithTimeout(ctx, 2*time.Second)
			out[i], err = suite.FetchMetricValue(call, cluster.MustNode(uint64(i+1)).APIAddr(), "wukongim_mqtt_storage_bytes", map[string]string{"state": "reserved"})
			cancel()
			require.NoError(t, err)
		}
		return out
	}
	baseline := snapshot()
	for _, v := range baseline {
		require.Positive(t, v)
		require.LessOrEqual(t, v, float64(8192))
	}
	report["reserved_before_partition"] = baseline
	bob := connect("partition-bob-1")
	original, err := bob.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, body, original.Payload)
	require.Equal(t, strconv.FormatInt(accepted.MessageID, 10), original.Properties.User.Get("wk.message_id"))
	report["unfinished_packet_id"] = original.PacketID

	phase = "partition"
	require.NoError(t, mesh.Isolate(3))
	t.Cleanup(func() { mesh.Heal() })
	started := time.Now()
	refusals := make([]map[string]any, 0, 3)
	for i := range 3 {
		call, cancel := context.WithTimeout(ctx, 8*time.Second)
		ack, sendErr := suite.PostMessageSend(call, cluster.MustNode(uint64(i+1)).APIAddr(), sendBody("carol", fmt.Sprintf("partition-denied-%d", i+1)))
		cancel()
		require.True(t, sendErr != nil || ack.Reason != uint8(frame.ReasonSuccess), "partition ingress %d accepted unfunded content: %+v", i+1, ack)
		refusals = append(refusals, map[string]any{"node": i + 1, "refused": true, "reason": ack.Reason})
	}
	report["ingress_refusals"] = refusals
	for time.Since(started) < 35*time.Second {
		values := snapshot()
		var total float64
		for i, v := range values {
			require.GreaterOrEqual(t, v, baseline[i], "partition refunded accepted debt")
			require.LessOrEqual(t, v, float64(8192))
			total += v
			_, exited := cluster.MustNode(uint64(i + 1)).Process.ExitResult()
			require.False(t, exited, "partition killed node %d", i+1)
		}
		require.LessOrEqual(t, total, float64(24576))
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
	report["partition_duration_ms"] = time.Since(started).Milliseconds()
	report["reserved_during_partition"] = snapshot()
	links := requirePartitionEvidence(t, ctx, cluster, mesh)
	report["partition_links"] = links
	report["surviving_slot_quorum"] = true

	phase = "recovery"
	mesh.Heal()
	ready()
	join, cancel := context.WithTimeout(ctx, 5*time.Second)
	require.NoError(t, bob.AbortAndWait(join))
	cancel()
	bob = connect("partition-bob-1")
	require.True(t, bob.Connack.SessionPresent)
	replay, err := bob.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, original.PacketID, replay.PacketID)
	require.True(t, replay.Duplicate())
	require.Equal(t, body, replay.Payload)
	require.Equal(t, original.Properties.User.Get("wk.message_id"), replay.Properties.User.Get("wk.message_id"))
	require.Equal(t, original.Properties.User.Get("wk.message_seq"), replay.Properties.User.Get("wk.message_seq"))
	require.NoError(t, bob.Client.Ack(replay))
	quiet, cancel := context.WithTimeout(ctx, 3*time.Second)
	_, err = bob.Receive(quiet)
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, baseline, snapshot(), "one subscriber freed another subscriber's body")
	report["persistent_replay_preserved"] = true
	other := connect("partition-bob-2")
	require.True(t, other.Connack.SessionPresent)
	p, err := other.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, body, p.Payload)
	require.Equal(t, replay.Properties.User.Get("wk.message_id"), p.Properties.User.Get("wk.message_id"))
	require.NoError(t, other.Client.Ack(p))
	phase = "retirement"
	require.Eventually(t, func() bool {
		for _, v := range snapshot() {
			if v != 0 {
				return false
			}
		}
		return true
	}, 30*time.Second, 100*time.Millisecond, "recovery never proved retirement")
	report["reserved_after_retirement"] = snapshot()
	reopened, err := suite.PostMessageSendEventually(ctx, first.APIAddr(), sendBody("bob", "partition-reopened"))
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSuccess), reopened.Reason)
	p, err = bob.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatInt(reopened.MessageID, 10), p.Properties.User.Get("wk.message_id"))
	require.Equal(t, body, p.Payload)
	require.NoError(t, bob.Client.Ack(p))
	p, err = other.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatInt(reopened.MessageID, 10), p.Properties.User.Get("wk.message_id"))
	require.NoError(t, other.Client.Ack(p))
	report["reopened"] = reopened
	for _, c := range []*suite.MQTTClient{bob, other} {
		quiet, cancel := context.WithTimeout(ctx, time.Second)
		_, err = c.Receive(quiet)
		cancel()
		require.ErrorIs(t, err, context.DeadlineExceeded)
	}
	require.Eventually(t, func() bool {
		for _, v := range snapshot() {
			if v != 0 {
				return false
			}
		}
		return true
	}, 30*time.Second, 100*time.Millisecond, "reopened completion left storage debt")
	report["final_reserved"] = snapshot()
	phase = "complete"
}

// requirePartitionEvidence distinguishes network isolation from mere request
// refusal and requires both survivors to agree on every actual Slot leader.
func requirePartitionEvidence(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, mesh *suite.ClusterTCPPartition) map[string]suite.TCPLinkObservation {
	t.Helper()
	links := mesh.Snapshot()
	require.Zero(t, links["3->3"].Closed+links["3->3"].Refused, "partition interrupted node-local TCP")
	for _, from := range []uint64{1, 2, 3} {
		for _, to := range []uint64{1, 2, 3} {
			if from == to || from != 3 && to != 3 {
				continue
			}
			v := links[fmt.Sprintf("%d->%d", from, to)]
			require.Positive(t, v.Forwarded, "direction %d->%d was never usable", from, to)
			require.Positive(t, v.Closed+v.Refused, "untested TCP direction %d->%d", from, to)
		}
	}
	leaders := make(map[uint32]uint64)
	for _, id := range []uint64{1, 2} {
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		var view struct {
			Items []suite.SlotDTO `json:"items"`
		}
		_, e := suite.GetJSON(call, fmt.Sprintf("http://%s/manager/slots?node_id=%d", cluster.MustNode(id).ManagerAddr(), id), &view)
		cancel()
		require.NoError(t, e)
		require.Len(t, view.Items, 3)
		for _, slot := range view.Items {
			require.NotNil(t, slot.NodeLog)
			require.Equal(t, id, slot.NodeLog.NodeID)
			require.True(t, slot.Runtime.HasQuorum)
			require.Contains(t, []uint64{1, 2}, slot.NodeLog.LeaderID)
			if id == 1 {
				leaders[slot.SlotID] = slot.NodeLog.LeaderID
			} else {
				require.Equal(t, leaders[slot.SlotID], slot.NodeLog.LeaderID, "survivors disagree on Slot %d", slot.SlotID)
			}
		}
	}
	return links
}
