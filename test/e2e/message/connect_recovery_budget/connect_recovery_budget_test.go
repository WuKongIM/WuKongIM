//go:build e2e

package connect_recovery_budget

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// budgetReport contains bounded public outcomes, never protocol identities or bodies.
type budgetReport struct {
	Schema               string           `json:"schema"`
	Topology             string           `json:"topology"`
	Scope                string           `json:"scope"`
	Phase                string           `json:"phase"`
	Passed               bool             `json:"passed"`
	BinarySHA256         string           `json:"binary_sha256,omitempty"`
	Nodes                int              `json:"nodes"`
	HashSlots            int              `json:"hash_slots"`
	PhysicalSlots        int              `json:"physical_slots"`
	Ingress              uint64           `json:"ingress_node"`
	Authority            uint64           `json:"authority_node"`
	HashSlot             uint16           `json:"target_hash_slot"`
	SlotID               uint32           `json:"target_physical_slot"`
	ConnectAttempts      int              `json:"connect_attempts"`
	ConnectSucceeded     bool             `json:"connect_succeeded"`
	ConnackReasonKnown   bool             `json:"connack_reason_known"`
	ConnackReason        uint8            `json:"connack_reason"`
	ConnectNanos         int64            `json:"connect_nanos"`
	ActivationLimitNanos int64            `json:"activation_limit_nanos"`
	ClientLimitNanos     int64            `json:"client_operation_limit_nanos"`
	VerifiedMessages     int              `json:"verified_messages"`
	RecvACKs             int              `json:"recv_acks"`
	ClientsClosed        bool             `json:"clients_closed"`
	ProcessesExited      bool             `json:"processes_exited"`
	ProcessGroupsJoined  bool             `json:"process_groups_joined"`
	Relay                relayObservation `json:"relay"`
}

// TestConnectRecoveryBudget preserves the first CONNECT result across a transient owner read.
func TestConnectRecoveryBudget(t *testing.T) {
	for _, tc := range []struct {
		name  string
		nodes int
	}{
		{name: "single_node_cluster", nodes: 1},
		{name: "three_node_cluster", nodes: 3},
	} {
		t.Run(tc.name, func(t *testing.T) { runConnectBudget(t, tc.name, tc.nodes) })
	}
}

func runConnectBudget(t *testing.T, topology string, nodes int) {
	t.Helper()
	report := budgetReport{Schema: "connect-recovery-budget/v1", Topology: topology, Scope: "policy_mechanism_not_performance_acceptance", Phase: "startup", Nodes: nodes, HashSlots: 256, PhysicalSlots: 12, Ingress: 1, ActivationLimitNanos: int64(3 * time.Second), ClientLimitNanos: int64(5 * time.Second)}
	var cluster *suite.StartedCluster
	var mesh *suite.ClusterTCPPartition
	var clients []*suite.WKProtoClient
	// Keep process owners after StartedNode.Stop detaches its public pointer.
	var processes []*suite.NodeProcess
	fault := newOwnerReadRelay()
	// Register first so this bounded receipt observes every later cleanup.
	t.Cleanup(func() {
		report.ClientsClosed = true
		for _, client := range clients {
			if client.Close() != nil {
				report.ClientsClosed = false
				t.Error("client cleanup failed")
			}
		}
		if mesh != nil {
			mesh.Close()
		}
		report.ProcessesExited = len(processes) == nodes
		report.ProcessGroupsJoined = len(processes) == nodes
		for _, process := range processes {
			if process.Stop() != nil {
				report.ProcessGroupsJoined = false
				t.Error("process group cleanup failed")
			}
			_, exited := process.ExitResult()
			report.ProcessesExited = report.ProcessesExited && exited
		}
		report.Relay = fault.snapshot()
		report.Passed = !t.Failed() && report.ClientsClosed && report.ProcessesExited && report.ProcessGroupsJoined && report.Relay.ActiveCopies == 0
		if !report.ProcessesExited || !report.ProcessGroupsJoined || report.Relay.ActiveCopies != 0 {
			t.Error("owned cleanup incomplete")
		}
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.LessOrEqual(t, len(data), 16<<10, "bounded result exceeded")
		dir := os.Getenv("WK_E2E_CONNECT_RECOVERY_BUDGET_REPORT_DIR")
		if dir == "" {
			dir = filepath.Join(os.TempDir(), "wukongim-connect-recovery-budget")
		}
		require.NoError(t, os.MkdirAll(dir, 0755))
		path := filepath.Join(dir, topology+".json")
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	mesh = suite.NewClusterTCPPartition(t)
	require.NoError(t, mesh.SetUpstreamCopy(fault.copy))
	opts := []suite.Option{suite.WithManagerHTTP(), suite.WithClusterTCPPartition(mesh)}
	for i := 1; i <= nodes; i++ {
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i), map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12",
			"WK_CLUSTER_SLOT_REPLICA_N": fmt.Sprint(nodes), "WK_CLUSTER_CHANNEL_REPLICA_N": fmt.Sprint(nodes),
			"WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_DELIVERY_ENABLE": "true",
		}))
	}
	cluster = suite.New(t).StartStaticCluster(nodes, opts...)
	for _, node := range cluster.Nodes {
		processes = append(processes, node.Process)
	}
	// Mark planned teardown before process cleanup closes cluster links.
	t.Cleanup(fault.beginStopping)
	binaryFile, err := os.Open(cluster.MustNode(1).Process.BinaryPath)
	require.NoError(t, err, "open selected binary")
	hasher := sha256.New()
	n, hashErr := io.Copy(hasher, io.LimitReader(binaryFile, (128<<20)+1))
	closeErr := binaryFile.Close()
	require.NoError(t, hashErr, "hash selected binary")
	require.NoError(t, closeErr, "close selected binary")
	require.LessOrEqual(t, n, int64(128<<20), "selected binary exceeded hash bound")
	report.BinarySHA256 = hex.EncodeToString(hasher.Sum(nil))
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	ready, done := context.WithTimeout(ctx, 35*time.Second)
	require.NoError(t, cluster.WaitClusterReady(ready), "public cluster readiness failed")
	convergence, err := cluster.WaitSlotLeadersStable(ready, time.Second)
	done()
	require.NoError(t, err, "actual Slot authority did not stabilize")
	slots := cluster.ManagerClient(t, 1).MustSlots(t)
	target, hashSlot, slot, authority := freshRemoteUID(t, slots, convergence.Leaders, nodes)
	report.HashSlot, report.SlotID, report.Authority = hashSlot, slot, authority
	report.Phase = "receiver"
	receiverUID := "e2e-budget-receiver-" + topology
	receiverNode := cluster.MustNode(uint64(nodes))
	receiverToken := "budget-receiver-fixture-token"
	provisionToken(t, ctx, receiverNode, receiverUID, receiverToken)
	receiver, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	clients = append(clients, receiver)
	t.Cleanup(func() {
		if receiver.Close() != nil {
			t.Error("receiver close failed")
		}
	})
	call, stop := context.WithTimeout(ctx, 5*time.Second)
	_, err = receiver.ConnectWithTokenContext(call, receiverNode.GatewayAddr(), receiverUID, "budget-receiver-device", receiverToken)
	stop()
	require.True(t, err == nil, "authenticated receiver CONNECT failed")
	targetToken := "budget-target-fixture-token"
	provisionToken(t, ctx, cluster.MustNode(1), target, targetToken)
	delay := time.Duration(0)
	if nodes == 3 {
		delay = 350 * time.Millisecond
	}
	fault.arm(target, delay)
	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	clients = append(clients, sender)
	t.Cleanup(func() {
		if sender.Close() != nil {
			t.Error("sender close failed")
		}
	})
	report.Phase = "target_connect"
	report.ConnectAttempts = 1
	call, stop = context.WithTimeout(ctx, 5*time.Second)
	began := time.Now()
	connack, connectErr := sender.ConnectWithTokenContext(call, cluster.MustNode(1).GatewayAddr(), target, "budget-target-device", targetToken)
	report.ConnectNanos = time.Since(began).Nanoseconds()
	stop()
	if connectErr != nil {
		var rejected interface {
			ConnackReason() (frame.ReasonCode, bool)
		}
		if errors.As(connectErr, &rejected) {
			reason, known := rejected.ConnackReason()
			report.ConnackReasonKnown, report.ConnackReason = known, uint8(reason)
		}
	} else if connack != nil {
		report.ConnackReasonKnown = true
		report.ConnackReason = uint8(connack.ReasonCode)
	}
	if nodes == 3 {
		wait, finish := context.WithTimeout(ctx, time.Second)
		delayErr := fault.waitDelay(wait)
		finish()
		require.NoError(t, delayErr, "owner-read delay was not completed")
	}
	// A business RED is meaningful only after the exact network fault completed.
	if nodes == 3 {
		proof := fault.snapshot()
		require.Equal(t, uint64(1), proof.DelaysStarted, "fault must hit exactly one owner read")
		require.Contains(t, []string{"legacy_uid", "full_hash_slot"}, proof.DelayProtocol)
		if proof.DelayProtocol == "full_hash_slot" {
			require.Contains(t, []uint8{4, 5}, proof.DelayOperation)
		} else {
			require.Equal(t, uint8(11), proof.DelayOperation)
		}
		require.Equal(t, uint64(1), proof.DelayedFramesForwarded, "delayed request was not forwarded")
		require.GreaterOrEqual(t, proof.HoldNanos, int64(delay))
		require.Equal(t, "", proof.FailureKind, "fault fixture failed")
	}
	if connectErr != nil {
		t.Fatalf("first CONNECT failed: connack_known=%t connack_reason=%d", report.ConnackReasonKnown, report.ConnackReason)
	}
	require.NotNil(t, connack)
	require.Equal(t, frame.ReasonSuccess, connack.ReasonCode)
	report.ConnectSucceeded = true
	require.Less(t, report.ConnectNanos, int64(3*time.Second), "first CONNECT exceeded original activation ceiling")
	observed := fault.snapshot()
	if nodes == 3 {
		require.Equal(t, uint64(1), observed.DelaysStarted, "fault must hit exactly one owner read")
		require.Equal(t, uint64(1), observed.DelayedFramesForwarded)
		require.GreaterOrEqual(t, observed.HoldNanos, int64(delay))
		require.Equal(t, authority, observed.FromNode, "delay must originate at observed authority")
		require.NotEqual(t, observed.FromNode, observed.ToNode)
		require.Equal(t, uint64(1), observed.RegisterRequests, "remote Register must not be retried")
	} else {
		require.Zero(t, observed.DelaysStarted, "single-node control cannot fault remote owner reads")
	}
	report.Phase = "messages"
	verifyMessage(t, sender, receiver, target, receiverUID, 1, "budget-forward", []byte("budget-forward-content"))
	report.VerifiedMessages++
	report.RecvACKs++
	verifyMessage(t, receiver, sender, receiverUID, target, 2, "budget-return", []byte("budget-return-content"))
	report.VerifiedMessages++
	report.RecvACKs++
	require.Equal(t, "", fault.snapshot().FailureKind, "relay protocol observation failed")
	report.Phase = "complete"
}

// freshRemoteUID uses public placement evidence; it never reads server/storage internals.
func freshRemoteUID(t *testing.T, slots []suite.SlotDTO, leaders []suite.ActualSlotLeader, nodes int) (string, uint16, uint32, uint64) {
	t.Helper()
	coverage := make(map[uint16]suite.SlotDTO, 256)
	actual := make(map[uint32]uint64, len(leaders))
	for _, leader := range leaders {
		require.NotZero(t, leader.LeaderID)
		_, duplicate := actual[leader.SlotID]
		require.False(t, duplicate, "duplicate stable Slot leader")
		actual[leader.SlotID] = leader.LeaderID
	}
	require.Len(t, actual, len(slots))
	for _, slot := range slots {
		require.NotNil(t, slot.HashSlots)
		leader, known := actual[slot.SlotID]
		require.True(t, known)
		require.NotZero(t, leader)
		for _, hash := range slot.HashSlots.Items {
			require.Less(t, hash, uint16(256))
			_, duplicate := coverage[hash]
			require.False(t, duplicate, "duplicate HashSlot mapping")
			coverage[hash] = slot
		}
	}
	require.Len(t, coverage, 256)
	// Full recovery completes a whole Hash Slot. A target sharing a readiness
	// or independent receiver slot would legitimately have no cold owner Begin.
	excluded := make(map[uint16]bool)
	for _, uid := range []string{"e2e-budget-receiver-single_node_cluster", "e2e-budget-receiver-three_node_cluster", "e2e-ready-1", "e2e-ready-2", "e2e-ready-3"} {
		excluded[routing.HashSlotForKey(uid, 256)] = true
	}
	for n := 0; n < 4096; n++ {
		uid := fmt.Sprintf("e2e-connect-budget-fresh-%d", n)
		hash := routing.HashSlotForKey(uid, 256)
		if excluded[hash] {
			continue
		}
		slot, ok := coverage[hash]
		require.True(t, ok)
		if nodes == 1 || actual[slot.SlotID] != 1 {
			return uid, hash, slot.SlotID, actual[slot.SlotID]
		}
	}
	t.Fatal("could not select fresh remote-authority UID from public placement")
	return "", 0, 0, 0
}

func provisionToken(t *testing.T, ctx context.Context, node *suite.StartedNode, uid, token string) {
	t.Helper()
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := suite.PostJSON(call, "http://"+node.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": token, "device_flag": int(frame.APP), "device_level": 0}, nil)
	require.True(t, err == nil, "public Token provisioning failed")
}

func verifyMessage(t *testing.T, sender, receiver *suite.WKProtoClient, from, to string, seq uint64, no string, payload []byte) {
	t.Helper()
	err := sender.SendFrame(&frame.SendPacket{ChannelID: to, ChannelType: frame.ChannelTypePerson, ClientSeq: seq, ClientMsgNo: no, Payload: payload})
	require.True(t, err == nil, "WKProto SEND failed")
	ack, err := sender.ReadSendAck()
	require.True(t, err == nil, "WKProto SENDACK failed")
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	require.True(t, ack.ClientSeq == seq && ack.ClientMsgNo == no, "SENDACK correlation mismatch")
	require.NotZero(t, ack.MessageID)
	require.NotZero(t, ack.MessageSeq)
	recv, err := receiver.ReadRecv()
	require.True(t, err == nil, "WKProto RECV failed")
	require.True(t, recv.FromUID == from && recv.ChannelID == from, "RECV sender/channel mismatch")
	require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
	require.True(t, bytes.Equal(recv.Payload, payload), "RECV payload mismatch")
	require.True(t, recv.MessageID == ack.MessageID && recv.MessageSeq == ack.MessageSeq, "RECV message identity mismatch")
	require.True(t, receiver.RecvAck(recv.MessageID, recv.MessageSeq) == nil, "WKProto RECVACK failed")
}
