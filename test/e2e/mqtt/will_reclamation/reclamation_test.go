//go:build e2e && (darwin || linux)

package will_reclamation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) { runReclamation(t, count, 2) })
	}
}

// Production-cap acceptance is opt-in because it commits 1,023 retained Wills
// before each topology's capacity refusal and exact current-attempt restart.
func TestWillJournalProductionCapacityPreservesCurrentRecovery(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" || os.Getenv("WK_E2E_MQTT_WILL_CAPACITY") != "1" {
		t.Skip("requires an instrumented binary and the full-capacity workload opt-in")
	}
	require.NotEmpty(t, os.Getenv("WK_E2E_BINARY"))
	for _, count := range []int{1, 3} {
		topology := "single-node-cluster"
		if count == 3 {
			topology = "three-node-cluster"
		}
		t.Run(topology, func(t *testing.T) { runReclamation(t, count, 1024) })
	}
}

func runReclamation(t *testing.T, count, journalCapacity int) {
	phase := "startup"
	report := map[string]any{"scenario": "mqtt-will-journal-reclamation", "nodes": count, "hash_slots": 256, "journal_capacity": journalCapacity, "production_capacity": journalCapacity == 1024, "resubscriptions": 0, "connect_retries": 0}
	dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	t.Cleanup(func() {
		report["passed"], report["last_phase"] = !t.Failed(), phase
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0700))
		name := fmt.Sprintf("mqtt-will-reclamation-%d.json", count)
		if journalCapacity == 1024 {
			name = fmt.Sprintf("mqtt-will-reclamation-%d-1024.json", count)
		}
		path := filepath.Join(dir, name)
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
	workloadBound := 120 * time.Second
	if journalCapacity == 1024 {
		workloadBound = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), workloadBound)
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
		if journalCapacity == 2 {
			require.NoError(t, f.Enable(ctx, capacity, `return(2)`))
		}
		require.NoError(t, f.Enable(ctx, full, `return(true)`))
		require.NoError(t, f.Enable(ctx, cleanup, `return(true)`))
		pause := "sleep(60000)"
		if journalCapacity == 1024 {
			pause = "sleep(2400000)"
		}
		require.NoError(t, f.Enable(ctx, started, pause))
	}
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-reclamation-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	abort := func(c *suite.MQTTClient) {
		call, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		require.NoError(t, c.AbortAndWait(call))
	}
	connectRecipient := func() *suite.MQTTClient {
		window := uint16(1)
		if journalCapacity == 1024 {
			window = 16
		}
		c, err := suite.ConnectMQTT(ctx, addrs[0], "bob", "bob-reclamation-token", "will-reclamation-recipient", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: window})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() { abort(c) })
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
		t.Cleanup(func() { abort(alice) })
		abort(alice)
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
	identities := make(map[string]struct{}, journalCapacity+1)
	if journalCapacity == 1024 {
		// Thirty-two submitted lifetimes and one receiver bound fixture pressure.
		// Zero-delay Wills detach on abnormal closure or the next Clean Start;
		// no per-receipt foreground wait is required to preserve the obligation.
		fillStarted := time.Now()
		credits := make(chan struct{}, 32)
		result := make(chan terminalCollection, 1)
		joined := make(chan struct{})
		go func() {
			defer close(joined)
			result <- collectTerminalWills(ctx, t, bob, journalCapacity-1, credits)
		}()
		t.Cleanup(func() { cancel(); <-joined })
		for i := 1; i < journalCapacity; i++ {
			select {
			case credits <- struct{}{}:
			case out := <-result:
				require.NoError(t, out.err)
				t.Fatal("receiver completed before the entire workload was submitted")
			case <-ctx.Done():
				t.Fatal("full-capacity submission deadline")
			}
			publishWill(fmt.Sprintf("terminal-%04d", i))
		}
		select {
		case out := <-result:
			report["retained_terminal_receipts"] = out.count
			require.NoError(t, out.err, cluster.DumpDiagnostics())
			require.Equal(t, journalCapacity-1, out.count)
			identities = out.identities
		case <-ctx.Done():
			t.Fatal("full-capacity receipt deadline")
		}
		report["fill_duration_ms"], report["publisher_inflight_bound"] = time.Since(fillStarted).Milliseconds(), 32
	} else {
		publishWill("terminal-0001")
		terminal := receive("terminal-0001")
		identities[terminal.Properties.User.Get("wk.message_id")] = struct{}{}
		report["retained_terminal_receipts"] = 1
	}
	require.Eventually(t, func() bool {
		n, err := faults[executorIndex].Count(ctx, cleanup)
		return err == nil && n >= journalCapacity-1
	}, 5*time.Second, 50*time.Millisecond, "captured executor must retain all terminal attempts")
	n, err := faults[executorIndex].Count(ctx, full)
	require.NoError(t, err)
	require.Zero(t, n, "capacity must not refuse before the exact journal fills")
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
	_, duplicate := identities[after.Properties.User.Get("wk.message_id")]
	require.False(t, duplicate)
	identities[after.Properties.User.Get("wk.message_id")] = struct{}{}
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
	abort(bob)
	bob = connectRecipient()
	require.True(t, bob.Connack.SessionPresent)
	current := receive("current-original")
	_, duplicate = identities[current.Properties.User.Get("wk.message_id")]
	require.False(t, duplicate)
	report["current_attempt_preserved"], report["executor_restarts"] = true, 1
	phase = "quiet-after-completion"
	quiet, done := context.WithTimeout(ctx, 15*time.Second)
	_, err = bob.Receive(quiet)
	done()
	require.ErrorIs(t, err, context.DeadlineExceeded, "quiet observation must not mask transport closure")
	report["business_publications"], report["unexpected_deliveries"] = journalCapacity+1, 0
	phase = "complete"
}

// terminalCollection transfers only bounded identity evidence to the test thread.
type terminalCollection struct {
	count      int
	identities map[string]struct{}
	err        error
}

// collectTerminalWills validates independent wire receipts and ACKs them in one
// joined receiver. It retains no payloads and never uses testing Fatal in a worker.
func collectTerminalWills(ctx context.Context, t *testing.T, bob *suite.MQTTClient, count int, credits <-chan struct{}) (out terminalCollection) {
	out.identities = make(map[string]struct{}, count)
	seen := make([]bool, count+1)
	for out.count < count {
		call, done := context.WithTimeout(ctx, 90*time.Second)
		p, err := bob.Receive(call)
		done()
		if err != nil {
			out.err = err
			return out
		}
		if p.QoS != 1 || p.Duplicate() || p.Properties == nil {
			out.err = fmt.Errorf("invalid terminal receipt after %d publications", out.count)
			return out
		}
		number, err := strconv.Atoi(strings.TrimPrefix(p.Properties.User.Get("wk.client_msg_no"), "terminal-"))
		if err != nil || number < 1 || number > count || seen[number] || string(p.Payload) != fmt.Sprintf("terminal-%04d", number) {
			out.err = fmt.Errorf("unexpected terminal publication after %d receipts", out.count)
			return out
		}
		identity := p.Properties.User.Get("wk.message_id")
		_, duplicate := out.identities[identity]
		if identity == "" || duplicate || p.Properties.User.Get("wk.from_uid") != "alice" {
			out.err = fmt.Errorf("invalid terminal identity after %d receipts", out.count)
			return out
		}
		if err := bob.Client.Ack(p); err != nil {
			out.err = err
			return out
		}
		seen[number], out.identities[identity] = true, struct{}{}
		out.count++
		<-credits
		if out.count%128 == 0 {
			t.Logf("retained terminal receipts: %d/%d", out.count, count)
		}
	}
	return out
}
