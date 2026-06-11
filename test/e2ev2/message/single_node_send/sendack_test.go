//go:build e2e

package single_node_send

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestWukongIMV2SingleNodeClusterSendProjectsConversationList(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster()

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	require.NoError(t, client.Connect(node.GatewayAddr(), "v2-sender", "v2-sender-device"), node.DumpDiagnostics())

	const (
		clientSeq   uint64 = 1
		clientMsgNo        = "wukongimv2-sendack-e2e-1"
		payload            = "hello from wukongimv2 e2e"
	)
	require.NoError(t, client.SendFrame(&frame.SendPacket{
		ChannelID:   "v2-recipient",
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

	senderPage := requireSingleConversationEventually(t, *node, "v2-sender", "v2-recipient", func(item suite.ConversationListItem) error {
		if item.ChannelID != "v2-recipient" || item.ChannelType != int64(frame.ChannelTypePerson) {
			return fmt.Errorf("conversation key = %s/%d, want peer person channel", item.ChannelID, item.ChannelType)
		}
		if item.SparseActive {
			return fmt.Errorf("sparse_active = true, want dense person conversation")
		}
		if item.LastMessage == nil {
			return fmt.Errorf("last_message is nil")
		}
		if item.LastMessage.MessageID != uint64(sendack.MessageID) || item.LastMessage.MessageSeq != sendack.MessageSeq {
			return fmt.Errorf("last message id/seq = %d/%d, want %d/%d", item.LastMessage.MessageID, item.LastMessage.MessageSeq, sendack.MessageID, sendack.MessageSeq)
		}
		if item.LastMessage.FromUID != "v2-sender" || item.LastMessage.ClientMsgNo != clientMsgNo || string(item.LastMessage.Payload) != payload {
			return fmt.Errorf("last message = %#v, want original committed message", item.LastMessage)
		}
		return nil
	})
	require.Equal(t, 0, senderPage.More)

	receiverPage := requireSingleConversationEventually(t, *node, "v2-recipient", "v2-sender", func(item suite.ConversationListItem) error {
		if item.ChannelID != "v2-sender" || item.ChannelType != int64(frame.ChannelTypePerson) {
			return fmt.Errorf("conversation key = %s/%d, want sender peer person channel", item.ChannelID, item.ChannelType)
		}
		if item.LastMessage == nil || item.LastMessage.ClientMsgNo != clientMsgNo || string(item.LastMessage.Payload) != payload {
			return fmt.Errorf("conversation = %#v, want projected last message", item)
		}
		return nil
	})
	require.Equal(t, 0, receiverPage.More)
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
