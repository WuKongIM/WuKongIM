//go:build e2e && (darwin || linux)

package owner_outage

import (
	"context"
	"encoding/json"
	"fmt"
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

// Refused takeover is checked independently of general cluster availability.
// Only restored communication and exact-owner isolation permit resumed delivery.
func TestUnavailableOwnerRefusesTakeoverAndPreservesPersistentSession(t *testing.T) {
	for _, fault := range []string{"suspend", "crash"} {
		t.Run(fault, func(t *testing.T) { testOwnerOutage(t, fault) })
	}
}

func testOwnerOutage(t *testing.T, fault string) {
	reportDir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
	if reportDir == "" {
		reportDir = t.TempDir()
	}
	phase := "startup"
	report := map[string]any{"scenario": "mqtt-three-node-owner-outage", "fault": fault, "nodes": 3, "hash_slots": 256, "slot_replicas": 3, "channel_replicas": 2, "resubscriptions": 0, "target_connect_retries": 0, "refused_connects": 0, "successful_cross_node_takeovers": 0}
	// Register first so the artifact reflects all later client/process cleanup.
	t.Cleanup(func() {
		report["passed"], report["last_phase"] = !t.Failed(), phase
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(reportDir, 0755))
		path := filepath.Join(reportDir, "mqtt-owner-outage-"+fault+".json")
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	opts := []suite.Option{suite.WithManagerHTTP(), suite.WithNodeLogRootDir(filepath.Join(reportDir, fault+"-logs"))}
	addrs := make([]string, 3)
	for i := range 3 {
		addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_SLOT_REPLICA_N": "3", "WK_CLUSTER_CHANNEL_REPLICA_N": "2", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i]}))
	}
	cluster := suite.New(t).StartThreeNodeCluster(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ready := func() suite.SlotLeaderConvergence {
		call, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		require.NoError(t, cluster.WaitClusterReady(call), cluster.DumpDiagnostics())
		view, err := cluster.WaitSlotLeadersStable(call, time.Second)
		require.NoError(t, err, cluster.DumpDiagnostics())
		return view
	}
	initial := ready()
	first, oldNode := cluster.MustNode(1), cluster.MustNode(3)
	for _, uid := range []string{"alice", "bob"} {
		_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-fixture-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
	}
	connect := func(node int, clientID string, clean bool, expiry uint32) *suite.MQTTClient {
		client, err := suite.ConnectMQTT(ctx, addrs[node-1], "bob", "bob-fixture-token", clientID, clean, expiry, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, cluster.DumpDiagnostics())
		t.Cleanup(func() { _ = client.Abort() })
		return client
	}
	send := func(no string) *frame.SendackPacket {
		alice, err := suite.NewWKProtoClient()
		require.NoError(t, err)
		defer alice.Close()
		_, err = alice.ConnectAuthenticatedContext(ctx, first.GatewayAddr(), "alice", "outage-alice-"+no, "alice-fixture-token", frame.WEB)
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.NoError(t, alice.SendFrame(&frame.SendPacket{ChannelID: "bob", ChannelType: frame.ChannelTypePerson, ClientSeq: 1, ClientMsgNo: no, Payload: []byte(no)}))
		ack, err := alice.ReadSendAck()
		require.NoError(t, err)
		require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
		return ack
	}
	receive := func(client *suite.MQTTClient) *paho.Publish {
		call, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		packet, err := client.Receive(call)
		require.NoError(t, err, cluster.DumpDiagnostics())
		return packet
	}
	identity := func(packet *paho.Publish, ack *frame.SendackPacket, no string) {
		require.Equal(t, byte(1), packet.QoS)
		require.Equal(t, []byte(no), packet.Payload)
		require.NotNil(t, packet.Properties)
		require.Equal(t, strconv.FormatInt(ack.MessageID, 10), packet.Properties.User.Get("wk.message_id"))
		require.Equal(t, strconv.FormatUint(ack.MessageSeq, 10), packet.Properties.User.Get("wk.message_seq"))
		require.Equal(t, no, packet.Properties.User.Get("wk.client_msg_no"))
	}
	phase = "initial-exchange"
	before := connect(3, "outage-bob", true, 600)
	require.False(t, before.Connack.SessionPresent)
	sub, err := before.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "wk/v1/users/Ym9i/messages", QoS: 1}}})
	require.NoError(t, err)
	require.Equal(t, []byte{1}, sub.Reasons)
	ack := send("outage-unacked")
	original := receive(before)
	identity(original, ack, "outage-unacked")
	require.False(t, original.Duplicate())
	require.NotZero(t, original.PacketID)
	awaitActiveOwners(t, ctx, cluster, map[uint64]float64{1: 0, 2: 0, 3: 1})

	phase = "owner-loss"
	process := oldNode.Process
	pid := process.Cmd.Process.Pid
	suspended := false
	t.Cleanup(func() {
		if suspended {
			require.NoError(t, syscall.Kill(-pid, syscall.SIGCONT))
		}
	})
	lostAt := time.Now()
	if fault == "suspend" {
		require.NoError(t, syscall.Kill(-pid, syscall.SIGSTOP))
		suspended = true
		require.NoError(t, syscall.Kill(-pid, 0))
		report["old_process_alive_during_loss"] = true
	} else {
		require.NoError(t, syscall.Kill(-pid, syscall.SIGKILL))
		select {
		case <-process.Done():
		case <-ctx.Done():
			t.Fatal("killed owner did not exit")
		}
		_ = process.Stop() // Join the entire group before any later restart.
		report["old_process_exited"] = true
	}
	refuse := func(node int) {
		client, err := suite.ConnectMQTT(ctx, addrs[node-1], "bob", "bob-fixture-token", "outage-bob", false, 600)
		if client != nil {
			_ = client.Abort()
		}
		require.Error(t, err, "unavailable Owner was admitted through node %d", node)
		report["refused_connects"] = report["refused_connects"].(int) + 1
	}
	phase = "refusal-before-expiry"
	refuse(1)
	refuse(2)
	awaitActiveOwners(t, ctx, cluster, map[uint64]float64{1: 0, 2: 0})
	report["survivor_active_owners_after_early_refusals"] = 0
	report["early_refusal_complete_ms"] = time.Since(lostAt).Milliseconds()
	require.Less(t, time.Since(lostAt), 30*time.Second, "early refusal must precede the captured grant bound")
	t.Log("early target CONNECTs refused; retaining the outage beyond the captured grant")
	// Actual elapsed time is essential here; mocked clocks would not test expiry.
	wait := time.NewTimer(time.Until(lostAt.Add(35 * time.Second)))
	select {
	case <-wait.C:
	case <-ctx.Done():
		wait.Stop()
		t.Fatal("outage window did not complete")
	}
	report["outage_before_late_refusal_ms"] = time.Since(lostAt).Milliseconds()
	phase = "survivor-controls"
	awaitSurvivorQuorum(t, ctx, cluster, initial)
	report["surviving_slot_quorum_verified"] = true
	for _, node := range []int{1, 2} {
		control := connect(node, fmt.Sprintf("outage-control-%d", node), true, 0)
		require.False(t, control.Connack.SessionPresent)
		require.NoError(t, control.Close())
	}
	report["independent_connect_controls"] = 2
	phase = "refusal-after-expiry"
	refuse(1)
	refuse(2)
	awaitActiveOwners(t, ctx, cluster, map[uint64]float64{1: 0, 2: 0})
	report["survivor_active_owners_after_late_refusals"] = 0
	if suspended {
		require.NoError(t, syscall.Kill(-pid, 0))
	}
	t.Log("both surviving ingresses refused target takeover after the lease bound")

	phase = "owner-recovery"
	if suspended {
		require.NoError(t, syscall.Kill(-pid, syscall.SIGCONT))
		suspended = false
		report["process_restarts"] = 0
	} else {
		require.NoError(t, cluster.StartStoppedNode(3), cluster.DumpDiagnostics())
		report["process_restarts"] = 1
	}
	ready()
	phase = "persistent-cross-node-handoffs"
	previous := before
	for _, node := range []int{1, 2} {
		after := connect(node, "outage-bob", false, 600)
		require.True(t, after.Connack.SessionPresent)
		select {
		case <-previous.Client.Done():
		case <-ctx.Done():
			t.Fatal("previous Owner client remained open after takeover")
		}
		replay := receive(after)
		identity(replay, ack, "outage-unacked")
		require.True(t, replay.Duplicate())
		require.Equal(t, original.PacketID, replay.PacketID)
		report["successful_cross_node_takeovers"] = report["successful_cross_node_takeovers"].(int) + 1
		previous = after
		if node == 2 {
			require.NoError(t, after.Client.Ack(replay))
		}
	}
	report["session_present"], report["packet_id_preserved"], report["dup_on_resume"], report["original_identity_preserved"], report["previous_clients_closed"] = true, true, true, true, true
	phase = "fresh-delivery"
	freshAck := send("outage-after")
	fresh := receive(previous)
	identity(fresh, freshAck, "outage-after")
	require.False(t, fresh.Duplicate())
	require.NoError(t, previous.Client.Ack(fresh))
	quiet, done := context.WithTimeout(ctx, 2*time.Second)
	extra, err := previous.Receive(quiet)
	done()
	require.ErrorIs(t, err, context.DeadlineExceeded, "unexpected extra/closed delivery: %v", extra)
	report["fresh_deliveries"], report["extra_deliveries"] = 1, 0
	awaitActiveOwners(t, ctx, cluster, map[uint64]float64{1: 0, 2: 1, 3: 0})
	report["final_active_owners"] = 1
	phase = "complete"
}

// Fixed aggregate observations support diagnosis; MQTT receipts prove admission.
func awaitActiveOwners(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, expected map[uint64]float64) {
	t.Helper()
	call, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for call.Err() == nil {
		valid := true
		for node, want := range expected {
			read, done := context.WithTimeout(call, time.Second)
			value, err := suite.FetchMetricValue(read, cluster.MustNode(node).APIAddr(), "wukongim_mqtt_owner_work", map[string]string{"state": "active"})
			done()
			if err != nil || value != want {
				valid = false
				break
			}
		}
		if valid {
			return
		}
		select {
		case <-call.Done():
		case <-ticker.C:
		}
	}
	t.Fatal("fixed aggregate active Owner observations did not converge")
}

// Observe both surviving voters, rather than treating target refusal as quorum proof.
func awaitSurvivorQuorum(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, initial suite.SlotLeaderConvergence) {
	t.Helper()
	call, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for call.Err() == nil {
		leaders := make(map[uint32]uint64, len(initial.Leaders))
		valid := true
		for _, node := range []uint64{1, 2} {
			var view struct {
				Items []suite.SlotDTO `json:"items"`
			}
			read, done := context.WithTimeout(call, 2*time.Second)
			_, err := suite.GetJSON(read, fmt.Sprintf("http://%s/manager/slots?node_id=%d", cluster.MustNode(node).ManagerAddr(), node), &view)
			done()
			if err != nil || len(view.Items) != len(initial.Leaders) {
				valid = false
				break
			}
			for _, slot := range view.Items {
				if slot.NodeLog == nil || slot.NodeLog.NodeID != node || slot.NodeLog.LeaderID == 0 || slot.NodeLog.LeaderID > 2 || !slot.Runtime.HasQuorum || slot.NodeLog.AppliedIndex > slot.NodeLog.CommitIndex {
					valid = false
					break
				}
				if prior, found := leaders[slot.SlotID]; found && prior != slot.NodeLog.LeaderID {
					valid = false
					break
				}
				leaders[slot.SlotID] = slot.NodeLog.LeaderID
			}
		}
		if valid && len(leaders) == len(initial.Leaders) {
			return
		}
		select {
		case <-call.Done():
		case <-ticker.C:
		}
	}
	t.Fatal("surviving voters did not expose agreed Slot quorum before target-refusal checks")
}
