//go:build e2e && (darwin || linux)

package will_reclamation_races

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

const startedCut = "wkMQTTWillAfterStarted"
const startedReply = "wkMQTTWillStartedCASReply"
const successorReply = "wkMQTTWillSuccessorCASReply"
const terminalReply = "wkMQTTWillTerminalCASReply"
const beforeRelease = "wkMQTTWillReclamationBeforeRelease"
const full = "wkMQTTWillJournalFull"
const publicationAttempt = "wkMQTTWillPublicationAttempt"
const afterPublication = "wkMQTTWillAfterPublication"
const beforeAppend = "wkMQTTWillBeforeAppendAdmission"
const afterAppend = "wkMQTTWillAfterAppendAdmission"

func TestWillReclamationRetainsDelayedCASRecovery(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires a temporary gofail product binary")
	}
	require.NotEmpty(t, os.Getenv("WK_E2E_BINARY"))
	for _, count := range []int{1, 3} {
		for _, cut := range []string{"started-reply", "successor-reply", "late-release"} {
			topology := "single-node-cluster"
			if count == 3 {
				topology = "three-node-cluster"
			}
			t.Run(fmt.Sprintf("%s/%s", topology, cut), func(t *testing.T) { runRace(t, count, cut) })
		}
	}
}

func runRace(t *testing.T, count int, cut string) {
	started := time.Now()
	phase := "startup"
	report := map[string]any{"scenario": "mqtt-will-reclamation-race", "nodes": count, "hash_slots": 256, "cut": cut, "instrumented_journal_cap": 2, "connect_retries": 0, "resubscriptions": 0}
	dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	t.Cleanup(func() {
		report["passed"], report["last_phase"] = !t.Failed(), phase
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0700))
		path := filepath.Join(dir, fmt.Sprintf("mqtt-will-race-%d-%s.json", count, cut))
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	var opts []suite.Option
	if logs := os.Getenv("WK_E2E_MQTT_LOG_DIR"); logs != "" {
		opts = append(opts, suite.WithNodeLogRootDir(logs))
	}
	addrs := make([]string, count)
	faults := make([]suite.GofailEndpoint, count)
	for i := range count {
		addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		faults[i] = suite.ReserveGofailEndpoint(t)
		startup := []string{"wkMQTTWillJournalLimit=return(2)", full + "=return(true)", publicationAttempt + "=return(true)", afterPublication + "=return(false)", beforeAppend + "=return(false)", afterAppend + "=return(false)"}
		if cut == "successor-reply" {
			startup = append(startup, successorReply+"=1*sleep(120000)->return(false)")
		}
		nodeConfig := map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i]}
		nodeEnv := []string{faults[i].Env(), "GOFAIL_FAILPOINTS=" + strings.Join(startup, ";")}
		if os.Getenv("WK_E2E_MQTT_LOG_DIR") != "" {
			nodeEnv = append(nodeEnv, "WK_DEBUG_API_ENABLE=true")
		}
		opts = append(opts, suite.WithNodeEnv(uint64(i+1), nodeEnv...), suite.WithNodeConfigOverrides(uint64(i+1), nodeConfig))
	}
	cluster := suite.New(t).StartStaticCluster(count, append(opts, suite.WithManagerHTTP())...)
	defer func() {
		// Capture bounded controls before process cleanup, even after a failed
		// assertion. Raw diagnostic logs remain outside the body-free receipt.
		call, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		counts := make([]map[string]int, count)
		for i, f := range faults {
			counts[i] = make(map[string]int, 10)
			for _, name := range []string{startedCut, startedReply, successorReply, terminalReply, beforeRelease, full, publicationAttempt, afterPublication, beforeAppend, afterAppend} {
				if n, err := f.Count(call, name); err == nil {
					counts[i][name] = n
				}
			}
		}
		report["final_control_counts"] = counts
		report["elapsed_ms"] = time.Since(started).Milliseconds()
	}()
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
		_, err := f.WaitListed(call, startedCut, startedReply, successorReply, terminalReply, beforeRelease, full, publicationAttempt)
		done()
		require.NoError(t, err)
	}
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-race-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	abort := func(c *suite.MQTTClient) {
		call, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		require.NoError(t, c.AbortAndWait(call))
	}
	connectRecipient := func() *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addrs[0], "bob", "bob-race-token", "will-race-recipient", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() { abort(c) })
		return c
	}
	bob := connectRecipient()
	require.False(t, bob.Connack.SessionPresent)
	sub, err := bob.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
	require.NoError(t, err)
	require.Equal(t, []byte{1}, sub.Reasons)
	set := func(name, expression string) {
		for _, f := range faults {
			require.NoError(t, f.Enable(ctx, name, expression))
		}
	}
	disable := func(name string) {
		for _, f := range faults {
			require.NoError(t, f.Disable(ctx, name))
		}
	}
	waitCut := func(name string, minimum int) int {
		index := -1
		require.Eventually(t, func() bool {
			for i, f := range faults {
				call, done := context.WithTimeout(ctx, time.Second)
				n, e := f.Count(call, name)
				done()
				if e == nil && n >= minimum {
					index = i
					return true
				}
			}
			return false
		}, 25*time.Second, 50*time.Millisecond, "exact process cut must execute")
		return index
	}
	restart := func(index int) {
		// Close the recipient before the recovering worker starts, so an old
		// live transport cannot receive an unobserved original during readiness.
		abort(bob)
		executor := cluster.MustNode(uint64(index + 1))
		require.NoError(t, syscall.Kill(-executor.Process.Cmd.Process.Pid, syscall.SIGKILL))
		select {
		case <-executor.Process.Done():
		case <-ctx.Done():
			t.Fatal("executor did not exit")
		}
		_ = executor.Process.Stop()
		require.NoError(t, cluster.StartStoppedNode(uint64(index+1)), cluster.DumpDiagnostics())
		ready()
		bob = connectRecipient()
		require.True(t, bob.Connack.SessionPresent)
	}
	publish := func(body string) {
		c, err := suite.ConnectMQTT(ctx, addrs[count-1], "alice", "alice-race-token", "will-race-publisher", true, 60, suite.MQTTConnectOptions{Will: &paho.WillMessage{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Payload: []byte(body)}, WillProperties: &paho.WillProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: body}}}})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() { abort(c) })
		abort(c)
	}
	identities := make(map[string]struct{}, 3)
	receive := func(body string, bound time.Duration) {
		waiting := time.Now()
		call, done := context.WithTimeout(ctx, bound)
		p, e := bob.Receive(call)
		done()
		if body == "after-pressure" {
			report["after_pressure_wait_ms"] = time.Since(waiting).Milliseconds()
			report["after_pressure_receipt_bound_ms"] = bound.Milliseconds()
		}
		if e != nil {
			// A bounded public history page distinguishes a committed message awaiting
			// delivery from an unresolved SEND, without inspecting storage.
			inspect, finish := context.WithTimeout(context.Background(), 3*time.Second)
			var lookup struct {
				Messages []struct {
					Payload []byte `json:"payload"`
				} `json:"messages"`
			}
			_, lookupErr := suite.PostJSON(inspect, "http://"+cluster.MustNode(1).APIAddr()+"/channel/messagesync", map[string]any{"login_uid": "bob", "channel_id": "alice", "channel_type": 1, "limit": 8}, &lookup)
			finish()
			report["timeout_lookup_succeeded"] = lookupErr == nil
			if lookupErr == nil {
				matched := 0
				for _, m := range lookup.Messages {
					if string(m.Payload) == body {
						matched++
					}
				}
				report["timeout_history_messages"] = len(lookup.Messages)
				report["timeout_committed_messages"] = matched
			}
			if logs := os.Getenv("WK_E2E_MQTT_LOG_DIR"); logs != "" {
				inspect, finish := context.WithTimeout(context.Background(), 3*time.Second)
				_ = suite.WriteGoroutineStacks(inspect, "http://"+cluster.MustNode(1).APIAddr(), filepath.Join(logs, fmt.Sprintf("timeout-%d-%s-goroutines.txt", count, cut)))
				finish()
			}
		}
		require.NoError(t, e, cluster.DumpDiagnostics())
		require.Equal(t, byte(1), p.QoS)
		require.Equal(t, []byte(body), p.Payload)
		if body != "current-original" {
			require.False(t, p.Duplicate())
		} else {
			// A begun exchange may precede fixture reconnect. DUP describes that
			// QoS exchange; distinct identity and quiet still forbid a new Will.
			report["original_exchange_replayed"] = p.Duplicate()
		}
		require.NotNil(t, p.Properties)
		require.Equal(t, body, p.Properties.User.Get("wk.client_msg_no"))
		require.Equal(t, "alice", p.Properties.User.Get("wk.from_uid"))
		identity := p.Properties.User.Get("wk.message_id")
		require.NotEmpty(t, identity)
		_, duplicate := identities[identity]
		require.False(t, duplicate)
		identities[identity] = struct{}{}
		require.NoError(t, bob.Client.Ack(p))
	}
	quiet := func(duration time.Duration) {
		if duration <= 0 {
			return
		}
		call, done := context.WithTimeout(ctx, duration)
		_, e := bob.Receive(call)
		done()
		require.ErrorIs(t, e, context.DeadlineExceeded, "quiet must not mask a closed recipient")
	}
	initialCut := startedCut
	if cut == "started-reply" {
		initialCut = startedReply
	}
	set(initialCut, "1*sleep(120000)->return(false)")
	phase = "retain-current-at-CAS"
	publish("current-original")
	executor := waitCut(initialCut, 1)
	disable(initialCut)
	if cut == "successor-reply" {
		phase = "restart-before-successor"
		restart(executor)
		executor = waitCut(successorReply, 1)
		// gofail holds its term lock during sleep. Clear future acquisitions
		// while the selected current call keeps its original delayed action.
		disable(successorReply)
		report["executor_restarts"] = 1
	}
	cutAt := time.Now()
	report["current_cut_reached"] = true
	// Keep a second committed terminal attempt present while its CAS reply is
	// delayed. Fresh terminal evidence may reclaim it even though that caller
	// remains unknown; the current Started/successor record must stay.
	set(terminalReply, "1*sleep(60000)->return(false)")
	if cut == "late-release" {
		set(beforeRelease, "1*sleep(10000)->return(true)")
	}
	phase = "retain-terminal-CAS-reply"
	publish("terminal-original")
	receive("terminal-original", 35*time.Second)
	terminalExecutor := waitCut(terminalReply, 1)
	disable(terminalReply)
	require.Equal(t, executor, terminalExecutor, "pressure must reach the original journal")
	report["terminal_reply_cut_reached"] = true
	phase = "pressure-during-unknown-CAS"
	publish("after-pressure")
	require.Eventually(t, func() bool {
		n, e := faults[executor].Count(ctx, full)
		return e == nil && n > 0
	}, 25*time.Second, 50*time.Millisecond)
	report["executor_capacity_refusal"] = true
	if cut == "late-release" {
		phase = "late-page-retains-capacity"
		require.Equal(t, executor, waitCut(beforeRelease, 2))
		quiet(2 * time.Second)
		n, e := faults[executor].Count(ctx, publicationAttempt)
		require.NoError(t, e)
		require.Equal(t, 1, n, "expired cleanup cannot release capacity before a new fresh page")
		report["expired_page_preserved_capacity"] = true
		disable(beforeRelease)
	}
	phase = "publication-after-fresh-cleanup"
	// This completion window includes a fresh claim and background projection;
	// it does not extend the expired reclamation page or the execution lease.
	receive("after-pressure", 60*time.Second)
	quiet(time.Until(cutAt.Add(21 * time.Second)))
	// Join Paho's asynchronous manual PUBACK observation before killing its
	// owner node, so the fixture does not create an unrelated unfinished replay.
	quiet(3 * time.Second)
	n, err := faults[executor].Count(ctx, publicationAttempt)
	require.NoError(t, err)
	require.Equal(t, 2, n, "unknown CAS cannot authorize publication from the held original")
	report["publications_while_current_unknown"] = n
	report["unknown_window_ms"] = 21000
	phase = "recover-retained-current"
	if cut != "successor-reply" {
		restart(executor)
		report["executor_restarts"] = 1
	}
	receive("current-original", 140*time.Second)
	report["current_attempt_preserved"] = true
	phase = "quiet-after-completion"
	quiet(15 * time.Second)
	var finalAttempts int
	for _, f := range faults {
		n, e := f.Count(ctx, publicationAttempt)
		require.NoError(t, e)
		finalAttempts += n
	}
	expectedAttempts := 1 // Earlier publications were on the killed executor.
	if cut == "successor-reply" {
		expectedAttempts = 3
	}
	require.Equal(t, expectedAttempts, finalAttempts, "business publication count is separate from QoS replay")
	report["publication_attempts_after_last_restart"] = finalAttempts
	report["business_publications"], report["unexpected_deliveries"] = 3, 0
	phase = "complete"
}
