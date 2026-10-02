//go:build e2e

package storage_partition

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

// A real prepared physical commit loses its reply during TCP isolation. Neither
// the request deadline nor Channel handle closure may return that node's debt.
func TestPartitionRetainsUnknownPreparationUntilResolved(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires temporary-copy gofail candidate")
	}
	dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	phase := "startup"
	report := map[string]any{"scenario": "mqtt-storage-partition-unknown", "nodes": 3, "hash_slots": 256, "node_bytes": 16384, "cluster_bytes": 32768, "resubscriptions": 0}
	t.Cleanup(func() {
		report["passed"], report["last_phase"] = !t.Failed(), phase
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0755))
		path := filepath.Join(dir, "mqtt-storage-partition-unknown.json")
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	mesh := suite.NewClusterTCPPartition(t)
	addresses := make([]string, 3)
	faults := make([]suite.GofailEndpoint, 3)
	opts := []suite.Option{suite.WithManagerHTTP(), suite.WithClusterTCPPartition(mesh), suite.WithNodeLogRootDir(filepath.Join(dir, "unknown-logs"))}
	for i := range 3 {
		addresses[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		faults[i] = suite.ReserveGofailEndpoint(t)
		opts = append(opts, suite.WithNodeEnv(uint64(i+1), faults[i].Env()), suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_SLOT_REPLICA_N": "3", "WK_CLUSTER_CHANNEL_REPLICA_N": "3", "WK_GATEWAY_TOKEN_AUTH_ON": "true",
			"WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addresses[i], "WK_METRICS_ENABLE": "true", "WK_MQTT_STORAGE_NODE_BYTES": "16384", "WK_MQTT_STORAGE_CLUSTER_BYTES": "32768",
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
	const fault = "wkMQTTStoragePrepareBeforeCommit"
	const committed = "wkMQTTStoragePrepareAfterCommit"
	for _, endpoint := range faults {
		inspect, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err := endpoint.WaitListed(inspect, fault, committed)
		cancel()
		require.NoError(t, err)
	}
	first := cluster.MustNode(1)
	for _, uid := range []string{"alice", "bob", "carol", "dave"} {
		_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-unknown-partition-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	connect := func(uid string) *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addresses[0], uid, uid+"-unknown-partition-token", "unknown-partition-"+uid, false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() {
			join, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, c.AbortAndWait(join))
		})
		return c
	}
	for _, uid := range []string{"bob", "carol", "dave"} {
		c := connect(uid)
		topic := "wk/v1/users/" + base64.RawURLEncoding.EncodeToString([]byte(uid)) + "/messages"
		ack, err := c.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 1}}})
		require.NoError(t, err)
		require.Equal(t, []byte{1}, ack.Reasons)
		require.NoError(t, c.Close())
	}
	body := make([]byte, 2048)
	request := func(uid, no string) map[string]any {
		return map[string]any{"from_uid": "alice", "channel_id": uid, "channel_type": 1, "client_msg_no": no, "payload": body}
	}
	accepted, err := suite.PostMessageSendEventually(ctx, first.APIAddr(), request("bob", "unknown-accepted"))
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSuccess), accepted.Reason)
	report["accepted"] = accepted
	snapshot := func() []float64 {
		values := make([]float64, 3)
		for i := range 3 {
			call, cancel := context.WithTimeout(ctx, 2*time.Second)
			v, err := suite.FetchMetricValue(call, cluster.MustNode(uint64(i+1)).APIAddr(), "wukongim_mqtt_storage_bytes", map[string]string{"state": "reserved"})
			cancel()
			require.NoError(t, err)
			values[i] = v
		}
		return values
	}
	baseline := snapshot()
	for _, v := range baseline {
		require.Positive(t, v)
	}
	report["reserved_before_unknown"] = baseline
	// Sleep does not replace the commit or fabricate a transport error. It delays
	// the existing boundary, then the actual batch.Commit still executes normally.
	phase = "delayed-prepare"
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
	require.NoError(t, faults[2].Enable(call, fault, "1*sleep(12000)"))
	require.NoError(t, faults[2].Enable(call, committed, "return(true)"))
	before, err := faults[2].Count(call, fault)
	require.NoError(t, err)
	committedBefore, err := faults[2].Count(call, committed)
	require.NoError(t, err)
	cancel()
	type outcome struct {
		ack suite.MessageSendResponse
		err error
	}
	result := make(chan outcome, 1)
	call, cancel = context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	go func() {
		ack, e := suite.PostMessageSend(call, first.APIAddr(), request("carol", "unknown-prepared"))
		result <- outcome{ack, e}
	}()
	require.Eventually(t, func() bool {
		inspect, stop := context.WithTimeout(ctx, time.Second)
		n, e := faults[2].Count(inspect, fault)
		stop()
		return e == nil && n > before
	}, 8*time.Second, 20*time.Millisecond, "physical preparation boundary not exercised")
	require.NoError(t, mesh.Isolate(3))
	t.Cleanup(func() { mesh.Heal() })
	phase = "unknown-partition"
	select {
	case out := <-result:
		require.True(t, out.err != nil || out.ack.Reason != uint8(frame.ReasonSuccess), "original was admitted despite incomplete funding")
		report["unknown_request_refused"] = true
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A reservation gauge predates the physical commit. Require a separate
	// successful post-commit witness while the reply path is still disconnected.
	require.Eventually(t, func() bool {
		inspect, stop := context.WithTimeout(ctx, time.Second)
		n, e := faults[2].Count(inspect, committed)
		stop()
		return e == nil && n > committedBefore
	}, 20*time.Second, 100*time.Millisecond, "delayed physical preparation never committed under isolation")
	inspect, stop := context.WithTimeout(ctx, time.Second)
	committedAfter, err := faults[2].Count(inspect, committed)
	stop()
	require.NoError(t, err)
	require.Equal(t, committedBefore+1, committedAfter)
	report["physical_commit_witness_before_heal"] = true
	report["charged_commits_before_heal"] = committedAfter - committedBefore
	require.Eventually(t, func() bool { return snapshot()[2] > baseline[2] }, 20*time.Second, 100*time.Millisecond, "no retained physical uncertainty on isolated node")
	unknown := snapshot()
	report["reserved_unknown"] = unknown
	observed := time.Now()
	for time.Since(observed) < 10*time.Second {
		values := snapshot()
		require.GreaterOrEqual(t, values[2], unknown[2], "network absence returned uncertain preparation credit")
		var total float64
		for _, v := range values {
			require.LessOrEqual(t, v, float64(16384))
			total += v
		}
		require.LessOrEqual(t, total, float64(32768))
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
	report["retained_unknown_observation_ms"] = time.Since(observed).Milliseconds()
	call, cancel = context.WithTimeout(ctx, 8*time.Second)
	denied, sendErr := suite.PostMessageSend(call, cluster.MustNode(2).APIAddr(), request("dave", "unknown-extra"))
	cancel()
	require.True(t, sendErr != nil || denied.Reason != uint8(frame.ReasonSuccess))
	report["additional_source_refused"] = true
	report["partition_links"] = requirePartitionEvidence(t, ctx, cluster, mesh)
	report["surviving_slot_quorum"] = true
	for i := range 3 {
		_, exited := cluster.MustNode(uint64(i + 1)).Process.ExitResult()
		require.False(t, exited)
	}

	phase = "resolve"
	mesh.Heal()
	ready()
	carol := connect("carol")
	require.True(t, carol.Connack.SessionPresent)
	quiet, stop := context.WithTimeout(ctx, 3*time.Second)
	_, err = carol.Receive(quiet)
	stop()
	require.ErrorIs(t, err, context.DeadlineExceeded, "unknown funding dispatched an original")
	report["unknown_source_quiet_before_retry"] = true
	resolved, err := suite.PostMessageSendEventually(ctx, first.APIAddr(), request("carol", "unknown-prepared"))
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), resolved.Reason)
	p, err := carol.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, body, p.Payload)
	require.Equal(t, strconv.FormatInt(resolved.MessageID, 10), p.Properties.User.Get("wk.message_id"))
	require.NoError(t, carol.Client.Ack(p))
	bob := connect("bob")
	require.True(t, bob.Connack.SessionPresent)
	p, err = bob.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, body, p.Payload)
	require.Equal(t, strconv.FormatInt(accepted.MessageID, 10), p.Properties.User.Get("wk.message_id"))
	require.NoError(t, bob.Client.Ack(p))
	report["resolved"] = resolved
	phase = "retirement"
	require.Eventually(t, func() bool {
		for _, v := range snapshot() {
			if v != 0 {
				return false
			}
		}
		return true
	}, 30*time.Second, 100*time.Millisecond, "resolved unknown debt never retired")
	report["reserved_after_retirement"] = snapshot()
	final, err := suite.PostMessageSendEventually(ctx, first.APIAddr(), request("dave", "unknown-reopened"))
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSuccess), final.Reason)
	dave := connect("dave")
	require.True(t, dave.Connack.SessionPresent)
	p, err = dave.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatInt(final.MessageID, 10), p.Properties.User.Get("wk.message_id"))
	require.Equal(t, body, p.Payload)
	require.NoError(t, dave.Client.Ack(p))
	for _, c := range []*suite.MQTTClient{carol, bob, dave} {
		quiet, stop := context.WithTimeout(ctx, time.Second)
		_, err = c.Receive(quiet)
		stop()
		require.ErrorIs(t, err, context.DeadlineExceeded)
	}
	call, cancel = context.WithTimeout(ctx, 2*time.Second)
	hits, err := faults[2].Count(call, fault)
	cancel()
	require.NoError(t, err)
	require.Equal(t, before+1, hits)
	report["physical_cut_hits"] = hits - before
	report["reopened"] = final
	require.Eventually(t, func() bool {
		for _, v := range snapshot() {
			if v != 0 {
				return false
			}
		}
		return true
	}, 30*time.Second, 100*time.Millisecond, "reopened completion left unknown storage debt")
	report["final_reserved"] = snapshot()
	phase = "complete"
}
