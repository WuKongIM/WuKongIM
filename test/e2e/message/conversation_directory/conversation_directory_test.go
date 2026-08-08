//go:build e2e

package conversation_directory

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestWukongIMConversationDirectoryPaginatesByActivationWithoutMessageTimeReorder(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const (
		uid       = "directory-page-user"
		senderUID = "directory-page-sender"
	)
	channels := []string{
		"directory-page-a",
		"directory-page-b",
		"directory-page-c",
	}
	for index, channelID := range channels {
		createGroup(t, ctx, *node, channelID, uid, senderUID)
		sendGroupMessage(t, ctx, *node, senderUID, channelID, fmt.Sprintf("directory-page-initial-%d", index+1))
	}

	postConversationMutation(t, ctx, *node, "/conversations/activate", map[string]any{
		"uid": uid, "channel_id": channels[2], "channel_type": frame.ChannelTypeGroup,
	})

	first := requireFirstConversationPageEventually(t, *node, suite.ConversationListRequest{
		UID: uid, Limit: 1,
	}, channels[2])
	require.False(t, first.Done)
	require.NotEmpty(t, first.NextCursor)
	require.Positive(t, first.Conversations[0].ActiveAt)
	activatedAt := first.Conversations[0].ActiveAt

	// A later message in another channel must not mutate its activation priority.
	sendGroupMessage(t, ctx, *node, senderUID, channels[0], "directory-page-newer-message")
	first = requireFirstConversationPageEventually(t, *node, suite.ConversationListRequest{
		UID: uid, Limit: 1,
	}, channels[2])
	require.Equal(t, activatedAt, first.Conversations[0].ActiveAt)

	seen := make(map[string]struct{}, len(channels))
	page := first
	for {
		require.Len(t, page.Conversations, 1, node.DumpDiagnostics())
		channelID := page.Conversations[0].ChannelID
		_, duplicate := seen[channelID]
		require.False(t, duplicate, "duplicate channel %s while paging", channelID)
		seen[channelID] = struct{}{}
		if page.Done {
			break
		}
		require.NotEmpty(t, page.NextCursor)
		previousCursor := page.NextCursor
		var err error
		page, err = suite.PostConversationListPage(ctx, node.APIAddr(), suite.ConversationListRequest{
			UID: uid, Limit: 1, Cursor: previousCursor,
		})
		require.NoError(t, err, node.DumpDiagnostics())
		if !page.Done {
			require.NotEqual(t, previousCursor, page.NextCursor, "directory cursor did not advance")
		}
	}
	require.Equal(t, map[string]struct{}{
		channels[0]: {}, channels[1]: {}, channels[2]: {},
	}, seen)
}

func TestWukongIMConversationDirectoryMaintainsMonotonicBadgeBaseline(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const (
		channelID = "directory-badge-group"
		uid       = "directory-badge-user"
		peerUID   = "directory-badge-peer"
	)
	createGroup(t, ctx, *node, channelID, uid, peerUID)

	first := sendGroupMessage(t, ctx, *node, peerUID, channelID, "directory-badge-1")
	require.Equal(t, uint64(1), first.MessageSeq)
	requireConversationStateEventually(t, *node, uid, channelID, func(item suite.ConversationListItem) error {
		return requireConversationState(item, 0, 1, 1, "directory-badge-1")
	})

	second := sendGroupMessage(t, ctx, *node, uid, channelID, "directory-badge-own-2")
	require.Equal(t, uint64(2), second.MessageSeq)
	requireConversationStateEventually(t, *node, uid, channelID, func(item suite.ConversationListItem) error {
		return requireConversationState(item, 0, 0, 2, "directory-badge-own-2")
	})

	for seq := uint64(3); seq <= 5; seq++ {
		resp := sendGroupMessage(t, ctx, *node, peerUID, channelID, fmt.Sprintf("directory-badge-%d", seq))
		require.Equal(t, seq, resp.MessageSeq)
	}
	requireConversationStateEventually(t, *node, uid, channelID, func(item suite.ConversationListItem) error {
		return requireConversationState(item, 0, 3, 5, "directory-badge-5")
	})

	postConversationMutation(t, ctx, *node, "/conversations/setUnread", map[string]any{
		"uid": uid, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup, "unread": 2,
	})
	requireConversationStateEventually(t, *node, uid, channelID, func(item suite.ConversationListItem) error {
		return requireConversationState(item, 3, 2, 5, "directory-badge-5")
	})

	// Asking for a larger unread cap cannot move the stored badge floor backward.
	postConversationMutation(t, ctx, *node, "/conversations/setUnread", map[string]any{
		"uid": uid, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup, "unread": 10,
	})
	requireConversationStateEventually(t, *node, uid, channelID, func(item suite.ConversationListItem) error {
		return requireConversationState(item, 3, 2, 5, "directory-badge-5")
	})

	postConversationMutation(t, ctx, *node, "/conversations/clearUnread", map[string]any{
		"uid": uid, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
	})
	requireConversationStateEventually(t, *node, uid, channelID, func(item suite.ConversationListItem) error {
		return requireConversationState(item, 5, 0, 5, "directory-badge-5")
	})

	sixth := sendGroupMessage(t, ctx, *node, peerUID, channelID, "directory-badge-6")
	require.Equal(t, uint64(6), sixth.MessageSeq)
	requireConversationStateEventually(t, *node, uid, channelID, func(item suite.ConversationListItem) error {
		return requireConversationState(item, 5, 1, 6, "directory-badge-6")
	})
}

func TestWukongIMConversationDirectoryDistinguishesHideRemoveAndRejoin(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const (
		channelID = "directory-lifecycle-group"
		uid       = "directory-lifecycle-user"
		peerUID   = "directory-lifecycle-peer"
	)
	createGroup(t, ctx, *node, channelID, uid, peerUID)

	first := sendGroupMessage(t, ctx, *node, peerUID, channelID, "directory-lifecycle-1")
	require.Equal(t, uint64(1), first.MessageSeq)
	requireConversationStateEventually(t, *node, uid, channelID, func(item suite.ConversationListItem) error {
		return requireConversationState(item, 0, 1, 1, "directory-lifecycle-1")
	})

	postConversationMutation(t, ctx, *node, "/conversations/delete", map[string]any{
		"uid": uid, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
	})
	requireConversationHiddenEventually(t, *node, uid, channelID)

	second := sendGroupMessage(t, ctx, *node, peerUID, channelID, "directory-lifecycle-2")
	require.Equal(t, uint64(2), second.MessageSeq)
	item := requireConversationStateEventually(t, *node, uid, channelID, func(item suite.ConversationListItem) error {
		if item.DeletedToSeq != 1 {
			return fmt.Errorf("deleted_to_seq = %d, want 1 after hide", item.DeletedToSeq)
		}
		return requireConversationState(item, 0, 1, 2, "directory-lifecycle-2")
	})
	require.Zero(t, item.ActiveAt, "hide must reset activation priority")

	postConversationMutation(t, ctx, *node, "/channel/subscriber_remove", map[string]any{
		"channel_id": channelID, "channel_type": frame.ChannelTypeGroup, "subscribers": []string{uid},
	})
	requireConversationDeleteEventually(t, *node, uid, channelID)

	postConversationMutation(t, ctx, *node, "/channel/subscriber_add", map[string]any{
		"channel_id": channelID, "channel_type": frame.ChannelTypeGroup, "subscribers": []string{uid},
	})
	requireConversationHiddenEventually(t, *node, uid, channelID)

	third := sendGroupMessage(t, ctx, *node, peerUID, channelID, "directory-lifecycle-3")
	require.Equal(t, uint64(3), third.MessageSeq)
	requireConversationStateEventually(t, *node, uid, channelID, func(item suite.ConversationListItem) error {
		if item.DeletedToSeq != 2 {
			return fmt.Errorf("deleted_to_seq = %d, want rejoin floor 2", item.DeletedToSeq)
		}
		return requireConversationState(item, 2, 1, 3, "directory-lifecycle-3")
	})
}

func createGroup(t *testing.T, ctx context.Context, node suite.StartedNode, channelID string, subscribers ...string) {
	t.Helper()
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
		"reset": 1, "subscribers": subscribers,
	}), node.DumpDiagnostics())
}

func sendGroupMessage(t *testing.T, ctx context.Context, node suite.StartedNode, fromUID, channelID, clientMsgNo string) suite.MessageSendResponse {
	t.Helper()
	resp, err := suite.PostMessageSendEventually(ctx, node.APIAddr(), map[string]any{
		"from_uid": fromUID, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
		"client_msg_no": clientMsgNo,
		"payload":       base64.StdEncoding.EncodeToString([]byte(clientMsgNo)),
	})
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), resp.Reason, node.DumpDiagnostics())
	require.NotZero(t, resp.MessageSeq)
	return resp
}

func postConversationMutation(t *testing.T, ctx context.Context, node suite.StartedNode, path string, body map[string]any) {
	t.Helper()
	_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+path, body, nil)
	require.NoError(t, err, node.DumpDiagnostics())
}

func requireFirstConversationPageEventually(t *testing.T, node suite.StartedNode, req suite.ConversationListRequest, channelID string) suite.ConversationListPage {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastPage suite.ConversationListPage
	var lastErr error
	for {
		page, err := suite.PostConversationListPage(ctx, node.APIAddr(), req)
		if err == nil && len(page.Conversations) == 1 && page.Conversations[0].ChannelID == channelID {
			return page
		}
		lastPage = page
		lastErr = err
		select {
		case <-ctx.Done():
			t.Fatalf("first conversation page did not contain %s: lastPage=%#v lastErr=%v\n%s", channelID, lastPage, lastErr, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireConversationStateEventually(t *testing.T, node suite.StartedNode, uid, channelID string, check func(suite.ConversationListItem) error) suite.ConversationListItem {
	t.Helper()

	var matched suite.ConversationListItem
	suite.RequireConversationEventuallyWithin(t, node, uid, channelID, 10*time.Second, func(item suite.ConversationListItem) error {
		if err := check(item); err != nil {
			return err
		}
		matched = item
		return nil
	})
	return matched
}

func requireConversationState(item suite.ConversationListItem, readSeq, unread, messageSeq uint64, clientMsgNo string) error {
	if item.ReadSeq != readSeq || item.Unread != unread {
		return fmt.Errorf("read_seq/unread = %d/%d, want %d/%d", item.ReadSeq, item.Unread, readSeq, unread)
	}
	if item.LastMessage == nil {
		return fmt.Errorf("last_message is nil")
	}
	if item.LastMessage.MessageSeq != messageSeq || item.LastMessage.ClientMsgNo != clientMsgNo {
		return fmt.Errorf("last_message = %#v, want seq=%d client_msg_no=%s", item.LastMessage, messageSeq, clientMsgNo)
	}
	return nil
}

func requireConversationHiddenEventually(t *testing.T, node suite.StartedNode, uid, channelID string) {
	t.Helper()
	requireConversationPageEventually(t, node, uid, func(page suite.ConversationListPage) error {
		if _, ok := suite.FindConversation(page, channelID); ok {
			return fmt.Errorf("conversation %s is still visible", channelID)
		}
		if _, ok := suite.FindConversationKey(page.Deletes, channelID, int64(frame.ChannelTypeGroup)); ok {
			return fmt.Errorf("hidden conversation %s was reported as a membership delete", channelID)
		}
		if !page.Done {
			return fmt.Errorf("directory pass is not complete")
		}
		return nil
	})
}

func requireConversationDeleteEventually(t *testing.T, node suite.StartedNode, uid, channelID string) {
	t.Helper()
	requireConversationPageEventually(t, node, uid, func(page suite.ConversationListPage) error {
		if _, ok := suite.FindConversation(page, channelID); ok {
			return fmt.Errorf("removed conversation %s is still visible", channelID)
		}
		if _, ok := suite.FindConversationKey(page.Deletes, channelID, int64(frame.ChannelTypeGroup)); !ok {
			return fmt.Errorf("membership delete %s is missing from %#v", channelID, page.Deletes)
		}
		return nil
	})
}

func requireConversationPageEventually(t *testing.T, node suite.StartedNode, uid string, check func(suite.ConversationListPage) error) suite.ConversationListPage {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastPage suite.ConversationListPage
	var lastErr error
	for {
		page, err := suite.PostConversationList(ctx, node.APIAddr(), uid, 10)
		if err == nil {
			lastPage = page
			if checkErr := check(page); checkErr == nil {
				return page
			} else {
				lastErr = checkErr
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("conversation page for uid %s did not converge: lastPage=%#v lastErr=%v\n%s", uid, lastPage, lastErr, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}
