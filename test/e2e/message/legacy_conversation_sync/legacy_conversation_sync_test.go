//go:build e2e

package legacy_conversation_sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const legacySyncPollInterval = 100 * time.Millisecond

type legacySyncRequest struct {
	UID             string `json:"uid"`
	Version         int64  `json:"version"`
	LastMessageSeqs string `json:"last_msg_seqs"`
	MessageCount    int    `json:"msg_count"`
}

type legacySyncRow struct {
	ChannelID       string                `json:"channel_id"`
	ChannelType     uint8                 `json:"channel_type"`
	Unread          uint64                `json:"unread"`
	Timestamp       int64                 `json:"timestamp"`
	LastMessageSeq  uint64                `json:"last_msg_seq"`
	LastClientMsgNo string                `json:"last_client_msg_no"`
	ReadToMsgSeq    uint64                `json:"readed_to_msg_seq"`
	Version         int64                 `json:"version"`
	Recents         []legacyRecentMessage `json:"recents"`
}

type legacyRecentMessage struct {
	MessageID    int64  `json:"message_id"`
	MessageIDStr string `json:"message_idstr"`
	ClientMsgNo  string `json:"client_msg_no"`
	MessageSeq   uint64 `json:"message_seq"`
	FromUID      string `json:"from_uid"`
	ChannelID    string `json:"channel_id"`
	ChannelType  uint8  `json:"channel_type"`
	Timestamp    int32  `json:"timestamp"`
	Payload      []byte `json:"payload"`
}

type committedMessage struct {
	MessageID   int64
	MessageSeq  uint64
	ClientMsgNo string
	FromUID     string
	Payload     string
}

type expectedLegacySync struct {
	ChannelID    string
	ChannelType  uint8
	Unread       uint64
	ReadToMsgSeq uint64
	Message      committedMessage
}

func TestLegacyConversationSyncSingleNodeCluster(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster(
		suite.WithNodeConfigOverrides(1, map[string]string{
			"WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_CLUSTER_HASH_SLOT_COUNT": "256",
		}),
	)

	t.Run("person full and cursor sync project both users", func(t *testing.T) {
		runLegacyPersonSyncFlow(t, *node, node.DumpDiagnostics, legacyPersonUsers{
			SenderUID: "legacy-single-person-sender", RecipientUID: "legacy-single-person-recipient",
			MessagePrefix: "legacy-single-person",
		})
	})

	t.Run("group full and cursor sync project every member", func(t *testing.T) {
		runLegacyGroupSyncFlow(t, *node, node.DumpDiagnostics, legacyGroupUsers{
			ChannelID:     "legacy-single-group",
			SenderUID:     "legacy-single-group-sender",
			RecipientUIDs: []string{"legacy-single-group-recipient-1", "legacy-single-group-recipient-2"},
			MessagePrefix: "legacy-single-group",
		})
	})
}

func TestLegacyConversationSyncMultiNodeCluster(t *testing.T) {
	cluster := suite.New(t).StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, map[string]string{
			"WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_CLUSTER_HASH_SLOT_COUNT": "256",
		}),
		suite.WithNodeConfigOverrides(2, map[string]string{
			"WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_CLUSTER_HASH_SLOT_COUNT": "256",
		}),
		suite.WithNodeConfigOverrides(3, map[string]string{
			"WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_CLUSTER_HASH_SLOT_COUNT": "256",
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())
	ingress := cluster.MustNode(1)

	t.Run("person sync crosses UID and Channel leaders", func(t *testing.T) {
		runRemoteLegacyPersonSyncFlow(t, cluster, *ingress)
	})

	t.Run("group sync crosses UID and Channel leaders", func(t *testing.T) {
		runRemoteLegacyGroupSyncFlow(t, cluster, *ingress)
	})
}

func runRemoteLegacyPersonSyncFlow(t *testing.T, cluster *suite.StartedCluster, ingress suite.StartedNode) {
	t.Helper()
	for candidate := 0; candidate < 40; candidate++ {
		users := legacyPersonUsers{
			SenderUID:     fmt.Sprintf("legacy-multi-person-sender-%02d", candidate),
			RecipientUID:  uidOwnedByRemoteSlotLeader(t, cluster, ingress.Spec.ID, fmt.Sprintf("legacy-multi-person-recipient-%02d", candidate)),
			MessagePrefix: fmt.Sprintf("legacy-multi-person-%02d", candidate),
		}
		diagnostics := cluster.DumpDiagnostics
		client, first := prepareLegacyPersonFirstMessage(t, ingress, diagnostics, users)
		canonicalChannelID := runtimechannelid.EncodePersonChannel(users.SenderUID, users.RecipientUID)
		meta := suite.RequireChannelRuntimeMetaEventually(t, cluster, &ingress, canonicalChannelID, frame.ChannelTypePerson, 20*time.Second)
		if meta.Leader == ingress.Spec.ID {
			_ = client.Close()
			continue
		}
		finishLegacyPersonSyncFlow(t, ingress, diagnostics, users, client, first)
		require.NoError(t, client.Close())
		return
	}
	t.Fatalf("no person Channel Leader differed from ingress node %d\n%s", ingress.Spec.ID, cluster.DumpDiagnostics())
}

type legacyPersonUsers struct {
	SenderUID     string
	RecipientUID  string
	MessagePrefix string
}

func runLegacyPersonSyncFlow(t *testing.T, node suite.StartedNode, diagnostics func() string, users legacyPersonUsers) {
	t.Helper()
	client, first := prepareLegacyPersonFirstMessage(t, node, diagnostics, users)
	defer func() { _ = client.Close() }()
	finishLegacyPersonSyncFlow(t, node, diagnostics, users, client, first)
}

func prepareLegacyPersonFirstMessage(t *testing.T, node suite.StartedNode, diagnostics func() string, users legacyPersonUsers) (*suite.WKProtoClient, committedMessage) {
	t.Helper()
	requireLegacySyncEmpty(t, node.APIAddr(), users.SenderUID, diagnostics)
	requireLegacySyncEmpty(t, node.APIAddr(), users.RecipientUID, diagnostics)
	client := connectLegacySender(t, node, users.SenderUID, diagnostics)
	first := sendCommittedMessage(t, client, users.SenderUID, users.RecipientUID, frame.ChannelTypePerson, 1, users.MessagePrefix+"-1", diagnostics)
	return client, first
}

func finishLegacyPersonSyncFlow(t *testing.T, node suite.StartedNode, diagnostics func() string, users legacyPersonUsers, client *suite.WKProtoClient, first committedMessage) {
	t.Helper()
	firstRows := map[string]legacySyncRow{
		users.SenderUID: requireLegacySyncEventually(t, node.APIAddr(), legacySyncRequest{
			UID: users.SenderUID, MessageCount: 1,
		}, expectedLegacySync{
			ChannelID: users.RecipientUID, ChannelType: frame.ChannelTypePerson,
			Unread: 0, ReadToMsgSeq: first.MessageSeq, Message: first,
		}, diagnostics),
		users.RecipientUID: requireLegacySyncEventually(t, node.APIAddr(), legacySyncRequest{
			UID: users.RecipientUID, MessageCount: 1,
		}, expectedLegacySync{
			ChannelID: users.SenderUID, ChannelType: frame.ChannelTypePerson,
			Unread: 1, ReadToMsgSeq: 0, Message: first,
		}, diagnostics),
	}

	second := sendCommittedMessage(t, client, users.SenderUID, users.RecipientUID, frame.ChannelTypePerson, 2, users.MessagePrefix+"-2", diagnostics)
	for _, tc := range []struct {
		uid          string
		channelID    string
		unread       uint64
		readToMsgSeq uint64
	}{
		{uid: users.SenderUID, channelID: users.RecipientUID, unread: 0, readToMsgSeq: second.MessageSeq},
		{uid: users.RecipientUID, channelID: users.SenderUID, unread: 2, readToMsgSeq: 0},
	} {
		firstRow := firstRows[tc.uid]
		requireLegacySyncEventually(t, node.APIAddr(), legacySyncRequest{
			UID: tc.uid, Version: firstRow.Version,
			LastMessageSeqs: fmt.Sprintf("%s:%d:%d", tc.channelID, frame.ChannelTypePerson, first.MessageSeq),
			MessageCount:    1,
		}, expectedLegacySync{
			ChannelID: tc.channelID, ChannelType: frame.ChannelTypePerson,
			Unread: tc.unread, ReadToMsgSeq: tc.readToMsgSeq, Message: second,
		}, diagnostics)
	}
}

func runRemoteLegacyGroupSyncFlow(t *testing.T, cluster *suite.StartedCluster, ingress suite.StartedNode) {
	t.Helper()
	prefix := fmt.Sprintf("legacy-multi-group-%d", time.Now().UnixNano())
	for candidate := 0; candidate < 40; candidate++ {
		channelID := fmt.Sprintf("%s-%02d", prefix, candidate)
		users := legacyGroupUsers{
			ChannelID:     channelID,
			SenderUID:     fmt.Sprintf("legacy-multi-group-sender-%02d", candidate),
			MessagePrefix: fmt.Sprintf("legacy-multi-group-%02d", candidate),
			RecipientUIDs: []string{
				uidOwnedByRemoteSlotLeader(t, cluster, ingress.Spec.ID, fmt.Sprintf("legacy-multi-group-recipient-1-%02d", candidate)),
				fmt.Sprintf("legacy-multi-group-recipient-2-%02d", candidate),
			},
		}
		members, client, first := prepareLegacyGroupFirstMessage(t, ingress, cluster.DumpDiagnostics, users)
		meta := suite.RequireChannelRuntimeMetaEventually(t, cluster, &ingress, channelID, frame.ChannelTypeGroup, 20*time.Second)
		if meta.Leader == ingress.Spec.ID {
			_ = client.Close()
			continue
		}
		finishLegacyGroupSyncFlow(t, ingress, cluster.DumpDiagnostics, users, members, client, first)
		require.NoError(t, client.Close())
		return
	}
	t.Fatalf("no group Channel Leader differed from ingress node %d\n%s", ingress.Spec.ID, cluster.DumpDiagnostics())
}

func uidOwnedByRemoteSlotLeader(t *testing.T, cluster *suite.StartedCluster, ingressNodeID uint64, prefix string) string {
	t.Helper()
	slots := cluster.ManagerClient(t, ingressNodeID).MustSlots(t)
	var hashSlotCount uint16
	for _, slot := range slots {
		if slot.HashSlots == nil {
			continue
		}
		for _, hashSlot := range slot.HashSlots.Items {
			if hashSlot >= hashSlotCount {
				hashSlotCount = hashSlot + 1
			}
		}
	}
	require.NotZero(t, hashSlotCount, "manager Slot inventory has no physical Hash Slots\n%s", cluster.DumpDiagnostics())

	for _, slot := range slots {
		if slot.Runtime.LeaderID == 0 || slot.Runtime.LeaderID == ingressNodeID || slot.HashSlots == nil {
			continue
		}
		for candidate := 0; candidate < 100_000; candidate++ {
			uid := fmt.Sprintf("%s-%d", prefix, candidate)
			hashSlot := routing.HashSlotForKey(uid, hashSlotCount)
			for _, owned := range slot.HashSlots.Items {
				if hashSlot == owned {
					return uid
				}
			}
		}
	}
	t.Fatalf("no UID Slot Leader differed from ingress node %d\n%s", ingressNodeID, cluster.DumpDiagnostics())
	return ""
}

type legacyGroupUsers struct {
	ChannelID     string
	SenderUID     string
	RecipientUIDs []string
	MessagePrefix string
}

func runLegacyGroupSyncFlow(t *testing.T, node suite.StartedNode, diagnostics func() string, users legacyGroupUsers) {
	t.Helper()
	members, client, first := prepareLegacyGroupFirstMessage(t, node, diagnostics, users)
	defer func() { _ = client.Close() }()
	finishLegacyGroupSyncFlow(t, node, diagnostics, users, members, client, first)
}

func prepareLegacyGroupFirstMessage(t *testing.T, node suite.StartedNode, diagnostics func() string, users legacyGroupUsers) ([]string, *suite.WKProtoClient, committedMessage) {
	t.Helper()
	require.Len(t, users.RecipientUIDs, 2)
	members := append([]string{users.SenderUID}, users.RecipientUIDs...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id": users.ChannelID, "channel_type": frame.ChannelTypeGroup,
		"reset": 1, "subscribers": members,
	}), diagnostics())
	cancel()

	for _, uid := range members {
		requireLegacySyncEmpty(t, node.APIAddr(), uid, diagnostics)
	}

	client := connectLegacySender(t, node, users.SenderUID, diagnostics)
	first := sendCommittedMessage(t, client, users.SenderUID, users.ChannelID, frame.ChannelTypeGroup, 1, users.MessagePrefix+"-1", diagnostics)
	return members, client, first
}

func finishLegacyGroupSyncFlow(t *testing.T, node suite.StartedNode, diagnostics func() string, users legacyGroupUsers, members []string, client *suite.WKProtoClient, first committedMessage) {
	t.Helper()
	firstRows := make(map[string]legacySyncRow, len(members))
	for _, uid := range members {
		unread, readToMsgSeq := legacyBadgeExpectation(uid, users.SenderUID, first.MessageSeq, 1)
		firstRows[uid] = requireLegacySyncEventually(t, node.APIAddr(), legacySyncRequest{
			UID: uid, MessageCount: 1,
		}, expectedLegacySync{
			ChannelID: users.ChannelID, ChannelType: frame.ChannelTypeGroup,
			Unread: unread, ReadToMsgSeq: readToMsgSeq, Message: first,
		}, diagnostics)
	}

	second := sendCommittedMessage(t, client, users.SenderUID, users.ChannelID, frame.ChannelTypeGroup, 2, users.MessagePrefix+"-2", diagnostics)
	for _, uid := range members {
		unread, readToMsgSeq := legacyBadgeExpectation(uid, users.SenderUID, second.MessageSeq, 2)
		firstRow := firstRows[uid]
		requireLegacySyncEventually(t, node.APIAddr(), legacySyncRequest{
			UID: uid, Version: firstRow.Version,
			LastMessageSeqs: fmt.Sprintf("%s:%d:%d", users.ChannelID, frame.ChannelTypeGroup, first.MessageSeq),
			MessageCount:    1,
		}, expectedLegacySync{
			ChannelID: users.ChannelID, ChannelType: frame.ChannelTypeGroup,
			Unread: unread, ReadToMsgSeq: readToMsgSeq, Message: second,
		}, diagnostics)
	}
	requireLegacySyncBudgetPreservesConversations(t, node, diagnostics, users, members, client, first, second)
}

// requireLegacySyncBudgetPreservesConversations reuses the group flow to prove
// an oversized recent-message request retains every selected conversation.
func requireLegacySyncBudgetPreservesConversations(t *testing.T, node suite.StartedNode, diagnostics func() string, users legacyGroupUsers, members []string, client *suite.WKProtoClient, first, second committedMessage) {
	t.Helper()
	started := time.Now().UTC()
	var observations []map[string]any
	passed := false
	defer func() {
		dir := os.Getenv("WK_E2E_LEGACY_SYNC_REPORT_DIR")
		if dir == "" {
			dir = node.Spec.RootDir
		}
		path := filepath.Join(dir, strings.SplitN(t.Name(), "/", 2)[0]+".json")
		body, err := json.MarshalIndent(map[string]any{
			"scenario": "legacy_sync_recent_budget", "test": t.Name(), "hash_slots": 256,
			"started_at": started, "finished_at": time.Now().UTC(),
			"passed": passed && !t.Failed(), "requests": observations,
		}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(path, append(body, '\n'), 0o644))
		t.Logf("legacy sync budget evidence: %s", path)
	}()

	otherChannelID := users.ChannelID + "-budget"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id": otherChannelID, "channel_type": frame.ChannelTypeGroup,
		"reset": 1, "subscribers": members,
	})
	cancel()
	require.NoError(t, err, diagnostics())
	other := sendCommittedMessage(t, client, users.SenderUID, otherChannelID, frame.ChannelTypeGroup, 3, users.MessagePrefix+"-budget", diagnostics)

	for _, uid := range []string{users.SenderUID, users.RecipientUIDs[0]} {
		unread, readToMsgSeq := legacyBadgeExpectation(uid, users.SenderUID, second.MessageSeq, 2)
		otherUnread, otherReadToMsgSeq := legacyBadgeExpectation(uid, users.SenderUID, other.MessageSeq, 1)
		want := []expectedLegacySync{
			{ChannelID: users.ChannelID, ChannelType: frame.ChannelTypeGroup, Unread: unread, ReadToMsgSeq: readToMsgSeq, Message: second},
			{ChannelID: otherChannelID, ChannelType: frame.ChannelTypeGroup, Unread: otherUnread, ReadToMsgSeq: otherReadToMsgSeq, Message: other},
		}
		req := legacySyncRequest{
			UID: uid, LastMessageSeqs: fmt.Sprintf("%s:%d:%d", users.ChannelID, frame.ChannelTypeGroup, first.MessageSeq), MessageCount: 1,
		}
		var lastErr error
		require.Eventually(t, func() bool {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			rows, err := postLegacySync(ctx, node.APIAddr(), req)
			lastErr = err
			if err == nil {
				lastErr = checkLegacySyncRows(rows, want)
			}
			return lastErr == nil
		}, 20*time.Second, legacySyncPollInterval, "both conversations must be visible before testing the budget\n%s", diagnostics())

		// Two selected conversations exceed the old 10,000 requested-record
		// guard even though this cursor needs only their latest messages.
		req.MessageCount = 10000
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var rows []legacySyncRow
		body, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/conversation/sync", req, &rows)
		cancel()
		observation := map[string]any{"request": req, "expected": want, "response": json.RawMessage(body), "http_succeeded": err == nil}
		if err != nil {
			observation["error"] = err.Error()
		}
		observations = append(observations, observation)
		require.NoError(t, err, diagnostics())
		require.NoError(t, checkLegacySyncRows(rows, want), diagnostics())
	}
	passed = true
}

func checkLegacySyncRows(rows []legacySyncRow, want []expectedLegacySync) error {
	if len(rows) != len(want) {
		return fmt.Errorf("conversation count = %d, want %d", len(rows), len(want))
	}
	for _, expected := range want {
		found := false
		for _, row := range rows {
			if row.ChannelID == expected.ChannelID && row.ChannelType == expected.ChannelType {
				if err := checkLegacySyncRow(row, expected); err != nil {
					return err
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing conversation %s/%d", expected.ChannelID, expected.ChannelType)
		}
	}
	return nil
}

func legacyBadgeExpectation(uid, senderUID string, senderReadToMsgSeq, recipientUnread uint64) (uint64, uint64) {
	if uid == senderUID {
		return 0, senderReadToMsgSeq
	}
	return recipientUnread, 0
}

func connectLegacySender(t *testing.T, node suite.StartedNode, uid string, diagnostics func() string) *suite.WKProtoClient {
	t.Helper()
	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	require.NoError(t, client.Connect(node.GatewayAddr(), uid, uid+"-device"), diagnostics())
	return client
}

func sendCommittedMessage(t *testing.T, client *suite.WKProtoClient, fromUID, channelID string, channelType uint8, clientSeq uint64, clientMsgNo string, diagnostics func() string) committedMessage {
	t.Helper()
	payload := "payload-" + clientMsgNo
	require.NoError(t, client.SendFrame(&frame.SendPacket{
		ChannelID: channelID, ChannelType: channelType,
		ClientSeq: clientSeq, ClientMsgNo: clientMsgNo, Payload: []byte(payload),
	}), diagnostics())
	ack, err := client.ReadSendAck()
	require.NoError(t, err, diagnostics())
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode, diagnostics())
	require.Equal(t, clientSeq, ack.ClientSeq)
	require.Equal(t, clientMsgNo, ack.ClientMsgNo)
	require.NotZero(t, ack.MessageID)
	require.NotZero(t, ack.MessageSeq)
	return committedMessage{
		MessageID: ack.MessageID, MessageSeq: ack.MessageSeq, ClientMsgNo: clientMsgNo,
		FromUID: fromUID, Payload: payload,
	}
}

func requireLegacySyncEmpty(t *testing.T, apiAddr, uid string, diagnostics func() string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := postLegacySync(ctx, apiAddr, legacySyncRequest{UID: uid, MessageCount: 1})
	require.NoError(t, err, diagnostics())
	require.Empty(t, rows, diagnostics())
}

func requireLegacySyncEventually(t *testing.T, apiAddr string, req legacySyncRequest, want expectedLegacySync, diagnostics func() string) legacySyncRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(legacySyncPollInterval)
	defer ticker.Stop()

	var lastRows []legacySyncRow
	var lastErr error
	for {
		requestCtx, requestCancel := context.WithTimeout(ctx, 5*time.Second)
		rows, err := postLegacySync(requestCtx, apiAddr, req)
		requestCancel()
		if err == nil {
			lastRows = rows
			if len(rows) == 1 {
				if checkErr := checkLegacySyncRow(rows[0], want); checkErr == nil {
					return rows[0]
				} else {
					lastErr = checkErr
				}
			} else {
				lastErr = fmt.Errorf("conversation count = %d, want exactly one", len(rows))
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("legacy conversation sync for %s did not converge: rows=%+v lastErr=%v\n%s", req.UID, lastRows, lastErr, diagnostics())
		case <-ticker.C:
		}
	}
}

func postLegacySync(ctx context.Context, apiAddr string, req legacySyncRequest) ([]legacySyncRow, error) {
	var rows []legacySyncRow
	_, err := suite.PostJSON(ctx, "http://"+apiAddr+"/conversation/sync", req, &rows)
	return rows, err
}

func checkLegacySyncRow(got legacySyncRow, want expectedLegacySync) error {
	if got.ChannelID != want.ChannelID || got.ChannelType != want.ChannelType {
		return fmt.Errorf("conversation key = %s/%d, want %s/%d", got.ChannelID, got.ChannelType, want.ChannelID, want.ChannelType)
	}
	if got.Unread != want.Unread || got.ReadToMsgSeq != want.ReadToMsgSeq {
		return fmt.Errorf("badge = unread:%d read_to:%d, want unread:%d read_to:%d", got.Unread, got.ReadToMsgSeq, want.Unread, want.ReadToMsgSeq)
	}
	if got.Timestamp <= 0 || got.Version <= 0 {
		return fmt.Errorf("timestamp/version = %d/%d, want positive", got.Timestamp, got.Version)
	}
	if got.LastMessageSeq != want.Message.MessageSeq || got.LastClientMsgNo != want.Message.ClientMsgNo {
		return fmt.Errorf("last message = seq:%d client_msg_no:%s, want seq:%d client_msg_no:%s", got.LastMessageSeq, got.LastClientMsgNo, want.Message.MessageSeq, want.Message.ClientMsgNo)
	}
	if len(got.Recents) != 1 {
		return fmt.Errorf("recent count = %d, want exactly one", len(got.Recents))
	}
	recent := got.Recents[0]
	if got.Timestamp != int64(recent.Timestamp) {
		return fmt.Errorf("conversation timestamp = %d, want recent timestamp %d", got.Timestamp, recent.Timestamp)
	}
	if recent.MessageID != want.Message.MessageID || recent.MessageIDStr != strconv.FormatInt(want.Message.MessageID, 10) || recent.MessageSeq != want.Message.MessageSeq {
		return fmt.Errorf("recent id/idstr/seq = %d/%s/%d, want %d/%s/%d", recent.MessageID, recent.MessageIDStr, recent.MessageSeq, want.Message.MessageID, strconv.FormatInt(want.Message.MessageID, 10), want.Message.MessageSeq)
	}
	if recent.ClientMsgNo != want.Message.ClientMsgNo || recent.FromUID != want.Message.FromUID || string(recent.Payload) != want.Message.Payload {
		return fmt.Errorf("recent identity/payload = client_msg_no:%s from:%s payload:%q, want client_msg_no:%s from:%s payload:%q", recent.ClientMsgNo, recent.FromUID, recent.Payload, want.Message.ClientMsgNo, want.Message.FromUID, want.Message.Payload)
	}
	if recent.ChannelID != want.ChannelID || recent.ChannelType != want.ChannelType {
		return fmt.Errorf("recent channel = %s/%d, want %s/%d", recent.ChannelID, recent.ChannelType, want.ChannelID, want.ChannelType)
	}
	if recent.Timestamp <= 0 {
		return fmt.Errorf("recent timestamp = %d, want positive", recent.Timestamp)
	}
	return nil
}
