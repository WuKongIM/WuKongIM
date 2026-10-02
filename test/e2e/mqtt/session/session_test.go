//go:build e2e

package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

func TestPersistentSessionReconnectFutureInboxAndTakeover(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) {
			var opts []suite.Option
			addrs := make([]string, count)
			for i := range count {
				addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
				opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i]}))
			}
			s := suite.New(t)
			var first *suite.StartedNode
			if count == 1 {
				first = s.StartSingleNodeCluster(opts...)
			} else {
				cluster := s.StartThreeNodeCluster(append(opts, suite.WithManagerHTTP())...)
				ready, done := context.WithTimeout(context.Background(), 30*time.Second)
				require.NoError(t, cluster.WaitClusterReady(ready), cluster.DumpDiagnostics())
				_, err := cluster.WaitSlotLeadersStable(ready, time.Second)
				done()
				require.NoError(t, err, cluster.DumpDiagnostics())
				first = cluster.MustNode(1)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			for _, uid := range []string{"alice", "bob", "carol"} {
				_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-fixture-token", "device_flag": 1, "device_level": 1}, nil)
				require.NoError(t, err)
			}
			native := func(uid string) *suite.WKProtoClient {
				c, err := suite.NewWKProtoClient()
				require.NoError(t, err)
				t.Cleanup(func() { _ = c.Close() })
				_, err = c.ConnectAuthenticatedContext(ctx, first.GatewayAddr(), uid, uid+"-wk", uid+"-fixture-token", frame.WEB)
				require.NoError(t, err)
				return c
			}
			alice, carol := native("alice"), native("carol")
			send := func(c *suite.WKProtoClient, seq uint64, no string) *frame.SendackPacket {
				require.NoError(t, c.SendFrame(&frame.SendPacket{ChannelID: "bob", ChannelType: frame.ChannelTypePerson, ClientSeq: seq, ClientMsgNo: no, Payload: []byte(no)}))
				ack, err := c.ReadSendAck()
				require.NoError(t, err)
				require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
				return ack
			}
			connect := func(addr string, clean bool) *suite.MQTTClient {
				c, err := suite.ConnectMQTT(ctx, addr, "bob", "bob-fixture-token", "persistent-bob", clean, 120, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
				require.NoError(t, err, first.DumpDiagnostics())
				t.Cleanup(func() { _ = c.Abort() })
				return c
			}
			receive := func(c *suite.MQTTClient) *paho.Publish {
				p, err := c.Receive(ctx)
				require.NoError(t, err, first.DumpDiagnostics())
				return p
			}
			identity := func(p *paho.Publish, ack *frame.SendackPacket, no string) {
				require.Equal(t, []byte(no), p.Payload)
				require.NotNil(t, p.Properties)
				require.Equal(t, strconv.FormatInt(ack.MessageID, 10), p.Properties.User.Get("wk.message_id"))
				require.Equal(t, strconv.FormatUint(ack.MessageSeq, 10), p.Properties.User.Get("wk.message_seq"))
				require.Equal(t, no, p.Properties.User.Get("wk.client_msg_no"))
			}
			bob := connect(addrs[count-1], true)
			require.False(t, bob.Connack.SessionPresent)
			sub, err := bob.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
			require.NoError(t, err)
			require.Equal(t, []byte{1}, sub.Reasons)
			unacked := send(alice, 1, "unacked")
			original := receive(bob)
			identity(original, unacked, "unacked")
			require.False(t, original.Duplicate())
			require.NotZero(t, original.PacketID)
			backlog := send(alice, 2, "backlog")
			quiet, done := context.WithTimeout(ctx, 200*time.Millisecond)
			extra, e := bob.Receive(quiet)
			done()
			require.ErrorIs(t, e, context.DeadlineExceeded, "Receive Maximum violated: %v", extra)
			require.NoError(t, bob.Abort())
			// Carol has never sent to Bob. This source must be protected while the
			// persistent inbox has no online owner; no subscription is reinstalled.
			future := send(carol, 1, "future-offline")
			resumed := connect(addrs[0], false)
			require.True(t, resumed.Connack.SessionPresent)
			replay := receive(resumed)
			identity(replay, unacked, "unacked")
			require.True(t, replay.Duplicate())
			require.Equal(t, original.PacketID, replay.PacketID)
			require.NoError(t, resumed.Client.Ack(replay))
			pending := map[string]*frame.SendackPacket{"backlog": backlog, "future-offline": future}
			for range 2 {
				p := receive(resumed)
				no := p.Properties.User.Get("wk.client_msg_no")
				ack, ok := pending[no]
				require.True(t, ok, "unexpected backlog identity %q", no)
				identity(p, ack, no)
				require.False(t, p.Duplicate())
				delete(pending, no)
				require.NoError(t, resumed.Client.Ack(p))
			}
			takeoverAck := send(alice, 3, "takeover-unacked")
			beforeTakeover := receive(resumed)
			identity(beforeTakeover, takeoverAck, "takeover-unacked")
			successor := connect(addrs[count-1], false)
			require.True(t, successor.Connack.SessionPresent)
			select {
			case <-resumed.Client.Done():
			case <-ctx.Done():
				t.Fatal("previous live owner did not close")
			}
			taken := receive(successor)
			identity(taken, takeoverAck, "takeover-unacked")
			require.True(t, taken.Duplicate())
			require.Equal(t, beforeTakeover.PacketID, taken.PacketID)
			require.NoError(t, successor.Client.Ack(taken))
			denied, e := suite.ConnectMQTT(ctx, addrs[0], "carol", "carol-fixture-token", "persistent-bob", false, 120)
			if denied != nil {
				_ = denied.Close()
			}
			require.Error(t, e, "another UID took the bound ClientID")
			finalAck := send(alice, 4, "after-ack")
			last := receive(successor)
			identity(last, finalAck, "after-ack")
			require.False(t, last.Duplicate())
			require.NoError(t, successor.Client.Ack(last))
			require.NoError(t, successor.Close())
			reportDir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
			if reportDir == "" {
				reportDir = first.Spec.RootDir
			}
			require.NoError(t, os.MkdirAll(reportDir, 0755))
			report, err := json.MarshalIndent(map[string]any{"scenario": "mqtt-persistent-session", "nodes": count, "hash_slots": 256, "passed": true, "session_present": true, "resubscriptions": 0, "packet_id_preserved": true, "dup_on_resume": true, "future_contact_offline_delivered": true, "live_takeover_closed_old_owner": true, "client_id_uid_binding_enforced": true, "receive_maximum": 1, "message_ids": []string{strconv.FormatInt(unacked.MessageID, 10), strconv.FormatInt(backlog.MessageID, 10), strconv.FormatInt(future.MessageID, 10), strconv.FormatInt(takeoverAck.MessageID, 10), strconv.FormatInt(finalAck.MessageID, 10)}}, "", "  ")
			require.NoError(t, err)
			path := filepath.Join(reportDir, fmt.Sprintf("mqtt-session-%d.json", count))
			require.NoError(t, os.WriteFile(path, append(report, '\n'), 0600))
			t.Logf("result artifact: %s", path)
		})
	}
}

func TestPersistentSessionSurvivesOwnerProcessRestart(t *testing.T) {
	s := suite.New(t)
	mqttAddr := suite.ReserveLoopbackPorts(t).GatewayAddr
	n := s.StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": mqttAddr}))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+n.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-fixture-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	connect := func() *suite.MQTTClient {
		c, err := suite.ConnectMQTT(ctx, mqttAddr, "bob", "bob-fixture-token", "restart-bob", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, n.DumpDiagnostics())
		t.Cleanup(func() { _ = c.Abort() })
		return c
	}
	before := connect()
	require.False(t, before.Connack.SessionPresent)
	sub, err := before.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
	require.NoError(t, err)
	require.Equal(t, []byte{1}, sub.Reasons)
	alice, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = alice.Close() })
	_, err = alice.ConnectAuthenticatedContext(ctx, n.GatewayAddr(), "alice", "restart-alice", "alice-fixture-token", frame.WEB)
	require.NoError(t, err)
	require.NoError(t, alice.SendFrame(&frame.SendPacket{ChannelID: "bob", ChannelType: frame.ChannelTypePerson, ClientSeq: 1, ClientMsgNo: "restart-unacked", Payload: []byte("restart body")}))
	ack, err := alice.ReadSendAck()
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	original, err := before.Receive(ctx)
	require.NoError(t, err)
	require.False(t, original.Duplicate())
	require.NoError(t, n.Restart(n.Process.BinaryPath), n.DumpDiagnostics())
	require.NoError(t, n.Process.WaitWKProtoReady(ctx, n.GatewayAddr()), n.DumpDiagnostics())
	after := connect()
	require.True(t, after.Connack.SessionPresent)
	replay, err := after.Receive(ctx)
	require.NoError(t, err, n.DumpDiagnostics())
	require.True(t, replay.Duplicate())
	require.Equal(t, original.PacketID, replay.PacketID)
	require.Equal(t, original.Payload, replay.Payload)
	require.Equal(t, strconv.FormatInt(ack.MessageID, 10), replay.Properties.User.Get("wk.message_id"))
	require.NoError(t, after.Client.Ack(replay))
	reportDir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if reportDir == "" {
		reportDir = n.Spec.RootDir
	}
	require.NoError(t, os.MkdirAll(reportDir, 0755))
	report, err := json.MarshalIndent(map[string]any{"scenario": "mqtt-session-owner-restart", "nodes": 1, "hash_slots": 256, "passed": true, "session_present": true, "resubscriptions": 0, "packet_id_preserved": true, "dup_on_resume": true, "message_id": strconv.FormatInt(ack.MessageID, 10)}, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(reportDir, "mqtt-session-owner-restart.json")
	require.NoError(t, os.WriteFile(path, append(report, '\n'), 0600))
	t.Logf("result artifact: %s", path)
}

func TestMQTTProcessShutdownClosesActiveOwner(t *testing.T) {
	s := suite.New(t)
	addr := suite.ReserveLoopbackPorts(t).GatewayAddr
	n := s.StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addr}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := suite.PostJSON(ctx, "http://"+n.APIAddr()+"/user/token", map[string]any{"uid": "bob", "token": "shutdown-fixture-token", "device_flag": 1, "device_level": 1}, nil)
	require.NoError(t, err)
	c, err := suite.ConnectMQTT(ctx, addr, "bob", "shutdown-fixture-token", "shutdown-bob", false, 600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Abort() })
	p := n.Process
	require.NoError(t, p.Stop(), p.DumpDiagnostics())
	exitErr, exited := p.ExitResult()
	require.True(t, exited)
	require.NoError(t, exitErr, p.DumpDiagnostics())
	select {
	case <-c.Client.Done():
	case <-ctx.Done():
		t.Fatal("MQTT client remained open after process shutdown")
	}
	log, err := os.ReadFile(filepath.Join(n.Spec.LogDir, "app.log"))
	require.NoError(t, err)
	require.NotContains(t, string(log), "internal.app.lifecycle_stop_failed", p.DumpDiagnostics())
	reportDir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if reportDir == "" {
		reportDir = n.Spec.RootDir
	}
	require.NoError(t, os.MkdirAll(reportDir, 0755))
	path := filepath.Join(reportDir, "mqtt-session-shutdown.json")
	require.NoError(t, os.WriteFile(path, []byte("{\"scenario\":\"mqtt-active-owner-shutdown\",\"nodes\":1,\"hash_slots\":256,\"passed\":true,\"exit_success\":true,\"client_closed\":true,\"lifecycle_stop_failures\":0}\n"), 0600))
	t.Logf("result artifact: %s", path)
}
