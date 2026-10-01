//go:build e2e && (darwin || linux)

package will_recovery

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

const startedCut = "wkMQTTWillAfterStarted"
const publishedCut = "wkMQTTWillAfterPublication"
const publishAttempt = "wkMQTTWillPublicationAttempt"
const acceptedDelay = "wkMQTTWillAcceptedAppendDelay"

// Started recovery must distinguish a sealed non-dispatch from an unknown effect.
func TestStartedWillRecoveryPreservesOnePublication(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires a temporary gofail product binary")
	}
	require.NotEmpty(t, os.Getenv("WK_E2E_BINARY"))
	for _, count := range []int{1, 3} {
		for _, cut := range []string{"undispatched", "published", "admitted"} {
			t.Run(fmt.Sprintf("%d-node-cluster/%s", count, cut), func(t *testing.T) { runRecovery(t, count, cut) })
		}
	}
}

func runRecovery(t *testing.T, count int, cut string) {
	phase := "startup"
	report := map[string]any{"scenario": "mqtt-started-will-recovery", "nodes": count, "hash_slots": 256, "cut": cut, "resubscriptions": 0, "connect_retries": 0}
	dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	t.Cleanup(func() {
		report["passed"], report["last_phase"] = !t.Failed(), phase
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0700))
		path := filepath.Join(dir, fmt.Sprintf("mqtt-will-recovery-%d-%s.json", count, cut))
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	var opts []suite.Option
	addrs := make([]string, count)
	faults := make([]suite.GofailEndpoint, count)
	for i := range count {
		addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		faults[i] = suite.ReserveGofailEndpoint(t)
		opts = append(opts, suite.WithNodeEnv(uint64(i+1), faults[i].Env()), suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i]}))
	}
	s := suite.New(t)
	cluster := s.StartStaticCluster(count, append(opts, suite.WithManagerHTTP())...)
	first := cluster.MustNode(1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
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
		_, err := f.WaitListed(call, startedCut, publishedCut, publishAttempt, acceptedDelay)
		done()
		require.NoError(t, err)
	}
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-will-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	connectRecipient := func() *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addrs[0], "bob", "bob-will-token", "will-persistent-recipient", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, first.DumpDiagnostics())
		t.Cleanup(func() { _ = c.Abort() })
		return c
	}
	bob := connectRecipient()
	require.False(t, bob.Connack.SessionPresent)
	sub, err := bob.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
	require.NoError(t, err)
	require.Equal(t, []byte{1}, sub.Reasons)
	faultName, expression := startedCut, `sleep(60000)`
	if cut == "published" {
		faultName = publishedCut
	} else if cut == "admitted" {
		faultName, expression = acceptedDelay, `return(30000)`
	}
	for _, f := range faults {
		require.NoError(t, f.Enable(ctx, publishAttempt, `return(true)`))
		require.NoError(t, f.Enable(ctx, faultName, expression))
	}
	delay := uint32(1)
	alice, err := suite.ConnectMQTT(ctx, addrs[count-1], "alice", "alice-will-token", "will-crash-publisher", true, 60, suite.MQTTConnectOptions{Will: &paho.WillMessage{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Payload: []byte("original-will")}, WillProperties: &paho.WillProperties{WillDelayInterval: &delay, User: paho.UserProperties{{Key: "wk.client_msg_no", Value: "will-original"}}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = alice.Abort() })
	require.NoError(t, alice.Abort())
	phase = "reach-cut"
	cutNode := -1
	require.Eventually(t, func() bool {
		for i, f := range faults {
			call, done := context.WithTimeout(ctx, time.Second)
			n, e := f.Count(call, faultName)
			done()
			if e == nil && n > 0 {
				cutNode = i
				return true
			}
		}
		return false
	}, 20*time.Second, 50*time.Millisecond, "the exact publication cut must execute")
	report["cut_reached"] = true
	var original *paho.Publish
	receive := func(c *suite.MQTTClient) *paho.Publish {
		call, done := context.WithTimeout(ctx, 35*time.Second)
		defer done()
		p, e := c.Receive(call)
		require.NoError(t, e, first.DumpDiagnostics())
		require.Equal(t, byte(1), p.QoS)
		require.Equal(t, []byte("original-will"), p.Payload)
		require.NotNil(t, p.Properties)
		require.Equal(t, "alice", p.Properties.User.Get("wk.from_uid"))
		require.Equal(t, "will-original", p.Properties.User.Get("wk.client_msg_no"))
		require.NotEmpty(t, p.Properties.User.Get("wk.message_id"))
		require.NotEmpty(t, p.Properties.User.Get("wk.message_seq"))
		return p
	}
	quiet := func(c *suite.MQTTClient, duration time.Duration) {
		call, done := context.WithTimeout(ctx, duration)
		_, e := c.Receive(call)
		done()
		require.ErrorIs(t, e, context.DeadlineExceeded, "quiet observation must not mask a closed receiver")
	}
	if cut == "admitted" {
		phase = "unknown-admitted-effect"
		control, e := suite.ConnectMQTT(ctx, addrs[0], "bob", "bob-will-token", "will-control", true, 0)
		require.NoError(t, e)
		require.NoError(t, control.Close())
		report["independent_connect_control"] = true
		quiet(bob, 21*time.Second)
		var attempts int
		for _, f := range faults {
			n, e := f.Count(ctx, publishAttempt)
			require.NoError(t, e)
			attempts += n
		}
		require.Equal(t, 1, attempts, "elapsed lease or missing receipt cannot authorize another publish")
		report["attempts_while_unknown"], report["unknown_effect_window_ms"] = attempts, 21000
		for _, f := range faults {
			require.NoError(t, f.Disable(ctx, faultName))
		}
	} else {
		if cut == "published" {
			original = receive(bob) // Leave its begun exchange unacknowledged.
			require.False(t, original.Duplicate())
		}
		for _, f := range faults {
			require.NoError(t, f.Disable(ctx, faultName))
		}
		phase = "executor-crash-and-restart"
		executor := cluster.MustNode(uint64(cutNode + 1))
		require.NoError(t, syscall.Kill(-executor.Process.Cmd.Process.Pid, syscall.SIGKILL))
		select {
		case <-executor.Process.Done():
		case <-ctx.Done():
			t.Fatal("executor process did not exit")
		}
		_ = executor.Process.Stop()
		require.NoError(t, cluster.StartStoppedNode(uint64(cutNode+1)), cluster.DumpDiagnostics())
		first = cluster.MustNode(1)
		ready()
		report["executor_restarts"] = 1
		_ = bob.Abort() // The executor crash may already have closed this transport.
		bob = connectRecipient()
		require.True(t, bob.Connack.SessionPresent, "recovery must preserve its existing subscription")
	}
	phase = "original-will-delivery"
	p := receive(bob)
	if original != nil {
		require.True(t, p.Duplicate())
		require.Equal(t, original.PacketID, p.PacketID)
		require.Equal(t, original.Properties.User.Get("wk.message_id"), p.Properties.User.Get("wk.message_id"))
		require.Equal(t, original.Properties.User.Get("wk.message_seq"), p.Properties.User.Get("wk.message_seq"))
		report["original_exchange_preserved"] = true
	} else {
		require.False(t, p.Duplicate())
	}
	require.NoError(t, bob.Client.Ack(p))
	phase = "quiet-after-completion"
	quiet(bob, 15*time.Second)
	report["business_publications"], report["unexpected_deliveries"] = 1, 0
	phase = "complete"
}
