//go:build e2e

package subscribe_recovery

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

const intentFault = "wkMQTTSubscribeAfterIntent"
const completionFault = "wkMQTTSubscriptionEstablishmentBeforeCommit"
const completionEvent = "subscription_establishment_confirmed"
const confirmationFault = "wkMQTTReplayConfirmationMixedResult"

// Every replica result must be joined before classifying a temporary yield.
// A different replica's hard failure cannot become a retryable pending result.
func TestReplayConfirmationRejectsMixedReplicaFailures(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires a temporary gofail product binary")
	}
	for _, failure := range []string{"evidence", "callback"} {
		t.Run(failure, func(t *testing.T) { runRecovery(t, 3, "group", "mixed-"+failure) })
	}
}

func TestInterruptedSubscribeBackgroundRecovery(t *testing.T) {
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
		names := []string{intentFault, completionFault}
		if strings.HasPrefix(cut, "mixed-") {
			names = append(names, confirmationFault)
		}
		_, err := f.WaitListed(call, names...)
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
	// Seed the native person directory before subscribing, so inbox recovery
	// must finish both existing-source discovery and future-source qualification.
	send(1, "before-subscription")
	bob := connect(addrs[count-1])
	require.False(t, bob.Connack.SessionPresent)
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
	mixed := strings.HasPrefix(cut, "mixed-")
	faultName := intentFault
	if mixed {
		faultName = confirmationFault
		for _, f := range faults {
			call, done := context.WithTimeout(ctx, 2*time.Second)
			err := f.Enable(call, confirmationFault, `return("`+strings.TrimPrefix(cut, "mixed-")+`")`)
			done()
			require.NoError(t, err)
		}
	} else {
		toggle(faults[count-1], intentFault, true)
	}
	if cut == "before-completion" {
		for _, f := range faults {
			toggle(f, completionFault, true)
		}
	}
	_, err = bob.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 1}}})
	require.Error(t, err, "interrupted request cannot return SUBACK success")
	select {
	case <-bob.Client.Done():
	case <-ctx.Done():
		t.Fatal("interrupted foreground transport did not close")
	}
	call, done := context.WithTimeout(ctx, 2*time.Second)
	hits, err := faults[count-1].Count(call, faultName)
	done()
	require.NoError(t, err)
	if hits == 0 {
		for i, node := range nodes {
			t.Logf("intent point missing, node %d: %s", i+1, node.DumpDiagnostics())
		}
	}
	require.Positive(t, hits)
	gateHits := 0
	var offline *frame.SendackPacket
	if mixed {
		require.GreaterOrEqual(t, hits, 3, "every replica entered the joined cohort")
		call, done := context.WithTimeout(ctx, 2*time.Second)
		started, err := suite.FetchMetricValue(call, nodes[count-1].APIAddr(), "wukongim_goroutines_started_total", map[string]string{"module": "mqtt", "task": "replay_confirmation", "kind": "burst"})
		done()
		require.NoError(t, err)
		require.GreaterOrEqual(t, started, float64(3), "confirmation calls belong to the process supervisor")
		call, done = context.WithTimeout(ctx, 2*time.Second)
		v, err := suite.FetchMetricValue(call, nodes[count-1].APIAddr(), "wukongim_mqtt_subscription_closures_total", map[string]string{"operation": "subscribe", "reason": strings.TrimPrefix(cut, "mixed-")})
		done()
		require.NoError(t, err)
		require.EqualValues(t, 1, v, "pending must not hide a hard replica failure or cause deadline retries")
		require.Equal(t, baseline, eventCount())
		offline = send(2, "offline-pending")
		for _, f := range faults {
			toggle(f, confirmationFault, false)
		}
	}
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
		// The cursor is already prepared. A failed final activation must retain
		// this boundary when the later message advances the Channel tail.
		offline = send(2, "offline-pending")
		for _, f := range faults {
			toggle(f, completionFault, false)
		}
	}
	require.Eventually(t, func() bool { return eventCount() > baseline }, 40*time.Second, 200*time.Millisecond, "establishment did not finish while receiver was disconnected")
	if offline == nil {
		offline = send(2, "offline-pending")
	}
	// Resume the original Session without another SUBSCRIBE. The seed before
	// its original boundary must not appear, including after final-write retry.
	resumed := connect(addrs[0])
	require.True(t, resumed.Connack.SessionPresent)
	p := receive(resumed)
	identity(p, offline, "offline-pending")
	require.NoError(t, resumed.Client.Ack(p))
	fresh := send(3, "fresh-online")
	p = receive(resumed)
	identity(p, fresh, "fresh-online")
	require.NoError(t, resumed.Client.Ack(p))
	require.NoError(t, resumed.Close())
	dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if dir == "" {
		dir = nodes[0].Spec.RootDir
	}
	require.NoError(t, os.MkdirAll(dir, 0755))
	report := map[string]any{"scenario": "interrupted-subscribe", "target": target, "cut": cut, "nodes": count, "hash_slots": 256, "passed": true, "intent_fault_hits": hits, "completion_fault_hits": gateHits, "background_completion_before_reconnect": true, "foreground_subscribe_retries": 0, "session_present": true, "message_identity_preserved": true, "original_start_preserved": true, "offline_and_fresh_delivery": true, "failure_kind": "controlled-request-failure-and-connection-close"}
	if mixed {
		delete(report, "intent_fault_hits")
		report["confirmation_fault_hits"], report["hard_failure_preserved"], report["partial_suback_refused"] = hits, true, true
	}
	data, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(dir, fmt.Sprintf("mqtt-subscribe-%s-%s-%d.json", target, cut, count))
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
	t.Logf("result artifact: %s", path)
}
