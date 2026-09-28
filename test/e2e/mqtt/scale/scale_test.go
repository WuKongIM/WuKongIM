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
	n := suite.New(t).StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addr}))
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	api := n.APIAddr()
	uid := func(i int) string { return fmt.Sprintf("m%06d", i) }
	phases := map[string]int64{}
	mark := func(name string, start time.Time) { phases[name] = time.Since(start).Milliseconds() }

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
	}
	mark("provision_members", start)
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
	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sender.Close() })
	_, err = sender.ConnectAuthenticatedContext(ctx, n.GatewayAddr(), uid(0), "scale-sender", uid(0)+"-scale", frame.WEB)
	require.NoError(t, err)
	sentAt := map[uint64]time.Time{}
	ids := map[uint64]string{}
	var clientSeq uint64
	send := func(label string) {
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
	start = time.Now()
	send("after-churn")
	awaitAll(messages+1, 3*time.Minute)
	mark("post_churn_delivery", start)
	start = time.Now()
	want := float64(churners * rounds)
	var retiredDelta float64
	require.Eventually(t, func() bool {
		retiredDelta = retired() - retiredBefore
		return retiredDelta >= want
	}, 4*time.Minute, time.Second, "churn tombstones must be retired")
	mark("retirement", start)
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
	passed := duplicates == 0 && missing == 0 && reordered == 0 && wrongID == 0 && extra == 0 && len(seqs) == messages+1
	report := map[string]any{
		"scenario": "mqtt-group-scale", "nodes": 1, "hash_slots": 256, "passed": passed,
		"configured":    map[string]int{"members": members, "connections": conns, "messages": messages, "churn_subscribers": churners, "churn_rounds": rounds},
		"observed":      map[string]int{"subscribers": len(subs), "messages_sent": len(seqs), "duplicates": duplicates, "missing": missing, "reordered": reordered, "wrong_identity": wrongID, "unexpected": extra},
		"retired_delta": retiredDelta, "retired_expected_min": want, "phase_ms": phases,
		"delivery_latency_ms": map[string]int64{"p50": percentile(latencies, 0.5).Milliseconds(), "p99": percentile(latencies, 0.99).Milliseconds(), "max": percentile(latencies, 1).Milliseconds()},
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
}
