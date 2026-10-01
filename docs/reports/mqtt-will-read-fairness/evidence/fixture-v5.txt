//go:build e2e && (darwin || linux)

package will_reclamation_fairness

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

const (
	started          = "wkMQTTWillAfterStarted"
	capacity         = "wkMQTTWillJournalLimit"
	full             = "wkMQTTWillJournalFull"
	cleanup          = "wkMQTTWillReleaseFailure"
	publication      = "wkMQTTWillPublicationAttempt"
	page             = "wkMQTTWillReclamationPage"
	cursorHold       = "wkMQTTWillReclamationCursorHold"
	cursorAdvanced   = "wkMQTTWillReclamationCursorAdvanced"
	probeMatch       = "wkMQTTWillReclamationProbeMatch"
	probeSeen        = "wkMQTTWillReclamationProbeSeen"
	probePage        = "wkMQTTWillReclamationProbePage"
	readMatch        = "wkMQTTWillReclamationReadMatch"
	readExempt       = "wkMQTTWillReclamationReadExempt"
	readDeadline     = "wkMQTTWillReclamationReadDeadline"
	readCancel       = "wkMQTTWillReclamationReadCancel"
	deadlineObserved = "wkMQTTWillReclamationDeadlineObserved"
	cancelObserved   = "wkMQTTWillReclamationCancelObserved"
	selectedRetired  = "wkMQTTWillReclamationSelectedRetired"
)

func TestWillReclamationFailedReadFairness(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires a temporary gofail product binary")
	}
	require.NotEmpty(t, os.Getenv("WK_E2E_BINARY"))
	for _, nodes := range []int{1, 3} {
		topology := "single-node-cluster"
		if nodes == 3 {
			topology = "three-node-cluster"
		}
		for _, mode := range []string{"deadline", "canceled"} {
			t.Run(topology+"/"+mode, func(t *testing.T) { runFairness(t, nodes, mode) })
		}
	}
}

func runFairness(t *testing.T, nodes int, mode string) {
	phase := "startup"
	report := map[string]any{"scenario": "mqtt-will-failed-read-fairness", "nodes": nodes, "mode": mode, "hash_slots": 256, "logical_slot_groups": 1, "journal_capacity": 32, "page_bound": 16, "read_deadline_ms": 250, "page_deadline_ms": 750, "connect_retries": 0, "resubscriptions": 0}
	dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	// Registered first: artifacts are emitted after all client/process cleanup.
	t.Cleanup(func() {
		report["passed"], report["last_phase"] = !t.Failed(), phase
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0700))
		path := filepath.Join(dir, fmt.Sprintf("mqtt-will-fairness-%d-%s.json", nodes, mode))
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	var opts []suite.Option
	addresses := make([]string, nodes)
	faults := make([]suite.GofailEndpoint, nodes)
	for i := range nodes {
		addresses[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		faults[i] = suite.ReserveGofailEndpoint(t)
		opts = append(opts, suite.WithNodeEnv(uint64(i+1), faults[i].Env(), "GOFAIL_FAILPOINTS="+publication+"=return(true)"), suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "1", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addresses[i]}))
	}
	cluster := suite.New(t).StartStaticCluster(nodes, append(opts, suite.WithManagerHTTP())...)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ready := func() {
		call, done := context.WithTimeout(ctx, 60*time.Second)
		defer done()
		require.NoError(t, cluster.WaitClusterReady(call), cluster.DumpDiagnostics())
		_, err := cluster.WaitSlotLeadersStable(call, time.Second)
		require.NoError(t, err, cluster.DumpDiagnostics())
	}
	ready()
	observers := []string{full, cursorAdvanced, probeSeen, probePage, page, deadlineObserved, cancelObserved, selectedRetired}
	controls := []string{started, capacity, cleanup, cursorHold, probeMatch, readMatch, readExempt, readDeadline, readCancel}
	for _, f := range faults {
		call, done := context.WithTimeout(ctx, 3*time.Second)
		_, err := f.WaitListed(call, append(append([]string{publication}, observers...), controls...)...)
		done()
		require.NoError(t, err, "instrumentation prerequisite; this is not a product RED")
		for _, name := range observers {
			require.NoError(t, f.Enable(ctx, name, "return(true)"))
		}
		require.NoError(t, f.Enable(ctx, capacity, "return(32)"))
		require.NoError(t, f.Enable(ctx, cleanup, "return(true)"))
		require.NoError(t, f.Enable(ctx, cursorHold, "return(true)"))
		require.NoError(t, f.Enable(ctx, started, "sleep(1800000)"))
	}
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-fairness-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	abort := func(c *suite.MQTTClient) {
		call, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		require.NoError(t, c.AbortAndWait(call))
	}
	connectRecipient := func() *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addresses[0], "bob", "bob-fairness-token", "will-fairness-recipient", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 16})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() { abort(c) })
		return c
	}
	bob := connectRecipient()
	require.False(t, bob.Connack.SessionPresent)
	sub, err := bob.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
	require.NoError(t, err)
	require.Equal(t, []byte{1}, sub.Reasons)
	publishWill := func(client, body string) {
		alice, err := suite.ConnectMQTT(ctx, addresses[nodes-1], "alice", "alice-fairness-token", client, true, 60, suite.MQTTConnectOptions{Will: &paho.WillMessage{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Payload: []byte(body)}, WillProperties: &paho.WillProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: body}}}})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() { abort(alice) })
		abort(alice)
	}
	identities := make(map[string]struct{}, 33)
	validate := func(p *paho.Publish, body string) {
		require.Equal(t, byte(1), p.QoS)
		require.False(t, p.Duplicate())
		require.Equal(t, []byte(body), p.Payload)
		require.NotNil(t, p.Properties)
		require.Equal(t, body, p.Properties.User.Get("wk.client_msg_no"))
		require.Equal(t, "alice", p.Properties.User.Get("wk.from_uid"))
		id := p.Properties.User.Get("wk.message_id")
		require.NotEmpty(t, id)
		_, duplicate := identities[id]
		require.False(t, duplicate)
		identities[id] = struct{}{}
	}
	receive := func(body string, bound time.Duration) *paho.Publish {
		call, done := context.WithTimeout(ctx, bound)
		defer done()
		p, err := bob.Receive(call)
		require.NoError(t, err, "required business receipt: "+body, cluster.DumpDiagnostics())
		validate(p, body)
		return p
	}
	quiet := func(bound time.Duration) {
		call, done := context.WithTimeout(ctx, bound)
		defer done()
		_, err := bob.Receive(call)
		require.ErrorIs(t, err, context.DeadlineExceeded, "quiet must end on its deadline with a healthy recipient")
	}
	phase = "retain-current-started"
	publishWill("will-fairness-held", "held-original")
	executorIndex := -1
	require.Eventually(t, func() bool {
		for i, f := range faults {
			n, err := f.Count(ctx, started)
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
	exact := faults[executorIndex]
	report["captured_executor_node"] = executorIndex + 1
	// Keep failure-phase counters even when the required business receive fails.
	// Independent cleanup context survives the workload's deferred cancellation.
	t.Cleanup(func() {
		call, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		counts := make(map[string]int, len(observers)+1)
		for _, name := range append(append([]string{}, observers...), publication) {
			n, err := exact.Count(call, name)
			if err != nil {
				report["final_observers_reachable"] = false
				return
			}
			counts[name] = n
		}
		report["final_observers_reachable"], report["final_executor_counts"] = true, counts
	})
	count := func(name string) int {
		n, err := exact.Count(ctx, name)
		require.NoError(t, err)
		return n
	}
	waitCount := func(name string, minimum int, bound time.Duration) {
		require.Eventually(t, func() bool { return count(name) >= minimum }, bound, 50*time.Millisecond, name)
	}
	phase = "retain-terminal-attempts"
	fillStart := time.Now()
	terminalClients := make([]string, 31)
	expected := make(map[string]bool, 31)
	for i := range terminalClients {
		terminalClients[i] = fmt.Sprintf("will-fairness-terminal-%02d", i)
		body := fmt.Sprintf("terminal-%02d", i)
		expected[body] = true
		publishWill(terminalClients[i], body)
	}
	// Independent ClientIDs have no cross-client ordering contract. Submit the
	// bounded set first, then ACK every unique receipt through the same client.
	for i := range terminalClients {
		call, done := context.WithTimeout(ctx, 35*time.Second)
		p, err := bob.Receive(call)
		done()
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.NotNil(t, p.Properties)
		body := p.Properties.User.Get("wk.client_msg_no")
		require.True(t, expected[body], "each provisioned terminal must publish once")
		delete(expected, body)
		validate(p, body)
		require.NoError(t, bob.Client.Ack(p))
		report["terminal_receipts"] = i + 1
	}
	report["fill_duration_ms"] = time.Since(fillStart).Milliseconds()
	t.Log("all 31 terminal business receipts received and acknowledged")
	waitCount(cleanup, 31, 5*time.Second)
	require.Zero(t, count(full), "the exact journal must fill before refusal")
	report["terminal_cleanup_failed"] = true
	phase = "full-refusal-and-page-calibration"
	publishWill("will-fairness-pressure", "after-pressure")
	waitCount(full, 1, 20*time.Second)
	report["executor_capacity_refusal"] = true
	// Probe only our provisioned terminal IDs. Two complete membership probes
	// exclude a page that loaded the previous selector before the HTTP update.
	eligible := ""
	for i, client := range terminalClients {
		require.NoError(t, exact.Enable(ctx, probeMatch, fmt.Sprintf("return(%q)", client)))
		seen, pages := count(probeSeen), count(probePage)
		waitCount(probePage, pages+2, 45*time.Second)
		report["calibration_probes"] = i + 1
		if count(probeSeen) == seen {
			eligible = client
			break
		}
	}
	require.NotEmpty(t, eligible, "an exact eligible terminal must lie outside the fixed 16-candidate page")
	report["eligible_outside_held_page"] = true
	t.Log("captured journal refused; eligible terminal is outside the held page")
	require.Equal(t, 31, count(publication))
	require.Zero(t, count(cursorAdvanced))
	phase = "read-faults-with-held-cursor"
	require.NoError(t, exact.Enable(ctx, readMatch, `return("will-fairness-")`))
	require.NoError(t, exact.Enable(ctx, readExempt, fmt.Sprintf("return(%q)", eligible)))
	errorObserver, control := deadlineObserved, readDeadline
	if mode == "canceled" {
		errorObserver, control = cancelObserved, readCancel
	}
	require.NoError(t, exact.Enable(ctx, control, "return(true)"))
	pages := count(page)
	waitCount(page, pages+3, 45*time.Second)
	waitCount(errorObserver, 3, 5*time.Second)
	// All earlier healthy-read pages have completed before cleanup is enabled.
	require.NoError(t, exact.Disable(ctx, cleanup))
	quiet(21 * time.Second)
	require.Equal(t, 31, count(publication))
	require.Zero(t, count(selectedRetired))
	require.Zero(t, count(cursorAdvanced))
	report["held_cursor_quiet_ms"] = 21000
	report["read_errors_before_cursor_release"] = count(errorObserver)
	report["full_refusals_before_cursor_release"] = count(full)
	t.Log("healthy held-cursor quiet completed with actual read errors and no publication")
	phase = "ordinary-progress-after-cursor-release"
	require.NoError(t, exact.Disable(ctx, cursorHold))
	progressStart := time.Now()
	after := receive("after-pressure", 420*time.Second)
	report["progress_duration_ms"] = time.Since(progressStart).Milliseconds()
	require.NoError(t, bob.Client.Ack(after))
	report["publication_after_pressure"] = true
	t.Log("pending Will published with read faults still active")
	require.Positive(t, count(cursorAdvanced))
	require.Positive(t, count(selectedRetired))
	require.Positive(t, count(errorObserver))
	report["selected_retirements"] = count(selectedRetired)
	report["cursor_advances"] = count(cursorAdvanced)
	report["bounded_pages"] = count(page)
	quiet(3 * time.Second) // Join asynchronous Paho PUBACKs while healthy.
	abort(bob)
	oldPrefix := count(publication)
	require.Equal(t, 32, oldPrefix)
	report["old_process_publication_prefix"] = oldPrefix
	phase = "restart-retained-current"
	executor := cluster.MustNode(uint64(executorIndex + 1))
	require.NoError(t, syscall.Kill(-executor.Process.Cmd.Process.Pid, syscall.SIGKILL))
	select {
	case <-executor.Process.Done():
	case <-ctx.Done():
		t.Fatal("owned executor exit deadline")
	}
	_ = executor.Process.Stop()
	report["executor_crash_joined"] = true
	for i, f := range faults {
		if i == executorIndex {
			continue
		}
		// Started was already cleared on every node. Probe/read selectors were
		// enabled only on the now-joined executor; DELETE is not idempotent.
		for _, name := range []string{capacity, cleanup, cursorHold} {
			require.NoError(t, f.Disable(ctx, name))
		}
	}
	require.NoError(t, cluster.StartStoppedNode(uint64(executorIndex+1)), cluster.DumpDiagnostics())
	ready()
	bob = connectRecipient()
	require.True(t, bob.Connack.SessionPresent)
	current := receive("held-original", 45*time.Second)
	report["current_recovered_once"] = true
	abort(bob)
	bob = connectRecipient()
	require.True(t, bob.Connack.SessionPresent)
	call, done := context.WithTimeout(ctx, 30*time.Second)
	replay, err := bob.Receive(call)
	done()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, replay.Duplicate())
	require.Equal(t, current.PacketID, replay.PacketID)
	require.Equal(t, current.Payload, replay.Payload)
	require.NotNil(t, replay.Properties)
	require.Equal(t, current.Properties.User.Get("wk.message_id"), replay.Properties.User.Get("wk.message_id"))
	require.NoError(t, bob.Client.Ack(replay))
	report["persistent_original_packet_replay"] = true
	phase = "strict-final-quiet"
	quiet(15 * time.Second)
	live := 0
	for _, f := range faults {
		n, err := f.Count(ctx, publication)
		require.NoError(t, err)
		live += n
	}
	report["live_process_publication_attempts"] = live
	require.Equal(t, 33, oldPrefix+live, "old prefix and replacement-process effects must stay separate")
	report["strict_quiet_ms"] = 15000
	phase = "complete"
}
