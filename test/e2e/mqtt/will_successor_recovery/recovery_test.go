//go:build e2e && (darwin || linux)

package will_successor_recovery

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

const secondOriginMatch = "wkMQTTWillSecondOriginMatch"
const secondApplyMatch = "wkMQTTWillSecondApplyMatch"
const secondCompleteMatch = "wkMQTTWillSecondCompleteMatch"
const secondReplyMatch = "wkMQTTWillSecondReplyMatch"
const reclamationPage = "wkMQTTWillReclamationPage"
const retainedFirst = "wkMQTTWillReclamationFirstRetained"
const retainedSecond = "wkMQTTWillReclamationSecondRetained"
const uncertainFirst = "wkMQTTWillReclamationFirstUnconfirmed"
const uncertainSecond = "wkMQTTWillReclamationSecondUnconfirmed"

const firstStarted = "wkMQTTWillAfterStarted"
const firstReply = "wkMQTTWillStartedCASReply"

const beforeProposal = "wkSlotBeforeRawProposal"
const proposalMatch = "wkSlotProposalPauseMatch"
const appendMatch = "wkSlotAppendDropMatch"
const persistMatch = "wkSlotPersistedCommandMatch"
const droppedAppend = "wkSlotDroppedAppend"
const persistedUncommitted = "wkSlotPersistedUncommitted"
const beforeApply = "wkMQTTWillBeforeSecondApply"
const appliedReply = "wkMQTTWillSecondCASReply"
const publicationAttempt = "wkMQTTWillPublicationAttempt"
const originatingCAS = "wkMQTTWillBeforeSecondCAS"
const fsmComplete = "wkMQTTWillSecondFSMComplete"
const capacityLimit = "wkMQTTWillJournalLimit"
const capacityFull = "wkMQTTWillJournalFull"
const releaseFailure = "wkMQTTWillReleaseFailure"
const successorReply = "wkMQTTWillSelectedSuccessorReply"

func TestWillSuccessorRecoveryAcrossProposalCommitAndApply(t *testing.T) {
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
					runRecovery(t, nodes, state, outcome, 0)
				})
			}
		}
	}
}

// Pressure uses different already-admitted ClientIDs, so it can reach another
// Slot while the selected original proposal is still queued or uncommitted.
func TestWillSuccessorPressureWhileProposalInFlight(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires a temporary gofail product binary")
	}
	require.NotEmpty(t, os.Getenv("WK_E2E_BINARY"))
	for _, nodes := range []int{1, 3} {
		topology, state := "single-node-cluster", "queued"
		if nodes == 3 {
			topology, state = "three-node-cluster", "persisted-uncommitted"
		}
		for _, outcome := range []string{"late", "crash"} {
			t.Run(fmt.Sprintf("%s/%s-%s", topology, state, outcome), func(t *testing.T) { runRecovery(t, nodes, state, outcome, 12) })
		}
	}
}

func runRecovery(t *testing.T, nodes int, state, outcome string, pressureCount int) {
	started := time.Now()
	phase := "startup"
	report := map[string]any{"scenario": "mqtt-will-successor-recovery", "nodes": nodes, "hash_slots": 256, "logical_slots": 12, "state": state, "outcome": outcome, "instrumented_journal_capacity": 2, "connect_retries": 0, "resubscriptions": 0, "pressure_clients": pressureCount}
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
		path := filepath.Join(dir, fmt.Sprintf("mqtt-will-successor-%d-%s-%s-pressure-%d.json", nodes, state, outcome, pressureCount))
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
		startup := []string{capacityFull + "=return(true)", publicationAttempt + "=return(true)", firstReply + "=return(false)", appliedReply + "=return(false)", successorReply + "=return(false)", persistedUncommitted + "=return(false)", originatingCAS + "=return(false)", fsmComplete + "=return(false)", reclamationPage + "=return(false)", retainedFirst + "=return(false)", retainedSecond + "=return(false)", uncertainFirst + "=return(false)", uncertainSecond + "=return(false)"}
		opts = append(opts, suite.WithNodeEnv(uint64(i+1), faults[i].Env(), "GOFAIL_FAILPOINTS="+strings.Join(startup, ";")), suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addresses[i]}))
	}
	cluster := suite.New(t).StartStaticCluster(nodes, append(opts, suite.WithManagerHTTP())...)
	t.Cleanup(func() {
		call, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		counts := make([]map[string]int, nodes)
		for i, f := range faults {
			counts[i] = make(map[string]int)
			for _, name := range []string{originatingCAS, fsmComplete, appliedReply, successorReply, publicationAttempt, capacityFull, persistedUncommitted, droppedAppend, reclamationPage, retainedFirst, retainedSecond, uncertainFirst, uncertainSecond} {
				if n, err := f.Count(call, name); err == nil {
					counts[i][name] = n
				}
			}
		}
		report["final_control_counts"] = counts
	})
	workloadBound := 4 * time.Minute
	if pressureCount > 0 {
		workloadBound = 8 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), workloadBound)
	defer cancel()
	ready := func() {
		// Cluster startup and post-restart convergence are setup observations,
		// not a product latency contract or a change to the execution grant.
		bound := 60 * time.Second
		report["cluster_readiness_bound_ms"] = bound.Milliseconds()
		if phase == "ordinary-recovery" && pressureCount > 0 && outcome == "late" {
			// The selected sleep keeps running after its control is disabled.
			// This observes completion; it changes no execution or lease bound.
			bound = 110 * time.Second
			report["ordinary_recovery_readiness_bound_ms"] = bound.Milliseconds()
		}
		call, done := context.WithTimeout(ctx, bound)
		defer done()
		require.NoError(t, cluster.WaitClusterReady(call), cluster.DumpDiagnostics())
		inventory, err := cluster.WaitSlotLeadersStable(call, time.Second)
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Len(t, inventory.Leaders, 12)
	}
	ready()
	for _, f := range faults {
		call, done := context.WithTimeout(ctx, 3*time.Second)
		_, err := f.WaitListed(call, beforeProposal, proposalMatch, appendMatch, persistMatch, droppedAppend, persistedUncommitted, beforeApply, appliedReply, publicationAttempt, originatingCAS, fsmComplete, capacityFull, releaseFailure, successorReply, firstStarted, firstReply, capacityLimit, secondOriginMatch, secondApplyMatch, secondCompleteMatch, secondReplyMatch)
		done()
		require.NoError(t, err)
	}
	for _, f := range faults {
		require.NoError(t, f.Enable(ctx, capacityLimit, "return(2)"))
	}
	for _, f := range faults {
		for _, name := range []string{secondOriginMatch, secondApplyMatch, secondCompleteMatch, secondReplyMatch} {
			require.NoError(t, f.Enable(ctx, name, `return("will-successor-original")`))
		}
		if pressureCount > 0 {
			call, done := context.WithTimeout(ctx, 3*time.Second)
			_, err := f.WaitListed(call, reclamationPage, retainedFirst, retainedSecond, uncertainFirst, uncertainSecond)
			done()
			require.NoError(t, err)
		}
	}
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-successor-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	abort := func(c *suite.MQTTClient) {
		call, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		require.NoError(t, c.AbortAndWait(call))
	}
	connectRecipient := func() *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addresses[0], "bob", "bob-successor-token", "will-successor-recipient", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 16})
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
	expected := map[string]bool{"original-successor-will": true}
	pressureClients := make([]*suite.MQTTClient, 0, pressureCount)
	phase = "admit-independent-pressure-clients"
	for i := 0; i < pressureCount; i++ {
		body := fmt.Sprintf("successor-pressure-%02d", i)
		expected[body] = true
		c, err := suite.ConnectMQTT(ctx, addresses[nodes-1], "alice", "alice-successor-token", "will-"+body, true, 60, suite.MQTTConnectOptions{Will: &paho.WillMessage{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Payload: []byte(body)}, WillProperties: &paho.WillProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: body}}}})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() { abort(c) })
		pressureClients = append(pressureClients, c)
	}
	received := make(map[string]*paho.Publish, len(expected))
	identities := make(map[string]string, len(expected))
	accept := func(p *paho.Publish) string {
		body := string(p.Payload)
		require.True(t, expected[body], "unexpected business payload")
		require.Equal(t, byte(1), p.QoS)
		require.NotNil(t, p.Properties)
		require.Equal(t, body, p.Properties.User.Get("wk.client_msg_no"))
		require.Equal(t, "alice", p.Properties.User.Get("wk.from_uid"))
		id := p.Properties.User.Get("wk.message_id")
		require.NotEmpty(t, id)
		if prior := received[body]; prior != nil {
			require.Equal(t, prior.Properties.User.Get("wk.message_id"), id)
			require.Equal(t, prior.PacketID, p.PacketID)
			require.True(t, p.Duplicate(), "an unfinished exchange replays rather than publishing anew")
		} else {
			require.Empty(t, identities[id], "different business originals need distinct identities")
			identities[id], received[body] = body, p
		}
		if body != "original-successor-will" {
			require.NoError(t, bob.Client.Ack(p))
		}
		return body
	}
	phase = "create-real-first-started"
	set(firstStarted, "return(true)")
	alice, err := suite.ConnectMQTT(ctx, addresses[nodes-1], "alice", "alice-successor-token", "will-successor-original", true, 60, suite.MQTTConnectOptions{Will: &paho.WillMessage{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Payload: []byte("original-successor-will")}, WillProperties: &paho.WillProperties{User: paho.UserProperties{{Key: "wk.client_msg_no", Value: "original-successor-will"}}}})
	require.NoError(t, err)
	t.Cleanup(func() { abort(alice) })
	abort(alice)
	firstExecutor := -1
	require.Eventually(t, func() bool {
		for i, f := range faults {
			n, e := f.Count(ctx, firstStarted)
			r, re := f.Count(ctx, firstReply)
			if e == nil && re == nil && n > 0 && r > 0 {
				firstExecutor = i
				return true
			}
		}
		return false
	}, 25*time.Second, 50*time.Millisecond, "real first Started must apply before the unissued turn returns")
	report["first_started_executor"] = firstExecutor + 1
	disable(firstStarted)
	pause := "1*sleep(45000)->return(false)"
	if pressureCount > 0 {
		pause = "1*sleep(90000)->return(false)"
	}
	cut := beforeProposal
	switch state {
	case "queued":
		set(proposalMatch, `return("22636c69656e745f6964223a2277696c6c2d737563636573736f722d6f726967696e616c22")`)
		set(beforeProposal, pause)
	case "committed-unapplied":
		cut = beforeApply
		set(beforeApply, pause)
	case "persisted-uncommitted":
		cut = persistedUncommitted
		set(appendMatch, `return("22636c69656e745f6964223a2277696c6c2d737563636573736f722d6f726967696e616c22")`)
		set(persistMatch, `return("22636c69656e745f6964223a2277696c6c2d737563636573736f722d6f726967696e616c22")`)
		set(droppedAppend, "return(true)")
	default:
		t.Fatal("unknown proposal state")
	}
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
	require.Equal(t, firstExecutor, executor, "old and second-generation reservations must occupy the captured journal")
	if state != "persisted-uncommitted" {
		// Selected sleeps continue, while later calls stop queuing behind them.
		disable(cut)
	}
	if state == "queued" {
		disable(proposalMatch)
	}
	quiet := func(duration time.Duration) {
		call, done := context.WithTimeout(ctx, duration)
		item, e := bob.Receive(call)
		done()
		if e == nil && item != nil {
			body := string(item.Payload)
			observation := map[string]any{"known_business": expected[body], "original": body == "original-successor-will", "dup": item.Duplicate()}
			if prior := received[body]; prior != nil && item.Properties != nil {
				observation["same_message_id"] = prior.Properties.User.Get("wk.message_id") == item.Properties.User.Get("wk.message_id")
				observation["same_packet_id"] = prior.PacketID == item.PacketID
			}
			report["unexpected_quiet_delivery"] = observation
			t.Logf("body-free quiet observation: %v", observation)
		}
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
	if pressureCount > 0 {
		phase = "pressure-before-selected-resolution"
		for _, c := range pressureClients {
			abort(c)
		}
		require.Eventually(t, func() bool {
			full, e := faults[executor].Count(ctx, capacityFull)
			page, pe := faults[executor].Count(ctx, reclamationPage)
			return e == nil && pe == nil && full > 0 && page > 0
		}, 25*time.Second, 50*time.Millisecond, "independent clients must reach refusal and an actual page on the captured journal")
		report["in_flight_capacity_refusal"], report["in_flight_reclamation_page"] = true, true
	}
	phase = "expired-caller-and-grant"
	window, endWindow := context.WithTimeout(ctx, 21*time.Second)
	for {
		p, e := bob.Receive(window)
		if e != nil {
			require.ErrorIs(t, e, context.DeadlineExceeded, "observation must retain a healthy recipient")
			break
		}
		body := accept(p)
		if body == "original-successor-will" {
			require.True(t, pressureCount == 0 && nodes == 3 && state == "queued", "only a definite independent queued takeover may publish the original before resolution")
			require.Positive(t, sum(successorReply), "early original needs a real applied successor CAS")
		}
	}
	endWindow()
	var earlyIdentity string
	var earlyPacketID uint16
	if original := received["original-successor-will"]; original != nil {
		earlyIdentity, earlyPacketID = original.Properties.User.Get("wk.message_id"), original.PacketID
	}
	attemptsDuring := sum(publicationAttempt)
	newClaimsDuring := sum(successorReply)
	if pressureCount == 0 {
		require.LessOrEqual(t, attemptsDuring, 1)
	}
	if attemptsDuring > 0 && pressureCount == 0 {
		require.True(t, nodes == 3 && state == "queued")
		require.Positive(t, newClaimsDuring, "publication requires definite successor evidence")
	}
	if pressureCount > 0 {
		n, err := faults[executor].Count(ctx, publicationAttempt)
		require.NoError(t, err)
		require.Zero(t, n, "full captured journal cannot fund an independent publication without valid retirement authority")
		firstKept, err := faults[executor].Count(ctx, retainedFirst)
		require.NoError(t, err)
		firstUnknown, err := faults[executor].Count(ctx, uncertainFirst)
		require.NoError(t, err)
		secondKept, err := faults[executor].Count(ctx, retainedSecond)
		require.NoError(t, err)
		secondUnknown, err := faults[executor].Count(ctx, uncertainSecond)
		require.NoError(t, err)
		require.Positive(t, firstKept+firstUnknown, "real reads must retain the old current attempt")
		require.Positive(t, secondKept+secondUnknown, "real reads must retain the in-flight successor reservation")
		report["old_attempt_retained_during_pressure"], report["second_attempt_retained_during_pressure"] = true, true
	}
	secondReplyCount, err := faults[executor].Count(ctx, appliedReply)
	require.NoError(t, err)
	require.Zero(t, secondReplyCount, "the captured second-generation caller has no definite applied reply")
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
	if pressureCount > 0 {
		// No original arrived in this window, so Paho can flush every pressure
		// ACK before this first reconnect creates an unfinished original. Other
		// nodes can still send legitimate pressure originals while this happens.
		phase = "join-early-pressure-ack-window"
		join, finishJoin := context.WithTimeout(ctx, 15*time.Second)
		for {
			call, done := context.WithTimeout(join, 3*time.Second)
			item, e := bob.Receive(call)
			done()
			if e != nil {
				require.NoError(t, join.Err(), "pressure ACK observation must complete within its bound")
				require.ErrorIs(t, e, context.DeadlineExceeded, "pressure ACK observation must retain a healthy recipient")
				break
			}
			require.NotEqual(t, "original-successor-will", accept(item), "captured unresolved original remains unfunded")
		}
		finishJoin()
	}
	abort(bob)
	if pressureCount > 0 && outcome == "late" {
		disable(capacityLimit)
		report["ordinary_capacity_restored_after_pressure"] = true
	}
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
		// Keep the captured full journal closed through the counter snapshot
		// and joined kill. Restoring it earlier would allow pressure effects
		// between the final old-process count and SIGKILL. The replacement boot
		// starts with ordinary 1,024 admission; no uncertain record is erased.
		report["captured_capacity_held_until_exit"] = true
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
		for i, f := range faults {
			if i != executor {
				require.NoError(t, f.Disable(ctx, capacityLimit))
			}
		}
		report["ordinary_capacity_restored_after_exit"] = true
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
			require.Eventually(t, func() bool { return sum(fsmComplete) > 0 }, 100*time.Second, 50*time.Millisecond, "the delayed real proposal must reach durable FSM resolution")
			if pressureCount == 0 {
				require.Eventually(t, func() bool {
					n, err := faults[executor].Count(ctx, capacityFull)
					return err == nil && n > 0
				}, 30*time.Second, 50*time.Millisecond, "next reservation must encounter the old and unknown second-generation attempts")
				report["executor_capacity_refusal"] = true
			}
		}
	}
	bob = connectRecipient()
	require.True(t, bob.Connack.SessionPresent)
	report["persistent_reconnects"] = 1
	call, done := context.WithTimeout(ctx, 150*time.Second)
	var p *paho.Publish
	pendingPressure := make(map[string]bool)
	for len(received) < len(expected) || p == nil {
		item, e := bob.Receive(call)
		require.NoError(t, e, cluster.DumpDiagnostics())
		body := accept(item)
		if body == "original-successor-will" {
			p = item
		} else if p != nil {
			// Paho's ordered manual ACK tracker holds these behind the
			// intentionally unacknowledged original on this connection.
			pendingPressure[body] = true
		}
	}
	done()
	if earlyIdentity != "" {
		require.Equal(t, earlyIdentity, p.Properties.User.Get("wk.message_id"))
		require.Equal(t, earlyPacketID, p.PacketID)
		require.True(t, p.Duplicate())
	}
	// Deliberately leave the original exchange unfinished and prove an exact
	// replay through a second persistent reconnect, with no SUBSCRIBE.
	originalIdentity, originalPacketID := p.Properties.User.Get("wk.message_id"), p.PacketID
	if pressureCount > 0 {
		// Flush pressure ACKs preceding the original. Those following it are
		// ordered behind its withheld ACK and must replay on the next connection.
		phase = "join-pressure-ack-window"
		quiet(3 * time.Second)
		report["pressure_ack_observation_ms"] = 3000
	}
	report["unfinished_pressure_exchanges"] = len(pendingPressure)
	abort(bob)
	bob = connectRecipient()
	require.True(t, bob.Connack.SessionPresent)
	report["persistent_reconnects"] = 2
	call, done = context.WithTimeout(ctx, 35*time.Second)
	var replayed *paho.Publish
	for replayed == nil || len(pendingPressure) > 0 {
		item, e := bob.Receive(call)
		require.NoError(t, e, cluster.DumpDiagnostics())
		body := accept(item)
		if body == "original-successor-will" {
			require.Nil(t, replayed, "the original must replay only once on this connection")
			replayed = item
			require.NoError(t, bob.Client.Ack(item))
		} else {
			require.True(t, pendingPressure[body], "only pressure exchanges held behind the original may replay")
			delete(pendingPressure, body)
		}
	}
	done()
	require.Equal(t, p.Payload, replayed.Payload)
	require.Equal(t, originalIdentity, replayed.Properties.User.Get("wk.message_id"))
	require.Equal(t, originalPacketID, replayed.PacketID)
	require.True(t, replayed.Duplicate())
	report["original_packet_id_preserved"], report["original_exchange_replayed"] = true, true
	phase = "quiet-after-original"
	quiet(15 * time.Second)
	require.Equal(t, 1+pressureCount, killedPublicationAttempts+sum(publicationAttempt), "one original business publication across all process epochs")
	report["original_publication_preserved"] = true
	report["business_publications"], report["unexpected_deliveries"] = 1+pressureCount, 0
	phase = "complete"
}
