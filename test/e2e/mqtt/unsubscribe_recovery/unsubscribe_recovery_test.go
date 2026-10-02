//go:build e2e

package unsubscribe_recovery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

const intentFault = "wkMQTTUnsubscribeAfterIntent"
const completionFault = "wkMQTTSubscriptionRemovalBeforeCommit"
const completionEvent = "subscription_removal_confirmed"

func TestInterruptedUnsubscribeBackgroundRecovery(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("set WK_E2E_GOFAIL_MQTT=1 with a gofail-enabled product binary")
	}
	require.NotEmpty(t, strings.TrimSpace(os.Getenv("WK_E2E_BINARY")))
	for _, count := range []int{1, 3} {
		for _, target := range []string{"inbox", "group"} {
			for _, cut := range []string{"after-intent", "before-completion"} {
				t.Run(fmt.Sprintf("%d-node-cluster/%s/%s", count, target, cut), func(t *testing.T) { runRecovery(t, count, target, cut) })
			}
		}
	}
}

func runRecovery(t *testing.T, count int, target, cut string) {
	t.Helper()
	s := suite.New(t)
	var opts []suite.Option
	addrs := make([]string, count)
	faults := make([]suite.GofailEndpoint, count)
	for i := range count {
		addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		faults[i] = suite.ReserveGofailEndpoint(t)
		opts = append(opts, suite.WithNodeEnv(uint64(i+1), faults[i].Env()), suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i]}))
	}
	var nodes []*suite.StartedNode
	if count == 1 {
		nodes = append(nodes, s.StartSingleNodeCluster(opts...))
	} else {
		c := s.StartThreeNodeCluster(append(opts, suite.WithManagerHTTP())...)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		require.NoError(t, c.WaitClusterReady(ctx), c.DumpDiagnostics())
		_, err := c.WaitSlotLeadersStable(ctx, time.Second)
		cancel()
		require.NoError(t, err, c.DumpDiagnostics())
		for i := range count {
			nodes = append(nodes, c.MustNode(uint64(i+1)))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	for _, f := range faults {
		call, done := context.WithTimeout(ctx, 3*time.Second)
		_, err := f.WaitListed(call, intentFault, completionFault)
		done()
		require.NoError(t, err)
	}
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+nodes[0].APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-recovery-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	channelID := "bob"
	kind := frame.ChannelTypePerson
	topic := "wk/v1/users/Ym9i/messages"
	if target == "group" {
		channelID = "recover-group"
		kind = frame.ChannelTypeGroup
		topic = "wk/v1/groups/" + base64.RawURLEncoding.EncodeToString([]byte(channelID)) + "/messages"
		require.NoError(t, suite.PostChannel(ctx, nodes[0].APIAddr(), map[string]any{"channel_id": channelID, "channel_type": kind, "reset": 1, "subscribers": []string{"alice", "bob"}}))
	}
	connect := func(addr string) *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, addr, "bob", "bob-recovery-token", "recovery-bob", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, nodes[0].DumpDiagnostics())
		t.Cleanup(func() { _ = c.Abort() })
		return c
	}
	subscribe := func(c *suite.MQTTClient) {
		started := time.Now()
		a, err := c.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 1}}})
		if err != nil {
			t.Logf("SUBSCRIBE failed after %s", time.Since(started))
			for i, node := range nodes {
				call, done := context.WithTimeout(context.Background(), time.Second)
				samples, metricErr := suite.FetchMetricSamples(call, node.APIAddr())
				done()
				t.Logf("node %d MQTT metrics error: %v", i+1, metricErr)
				for _, sample := range samples {
					if strings.HasPrefix(sample.Name, "wukongim_mqtt_") && sample.Value != 0 {
						t.Logf("%s %v %v", sample.Name, sample.Labels, sample.Value)
					}
				}
				t.Logf("node %d: %s", i+1, node.DumpDiagnostics())
			}
		}
		require.NoError(t, err)
		require.Equal(t, []byte{1}, a.Reasons)
	}
	bob := connect(addrs[count-1])
	require.False(t, bob.Connack.SessionPresent)
	subscribe(bob)
	closureCount := func() float64 {
		call, done := context.WithTimeout(ctx, 2*time.Second)
		defer done()
		value, err := suite.FetchMetricValue(call, nodes[count-1].APIAddr(), "wukongim_mqtt_subscription_closures_total", map[string]string{"operation": "unsubscribe", "reason": "deadline"})
		require.NoError(t, err)
		return value
	}
	require.Zero(t, closureCount())
	alice, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = alice.Close() })
	_, err = alice.ConnectAuthenticatedContext(ctx, nodes[0].GatewayAddr(), "alice", "recovery-wk", "alice-recovery-token", frame.WEB)
	require.NoError(t, err)
	send := func(seq uint64, label string) *frame.SendackPacket {
		require.NoError(t, alice.SendFrame(&frame.SendPacket{ChannelID: channelID, ChannelType: kind, ClientSeq: seq, ClientMsgNo: "recover-" + label, Payload: []byte(label)}))
		ack, err := alice.ReadSendAck()
		require.NoError(t, err)
		require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
		return ack
	}
	receive := func(c *suite.MQTTClient) *paho.Publish {
		p, err := c.Receive(ctx)
		require.NoError(t, err, nodes[0].DumpDiagnostics())
		return p
	}
	identity := func(p *paho.Publish, a *frame.SendackPacket, label string) {
		require.Equal(t, []byte(label), p.Payload)
		require.NotNil(t, p.Properties)
		require.Equal(t, strconv.FormatInt(a.MessageID, 10), p.Properties.User.Get("wk.message_id"))
		require.Equal(t, strconv.FormatUint(a.MessageSeq, 10), p.Properties.User.Get("wk.message_seq"))
	}
	first := send(1, "unacked")
	original := receive(bob)
	identity(original, first, "unacked")
	require.False(t, original.Duplicate())
	require.NotZero(t, original.PacketID)
	send(2, "discarded-backlog")
	eventCount := func() float64 {
		var total float64
		for _, n := range nodes {
			call, done := context.WithTimeout(ctx, 2*time.Second)
			v, err := suite.FetchMetricValue(call, n.APIAddr(), "wukongim_mqtt_consumer_events_total", map[string]string{"event": completionEvent})
			done()
			require.NoError(t, err)
			total += v
		}
		return total
	}
	baseline := eventCount()
	require.Zero(t, baseline)
	toggle := func(f suite.GofailEndpoint, name string, on bool) {
		call, done := context.WithTimeout(ctx, 2*time.Second)
		defer done()
		var err error
		if on {
			err = f.Enable(call, name, `return(true)`)
		} else {
			err = f.Disable(call, name)
		}
		require.NoError(t, err)
	}
	toggle(faults[count-1], intentFault, true)
	if cut == "before-completion" {
		for _, f := range faults {
			toggle(f, completionFault, true)
		}
	}
	_, err = bob.Client.Unsubscribe(ctx, &paho.Unsubscribe{Topics: []string{topic}})
	require.Error(t, err, "interrupted request cannot return UNSUBACK success")
	select {
	case <-bob.Client.Done():
	case <-ctx.Done():
		t.Fatal("interrupted foreground transport did not close")
	}
	// Transport closure can become visible before the packet defer records it.
	require.Eventually(t, func() bool { return closureCount() == 1 }, time.Second, 10*time.Millisecond)
	closureObservations := closureCount()
	call, done := context.WithTimeout(ctx, 2*time.Second)
	hits, err := faults[count-1].Count(call, intentFault)
	done()
	require.NoError(t, err)
	if hits == 0 {
		for i, node := range nodes {
			t.Logf("intent point missing, node %d: %s", i+1, node.DumpDiagnostics())
		}
	}
	require.Positive(t, hits)
	gateHits := 0
	if cut == "before-completion" {
		require.Eventually(t, func() bool {
			gateHits = 0
			for _, f := range faults {
				call, done := context.WithTimeout(ctx, 2*time.Second)
				v, err := f.Count(call, completionFault)
				done()
				if err != nil {
					return false
				}
				gateHits += v
			}
			return gateHits > 0
		}, 30*time.Second, 100*time.Millisecond, "background projection never reached final completion")
		require.Equal(t, baseline, eventCount(), "completion cannot be observed before its commit")
		for _, f := range faults {
			toggle(f, completionFault, false)
		}
	}
	require.Eventually(t, func() bool { return eventCount() > baseline }, 40*time.Second, 200*time.Millisecond, "removal did not finish while receiver was disconnected")
	// No foreground UNSUBSCRIBE retry and no SUBSCRIBE before old exchange recovery.
	resumed := connect(addrs[0])
	require.True(t, resumed.Connack.SessionPresent)
	replay := receive(resumed)
	identity(replay, first, "unacked")
	require.True(t, replay.Duplicate())
	require.Equal(t, original.PacketID, replay.PacketID)
	require.NoError(t, resumed.Client.Ack(replay))
	send(3, "while-unsubscribed")
	quiet, stop := context.WithTimeout(ctx, 300*time.Millisecond)
	_, err = resumed.Receive(quiet)
	stop()
	require.ErrorIs(t, err, context.DeadlineExceeded, "closed intent leaked a new publication")
	subscribe(resumed)
	fresh := send(4, "fresh-generation")
	p := receive(resumed)
	identity(p, fresh, "fresh-generation")
	require.False(t, p.Duplicate())
	require.NoError(t, resumed.Client.Ack(p))
	require.NoError(t, resumed.Close())
	dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if dir == "" {
		dir = nodes[0].Spec.RootDir
	}
	require.NoError(t, os.MkdirAll(dir, 0755))
	report := map[string]any{"subscription_closure_observations": closureObservations, "scenario": "interrupted-unsubscribe", "target": target, "cut": cut, "nodes": count, "hash_slots": 256, "passed": true, "intent_fault_hits": hits, "completion_fault_hits": gateHits, "background_completion_before_reconnect": true, "foreground_unsubscribe_retries": 0, "session_present": true, "packet_id_preserved": true, "dup_on_resume": true, "message_identity_preserved": true, "resubscribe_delivers_only_fresh": true, "failure_kind": "controlled-request-failure-and-connection-close"}
	data, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(dir, fmt.Sprintf("mqtt-unsubscribe-%s-%s-%d.json", target, cut, count))
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
	t.Logf("result artifact: %s", path)
}

// TestColdGroupSubscriptionAdmission exercises the setup where failure was
// observed before the unsubscribe fault is enabled. One cluster amortizes startup while each
// attempt uses a fresh group and persistent Session, without request retries.
func TestColdGroupSubscriptionAdmission(t *testing.T) {
	runColdGroupSubscriptionAdmission(t, 64)
}

// TestColdStartupGroupSubscriptionAdmission retains process startup for every
// repetition; it does not substitute new groups in an already warmed cluster.
func TestColdStartupGroupSubscriptionAdmission(t *testing.T) {
	runColdGroupSubscriptionAdmission(t, 1)
}

func runColdGroupSubscriptionAdmission(t *testing.T, attempts int) {
	t.Helper()
	s := suite.New(t)
	var options []suite.Option
	addrs := make([]string, 3)
	var faults []suite.GofailEndpoint
	withFaultControl := attempts == 1 && os.Getenv("WK_E2E_GOFAIL_MQTT") == "1"
	for i := range addrs {
		addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		if withFaultControl {
			endpoint := suite.ReserveGofailEndpoint(t)
			faults = append(faults, endpoint)
			options = append(options, suite.WithNodeEnv(uint64(i+1), endpoint.Env()))
		}
		options = append(options, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i]}))
	}
	cluster := s.StartThreeNodeCluster(append(options, suite.WithManagerHTTP())...)
	ready, done := context.WithTimeout(context.Background(), 30*time.Second)
	require.NoError(t, cluster.WaitClusterReady(ready), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ready, time.Second)
	done()
	require.NoError(t, err, cluster.DumpDiagnostics())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	first, last := cluster.MustNode(1), cluster.MustNode(3)
	for _, endpoint := range faults {
		call, done := context.WithTimeout(ctx, 3*time.Second)
		_, err := endpoint.WaitListed(call, intentFault, completionFault)
		done()
		require.NoError(t, err)
	}
	durations := make([]int64, 0, attempts)
	completed := 0
	step := "credentials"
	var observations []map[string]any
	defer func() {
		report := map[string]any{"fault_control_enabled": withFaultControl, "scenario": "cold-group-subscribe", "nodes": 3, "hash_slots": 256, "attempt_limit": attempts, "completed": completed, "passed": !t.Failed() && completed == attempts, "last_step": step, "foreground_retries": 0, "subscribe_elapsed_us": durations, "closure_observations": observations}
		dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
		if dir == "" {
			dir = first.Spec.RootDir
		}
		require.NoError(t, os.MkdirAll(dir, 0755))
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		name := "mqtt-cold-group-subscribe.json"
		if attempts == 1 {
			name = "mqtt-startup-group-subscribe.json"
		}
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	}()
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-recovery-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	for i := 0; i < attempts; i++ {
		group, clientID := "recover-group", "recovery-bob"
		if i != 0 {
			group += fmt.Sprintf("-%d", i)
			clientID += fmt.Sprintf("-%d", i)
		}
		step = "create_group"
		require.NoError(t, suite.PostChannel(ctx, first.APIAddr(), map[string]any{"channel_id": group, "channel_type": frame.ChannelTypeGroup, "reset": 1, "subscribers": []string{"alice", "bob"}}))
		step = "connect"
		bob, err := suite.ConnectMQTT(ctx, addrs[2], "bob", "bob-recovery-token", clientID, false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, last.DumpDiagnostics())
		t.Cleanup(func() { _ = bob.Abort() })
		require.False(t, bob.Connack.SessionPresent)
		step = "subscribe"
		started := time.Now()
		ack, err := bob.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/groups/" + base64.RawURLEncoding.EncodeToString([]byte(group)) + "/messages", QoS: 1}}})
		durations = append(durations, time.Since(started).Microseconds())
		if err != nil {
			for nodeID := uint64(1); nodeID <= 3; nodeID++ {
				node := cluster.MustNode(nodeID)
				call, done := context.WithTimeout(context.Background(), time.Second)
				samples, metricErr := suite.FetchMetricSamples(call, node.APIAddr())
				done()
				t.Logf("attempt %d node %d metric error: %v", i, nodeID, metricErr)
				for _, v := range samples {
					if v.Name == "wukongim_mqtt_subscription_closures_total" && v.Value != 0 {
						observations = append(observations, map[string]any{"node": nodeID, "operation": v.Labels["operation"], "reason": v.Labels["reason"], "count": v.Value})
					}
				}
				t.Logf("node %d: %s", nodeID, node.DumpDiagnostics())
			}
		}
		require.NoError(t, err, "first SUBSCRIBE attempt %d after %s", i, time.Since(started))
		require.Equal(t, []byte{1}, ack.Reasons)
		step = "disconnect"
		require.NoError(t, bob.Close())
		completed++
	}
	step = "complete"
}
