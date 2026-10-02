//go:build e2e

package crash_recovery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

// TestPersistentSessionSurvivesOwnerProcessCrash kills the owner without any
// graceful shutdown and proves durable Session state carries the delivery.
func TestPersistentSessionSurvivesOwnerProcessCrash(t *testing.T) {
	s := suite.New(t)
	mqttAddr := suite.ReserveLoopbackPorts(t).GatewayAddr
	n := s.StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": mqttAddr}))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+n.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-fixture-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	connect := func() *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, mqttAddr, "bob", "bob-fixture-token", "crash-bob", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, n.DumpDiagnostics())
		t.Cleanup(func() { _ = c.Abort() })
		return c
	}
	send := func(no string, body string) *frame.SendackPacket {
		alice, err := suite.NewWKProtoClient()
		require.NoError(t, err)
		defer alice.Close()
		_, err = alice.ConnectAuthenticatedContext(ctx, n.GatewayAddr(), "alice", "crash-alice-"+no, "alice-fixture-token", frame.WEB)
		require.NoError(t, err, n.DumpDiagnostics())
		require.NoError(t, alice.SendFrame(&frame.SendPacket{ChannelID: "bob", ChannelType: frame.ChannelTypePerson, ClientSeq: 1, ClientMsgNo: no, Payload: []byte(body)}))
		ack, err := alice.ReadSendAck()
		require.NoError(t, err)
		require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
		return ack
	}

	before := connect()
	require.False(t, before.Connack.SessionPresent)
	sub, err := before.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
	require.NoError(t, err)
	require.Equal(t, []byte{1}, sub.Reasons)
	ack := send("crash-unacked", "crash body")
	original, err := before.Receive(ctx)
	require.NoError(t, err)
	require.False(t, original.Duplicate())

	// Abrupt loss: SIGKILL the whole process group, no graceful stop path.
	p := n.Process
	require.NoError(t, syscall.Kill(-p.Cmd.Process.Pid, syscall.SIGKILL))
	select {
	case <-p.Done():
	case <-ctx.Done():
		t.Fatal("killed node did not exit")
	}
	_ = p.Stop() // joins process-group cleanup before reusing ports/data
	restarted := &suite.NodeProcess{Spec: n.Spec, BinaryPath: p.BinaryPath}
	require.NoError(t, restarted.Start())
	n.Process = restarted
	require.NoError(t, n.Process.WaitWKProtoReady(ctx, n.GatewayAddr()), n.DumpDiagnostics())

	after := connect()
	require.True(t, after.Connack.SessionPresent, n.DumpDiagnostics())
	replay, err := after.Receive(ctx)
	require.NoError(t, err, n.DumpDiagnostics())
	require.True(t, replay.Duplicate())
	require.Equal(t, original.PacketID, replay.PacketID)
	require.Equal(t, original.Payload, replay.Payload)
	require.Equal(t, strconv.FormatInt(ack.MessageID, 10), replay.Properties.User.Get("wk.message_id"))
	require.NoError(t, after.Client.Ack(replay))

	// Delivery keeps working after recovery and the new message arrives once.
	next := send("crash-after", "after body")
	fresh, err := after.Receive(ctx)
	require.NoError(t, err, n.DumpDiagnostics())
	require.False(t, fresh.Duplicate())
	require.Equal(t, strconv.FormatInt(next.MessageID, 10), fresh.Properties.User.Get("wk.message_id"))
	require.NoError(t, after.Client.Ack(fresh))
	quiet, qcancel := context.WithTimeout(ctx, 2*time.Second)
	extra, err := after.Receive(quiet)
	qcancel()
	require.Error(t, err, "unexpected extra delivery %v", extra)

	reportDir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if reportDir == "" {
		reportDir = n.Spec.RootDir
	}
	require.NoError(t, os.MkdirAll(reportDir, 0755))
	report, err := json.MarshalIndent(map[string]any{"scenario": "mqtt-session-owner-crash", "nodes": 1, "hash_slots": 256, "passed": true, "signal": "SIGKILL", "session_present": true, "resubscriptions": 0, "packet_id_preserved": true, "dup_on_resume": true, "post_recovery_deliveries": 1, "extra_deliveries": 0}, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(reportDir, "mqtt-session-owner-crash.json")
	require.NoError(t, os.WriteFile(path, append(report, '\n'), 0600))
	t.Logf("result artifact: %s", path)
}
