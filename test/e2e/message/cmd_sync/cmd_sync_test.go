//go:build e2e

package cmd_sync

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const commandChannelSuffix = "____cmd"

// legacyCMDMessage is the compatible /message/sync item shape.
type legacyCMDMessage struct {
	Header       legacyCMDHeader `json:"header"`
	MessageID    int64           `json:"message_id"`
	ClientMsgNo  string          `json:"client_msg_no"`
	MessageSeq   uint64          `json:"message_seq"`
	FromUID      string          `json:"from_uid"`
	ChannelID    string          `json:"channel_id"`
	ChannelType  uint8           `json:"channel_type"`
	Timestamp    int32           `json:"timestamp"`
	Payload      []byte          `json:"payload"`
	MessageIDStr string          `json:"message_idstr"`
}

// legacyCMDHeader carries compatible message flags returned by /message/sync.
type legacyCMDHeader struct {
	NoPersist int `json:"no_persist"`
	RedDot    int `json:"red_dot"`
	SyncOnce  int `json:"sync_once"`
}

func TestWukongIMUnifiedConversationProjectionIsolatesCMDSync(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const (
		channelID       = "e2e-cmd-sync-room"
		aliceUID        = "cmd-sync-alice"
		bobUID          = "cmd-sync-bob"
		ordinaryMsgNo   = "cmd-sync-normal-1"
		ordinaryPayload = "ordinary conversation message"
		cmdMsgNo        = "cmd-sync-command-1"
		cmdPayload      = "command sync message"
	)
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"reset":        1,
		"subscribers":  []string{aliceUID, bobUID},
	}), node.DumpDiagnostics())

	alice := mustConnectWKProto(t, *node, aliceUID)
	defer func() { _ = alice.Close() }()

	sendWKProtoMessage(t, *node, alice, frame.SendPacket{
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypeGroup,
		ClientSeq:   1,
		ClientMsgNo: ordinaryMsgNo,
		Payload:     []byte(ordinaryPayload),
	})
	suite.RequireConversationEventually(t, *node, bobUID, channelID, func(item suite.ConversationListItem) error {
		return checkOrdinaryConversation(item, ordinaryMsgNo, ordinaryPayload)
	})

	cmdSend, err := suite.PostMessageSend(ctx, node.APIAddr(), map[string]any{
		"from_uid":      aliceUID,
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"client_msg_no": cmdMsgNo,
		"sync_once":     1,
		"payload":       base64.StdEncoding.EncodeToString([]byte(cmdPayload)),
	})
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), cmdSend.Reason, node.DumpDiagnostics())
	require.NotZero(t, cmdSend.MessageID)
	require.NotZero(t, cmdSend.MessageSeq)

	cmdMessages := requireMessageSyncEventually(t, *node, bobUID, cmdMsgNo, 10*time.Second)
	require.Len(t, cmdMessages, 1, node.DumpDiagnostics())
	require.Equal(t, cmdSend.MessageID, cmdMessages[0].MessageID)
	require.Equal(t, cmdSend.MessageSeq, cmdMessages[0].MessageSeq)
	require.Equal(t, aliceUID, cmdMessages[0].FromUID)
	require.Equal(t, channelID, cmdMessages[0].ChannelID)
	require.Equal(t, uint8(frame.ChannelTypeGroup), cmdMessages[0].ChannelType)
	require.Equal(t, 1, cmdMessages[0].Header.SyncOnce)
	require.Equal(t, cmdPayload, string(cmdMessages[0].Payload))
	requireNoCommandSuffix(t, cmdMessages[0].ChannelID)

	page, err := suite.PostConversationList(ctx, node.APIAddr(), bobUID, 10)
	require.NoError(t, err, node.DumpDiagnostics())
	for _, item := range page.Conversations {
		requireNoCommandSuffix(t, item.ChannelID)
	}
	item, ok := suite.FindConversation(page, channelID)
	require.True(t, ok, "ordinary conversation missing after cmd sync: %#v\n%s", page.Conversations, node.DumpDiagnostics())
	require.NoError(t, checkOrdinaryConversation(item, ordinaryMsgNo, ordinaryPayload), node.DumpDiagnostics())

	requireMessageSyncAck(t, *node, bobUID, cmdMessages[len(cmdMessages)-1].MessageSeq)
	requireMessageSyncEmptyEventually(t, *node, bobUID, 10*time.Second)
	requireMessageSyncEmptyFor(t, *node, bobUID, 500*time.Millisecond)
}

func TestWukongIMCMDSyncProjectionSurvivesRestartBeforeSync(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL": "1h",
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const (
		channelID  = "e2e-cmd-sync-restart-room"
		aliceUID   = "cmd-sync-restart-alice"
		bobUID     = "cmd-sync-restart-bob"
		cmdMsgNo   = "cmd-sync-restart-command-1"
		cmdPayload = "command sync message before restart"
	)
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"reset":        1,
		"subscribers":  []string{aliceUID, bobUID},
	}), node.DumpDiagnostics())

	cmdSend, err := suite.PostMessageSend(ctx, node.APIAddr(), map[string]any{
		"from_uid":      aliceUID,
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"client_msg_no": cmdMsgNo,
		"sync_once":     1,
		"payload":       base64.StdEncoding.EncodeToString([]byte(cmdPayload)),
	})
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), cmdSend.Reason, node.DumpDiagnostics())
	require.NotZero(t, cmdSend.MessageSeq)

	restartSingleNodeCluster(t, node)

	cmdMessages := requireMessageSyncEventually(t, *node, bobUID, cmdMsgNo, 10*time.Second)
	require.Len(t, cmdMessages, 1, node.DumpDiagnostics())
	require.Equal(t, cmdSend.MessageSeq, cmdMessages[0].MessageSeq)
	require.Equal(t, cmdPayload, string(cmdMessages[0].Payload))
	requireNoCommandSuffix(t, cmdMessages[0].ChannelID)

	restartSingleNodeCluster(t, node)
	cmdMessages = requireMessageSyncEventually(t, *node, bobUID, cmdMsgNo, 10*time.Second)
	require.Len(t, cmdMessages, 1, node.DumpDiagnostics())
	require.Equal(t, cmdSend.MessageSeq, cmdMessages[0].MessageSeq)
	require.Equal(t, cmdPayload, string(cmdMessages[0].Payload))

	requireMessageSyncAck(t, *node, bobUID, cmdMessages[len(cmdMessages)-1].MessageSeq)
	restartSingleNodeCluster(t, node)
	requireMessageSyncEmptyEventually(t, *node, bobUID, 10*time.Second)
	requireMessageSyncEmptyFor(t, *node, bobUID, 500*time.Millisecond)
}

func restartSingleNodeCluster(t *testing.T, node *suite.StartedNode) {
	t.Helper()

	require.NotNil(t, node)
	require.NotNil(t, node.Process)
	require.NoError(t, node.Process.Stop())
	require.NoError(t, node.Process.Start())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, suite.WaitHTTPReady(ctx, node.APIAddr(), "/readyz"), node.DumpDiagnostics())
	require.NoError(t, suite.WaitWKProtoReady(ctx, node.GatewayAddr()), node.DumpDiagnostics())
}

func mustConnectWKProto(t *testing.T, node suite.StartedNode, uid string) *suite.WKProtoClient {
	t.Helper()

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	require.NoError(t, client.Connect(node.GatewayAddr(), uid, uid+"-device"), node.DumpDiagnostics())
	return client
}

func sendWKProtoMessage(t *testing.T, node suite.StartedNode, client *suite.WKProtoClient, pkt frame.SendPacket) *frame.SendackPacket {
	t.Helper()

	require.NoError(t, client.SendFrame(&pkt), node.DumpDiagnostics())
	sendack, err := client.ReadSendAck()
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode, node.DumpDiagnostics())
	require.Equal(t, pkt.ClientSeq, sendack.ClientSeq)
	require.Equal(t, pkt.ClientMsgNo, sendack.ClientMsgNo)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)
	return sendack
}

func postMessageSync(ctx context.Context, apiAddr, uid string) ([]legacyCMDMessage, error) {
	var out []legacyCMDMessage
	_, err := suite.PostJSON(ctx, "http://"+apiAddr+"/message/sync", map[string]any{
		"uid":   uid,
		"limit": 10,
	}, &out)
	return out, err
}

func requireMessageSyncEventually(t *testing.T, node suite.StartedNode, uid, clientMsgNo string, timeout time.Duration) []legacyCMDMessage {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastMessages []legacyCMDMessage
	var lastErr error
	for {
		messages, err := postMessageSync(ctx, node.APIAddr(), uid)
		if err == nil {
			lastMessages = messages
			if containsClientMsgNo(messages, clientMsgNo) {
				return messages
			}
			lastErr = fmt.Errorf("client_msg_no %s not found in %#v", clientMsgNo, messages)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("message sync for uid %s timed out: lastMessages=%#v lastErr=%v\n%s", uid, lastMessages, lastErr, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireMessageSyncAck(t *testing.T, node suite.StartedNode, uid string, lastMessageSeq uint64) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/message/syncack", map[string]any{
		"uid":              uid,
		"last_message_seq": lastMessageSeq,
	}, nil)
	require.NoError(t, err, node.DumpDiagnostics())
}

func requireMessageSyncEmptyEventually(t *testing.T, node suite.StartedNode, uid string, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastMessages []legacyCMDMessage
	var lastErr error
	for {
		messages, err := postMessageSync(ctx, node.APIAddr(), uid)
		if err == nil {
			lastMessages = messages
			if len(messages) == 0 {
				return
			}
			lastErr = fmt.Errorf("message sync still returned %#v", messages)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("message sync for uid %s did not drain: lastMessages=%#v lastErr=%v\n%s", uid, lastMessages, lastErr, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireMessageSyncEmptyFor(t *testing.T, node suite.StartedNode, uid string, duration time.Duration) {
	t.Helper()

	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		messages, err := postMessageSync(ctx, node.APIAddr(), uid)
		cancel()
		require.NoError(t, err, node.DumpDiagnostics())
		require.Empty(t, messages, "message sync should remain empty after ack\n%s", node.DumpDiagnostics())
		time.Sleep(100 * time.Millisecond)
	}
}

func containsClientMsgNo(messages []legacyCMDMessage, clientMsgNo string) bool {
	for _, msg := range messages {
		if msg.ClientMsgNo == clientMsgNo {
			return true
		}
	}
	return false
}

func checkOrdinaryConversation(item suite.ConversationListItem, clientMsgNo, payload string) error {
	if item.SparseActive {
		return fmt.Errorf("sparse_active = true, want ordinary conversation row")
	}
	if item.LastMessage == nil {
		return fmt.Errorf("last_message is nil")
	}
	if item.LastMessage.ClientMsgNo != clientMsgNo || string(item.LastMessage.Payload) != payload {
		return fmt.Errorf("last_message = %#v, want client_msg_no=%s payload=%q", item.LastMessage, clientMsgNo, payload)
	}
	return nil
}

func requireNoCommandSuffix(t *testing.T, channelID string) {
	t.Helper()
	require.Falsef(t, strings.Contains(channelID, commandChannelSuffix), "channel id %q leaked command suffix", channelID)
}
