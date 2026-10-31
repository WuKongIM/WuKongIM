//go:build e2e

package presence_generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

type messageFact struct {
	ID       int64  `json:"message_id"`
	IDString string `json:"message_idstr"`
	Seq      uint64 `json:"message_seq"`
	Key      string `json:"client_msg_no"`
	From     string `json:"from_uid"`
	Payload  []byte `json:"payload"`
}

// validateIdentity verifies both public representations without float64 or
// string-to-number coercion, before comparing a history row with native ACKs.
func (m messageFact) validateIdentity() error {
	if m.ID <= 0 || m.IDString != strconv.FormatInt(m.ID, 10) {
		return fmt.Errorf("history message_id and message_idstr disagree")
	}
	return nil
}

type historyPage struct {
	More     int           `json:"more"`
	Messages []messageFact `json:"messages"`
}
type historyReceipt struct {
	Phase              string        `json:"phase"`
	Node               uint64        `json:"node_id"`
	Pages              int           `json:"pages"`
	UnavailableRetries int           `json:"unavailable_retries"`
	Messages           []messageFact `json:"messages"`
	Complete           bool          `json:"complete"`
}
type connectReceipt struct {
	Phase     string `json:"phase"`
	Node      uint64 `json:"node_id"`
	UID       string `json:"uid"`
	Attempts  int    `json:"attempts"`
	Succeeded bool   `json:"succeeded"`
	Nanos     int64  `json:"nanos"`
}
type receipt struct {
	Schema                string           `json:"schema"`
	Scope                 string           `json:"scope"`
	Phase                 string           `json:"phase"`
	Passed                bool             `json:"passed"`
	BinarySHA256          string           `json:"binary_sha256"`
	HashSlots             int              `json:"hash_slots"`
	PhysicalSlots         int              `json:"physical_slots"`
	Replicas              int              `json:"replicas"`
	ReceiverHash          uint16           `json:"receiver_hash_slot"`
	ReceiverSlot          uint32           `json:"receiver_physical_slot"`
	AuthorityBefore       uint64           `json:"authority_before"`
	AuthorityAfter        uint64           `json:"authority_after"`
	TransferAccepted      bool             `json:"transfer_accepted"`
	ReceiverKeptConnected bool             `json:"receiver_kept_connected_across_transfer"`
	InvalidAuthRefused    bool             `json:"invalid_auth_refused"`
	OwnerRestarts         int              `json:"owner_restarts"`
	WholeClusterRestarts  int              `json:"whole_cluster_restarts"`
	Connects              []connectReceipt `json:"connects"`
	Acknowledged          []messageFact    `json:"acknowledged"`
	RecvACKs              int              `json:"recv_acks"`
	ExactRedeliveries     int              `json:"exact_redeliveries"`
	History               []historyReceipt `json:"history"`
	ClientsClosed         bool             `json:"clients_closed"`
	ProcessOwners         int              `json:"process_owners"`
	ProcessesExited       bool             `json:"processes_exited"`
	ProcessGroupsJoined   bool             `json:"process_groups_joined"`
}

func TestPresenceGenerationSurvivesAuthorityAndBootChanges(t *testing.T) {
	out := receipt{Schema: "presence-generation-e2e/v2", Scope: "process_level_functional_not_performance_or_private_generation_observation", Phase: "startup", HashSlots: 256, PhysicalSlots: 12, Replicas: 3}
	var clients []*suite.WKProtoClient
	var processes []*suite.NodeProcess
	// Register first: the artifact runs after suite teardown and retains every
	// historical process owner, including owners detached during a restart.
	t.Cleanup(func() {
		out.ClientsClosed = true
		for _, client := range clients {
			if err := client.Close(); err != nil {
				out.ClientsClosed = false
				t.Error("client cleanup failed")
			}
		}
		out.ProcessOwners = len(processes)
		out.ProcessesExited, out.ProcessGroupsJoined = len(processes) > 0, len(processes) > 0
		for _, process := range processes {
			if err := process.Stop(); err != nil {
				out.ProcessGroupsJoined = false
				t.Error("process group cleanup failed")
			}
			_, exited := process.ExitResult()
			out.ProcessesExited = out.ProcessesExited && exited
		}
		if !out.ProcessesExited || !out.ProcessGroupsJoined {
			t.Error("owned process cleanup incomplete")
		}
		out.Passed = !t.Failed() && out.Phase == "complete" && out.ClientsClosed && out.ProcessesExited && out.ProcessGroupsJoined
		body, err := json.MarshalIndent(out, "", "  ")
		require.NoError(t, err)
		require.LessOrEqual(t, len(body), 64<<10)
		path := os.Getenv("WK_E2E_PRESENCE_GENERATION_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-presence-generation.json")
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, append(body, '\n'), 0600))
		t.Logf("presence generation artifact: %s", path)
	})
	opts := []suite.Option{suite.WithManagerHTTP()}
	for node := uint64(1); node <= 3; node++ {
		opts = append(opts, suite.WithNodeConfigOverrides(node, map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12",
			"WK_CLUSTER_SLOT_REPLICA_N": "3", "WK_CLUSTER_CHANNEL_REPLICA_N": "3",
			"WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_DELIVERY_ENABLE": "true",
		}))
	}
	cluster := suite.New(t).StartStaticCluster(3, opts...)
	for _, node := range cluster.Nodes {
		processes = append(processes, node.Process)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("phase=%s\n%s", out.Phase, cluster.DumpDiagnostics())
		}
	})
	f, err := os.Open(cluster.MustNode(1).Process.BinaryPath)
	require.NoError(t, err)
	h := sha256.New()
	n, hashErr := io.Copy(h, io.LimitReader(f, (128<<20)+1))
	require.NoError(t, f.Close())
	require.NoError(t, hashErr)
	require.LessOrEqual(t, n, int64(128<<20))
	out.BinarySHA256 = hex.EncodeToString(h.Sum(nil))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	stable := waitReady(t, ctx, cluster)
	uid, slot, before := receiverPlacement(t, cluster.ManagerClient(t, 1).MustSlots(t), stable)
	out.ReceiverHash, out.ReceiverSlot, out.AuthorityBefore = routing.HashSlotForKey(uid, 256), slot, before
	const senderUID = "presence-generation-sender"
	invalid, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	clients = append(clients, invalid)
	call, end := context.WithTimeout(ctx, 5*time.Second)
	_, err = invalid.ConnectWithTokenContext(call, cluster.MustNode(1).GatewayAddr(), "presence-generation-unregistered", "invalid", "unregistered")
	end()
	require.ErrorContains(t, err, frame.ReasonAuthFail.String())
	require.NoError(t, invalid.Close())
	out.InvalidAuthRefused = true
	for _, user := range []string{senderUID, uid} {
		_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/user/token", map[string]any{"uid": user, "token": token(user), "device_flag": int(frame.APP), "device_level": 0}, nil)
		require.NoError(t, err)
	}
	connect := func(node uint64, user string) *suite.WKProtoClient {
		c, err := suite.NewWKProtoClient()
		require.NoError(t, err)
		clients = append(clients, c)
		out.Connects = append(out.Connects, connectReceipt{Phase: out.Phase, Node: node, UID: user, Attempts: 1})
		record := &out.Connects[len(out.Connects)-1]
		call, end := context.WithTimeout(ctx, 5*time.Second)
		defer end()
		started := time.Now()
		ack, err := c.ConnectWithTokenContext(call, cluster.MustNode(node).GatewayAddr(), user, "presence-generation-device", token(user))
		record.Nanos = time.Since(started).Nanoseconds()
		require.NoError(t, err, "first CONNECT failed; no retry")
		require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
		record.Succeeded = true
		return c
	}
	out.Phase = "initial_connect_and_messages"
	sender, receiver := connect(1, senderUID), connect(3, uid)
	exchange(t, &out, sender, receiver, senderUID, uid)

	out.Phase = "authority_transfer_same_receiver_session"
	preferred := uint64(1)
	if preferred == before {
		preferred = 2
	}
	var accepted struct {
		SlotID  uint32 `json:"slot_id"`
		Target  uint64 `json:"target_node"`
		Actual  uint64 `json:"actual_leader"`
		Created bool   `json:"created"`
	}
	call, end = context.WithTimeout(ctx, 10*time.Second)
	controller, err := cluster.ManagerClient(t, 1).ControllerRaftStatus(call, 1)
	require.NoError(t, err)
	require.Contains(t, []uint64{1, 2, 3}, controller.LeaderID)
	_, err = suite.PostJSON(call, fmt.Sprintf("http://%s/manager/slots/%d/leader-transfer", cluster.MustNode(controller.LeaderID).ManagerAddr(), slot), map[string]any{"target_node": preferred}, &accepted)
	end()
	require.NoError(t, err, "single public transfer request")
	require.Equal(t, slot, accepted.SlotID)
	require.Equal(t, preferred, accepted.Target)
	require.Equal(t, before, accepted.Actual)
	require.True(t, accepted.Created)
	out.TransferAccepted = true
	out.AuthorityAfter = waitMoved(t, ctx, cluster, slot, before)
	// The first exchange direction targets the still-connected receiver. No
	// receiver CONNECT, PING, touch API or receiver-originated SEND primes it.
	exchange(t, &out, sender, receiver, senderUID, uid)
	out.ReceiverKeptConnected = true
	checkHistory(t, ctx, cluster, &out, uid, senderUID)

	out.Phase = "receiver_owner_restart"
	require.NoError(t, receiver.Close())
	require.NoError(t, cluster.MustNode(3).Stop())
	require.NoError(t, cluster.StartStoppedNode(3))
	processes = append(processes, cluster.MustNode(3).Process)
	out.OwnerRestarts++
	waitReady(t, ctx, cluster)
	receiver = connect(3, uid)
	exchange(t, &out, sender, receiver, senderUID, uid)
	checkHistory(t, ctx, cluster, &out, uid, senderUID)

	out.Phase = "whole_cluster_joined_restart"
	require.NoError(t, sender.Close())
	require.NoError(t, receiver.Close())
	for i := range cluster.Nodes {
		require.NoError(t, cluster.Nodes[i].Stop())
	}
	for i := range cluster.Nodes {
		require.NoError(t, cluster.StartStoppedNode(cluster.Nodes[i].Spec.ID))
		processes = append(processes, cluster.Nodes[i].Process)
	}
	out.WholeClusterRestarts++
	waitReady(t, ctx, cluster)
	out.Phase = "history_before_any_business_reconnect_or_send"
	checkHistory(t, ctx, cluster, &out, uid, senderUID)
	out.Phase = "first_connect_after_whole_cluster_restart"
	sender, receiver = connect(1, senderUID), connect(3, uid)
	exchange(t, &out, sender, receiver, senderUID, uid)
	checkHistory(t, ctx, cluster, &out, uid, senderUID)
	require.Len(t, out.Acknowledged, 8)
	require.Len(t, out.Connects, 5)
	require.Len(t, processes, 7)
	out.Phase = "complete"
}

func token(uid string) string { return "presence-generation-fixture-token-" + uid }

func waitReady(t *testing.T, ctx context.Context, cluster *suite.StartedCluster) suite.SlotLeaderConvergence {
	t.Helper()
	call, end := context.WithTimeout(ctx, 30*time.Second)
	defer end()
	require.NoError(t, cluster.WaitClusterReady(call))
	stable, err := cluster.WaitSlotLeadersStable(call, time.Second)
	require.NoError(t, err)
	require.Len(t, stable.Leaders, 12)
	return stable
}

func receiverPlacement(t *testing.T, slots []suite.SlotDTO, stable suite.SlotLeaderConvergence) (string, uint32, uint64) {
	t.Helper()
	leaders := make(map[uint32]uint64, 12)
	for _, item := range stable.Leaders {
		leaders[item.SlotID] = item.LeaderID
	}
	coverage := make(map[uint16]uint32, 256)
	for _, item := range slots {
		require.NotNil(t, item.HashSlots)
		require.Len(t, item.Assignment.DesiredPeers, 3)
		for _, hash := range item.HashSlots.Items {
			_, duplicate := coverage[hash]
			require.False(t, duplicate)
			coverage[hash] = item.SlotID
		}
	}
	require.Len(t, slots, 12)
	require.Len(t, coverage, 256)
	// Readiness and sender CONNECT must not prewarm the receiver's whole slot,
	// especially after a receiver-owner or whole-cluster boot change.
	excluded := make(map[uint16]bool)
	for _, uid := range []string{"e2e-ready-1", "e2e-ready-2", "e2e-ready-3", "presence-generation-sender"} {
		excluded[routing.HashSlotForKey(uid, 256)] = true
	}
	for i := 0; i < 4096; i++ {
		uid := fmt.Sprintf("presence-generation-receiver-%d", i)
		hash := routing.HashSlotForKey(uid, 256)
		if excluded[hash] {
			continue
		}
		slot := coverage[hash]
		if leader := leaders[slot]; leader != 0 && leader != 3 {
			return uid, slot, leader
		}
	}
	t.Fatal("no receiver with remote authority in public inventory")
	return "", 0, 0
}

func waitMoved(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, slot uint32, old uint64) uint64 {
	t.Helper()
	call, end := context.WithTimeout(ctx, 30*time.Second)
	defer end()
	for {
		stable, err := cluster.WaitSlotLeadersStable(call, 500*time.Millisecond)
		require.NoError(t, err)
		for _, item := range stable.Leaders {
			if item.SlotID == slot && item.LeaderID != old {
				return item.LeaderID
			}
		}
		select {
		case <-call.Done():
			t.Fatal("authority did not move before deadline")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func exchange(t *testing.T, out *receipt, sender, receiver *suite.WKProtoClient, from, to string) {
	t.Helper()
	for _, direction := range []struct {
		src, dst *suite.WKProtoClient
		from, to string
	}{{sender, receiver, from, to}, {receiver, sender, to, from}} {
		seq := uint64(len(out.Acknowledged) + 1)
		key := fmt.Sprintf("presence-generation-message-%d", seq)
		require.NoError(t, direction.src.SendFrame(&frame.SendPacket{ChannelID: direction.to, ChannelType: frame.ChannelTypePerson, ClientSeq: seq, ClientMsgNo: key, Payload: []byte(key)}))
		ack, err := direction.src.ReadSendAck()
		require.NoError(t, err)
		require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
		require.Equal(t, seq, ack.ClientSeq)
		require.Equal(t, key, ack.ClientMsgNo)
		require.NotZero(t, ack.MessageID)
		require.NotZero(t, ack.MessageSeq)
		if len(out.Acknowledged) > 0 {
			require.Greater(t, ack.MessageSeq, out.Acknowledged[len(out.Acknowledged)-1].Seq)
		}
		fact := messageFact{ID: ack.MessageID, IDString: strconv.FormatInt(ack.MessageID, 10), Seq: ack.MessageSeq, Key: key, From: direction.from, Payload: []byte(key)}
		out.Acknowledged = append(out.Acknowledged, fact)
		matched := false
		for attempts := 0; attempts < 16; attempts++ {
			recv, err := direction.dst.ReadRecv()
			require.NoError(t, err)
			observed := messageFact{ID: recv.MessageID, IDString: strconv.FormatInt(recv.MessageID, 10), Seq: recv.MessageSeq, Key: recv.ClientMsgNo, From: recv.FromUID, Payload: recv.Payload}
			known := false
			for _, prior := range out.Acknowledged {
				if prior.ID == observed.ID {
					require.Equal(t, prior, observed)
					known = true
					break
				}
			}
			require.True(t, known, "unexpected receive identity")
			require.Equal(t, direction.from, recv.FromUID)
			require.Equal(t, direction.from, recv.ChannelID)
			require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
			require.False(t, recv.NoPersist)
			require.False(t, recv.SyncOnce)
			require.NoError(t, direction.dst.RecvAck(recv.MessageID, recv.MessageSeq))
			out.RecvACKs++
			if observed.ID == fact.ID {
				matched = true
				break
			}
			out.ExactRedeliveries++
		}
		require.True(t, matched, "current acknowledged message missing within bounded redeliveries")
	}
}

func checkHistory(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, out *receipt, login, peer string) {
	t.Helper()
	// Wait for membership projection separately; successful wrong history never
	// becomes a retry. After restart this is a public read, not SEND or CONNECT.
	tail := out.Acknowledged[len(out.Acknowledged)-1]
	suite.RequireConversationEventuallyWithin(t, *cluster.MustNode(1), login, peer, 15*time.Second, func(item suite.ConversationListItem) error {
		if item.LastMessage == nil || item.LastMessage.MessageSeq != tail.Seq {
			return fmt.Errorf("acknowledged tail not projected")
		}
		return nil
	})
	for _, node := range cluster.Nodes {
		out.History = append(out.History, historyReceipt{Phase: out.Phase, Node: node.Spec.ID})
		record := &out.History[len(out.History)-1]
		call, end := context.WithTimeout(ctx, 15*time.Second)
		defer end() // Also release the timer when an assertion ends this phase.
		cursor := uint64(1)
		for pageNo := 0; pageNo < 8; pageNo++ {
			var page historyPage
			for {
				_, err := suite.PostJSON(call, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": login, "channel_id": peer, "channel_type": frame.ChannelTypePerson, "start_message_seq": cursor, "end_message_seq": 0, "pull_mode": 1, "limit": 2}, &page)
				if err == nil {
					break
				}
				require.True(t, historyUnavailable(err), "history returned non-retryable failure: %v", err)
				record.UnavailableRetries++
				select {
				case <-call.Done():
					require.NoError(t, call.Err())
				case <-time.After(100 * time.Millisecond):
				}
			}
			record.Pages++
			require.Contains(t, []int{0, 1}, page.More)
			require.NotEmpty(t, page.Messages)
			require.LessOrEqual(t, len(page.Messages), 2)
			for _, row := range page.Messages {
				require.NoError(t, row.validateIdentity(), "public numeric/string identity mismatch")
				position := len(record.Messages)
				require.Less(t, position, len(out.Acknowledged))
				require.Equal(t, out.Acknowledged[position], row, "successful history must match every acknowledged row")
				record.Messages = append(record.Messages, row)
			}
			if page.More == 0 {
				record.Complete = true
				break
			}
			cursor = page.Messages[len(page.Messages)-1].Seq + 1
		}
		end()
		require.True(t, record.Complete)
		require.Equal(t, out.Acknowledged, record.Messages)
	}
}

func historyUnavailable(err error) bool {
	var status *suite.HTTPStatusError
	var body struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	}
	return errors.As(err, &status) && status.StatusCode == http.StatusServiceUnavailable && json.Unmarshal([]byte(status.Body), &body) == nil && body.Code == "unavailable" && body.Status == http.StatusServiceUnavailable
}
