//go:build e2e

package single_node_send

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestWukongIMSingleNodeClusterSendProjectsConversationList(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster()

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	require.NoError(t, client.Connect(node.GatewayAddr(), "e2e-sender", "e2e-sender-device"), node.DumpDiagnostics())

	const (
		clientSeq   uint64 = 1
		clientMsgNo        = "wukongim-sendack-e2e-1"
		payload            = "hello from wukongim e2e"
	)
	require.NoError(t, client.SendFrame(&frame.SendPacket{
		ChannelID:   "e2e-recipient",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     []byte(payload),
	}), node.DumpDiagnostics())

	sendack, err := client.ReadSendAck()
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode, node.DumpDiagnostics())
	require.Equal(t, clientSeq, sendack.ClientSeq)
	require.Equal(t, clientMsgNo, sendack.ClientMsgNo)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)

	senderPage := requireSingleConversationEventually(t, *node, "e2e-sender", "e2e-recipient", func(item suite.ConversationListItem) error {
		if item.ChannelID != "e2e-recipient" || item.ChannelType != int64(frame.ChannelTypePerson) {
			return fmt.Errorf("conversation key = %s/%d, want peer person channel", item.ChannelID, item.ChannelType)
		}
		if item.LastMessage == nil {
			return fmt.Errorf("last_message is nil")
		}
		if item.LastMessage.MessageID != uint64(sendack.MessageID) || item.LastMessage.MessageSeq != sendack.MessageSeq {
			return fmt.Errorf("last message id/seq = %d/%d, want %d/%d", item.LastMessage.MessageID, item.LastMessage.MessageSeq, sendack.MessageID, sendack.MessageSeq)
		}
		if item.LastMessage.FromUID != "e2e-sender" || item.LastMessage.ClientMsgNo != clientMsgNo || string(item.LastMessage.Payload) != payload {
			return fmt.Errorf("last message = %#v, want original committed message", item.LastMessage)
		}
		return nil
	})
	require.True(t, senderPage.Done)

	receiverPage := requireSingleConversationEventually(t, *node, "e2e-recipient", "e2e-sender", func(item suite.ConversationListItem) error {
		if item.ChannelID != "e2e-sender" || item.ChannelType != int64(frame.ChannelTypePerson) {
			return fmt.Errorf("conversation key = %s/%d, want sender peer person channel", item.ChannelID, item.ChannelType)
		}
		if item.LastMessage == nil || item.LastMessage.ClientMsgNo != clientMsgNo || string(item.LastMessage.Payload) != payload {
			return fmt.Errorf("conversation = %#v, want projected last message", item)
		}
		return nil
	})
	require.True(t, receiverPage.Done)
}

func TestWukongIMPersonDirectoryReadyMakesLaterSendMembershipWriteFree(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster()

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	require.NoError(t, client.Connect(node.GatewayAddr(), "directory-ready-alice", "directory-ready-device"), node.DumpDiagnostics())

	first := sendPersonMessage(t, *node, client, "directory-ready-bob", 1, "directory-ready-first")
	requireSingleConversationEventually(t, *node, "directory-ready-alice", "directory-ready-bob", func(item suite.ConversationListItem) error {
		return requireConversationMessage(item, first.MessageSeq, "directory-ready-first")
	})
	requireSingleConversationEventually(t, *node, "directory-ready-bob", "directory-ready-alice", func(item suite.ConversationListItem) error {
		return requireConversationMessage(item, first.MessageSeq, "directory-ready-first")
	})

	before := requireMembershipMutationRows(t, *node, "ordinary")
	require.Positive(t, before, "first persistent person SEND must initialize both directory memberships")

	second := sendPersonMessage(t, *node, client, "directory-ready-bob", 2, "directory-ready-second")
	requireSingleConversationEventually(t, *node, "directory-ready-alice", "directory-ready-bob", func(item suite.ConversationListItem) error {
		return requireConversationMessage(item, second.MessageSeq, "directory-ready-second")
	})
	requireSingleConversationEventually(t, *node, "directory-ready-bob", "directory-ready-alice", func(item suite.ConversationListItem) error {
		return requireConversationMessage(item, second.MessageSeq, "directory-ready-second")
	})

	after := requireMembershipMutationRows(t, *node, "ordinary")
	require.Equal(t, before, after, "SEND after directory_ready changed ordinary membership proposal rows")
}

func sendPersonMessage(t *testing.T, node suite.StartedNode, client *suite.WKProtoClient, channelID string, clientSeq uint64, clientMsgNo string) *frame.SendackPacket {
	t.Helper()
	require.NoError(t, client.SendFrame(&frame.SendPacket{
		ChannelID: channelID, ChannelType: frame.ChannelTypePerson,
		ClientSeq: clientSeq, ClientMsgNo: clientMsgNo, Payload: []byte(clientMsgNo),
	}), node.DumpDiagnostics())
	ack, err := client.ReadSendAck()
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode, node.DumpDiagnostics())
	require.Equal(t, clientSeq, ack.ClientSeq)
	require.Equal(t, clientMsgNo, ack.ClientMsgNo)
	require.NotZero(t, ack.MessageSeq)
	return ack
}

func requireConversationMessage(item suite.ConversationListItem, messageSeq uint64, clientMsgNo string) error {
	if item.LastMessage == nil {
		return fmt.Errorf("last_message is nil")
	}
	if item.LastMessage.MessageSeq != messageSeq || item.LastMessage.ClientMsgNo != clientMsgNo {
		return fmt.Errorf("last_message = %#v, want seq=%d client_msg_no=%s", item.LastMessage, messageSeq, clientMsgNo)
	}
	return nil
}

func requireMembershipMutationRows(t *testing.T, node suite.StartedNode, directory string) float64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
	require.NoError(t, err, node.DumpDiagnostics())
	return suite.SumMetricSamples(samples, "wukongim_conversation_membership_mutation_rows_total", map[string]string{"directory": directory})
}

func requireSingleConversationEventually(t *testing.T, node suite.StartedNode, uid, channelID string, check func(suite.ConversationListItem) error) suite.ConversationListPage {
	t.Helper()

	return suite.RequireConversationEventually(t, node, uid, channelID, func(item suite.ConversationListItem) error {
		page, err := suite.PostConversationList(context.Background(), node.APIAddr(), uid, 10)
		if err != nil {
			return err
		}
		if len(page.Conversations) != 1 {
			return fmt.Errorf("conversation count = %d, want one", len(page.Conversations))
		}
		return check(page.Conversations[0])
	})
}
