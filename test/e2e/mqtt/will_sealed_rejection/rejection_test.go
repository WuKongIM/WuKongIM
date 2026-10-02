//go:build e2e && (darwin || linux)

package will_sealed_rejection

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

func TestSealedStartedRejectionReleasesCapacity(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires a temporary gofail product binary")
	}
	require.NotEmpty(t, os.Getenv("WK_E2E_BINARY"))
	for _, nodes := range []int{1, 3} {
		topology := "single-node-cluster"
		if nodes == 3 {
			topology = "three-node-cluster"
		}
		for _, cut := range []string{"reserved", "admitted"} {
			t.Run(topology+"/"+cut, func(t *testing.T) { runRejection(t, nodes, cut) })
		}
	}
}

func runRejection(t *testing.T, nodes int, cut string) {
	phase := "startup"
	report := map[string]any{"scenario": "mqtt-will-sealed-rejection", "nodes": nodes, "hash_slots": 256, "logical_slot_groups": 1, "journal_capacity": 1, "cut": cut, "connect_retries": 0, "resubscriptions": 0}
	dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	// Registered first: evidence runs after joined clients and product processes.
	t.Cleanup(func() {
		report["passed"], report["last_phase"] = !t.Failed(), phase
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0700))
		path := filepath.Join(dir, fmt.Sprintf("mqtt-will-sealed-%d-%s.json", nodes, cut))
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	const capacity = "wkMQTTWillJournalLimit"
	const full = "wkMQTTWillJournalFull"
	const publication = "wkMQTTWillPublicationAttempt"
	faultName := "wkMQTTWillAfterStarted"
	if cut == "admitted" {
		faultName = "wkMQTTWillAfterAdmitted"
	}
	var opts []suite.Option
	addresses := make([]string, nodes)
	faults := make([]suite.GofailEndpoint, nodes)
	for i := range nodes {
		addresses[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		faults[i] = suite.ReserveGofailEndpoint(t)
		opts = append(opts, suite.WithNodeEnv(uint64(i+1), faults[i].Env(), "GOFAIL_FAILPOINTS="+capacity+"=return(1);"+full+"=return(true);"+publication+"=return(true)"), suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "1", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addresses[i]}))
	}
	cluster := suite.New(t).StartStaticCluster(nodes, append(opts, suite.WithManagerHTTP())...)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	ready := func() {
		call, done := context.WithTimeout(ctx, 30*time.Second)
		defer done()
		require.NoError(t, cluster.WaitClusterReady(call), cluster.DumpDiagnostics())
		_, err := cluster.WaitSlotLeadersStable(call, time.Second)
		require.NoError(t, err, cluster.DumpDiagnostics())
	}
	ready()
	for _, f := range faults {
		call, done := context.WithTimeout(ctx, 3*time.Second)
		_, err := f.WaitListed(call, faultName, capacity, full, publication)
		done()
		require.NoError(t, err, "instrumentation prerequisite, not product RED")
		require.NoError(t, f.Enable(ctx, faultName, "sleep(30000)"))
	}
	api := "http://" + cluster.MustNode(1).APIAddr()
	for _, uid := range []string{"alice", "bob", "charlie"} {
		_, err := suite.PostJSON(ctx, api+"/user/token", map[string]any{"uid": uid, "token": uid + "-sealed-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	abort := func(c *suite.MQTTClient) {
		call, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		require.NoError(t, c.AbortAndWait(call))
	}
	connectRecipient := func() *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addresses[0], "bob", "bob-sealed-token", "sealed-recipient", false, 600)
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() { abort(c) })
		return c
	}
	bob := connectRecipient()
	require.False(t, bob.Connack.SessionPresent)
	sub, err := bob.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
	require.NoError(t, err)
	require.Equal(t, []byte{1}, sub.Reasons)
	publish := func(uid, body string) {
		c, err := suite.ConnectMQTT(ctx, addresses[nodes-1], uid, uid+"-sealed-token", "sealed-"+uid, true, 60, suite.MQTTConnectOptions{Will: &paho.WillMessage{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Payload: []byte(body)}, WillProperties: &paho.WillProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: body}}}})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() { abort(c) })
		abort(c)
	}
	quiet := func(duration time.Duration) {
		call, done := context.WithTimeout(ctx, duration)
		_, err := bob.Receive(call)
		done()
		require.ErrorIs(t, err, context.DeadlineExceeded, "silence must not mask a closed receiver")
	}
	phase = "hold-unissued-started"
	publish("alice", "revoked-original")
	executorIndex := -1
	require.Eventually(t, func() bool {
		for i, f := range faults {
			n, err := f.Count(ctx, faultName)
			if err == nil && n > 0 {
				executorIndex = i
				return true
			}
		}
		return false
	}, 20*time.Second, 50*time.Millisecond)
	for _, f := range faults {
		require.NoError(t, f.Disable(ctx, faultName))
	}
	report["executor_node"] = executorIndex + 1
	phase = "revoke-before-release"
	setBan := func(value int) {
		require.NoError(t, suite.PostChannel(ctx, cluster.MustNode(1).APIAddr(), map[string]any{"channel_id": "alice", "channel_type": 1, "send_ban": value}))
	}
	setBan(1)
	report["public_permission_revoked"] = true
	publish("charlie", "legal-after-pressure")
	require.Eventually(t, func() bool {
		n, err := faults[executorIndex].Count(ctx, full)
		return err == nil && n > 0
	}, 20*time.Second, 50*time.Millisecond, "same captured executor must refuse the full journal")
	report["captured_capacity_refusal"] = true
	quiet(3 * time.Second)
	phase = "legal-will-after-sealed-rejection"
	call, done := context.WithTimeout(ctx, 90*time.Second)
	p, err := bob.Receive(call)
	done()
	require.NoError(t, err, "sealed denied Will must free the captured executor's capacity: %s", cluster.DumpDiagnostics())
	require.Equal(t, []byte("legal-after-pressure"), p.Payload)
	require.Equal(t, byte(1), p.QoS)
	require.False(t, p.Duplicate())
	require.NotNil(t, p.Properties)
	require.Equal(t, "charlie", p.Properties.User.Get("wk.from_uid"))
	require.Equal(t, "legal-after-pressure", p.Properties.User.Get("wk.client_msg_no"))
	require.NotEmpty(t, p.Properties.User.Get("wk.message_id"))
	require.NotEmpty(t, p.Properties.User.Get("wk.message_seq"))
	n, err := faults[executorIndex].Count(ctx, publication)
	require.NoError(t, err)
	require.Equal(t, 1, n, "only the legal Will may attempt publication on the captured executor")
	report["captured_publication_attempts"], report["legal_message_id"] = n, p.Properties.User.Get("wk.message_id")
	quiet(3 * time.Second)
	phase = "restore-permission-and-restart"
	setBan(0)
	abort(bob)
	executor := cluster.MustNode(uint64(executorIndex + 1))
	require.NoError(t, syscall.Kill(-executor.Process.Cmd.Process.Pid, syscall.SIGKILL))
	select {
	case <-executor.Process.Done():
	case <-ctx.Done():
		t.Fatal("exact executor failed to exit")
	}
	_ = executor.Process.Stop()
	require.NoError(t, cluster.StartStoppedNode(uint64(executorIndex+1)), cluster.DumpDiagnostics())
	ready()
	bob = connectRecipient()
	require.True(t, bob.Connack.SessionPresent, "persistent subscription must survive without SUBSCRIBE")
	phase = "quiet-after-permission-restoration"
	quiet(21 * time.Second)
	report["executor_restarts"], report["business_publications"], report["unexpected_deliveries"], report["post_restart_quiet_ms"] = 1, 1, 0, 21000
	phase = "complete"
}
