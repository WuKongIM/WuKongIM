//go:build e2e

package scale

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

const group = "scale-group"

func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	require.NoError(t, err, name)
	require.Positive(t, n, name)
	return n
}

// receipt is one observed publication on one subscriber.
type receipt struct {
	seq uint64
	id  string
	at  time.Time
}

// subscriber owns one persistent MQTT Session and its ordered receipts.
type subscriber struct {
	uid string
	c   *suite.MQTTClient
	mu  sync.Mutex
	got []receipt
}

func (s *subscriber) snapshot() []receipt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]receipt(nil), s.got...)
}

// parallel runs f over n items with bounded concurrency and returns the first error.
func parallel(n, width int, f func(int) error) error {
	sem := make(chan struct{}, width)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			errs <- f(i)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[int(float64(len(sorted)-1)*p)]
}

// drain records publications until ctx ends; paho acknowledges QoS 1 itself.
func (s *subscriber) drain(ctx context.Context) {
	for {
		p, err := s.c.Receive(ctx)
		if err != nil {
			return
		}
		r := receipt{at: time.Now()}
		if p.Properties != nil {
			r.seq, _ = strconv.ParseUint(p.Properties.User.Get("wk.message_seq"), 10, 64)
			r.id = p.Properties.User.Get("wk.message_id")
		}
		s.mu.Lock()
		s.got = append(s.got, r)
		s.mu.Unlock()
	}
}

func TestGroupScaleDeliveryAndChurnRetirement(t *testing.T) {
	members := envInt(t, "WK_E2E_MQTT_SCALE_MEMBERS", 100000)
	conns := envInt(t, "WK_E2E_MQTT_SCALE_CONNECTIONS", 500)
	messages := envInt(t, "WK_E2E_MQTT_SCALE_MESSAGES", 20)
	churners := envInt(t, "WK_E2E_MQTT_SCALE_CHURN", 200)
	rounds := envInt(t, "WK_E2E_MQTT_SCALE_ROUNDS", 3)
	require.Less(t, conns, members, "member 0 is the sender")
	require.LessOrEqual(t, churners, conns)
	addr := suite.ReserveLoopbackPorts(t).GatewayAddr
	overrides := map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addr}
	profileEnabled := os.Getenv("WK_E2E_MQTT_SCALE_PROFILE") == "1"
	if profileEnabled {
		overrides["WK_DEBUG_API_ENABLE"] = "true"
	}
	n := suite.New(t).StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, overrides))
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	api := n.APIAddr()
	uid := func(i int) string { return fmt.Sprintf("m%06d", i) }
	phases := map[string]int64{}
	mark := func(name string, start time.Time) {
		phases[name] = time.Since(start).Milliseconds()
		t.Logf("phase %s completed in %d ms", name, phases[name])
	}
	t.Logf("scale API: %s", api)
	phase := "provision_members"
	confirmedMembers := 0
	defer func() {
		if !t.Failed() {
			return
		}
		dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
		if dir == "" {
			dir = n.Spec.RootDir
		}
		report := map[string]any{"scenario": "mqtt-group-scale", "passed": false, "failed_phase": phase, "profile_enabled": profileEnabled, "nodes": 1, "hash_slots": 256, "initial_slots": 12, "configured": map[string]int{"members": members, "connections": conns, "messages": messages, "churn_subscribers": churners, "churn_rounds": rounds}, "phase_ms": phases, "confirmed_members": confirmedMembers}
		require.NoError(t, os.MkdirAll(dir, 0700))
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		path := filepath.Join(dir, "mqtt-scale-failure.json")
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("failed result artifact: %s", path)
	}()

	start := time.Now()
	const batch = 5000
	for from := 0; from < members; from += batch {
		ids := make([]string, 0, batch)
		for i := from; i < min(from+batch, members); i++ {
			ids = append(ids, uid(i))
		}
		var err error
		if from == 0 {
			err = suite.PostChannel(ctx, api, map[string]any{"channel_id": group, "channel_type": frame.ChannelTypeGroup, "reset": 1, "subscribers": ids})
		} else {
			_, err = suite.PostJSON(ctx, "http://"+api+"/channel/subscriber_add", map[string]any{"channel_id": group, "channel_type": frame.ChannelTypeGroup, "subscribers": ids}, nil)
		}
		require.NoError(t, err, "provision members from %d", from)
		confirmedMembers += len(ids)
	}
	mark("provision_members", start)
	phase = "provision_tokens"
	topic := "wk/v1/groups/" + base64.RawURLEncoding.EncodeToString([]byte(group)) + "/messages"
	subscribe := func(c *suite.MQTTClient) error {
		ack, err := c.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 1}}})
		if err != nil {
			return err
		}
		if len(ack.Reasons) != 1 || ack.Reasons[0] != 1 {
			return fmt.Errorf("SUBACK reasons %v", ack.Reasons)
		}
		return nil
	}
	unsubscribe := func(c *suite.MQTTClient) error {
		ack, err := c.Client.Unsubscribe(ctx, &paho.Unsubscribe{Topics: []string{topic}})
		if err != nil {
			return err
		}
		if len(ack.Reasons) != 1 || ack.Reasons[0] != 0 {
			return fmt.Errorf("UNSUBACK reasons %v", ack.Reasons)
		}
		return nil
	}

	// Members 1..conns are online MQTT subscribers; member 0 sends over WKProto.
	start = time.Now()
	subs := make([]*subscriber, conns)
	require.NoError(t, parallel(conns+1, 32, func(i int) error {
		_, err := suite.PostJSON(ctx, "http://"+api+"/user/token", map[string]any{"uid": uid(i), "token": uid(i) + "-scale", "device_flag": 1, "device_level": 1}, nil)
		return err
	}))
	mark("provision_tokens", start)
	phase = "connect_subscribe"
	start = time.Now()
	drainCtx, stopDrain := context.WithCancel(ctx)
	defer stopDrain()
	// dumpMQTT logs nonzero MQTT series so a server-side closure reason is
	// visible before the node is stopped by cleanup.
	dumpMQTT := func() {
		call, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		samples, err := suite.FetchMetricSamples(call, api)
		if err != nil {
			t.Logf("MQTT metrics error: %v", err)
			return
		}
		for _, s := range samples {
			if len(s.Name) > 14 && s.Name[:14] == "wukongim_mqtt_" && s.Value != 0 {
				t.Logf("%s %v %v", s.Name, s.Labels, s.Value)
			}
		}
	}
	defer func() {
		if t.Failed() {
			dumpMQTT()
		}
	}()
	require.NoError(t, parallel(conns, 32, func(i int) error {
		u := uid(i + 1)
		c, err := suite.ConnectMQTT(ctx, addr, u, u+"-scale", "scale-"+u, false, 600)
		if err != nil {
			return fmt.Errorf("connect %s: %w", u, err)
		}
		if err = subscribe(c); err != nil {
			_ = c.Abort()
			return fmt.Errorf("subscribe %s: %w", u, err)
		}
		subs[i] = &subscriber{uid: u, c: c}
		return nil
	}), n.DumpDiagnostics())
	for _, s := range subs {
		t.Cleanup(func() { _ = s.c.Abort() })
		go s.drain(drainCtx)
	}
	mark("connect_subscribe", start)
	phase = "fanout"
	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sender.Close() })
	_, err = sender.ConnectAuthenticatedContext(ctx, n.GatewayAddr(), uid(0), "scale-sender", uid(0)+"-scale", frame.WEB)
	require.NoError(t, err)
	// The publisher waits through long fanout/churn windows. Gateway idle time
	// depends on inbound traffic, so keep this authenticated client alive through
	// real PING/PONG without reconnecting or replacing publication identities.
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	var heartbeatCount atomic.Uint64
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := sender.SendFrame(&frame.PingPacket{}); err != nil {
					heartbeatErr <- err
					return
				}
				heartbeatCount.Add(1)
			}
		}
	}()
	defer func() { stopHeartbeat(); <-heartbeatDone }()
	checkHeartbeat := func() {
		select {
		case err := <-heartbeatErr:
			require.NoError(t, err, "WKProto publisher heartbeat")
		default:
		}
	}
	sentAt := map[uint64]time.Time{}
	ids := map[uint64]string{}
	var clientSeq uint64
	send := func(label string) {
		checkHeartbeat()
		clientSeq++
		at := time.Now()
		require.NoError(t, sender.SendFrame(&frame.SendPacket{ChannelID: group, ChannelType: frame.ChannelTypeGroup, ClientSeq: clientSeq, ClientMsgNo: fmt.Sprintf("scale-%s-%d", label, clientSeq), Payload: []byte(label)}))
		ack, err := sender.ReadSendAck()
		require.NoError(t, err)
		require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
		sentAt[ack.MessageSeq], ids[ack.MessageSeq] = at, strconv.FormatInt(ack.MessageID, 10)
	}
	// awaitAll waits until every subscriber holds want receipts; it never
	// accepts fewer, so a silently smaller run cannot pass.
	awaitAll := func(want int, within time.Duration) {
		require.Eventually(t, func() bool {
			for _, s := range subs {
				if len(s.snapshot()) < want {
					return false
				}
			}
			return true
		}, within, 200*time.Millisecond, "every subscriber must receive %d messages", want)
	}

	start = time.Now()
	for range messages {
		send("fanout")
	}
	awaitAll(messages, 3*time.Minute)
	mark("fanout", start)
	phase = "idle_measurement"
	// Measure quiet cost only after the initial fanout has established replay
	// coverage. Include a complete ten-second refresh window, with no SENDs.
	barriers := func() float64 {
		call, done := context.WithTimeout(ctx, 2*time.Second)
		defer done()
		v, err := suite.FetchMetricValue(call, api, "wukongim_slot_read_barrier_duration_seconds_count", map[string]string{"result": "ok"})
		require.NoError(t, err)
		return v
	}
	time.Sleep(2 * time.Second) // Drain pending PUBACK and progress work.
	start = time.Now()
	idleBefore := barriers()
	time.Sleep(10 * time.Second)
	idleRate := (barriers() - idleBefore) / time.Since(start).Seconds()
	mark("idle_measurement", start)
	phase = "churn"
	t.Logf("idle Slot barriers/s=%.1f per subscriber/s=%.2f", idleRate, idleRate/float64(conns))

	retired := func() float64 {
		call, done := context.WithTimeout(ctx, 2*time.Second)
		defer done()
		v, err := suite.FetchMetricValue(call, api, "wukongim_mqtt_consumer_events_total", map[string]string{"event": "retired"})
		require.NoError(t, err)
		return v
	}
	retiredBefore := retired()
	start = time.Now()
	for round := range rounds {
		require.NoError(t, parallel(churners, 32, func(i int) error {
			if err := unsubscribe(subs[i].c); err != nil {
				return fmt.Errorf("round %d unsubscribe %s: %w", round, subs[i].uid, err)
			}
			if err := subscribe(subs[i].c); err != nil {
				return fmt.Errorf("round %d resubscribe %s: %w", round, subs[i].uid, err)
			}
			return nil
		}), n.DumpDiagnostics())
	}
	mark("churn", start)
	phase = "post_churn_delivery"
	start = time.Now()
	send("after-churn")
	awaitAll(messages+1, 3*time.Minute)
	mark("post_churn_delivery", start)
	phase = "retirement"
	start = time.Now()
	want := float64(churners * rounds)
	var retiredDelta float64
	require.Eventually(t, func() bool {
		retiredDelta = retired() - retiredBefore
		return retiredDelta >= want
	}, 4*time.Minute, time.Second, "churn tombstones must be retired")
	mark("retirement", start)
	phase = "verify"
	// Late or duplicate publications arrive within this quiet window.
	time.Sleep(2 * time.Second)
	stopDrain()
	// Every subscriber must see each sent message exactly once, in order, with
	// the SEND acknowledgement's identity; churned clients must not receive
	// anything sent while unsubscribed (nothing is sent during churn).
	seqs := make([]uint64, 0, len(sentAt))
	for seq := range sentAt {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	var duplicates, missing, reordered, wrongID, extra int
	var latencies []time.Duration
	lastReceipt := map[uint64]time.Time{}
	for _, s := range subs {
		got := s.snapshot()
		seen := map[uint64]bool{}
		var prev uint64
		for _, r := range got {
			if _, ok := sentAt[r.seq]; !ok {
				extra++
				continue
			}
			if seen[r.seq] {
				duplicates++
				continue
			}
			seen[r.seq] = true
			if r.seq <= prev {
				reordered++
			}
			prev = r.seq
			if r.id != ids[r.seq] {
				wrongID++
			}
			if r.at.After(lastReceipt[r.seq]) {
				lastReceipt[r.seq] = r.at
			}
		}
		missing += len(sentAt) - len(seen)
	}
	for _, seq := range seqs {
		latencies = append(latencies, lastReceipt[seq].Sub(sentAt[seq]))
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	checkHeartbeat()
	passed := duplicates == 0 && missing == 0 && reordered == 0 && wrongID == 0 && extra == 0 && len(seqs) == messages+1
	report := map[string]any{
		"scenario": "mqtt-group-scale", "nodes": 1, "hash_slots": 256, "initial_slots": 12, "profile_enabled": profileEnabled, "passed": passed && idleRate/float64(conns) < 10,
		"configured":    map[string]int{"members": members, "connections": conns, "messages": messages, "churn_subscribers": churners, "churn_rounds": rounds},
		"observed":      map[string]int{"subscribers": len(subs), "messages_sent": len(seqs), "duplicates": duplicates, "missing": missing, "reordered": reordered, "wrong_identity": wrongID, "unexpected": extra},
		"retired_delta": retiredDelta, "retired_expected_min": want, "phase_ms": phases, "wkproto_heartbeat_count": heartbeatCount.Load(),
		"delivery_latency_ms":      map[string]int64{"p50": percentile(latencies, 0.5).Milliseconds(), "p99": percentile(latencies, 0.99).Milliseconds(), "max": percentile(latencies, 1).Milliseconds()},
		"idle_barriers_per_second": idleRate, "idle_barriers_per_subscriber_second": idleRate / float64(conns),
	}
	if dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0700))
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		path := filepath.Join(dir, "mqtt-scale.json")
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	}
	require.Len(t, subs, conns)
	require.Len(t, seqs, messages+1)
	require.Zero(t, duplicates, "duplicates")
	require.Zero(t, missing, "missing")
	require.Zero(t, reordered, "reordered")
	require.Zero(t, wrongID, "wrong identity")
	require.Zero(t, extra, "unexpected publications")
	require.Less(t, idleRate/float64(conns), float64(10), "quiet subscribers must not saturate Slot reads")
}

// TestGroupMembershipProvisioningBudget isolates the HTTP setup that previously
// exhausted the full MQTT scale deadline, retaining real Slot/Raft persistence.
func TestGroupMembershipProvisioningBudget(t *testing.T) {
	if os.Getenv("WK_E2E_MQTT_MEMBERSHIP_PROBE") != "1" {
		t.Skip("set WK_E2E_MQTT_MEMBERSHIP_PROBE=1 for the bounded preparation probe")
	}
	const members = 10000
	overrides := map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12"}
	if os.Getenv("WK_E2E_MQTT_MEMBERSHIP_PROFILE") == "1" {
		overrides["WK_DEBUG_API_ENABLE"] = "true"
	}
	n := suite.New(t).StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, overrides))
	t.Logf("provisioning probe API: %s", n.APIAddr())
	metrics := func() []suite.MetricSample {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		samples, err := suite.FetchMetricSamples(ctx, n.APIAddr())
		require.NoError(t, err)
		return samples
	}
	before := metrics()
	rowLabels := map[string]string{"directory": "ordinary", "operation": "upsert"}
	rowsBefore := suite.SumMetricSamples(before, "wukongim_conversation_membership_mutation_rows_total", rowLabels)
	proposalsBefore := suite.SumMetricSamples(before, "wukongim_slot_proposals_total", nil)
	report := map[string]any{"scenario": "mqtt-member-provisioning", "members": members, "nodes": 1, "hash_slots": 256, "initial_slots": 12, "budget_seconds": 30, "profile_enabled": os.Getenv("WK_E2E_MQTT_MEMBERSHIP_PROFILE") == "1"}
	var batches []map[string]any
	start := time.Now()
	defer func() {
		after := metrics()
		report["passed"] = !t.Failed()
		report["elapsed_ms"] = time.Since(start).Milliseconds()
		report["batches"] = batches
		report["projected_rows_delta"] = suite.SumMetricSamples(after, "wukongim_conversation_membership_mutation_rows_total", rowLabels) - rowsBefore
		report["slot_proposals_delta"] = suite.SumMetricSamples(after, "wukongim_slot_proposals_total", nil) - proposalsBefore
		if dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR"); dir != "" {
			require.NoError(t, os.MkdirAll(dir, 0700))
			data, err := json.MarshalIndent(report, "", "  ")
			require.NoError(t, err)
			path := filepath.Join(dir, "mqtt-membership.json")
			require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
			t.Logf("result artifact: %s", path)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const channelID = "membership-budget-group"
	uid := func(i int) string { return fmt.Sprintf("budget-member-%06d", i) }
	for from := 0; from < members; from += 5000 {
		ids := make([]string, 5000)
		for i := range ids {
			ids[i] = uid(from + i)
		}
		batchStart := time.Now()
		var err error
		if from == 0 {
			err = suite.PostChannel(ctx, n.APIAddr(), map[string]any{"channel_id": channelID, "channel_type": frame.ChannelTypeGroup, "subscribers": ids})
		} else {
			_, err = suite.PostJSON(ctx, "http://"+n.APIAddr()+"/channel/subscriber_add", map[string]any{"channel_id": channelID, "channel_type": frame.ChannelTypeGroup, "subscribers": ids}, nil)
		}
		samples := metrics()
		batches = append(batches, map[string]any{"from": from, "size": len(ids), "elapsed_ms": time.Since(batchStart).Milliseconds(), "confirmed": err == nil, "projected_rows_delta": suite.SumMetricSamples(samples, "wukongim_conversation_membership_mutation_rows_total", rowLabels) - rowsBefore, "slot_proposals_delta": suite.SumMetricSamples(samples, "wukongim_slot_proposals_total", nil) - proposalsBefore})
		require.NoError(t, err, "prepare %d members within the 30-second setup budget", members)
	}
	report["provision_ms"] = time.Since(start).Milliseconds()
	require.Equal(t, float64(members), suite.SumMetricSamples(metrics(), "wukongim_conversation_membership_mutation_rows_total", rowLabels)-rowsBefore)
	verify, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	sent, err := suite.PostMessageSend(verify, n.APIAddr(), map[string]any{"from_uid": uid(0), "channel_id": channelID, "channel_type": frame.ChannelTypeGroup, "client_msg_no": "budget-one", "payload": base64.StdEncoding.EncodeToString([]byte("membership proof"))})
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSuccess), sent.Reason)
	for _, i := range []int{0, members / 2, members - 1} {
		suite.RequireConversationEventually(t, *n, uid(i), channelID, func(item suite.ConversationListItem) error {
			if item.LastMessage == nil || item.LastMessage.MessageID != uint64(sent.MessageID) || item.LastMessage.MessageSeq != sent.MessageSeq {
				return fmt.Errorf("sampled membership cannot hydrate committed publication")
			}
			return nil
		})
	}
	require.Equal(t, float64(members), suite.SumMetricSamples(metrics(), "wukongim_conversation_membership_mutation_rows_total", rowLabels)-rowsBefore, "SEND must not mutate ordinary memberships")
}

// TestGroupChurnWithInFlightDelivery isolates control contention before fanout
// drains. It does not require old unadmitted messages after subscription removal.
func TestGroupChurnWithInFlightDelivery(t *testing.T) {
	if os.Getenv("WK_E2E_MQTT_CHURN_PROBE") != "1" {
		t.Skip("set WK_E2E_MQTT_CHURN_PROBE=1 for the bounded contention probe")
	}
	conns := envInt(t, "WK_E2E_MQTT_CHURN_PROBE_CONNECTIONS", 500)
	churners := min(200, conns)
	const members, messages, rounds = 2000, 20, 3
	require.Less(t, conns, members)
	addr := suite.ReserveLoopbackPorts(t).GatewayAddr
	overrides := map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addr}
	profile := os.Getenv("WK_E2E_MQTT_CHURN_PROBE_PROFILE") == "1"
	if profile {
		overrides["WK_DEBUG_API_ENABLE"] = "true"
	}
	n := suite.New(t).StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, overrides))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	started := time.Now()
	api := n.APIAddr()
	t.Logf("churn probe API: %s", api)
	phase := "setup"
	var confirmedSubscribes, confirmedUnsubscribes atomic.Uint64
	subs := make([]*subscriber, conns)
	initialAtChurn := 0
	var freshSeq uint64
	report := map[string]any{"scenario": "mqtt-churn-in-flight", "nodes": 1, "hash_slots": 256, "initial_slots": 12, "members": members, "connections": conns, "messages": messages, "churn_subscribers": churners, "rounds": rounds, "profile_enabled": profile, "foreground_retries": 0}
	defer func() {
		report["passed"], report["phase"], report["elapsed_ms"] = !t.Failed(), phase, time.Since(started).Milliseconds()
		report["confirmed_subscribes"], report["confirmed_unsubscribes"] = confirmedSubscribes.Load(), confirmedUnsubscribes.Load()
		report["initial_receipts_at_churn"], report["initial_receipts_expected"] = initialAtChurn, conns*messages
		freshReceipts := 0
		if freshSeq != 0 {
			for _, s := range subs {
				if s != nil {
					for _, r := range s.snapshot() {
						if r.seq == freshSeq {
							freshReceipts++
						}
					}
				}
			}
		}
		report["fresh_receipts"] = freshReceipts
		call, done := context.WithTimeout(context.Background(), 2*time.Second)
		samples, err := suite.FetchMetricSamples(call, api)
		done()
		closures := map[string]float64{}
		if err == nil {
			for _, s := range samples {
				if s.Name == "wukongim_mqtt_subscription_closures_total" && s.Value != 0 {
					closures[s.Labels["operation"]+"/"+s.Labels["reason"]] += s.Value
				}
			}
		}
		report["closures"] = closures
		dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
		if dir == "" {
			dir = n.Spec.RootDir
		}
		require.NoError(t, os.MkdirAll(dir, 0700))
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		path := filepath.Join(dir, "mqtt-churn-in-flight.json")
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("churn result artifact: %s", path)
	}()
	uid := func(i int) string { return fmt.Sprintf("p%06d", i) }
	ids := make([]string, members)
	for i := range ids {
		ids[i] = uid(i)
	}
	require.NoError(t, suite.PostChannel(ctx, api, map[string]any{"channel_id": group, "channel_type": frame.ChannelTypeGroup, "reset": 1, "subscribers": ids}))
	require.NoError(t, parallel(conns+1, 32, func(i int) error {
		_, err := suite.PostJSON(ctx, "http://"+api+"/user/token", map[string]any{"uid": uid(i), "token": uid(i) + "-probe", "device_flag": 1, "device_level": 1}, nil)
		return err
	}))
	topic := "wk/v1/groups/" + base64.RawURLEncoding.EncodeToString([]byte(group)) + "/messages"
	subscribe := func(c *suite.MQTTClient) error {
		ack, err := c.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 1}}})
		if err != nil {
			return err
		}
		if len(ack.Reasons) != 1 || ack.Reasons[0] != 1 {
			return fmt.Errorf("SUBACK reasons %v", ack.Reasons)
		}
		confirmedSubscribes.Add(1)
		return nil
	}
	unsubscribe := func(c *suite.MQTTClient) error {
		ack, err := c.Client.Unsubscribe(ctx, &paho.Unsubscribe{Topics: []string{topic}})
		if err != nil {
			return err
		}
		if len(ack.Reasons) != 1 || ack.Reasons[0] != 0 {
			return fmt.Errorf("UNSUBACK reasons %v", ack.Reasons)
		}
		confirmedUnsubscribes.Add(1)
		return nil
	}
	drainCtx, stopDrain := context.WithCancel(ctx)
	defer stopDrain()
	require.NoError(t, parallel(conns, 32, func(i int) error {
		u := uid(i + 1)
		c, err := suite.ConnectMQTT(ctx, addr, u, u+"-probe", "probe-"+u, false, 600)
		if err != nil {
			return fmt.Errorf("connect %d: %w", i, err)
		}
		t.Cleanup(func() { _ = c.Abort() })
		if err = subscribe(c); err != nil {
			return fmt.Errorf("subscribe %d: %w", i, err)
		}
		s := &subscriber{uid: u, c: c}
		subs[i] = s
		go s.drain(drainCtx)
		return nil
	}), n.DumpDiagnostics())
	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sender.Close() })
	_, err = sender.ConnectAuthenticatedContext(ctx, n.GatewayAddr(), uid(0), "probe-sender", uid(0)+"-probe", frame.WEB)
	require.NoError(t, err)
	sentIDs := map[uint64]string{}
	var clientSeq uint64
	send := func() uint64 {
		clientSeq++
		require.NoError(t, sender.SendFrame(&frame.SendPacket{ChannelID: group, ChannelType: frame.ChannelTypeGroup, ClientSeq: clientSeq, ClientMsgNo: fmt.Sprintf("probe-%d", clientSeq), Payload: []byte("probe")}))
		ack, err := sender.ReadSendAck()
		require.NoError(t, err)
		require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
		sentIDs[ack.MessageSeq] = strconv.FormatInt(ack.MessageID, 10)
		return ack.MessageSeq
	}
	metricRetired := func() float64 {
		call, done := context.WithTimeout(ctx, 2*time.Second)
		defer done()
		v, err := suite.FetchMetricValue(call, api, "wukongim_mqtt_consumer_events_total", map[string]string{"event": "retired"})
		require.NoError(t, err)
		return v
	}
	retiredBefore := metricRetired()
	phase = "send_initial"
	for range messages {
		send()
	}
	for _, s := range subs {
		initialAtChurn += len(s.snapshot())
	}
	require.Less(t, initialAtChurn, conns*messages, "probe must overlap pending initial deliveries")
	phase = "churn"
	for round := range rounds {
		require.NoError(t, sender.SendFrame(&frame.PingPacket{}))
		require.NoError(t, parallel(churners, 32, func(i int) error {
			if err := unsubscribe(subs[i].c); err != nil {
				return fmt.Errorf("round %d unsubscribe %d: %w", round, i, err)
			}
			if err := subscribe(subs[i].c); err != nil {
				return fmt.Errorf("round %d resubscribe %d: %w", round, i, err)
			}
			return nil
		}), n.DumpDiagnostics())
	}
	phase = "fresh_delivery"
	require.NoError(t, sender.SendFrame(&frame.PingPacket{}))
	freshSeq = send()
	require.Eventually(t, func() bool {
		for _, s := range subs {
			found := false
			for _, r := range s.snapshot() {
				if r.seq == freshSeq {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}, 2*time.Minute, 200*time.Millisecond, "every subscriber must receive the fresh publication")
	phase = "retirement"
	var retiredDelta float64
	require.Eventually(t, func() bool {
		retiredDelta = metricRetired() - retiredBefore
		return retiredDelta >= float64(churners*rounds)
	}, time.Minute, time.Second)
	report["retired_delta"] = retiredDelta
	phase = "verify"
	time.Sleep(2 * time.Second)
	stopDrain()
	for i, s := range subs {
		seen := map[uint64]bool{}
		var prev uint64
		for _, r := range s.snapshot() {
			expected, ok := sentIDs[r.seq]
			require.True(t, ok, "unexpected publication for %d", i)
			require.Equal(t, expected, r.id, "publication identity for %d", i)
			require.False(t, seen[r.seq], "duplicate publication for %d", i)
			require.Greater(t, r.seq, prev, "publication order for %d", i)
			seen[r.seq], prev = true, r.seq
		}
		require.True(t, seen[freshSeq], "fresh publication for %d", i)
	}
	require.EqualValues(t, conns+churners*rounds, confirmedSubscribes.Load())
	require.EqualValues(t, churners*rounds, confirmedUnsubscribes.Load())
	phase = "complete"
}
