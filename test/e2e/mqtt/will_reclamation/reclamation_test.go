//go:build e2e && (darwin || linux)

package will_reclamation

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

func TestWillJournalPressurePreservesCurrentRecovery(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires a temporary gofail product binary")
	}
	require.NotEmpty(t, os.Getenv("WK_E2E_BINARY"))
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) { runReclamation(t, count) })
	}
}

func runReclamation(t *testing.T, count int) {
	phase := "startup"
	report := map[string]any{"scenario": "mqtt-will-journal-reclamation", "nodes": count, "hash_slots": 256, "instrumented_journal_cap": 2, "resubscriptions": 0, "connect_retries": 0}
	dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	t.Cleanup(func() {
		report["passed"], report["last_phase"] = !t.Failed(), phase
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0700))
		path := filepath.Join(dir, fmt.Sprintf("mqtt-will-reclamation-%d.json", count))
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	const started = "wkMQTTWillAfterStarted"
	const capacity = "wkMQTTWillJournalLimit"
	const full = "wkMQTTWillJournalFull"
	const cleanup = "wkMQTTWillReleaseFailure"
	var opts []suite.Option
	addrs := make([]string, count)
	faults := make([]suite.GofailEndpoint, count)
	for i := range count {
		addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		faults[i] = suite.ReserveGofailEndpoint(t)
		opts = append(opts, suite.WithNodeEnv(uint64(i+1), faults[i].Env()), suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i]}))
	}
	cluster := suite.New(t).StartStaticCluster(count, append(opts, suite.WithManagerHTTP())...)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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
		_, err := f.WaitListed(call, started, capacity, full, cleanup)
		done()
		require.NoError(t, err)
		require.NoError(t, f.Enable(ctx, capacity, `return(2)`))
		require.NoError(t, f.Enable(ctx, full, `return(true)`))
		require.NoError(t, f.Enable(ctx, cleanup, `return(true)`))
		require.NoError(t, f.Enable(ctx, started, `sleep(60000)`))
	}
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-reclamation-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	connectRecipient := func() *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addrs[0], "bob", "bob-reclamation-token", "will-reclamation-recipient", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() { _ = c.Abort() })
		return c
	}
	bob := connectRecipient()
	require.False(t, bob.Connack.SessionPresent)
	sub, err := bob.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
	require.NoError(t, err)
	require.Equal(t, []byte{1}, sub.Reasons)
	publishWill := func(body string) {
		alice, err := suite.ConnectMQTT(ctx, addrs[count-1], "alice", "alice-reclamation-token", "will-reclamation-publisher", true, 60, suite.MQTTConnectOptions{Will: &paho.WillMessage{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Payload: []byte(body)}, WillProperties: &paho.WillProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: body}}}})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() { _ = alice.Abort() })
		require.NoError(t, alice.Abort())
	}
	receive := func(body string) *paho.Publish {
		call, done := context.WithTimeout(ctx, 35*time.Second)
		defer done()
		p, err := bob.Receive(call)
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Equal(t, byte(1), p.QoS)
		require.False(t, p.Duplicate())
		require.Equal(t, []byte(body), p.Payload)
		require.NotNil(t, p.Properties)
		require.Equal(t, body, p.Properties.User.Get("wk.client_msg_no"))
		require.NotEmpty(t, p.Properties.User.Get("wk.message_id"))
		require.NoError(t, bob.Client.Ack(p))
		return p
	}
	phase = "retain-current-started"
	publishWill("current-original")
	executorIndex := -1
	require.Eventually(t, func() bool {
		for i, f := range faults {
			call, done := context.WithTimeout(ctx, time.Second)
			n, err := f.Count(call, started)
			done()
			if err == nil && n > 0 {
				executorIndex = i
				return true
			}
		}
		return false
	}, 20*time.Second, 50*time.Millisecond)
	for _, f := range faults {
		require.NoError(t, f.Disable(ctx, started))
	}
	phase = "lose-terminal-cleanup"
	publishWill("terminal-original")
	terminal := receive("terminal-original")
	require.Eventually(t, func() bool {
		n, err := faults[executorIndex].Count(ctx, cleanup)
		return err == nil && n > 0
	}, 5*time.Second, 50*time.Millisecond)
	for _, f := range faults {
		require.NoError(t, f.Disable(ctx, cleanup))
	}
	report["terminal_cleanup_failed"] = true
	phase = "publish-through-full-journal"
	publishWill("after-pressure")
	require.Eventually(t, func() bool {
		n, err := faults[executorIndex].Count(ctx, full)
		return err == nil && n > 0
	}, 20*time.Second, 50*time.Millisecond, "the captured executor must refuse its full journal")
	report["executor_capacity_refusal"] = true
	after := receive("after-pressure")
	require.NotEqual(t, terminal.Properties.User.Get("wk.message_id"), after.Properties.User.Get("wk.message_id"))
	report["publication_after_pressure"] = true
	// Paho batches manual PUBACKs. Observe a healthy quiet connection before
	// killing its node so the test does not create another unfinished exchange.
	ackWindow, ackDone := context.WithTimeout(ctx, 3*time.Second)
	_, err = bob.Receive(ackWindow)
	ackDone()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	phase = "restart-retained-current-attempt"
	executor := cluster.MustNode(uint64(executorIndex + 1))
	require.NoError(t, syscall.Kill(-executor.Process.Cmd.Process.Pid, syscall.SIGKILL))
	select {
	case <-executor.Process.Done():
	case <-ctx.Done():
		t.Fatal("executor process did not exit")
	}
	_ = executor.Process.Stop()
	require.NoError(t, cluster.StartStoppedNode(uint64(executorIndex+1)), cluster.DumpDiagnostics())
	ready()
	_ = bob.Abort()
	bob = connectRecipient()
	require.True(t, bob.Connack.SessionPresent)
	current := receive("current-original")
	require.NotEqual(t, after.Properties.User.Get("wk.message_id"), current.Properties.User.Get("wk.message_id"))
	report["current_attempt_preserved"], report["executor_restarts"] = true, 1
	phase = "quiet-after-completion"
	quiet, done := context.WithTimeout(ctx, 15*time.Second)
	_, err = bob.Receive(quiet)
	done()
	require.ErrorIs(t, err, context.DeadlineExceeded, "quiet observation must not mask transport closure")
	report["business_publications"], report["unexpected_deliveries"] = 3, 0
	phase = "complete"
}
