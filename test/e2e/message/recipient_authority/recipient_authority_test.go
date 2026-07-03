//go:build e2e

package recipient_authority

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestWukongIMGroupRecipientAuthorityUpdatesSubscribers(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const smallChannelID = "e2e-recipient-authority-small-group"
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id":   smallChannelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  []string{"conv-small-sender", "conv-small-a", "conv-small-b"},
	}))
	smallSend, err := suite.PostMessageSend(ctx, node.APIAddr(), map[string]any{
		"from_uid":      "conv-small-sender",
		"channel_id":    smallChannelID,
		"channel_type":  frame.ChannelTypeGroup,
		"client_msg_no": "conv-small-1",
		"payload":       base64.StdEncoding.EncodeToString([]byte("small group recipient authority")),
	})
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSuccess), smallSend.Reason)
	require.NotZero(t, smallSend.MessageID)
	require.NotZero(t, smallSend.MessageSeq)

	for _, uid := range []string{"conv-small-a", "conv-small-b"} {
		page := suite.RequireConversationEventually(t, *node, uid, smallChannelID, func(item suite.ConversationListItem) error {
			return checkRecipientConversation(item, smallSend, "conv-small-sender", "conv-small-1", "small group recipient authority")
		})
		require.Equal(t, 0, page.More)
	}
	page := suite.RequireConversationEventually(t, *node, "conv-small-sender", smallChannelID, func(item suite.ConversationListItem) error {
		return checkRecipientConversation(item, smallSend, "conv-small-sender", "conv-small-1", "small group recipient authority")
	})
	require.Equal(t, 0, page.More)
	suite.RequireConversationAbsent(t, ctx, *node, "conv-small-outsider", smallChannelID)

	const largeChannelID = "e2e-recipient-authority-large-group"
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id":   largeChannelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  []string{"conv-large-sender", "conv-large-a", "conv-large-b", "conv-large-c"},
	}))
	largeSend, err := suite.PostMessageSend(ctx, node.APIAddr(), map[string]any{
		"from_uid":      "conv-large-sender",
		"channel_id":    largeChannelID,
		"channel_type":  frame.ChannelTypeGroup,
		"client_msg_no": "conv-large-1",
		"payload":       base64.StdEncoding.EncodeToString([]byte("large group recipient authority")),
	})
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSuccess), largeSend.Reason)
	require.NotZero(t, largeSend.MessageID)
	require.NotZero(t, largeSend.MessageSeq)

	for _, uid := range []string{"conv-large-a", "conv-large-b", "conv-large-c"} {
		page := suite.RequireConversationEventually(t, *node, uid, largeChannelID, func(item suite.ConversationListItem) error {
			return checkRecipientConversation(item, largeSend, "conv-large-sender", "conv-large-1", "large group recipient authority")
		})
		require.Equal(t, 0, page.More)
	}
	page = suite.RequireConversationEventually(t, *node, "conv-large-sender", largeChannelID, func(item suite.ConversationListItem) error {
		return checkRecipientConversation(item, largeSend, "conv-large-sender", "conv-large-1", "large group recipient authority")
	})
	require.Equal(t, 0, page.More)
	suite.RequireConversationAbsent(t, ctx, *node, "conv-large-outsider", largeChannelID)

	suite.RequireMetricAtLeastEventually(t, *node, `wukongim_conversation_authority_admit_total`, map[string]string{"result": "ok"}, 1)
	suite.RequireMetricAtLeastEventually(t, *node, `wukongim_conversation_authority_list_total`, map[string]string{"result": "ok"}, 1)
}

func TestWukongIMHundredKGroupRecipientAuthorityUpdatesSubscribers(t *testing.T) {
	if os.Getenv("WK_E2E_100K_CONVERSATION") != "1" {
		t.Skip("set WK_E2E_100K_CONVERSATION=1 to run the 100k recipient-authority stress test")
	}

	node := suite.New(t).StartSingleNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	const channelID = "e2e-recipient-authority-100k-group"
	subscribers := hundredKConversationSubscribers()
	subscribers = append(subscribers, "conv-100k-sender")
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"subscribers":  subscribers,
	}))
	sendResp, err := suite.PostMessageSend(ctx, node.APIAddr(), map[string]any{
		"from_uid":      "conv-100k-sender",
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"client_msg_no": "conv-100k-1",
		"payload":       base64.StdEncoding.EncodeToString([]byte("100k recipient authority")),
	})
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSuccess), sendResp.Reason)

	for _, uid := range []string{hundredKConversationSubscriberUID(0), hundredKConversationSubscriberUID(50000), hundredKConversationSubscriberUID(99999)} {
		suite.RequireConversationEventuallyWithin(t, *node, uid, channelID, 5*time.Minute, func(item suite.ConversationListItem) error {
			return checkRecipientConversation(item, sendResp, "conv-100k-sender", "conv-100k-1", "100k recipient authority")
		})
	}
	suite.RequireConversationEventuallyWithin(t, *node, "conv-100k-sender", channelID, 5*time.Minute, func(item suite.ConversationListItem) error {
		return checkRecipientConversation(item, sendResp, "conv-100k-sender", "conv-100k-1", "100k recipient authority")
	})
	suite.RequireConversationAbsent(t, ctx, *node, "conv-100k-outsider", channelID)

	suite.RequireMetricAtLeastEventually(t, *node, `wukongim_conversation_authority_admit_total`, map[string]string{"result": "ok"}, 1)
	suite.RequireMetricAtLeastEventually(t, *node, `wukongim_conversation_authority_list_total`, map[string]string{"result": "ok"}, 1)
}

func checkRecipientConversation(item suite.ConversationListItem, send suite.MessageSendResponse, fromUID, clientMsgNo, payload string) error {
	if item.SparseActive {
		return fmt.Errorf("sparse_active = true, want recipient-scoped row")
	}
	if item.LastMessage == nil {
		return fmt.Errorf("last_message is nil")
	}
	if item.LastMessage.MessageID != uint64(send.MessageID) ||
		item.LastMessage.MessageSeq != send.MessageSeq ||
		item.LastMessage.FromUID != fromUID ||
		item.LastMessage.ClientMsgNo != clientMsgNo ||
		string(item.LastMessage.Payload) != payload {
		return fmt.Errorf("last_message = %#v, want recipient-authority message", item.LastMessage)
	}
	return nil
}

func hundredKConversationSubscribers() []string {
	subscribers := make([]string, 100000)
	for i := range subscribers {
		subscribers[i] = hundredKConversationSubscriberUID(i)
	}
	return subscribers
}

func hundredKConversationSubscriberUID(index int) string {
	return fmt.Sprintf("conv-100k-member-%06d", index)
}
