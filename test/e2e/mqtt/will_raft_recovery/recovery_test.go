//go:build e2e && (darwin || linux)

package will_raft_recovery

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

const beforeProposal = "wkSlotBeforeRawProposal"
const proposalMatch = "wkSlotProposalPauseMatch"
const appendMatch = "wkSlotAppendDropMatch"
const persistMatch = "wkSlotPersistedCommandMatch"
const droppedAppend = "wkSlotDroppedAppend"
const persistedUncommitted = "wkSlotPersistedUncommitted"
const beforeApply = "wkMQTTWillBeforeStartedApply"
const appliedReply = "wkMQTTWillStartedCASReply"
const publicationAttempt = "wkMQTTWillPublicationAttempt"
const originatingCAS = "wkMQTTWillBeforeStartedCAS"
const fsmComplete = "wkMQTTWillStartedFSMComplete"
const capacityFull = "wkMQTTWillJournalFull"
const releaseFailure = "wkMQTTWillReleaseFailure"
const successorReply = "wkMQTTWillSuccessorCASReply"

func TestWillRecoveryAcrossProposalCommitAndApply(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires a temporary gofail product binary")
	}
	require.NotEmpty(t, os.Getenv("WK_E2E_BINARY"))
	for _, nodes := range []int{1, 3} {
		states := []string{"queued", "committed-unapplied"}
		if nodes == 3 {
			states = append(states, "persisted-uncommitted")
		}
		for _, state := range states {
			for _, outcome := range []string{"late", "crash"} {
				topology := "single-node-cluster"
				if nodes == 3 {
					topology = "three-node-cluster"
				}
				t.Run(fmt.Sprintf("%s/%s-%s", topology, state, outcome), func(t *testing.T) {
					runRecovery(t, nodes, state, outcome)
				})
			}
		}
	}
}

func runRecovery(t *testing.T, nodes int, state, outcome string) {
	started := time.Now()
	phase := "startup"
	report := map[string]any{"scenario": "mqtt-will-raft-recovery", "nodes": nodes, "hash_slots": 256, "logical_slots": 12, "state": state, "outcome": outcome, "instrumented_journal_capacity": 2, "connect_retries": 0, "resubscriptions": 0}
	dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	// Registered before the cluster and clients, so their cleanup joins first.
	t.Cleanup(func() {
		report["passed"], report["last_phase"] = !t.Failed(), phase
		report["elapsed_ms"] = time.Since(started).Milliseconds()
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0700))
		path := filepath.Join(dir, fmt.Sprintf("mqtt-will-raft-%d-%s-%s.json", nodes, state, outcome))
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	var opts []suite.Option
	if logs := os.Getenv("WK_E2E_MQTT_LOG_DIR"); logs != "" {
		opts = append(opts, suite.WithNodeLogRootDir(logs))
	}
	addresses := make([]string, nodes)
	faults := make([]suite.GofailEndpoint, nodes)
	for i := range nodes {
		addresses[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		faults[i] = suite.ReserveGofailEndpoint(t)
		startup := []string{"wkMQTTWillJournalLimit=return(2)", capacityFull + "=return(true)", publicationAttempt + "=return(true)", appliedReply + "=return(false)", successorReply + "=return(false)", persistedUncommitted + "=return(false)", originatingCAS + "=return(false)", fsmComplete + "=return(false)"}
		opts = append(opts, suite.WithNodeEnv(uint64(i+1), faults[i].Env(), "GOFAIL_FAILPOINTS="+strings.Join(startup, ";")), suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addresses[i]}))
	}
	cluster := suite.New(t).StartStaticCluster(nodes, append(opts, suite.WithManagerHTTP())...)
	t.Cleanup(func() {
		call, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		counts := make([]map[string]int, nodes)
		for i, f := range faults {
			counts[i] = make(map[string]int)
			for _, name := range []string{originatingCAS, fsmComplete, appliedReply, successorReply, publicationAttempt, capacityFull, persistedUncommitted, droppedAppend} {
				if n, err := f.Count(call, name); err == nil {
					counts[i][name] = n
				}
			}
		}
		report["final_control_counts"] = counts
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	ready := func() {
		call, done := context.WithTimeout(ctx, 30*time.Second)
		defer done()
		require.NoError(t, cluster.WaitClusterReady(call), cluster.DumpDiagnostics())
		inventory, err := cluster.WaitSlotLeadersStable(call, time.Second)
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Len(t, inventory.Leaders, 12)
	}
	ready()
	for _, f := range faults {
		call, done := context.WithTimeout(ctx, 3*time.Second)
		_, err := f.WaitListed(call, beforeProposal, proposalMatch, appendMatch, persistMatch, droppedAppend, persistedUncommitted, beforeApply, appliedReply, publicationAttempt, originatingCAS, fsmComplete, capacityFull, releaseFailure, successorReply)
		done()
		require.NoError(t, err)
	}
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-raft-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	abort := func(c *suite.MQTTClient) {
		call, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		require.NoError(t, c.AbortAndWait(call))
	}
	connectRecipient := func() *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addresses[0], "bob", "bob-raft-token", "will-raft-recipient", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
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
	phase = "retain-one-terminal-attempt"
	set(releaseFailure, "return(true)")
	terminal, err := suite.ConnectMQTT(ctx, addresses[nodes-1], "alice", "alice-raft-token", "will-raft-publisher", true, 60, suite.MQTTConnectOptions{Will: &paho.WillMessage{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Payload: []byte("terminal-raft-will")}, WillProperties: &paho.WillProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: "terminal-raft-will"}}}})
	require.NoError(t, err)
	t.Cleanup(func() { abort(terminal) })
	abort(terminal)
	call, done := context.WithTimeout(ctx, 35*time.Second)
	terminalReceipt, err := bob.Receive(call)
	done()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, byte(1), terminalReceipt.QoS)
	require.False(t, terminalReceipt.Duplicate())
	require.Equal(t, []byte("terminal-raft-will"), terminalReceipt.Payload)
	require.NotNil(t, terminalReceipt.Properties)
	require.Equal(t, "terminal-raft-will", terminalReceipt.Properties.User.Get("wk.client_msg_no"))
	terminalIdentity := terminalReceipt.Properties.User.Get("wk.message_id")
	require.NotEmpty(t, terminalIdentity)
	require.NoError(t, bob.Client.Ack(terminalReceipt))
	terminalExecutor := -1
	require.Eventually(t, func() bool {
		for i, f := range faults {
			call, done := context.WithTimeout(ctx, time.Second)
			n, err := f.Count(call, releaseFailure)
			done()
			if err == nil && n > 0 {
				terminalExecutor = i
				return true
			}
		}
		return false
	}, 15*time.Second, 50*time.Millisecond, "terminal CAS must complete while exact cleanup fails")
	report["retained_terminal_node"] = terminalExecutor + 1
	// Re-enabling starts a new gofail counter epoch after the observed terminal
	// publication. Keep its receipt and cleanup proof separate from this window.
	set(publicationAttempt, "return(true)")
	for _, name := range []string{appliedReply, originatingCAS, fsmComplete} {
		set(name, "return(false)")
	}
	cut := beforeProposal
	switch state {
	case "queued":
		set(proposalMatch, `return("2264697370617463685f7374616765223a33")`)
		set(beforeProposal, "1*sleep(30000)->return(false)")
	case "committed-unapplied":
		cut = beforeApply
		set(beforeApply, "1*sleep(30000)->return(false)")
	case "persisted-uncommitted":
		cut = persistedUncommitted
		set(appendMatch, `return("2264697370617463685f7374616765223a33")`)
		set(persistMatch, `return("2264697370617463685f7374616765223a33")`)
		set(droppedAppend, "return(true)")
	default:
		t.Fatal("unknown proposal state")
	}
	alice, err := suite.ConnectMQTT(ctx, addresses[nodes-1], "alice", "alice-raft-token", "will-raft-publisher", true, 60, suite.MQTTConnectOptions{Will: &paho.WillMessage{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Payload: []byte("original-raft-will")}, WillProperties: &paho.WillProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: "original-raft-will"}}}})
	require.NoError(t, err)
	t.Cleanup(func() { abort(alice) })
	abort(alice)
	phase = "reach-real-state"
	executor := -1
	require.Eventually(t, func() bool {
		for i, f := range faults {
			call, done := context.WithTimeout(ctx, time.Second)
			n, e := f.Count(call, cut)
			done()
			owner, ownerErr := f.Count(ctx, originatingCAS)
			if e == nil && n > 0 && ownerErr == nil && owner > 0 {
				executor = i
				return true
			}
		}
		return false
	}, 25*time.Second, 50*time.Millisecond, "selected consensus boundary must be reached")
	report["state_cut_reached"], report["cut_node"] = true, executor+1
	require.Equal(t, terminalExecutor, executor, "both attempts must occupy the captured journal")
	if state != "persisted-uncommitted" {
		// Selected sleeps continue, while later calls stop queuing behind them.
		disable(cut)
	}
	if state == "queued" {
		disable(proposalMatch)
	}
	quiet := func(duration time.Duration) {
		call, done := context.WithTimeout(ctx, duration)
		_, e := bob.Receive(call)
		done()
		require.ErrorIs(t, e, context.DeadlineExceeded, "quiet must retain a healthy recipient")
	}
	sum := func(name string) int {
		n := 0
		for _, f := range faults {
			call, done := context.WithTimeout(ctx, time.Second)
			count, err := f.Count(call, name)
			done()
			require.NoError(t, err)
			n += count
		}
		return n
	}
	phase = "expired-caller-and-grant"
	var earlyIdentity string
	var earlyPacketID uint16
	window, endWindow := context.WithTimeout(ctx, 21*time.Second)
	first, receiveErr := bob.Receive(window)
	if receiveErr == nil {
		// A paused Raft worker stops this group's heartbeats. Surviving quorum
		// can choose a new leader and definitely claim a newer execution before
		// the old queued proposal runs. Keep its original exchange unfinished.
		require.True(t, nodes == 3 && state == "queued", "only independently claimed queued takeover may publish before this cut resolves")
		require.Equal(t, []byte("original-raft-will"), first.Payload)
		require.Equal(t, byte(1), first.QoS)
		require.NotNil(t, first.Properties)
		require.Equal(t, "original-raft-will", first.Properties.User.Get("wk.client_msg_no"))
		require.Equal(t, "alice", first.Properties.User.Get("wk.from_uid"))
		earlyIdentity = first.Properties.User.Get("wk.message_id")
		earlyPacketID = first.PacketID
		require.NotEmpty(t, earlyIdentity)
		require.NotEqual(t, terminalIdentity, earlyIdentity)
		require.Positive(t, sum(successorReply), "early dispatch needs a real applied newer Started CAS")
		_, receiveErr = bob.Receive(window)
	}
	require.ErrorIs(t, receiveErr, context.DeadlineExceeded, "remaining observation must be healthy and quiet")
	endWindow()
	attemptsDuring := sum(publicationAttempt)
	newClaimsDuring := sum(successorReply)
	require.LessOrEqual(t, attemptsDuring, 1, "unknown original proposal cannot add a second business publication")
	if attemptsDuring > 0 {
		require.True(t, nodes == 3 && state == "queued")
		require.Positive(t, newClaimsDuring, "publication requires definite successor evidence")
	}
	require.Zero(t, sum(appliedReply), "this is not an applied reply delay")
	completed, err := faults[executor].Count(ctx, fsmComplete)
	require.NoError(t, err)
	require.Zero(t, completed, "the captured executor has not completed its selected FSM apply")
	report["unknown_window_ms"], report["publications_while_unknown"] = 21000, attemptsDuring
	report["definite_successor_claims_during_window"] = newClaimsDuring
	report["original_received_during_window"] = earlyIdentity != ""
	if state == "committed-unapplied" {
		var inventory struct {
			Items []suite.SlotDTO `json:"items"`
		}
		call, done := context.WithTimeout(ctx, 3*time.Second)
		_, err := suite.GetJSON(call, fmt.Sprintf("http://%s/manager/slots?node_id=%d", cluster.MustNode(uint64(executor+1)).ManagerAddr(), executor+1), &inventory)
		done()
		require.NoError(t, err)
		gaps := make([]map[string]uint64, 0)
		for _, item := range inventory.Items {
			if item.NodeLog != nil && item.NodeLog.Role == "leader" && item.NodeLog.CommitIndex > item.NodeLog.AppliedIndex {
				gaps = append(gaps, map[string]uint64{"slot": uint64(item.SlotID), "commit": item.NodeLog.CommitIndex, "applied": item.NodeLog.AppliedIndex})
			}
		}
		require.NotEmpty(t, gaps, "public Raft observations must retain a committed/unapplied gap")
		report["committed_apply_gaps"] = gaps
	}
	// End observation before restoring replication or restarting the executor,
	// so resumed dispatch cannot disappear into an unobserved live transport.
	abort(bob)
	disable(releaseFailure)
	if state == "persisted-uncommitted" {
		report["dropped_append_batches"] = sum(droppedAppend)
		require.Positive(t, report["dropped_append_batches"])
		if outcome == "late" {
			disable(appendMatch)
			disable(persistMatch)
			disable(droppedAppend)
		}
	}
	phase = "ordinary-recovery"
	var killedPublicationAttempts int
	if outcome == "crash" {
		node := cluster.MustNode(uint64(executor + 1))
		n, err := faults[executor].Count(ctx, fsmComplete)
		require.NoError(t, err)
		require.Zero(t, n, "kill must occur before the selected command completes apply")
		killedPublicationAttempts, err = faults[executor].Count(ctx, publicationAttempt)
		require.NoError(t, err)
		report["publication_attempts_on_killed_process"] = killedPublicationAttempts
		require.NoError(t, syscall.Kill(-node.Process.Cmd.Process.Pid, syscall.SIGKILL))
		select {
		case <-node.Process.Done():
		case <-ctx.Done():
			t.Fatal("cut process did not exit")
		}
		_ = node.Process.Stop()
		if state == "persisted-uncommitted" {
			// Keep the captured leader's replication loss active until it has
			// exited; clearing before SIGKILL could accidentally commit the log.
			for i, f := range faults {
				if i != executor {
					for _, name := range []string{appendMatch, persistMatch, droppedAppend} {
						require.NoError(t, f.Disable(ctx, name))
					}
				}
			}
		}
		require.NoError(t, cluster.StartStoppedNode(uint64(executor+1)), cluster.DumpDiagnostics())
		ready()
		report["executor_restarts"] = 1
	} else {
		ready()
		if nodes == 3 && state == "queued" {
			// All-node convergence also waits for the old paused worker to resume
			// and observe the new leader. Its queued proposal may be discarded or
			// conflict; only a definite newer claim may authorize the original.
			require.Eventually(t, func() bool { return sum(successorReply) > 0 }, 30*time.Second, 50*time.Millisecond)
			report["newer_claim_fences_queued_proposal"] = true
		} else {
			require.Eventually(t, func() bool { return sum(fsmComplete) > 0 }, 45*time.Second, 50*time.Millisecond, "the delayed real proposal must reach durable FSM resolution")
			require.Eventually(t, func() bool {
				n, err := faults[executor].Count(ctx, capacityFull)
				return err == nil && n > 0
			}, 30*time.Second, 50*time.Millisecond, "successor reservation must encounter the retained terminal and current attempts")
			report["executor_capacity_refusal"] = true
		}
	}
	bob = connectRecipient()
	require.True(t, bob.Connack.SessionPresent)
	report["persistent_reconnects"] = 1
	call, done = context.WithTimeout(ctx, 100*time.Second)
	p, err := bob.Receive(call)
	done()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, byte(1), p.QoS)
	require.Equal(t, []byte("original-raft-will"), p.Payload)
	require.NotNil(t, p.Properties)
	require.Equal(t, "original-raft-will", p.Properties.User.Get("wk.client_msg_no"))
	require.Equal(t, "alice", p.Properties.User.Get("wk.from_uid"))
	require.NotEmpty(t, p.Properties.User.Get("wk.message_id"))
	require.NotEqual(t, terminalIdentity, p.Properties.User.Get("wk.message_id"))
	if earlyIdentity != "" {
		require.Equal(t, earlyIdentity, p.Properties.User.Get("wk.message_id"))
		require.Equal(t, earlyPacketID, p.PacketID)
		require.True(t, p.Duplicate(), "the deliberately unfinished original exchange must replay")
		report["original_packet_id_preserved"] = true
	}
	report["original_exchange_replayed"] = p.Duplicate()
	require.NoError(t, bob.Client.Ack(p))
	phase = "quiet-after-original"
	quiet(15 * time.Second)
	require.Equal(t, 1, killedPublicationAttempts+sum(publicationAttempt), "one original business publication across all process epochs")
	report["original_publication_preserved"] = true
	report["business_publications"], report["unexpected_deliveries"] = 2, 0
	phase = "complete"
}
