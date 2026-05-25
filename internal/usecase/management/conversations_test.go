package management

import (
	"context"
	"errors"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestListRecentConversationsMapsSyncResultAndTruncates(t *testing.T) {
	syncer := &fakeRecentConversationSyncer{
		result: conversationusecase.SyncResult{Conversations: []conversationusecase.SyncConversation{
			{
				ChannelID: "g1", ChannelType: 2, Unread: 3, Timestamp: 100,
				LastMsgSeq: 12, LastClientMsgNo: "c12", ReadToMsgSeq: 9, Version: 1000,
				Recents: []channel.Message{{MessageID: 99, MessageSeq: 12, ClientMsgNo: "c12", ChannelID: "g1", ChannelType: 2, FromUID: "u2", Timestamp: 100, Payload: []byte("hello")}},
			},
			{ChannelID: "g2", ChannelType: 2, Unread: 0, Timestamp: 90, LastMsgSeq: 8, ReadToMsgSeq: 8, Version: 900},
		}},
	}
	app := New(Options{Conversations: syncer})

	got, err := app.ListRecentConversations(context.Background(), RecentConversationsRequest{
		UID: " u1 ", Limit: 1, MsgCount: 1, OnlyUnread: true,
	})

	require.NoError(t, err)
	require.Equal(t, conversationusecase.SyncQuery{UID: "u1", Limit: 2, MsgCount: 1, OnlyUnread: true}, syncer.query)
	require.True(t, got.Truncated)
	require.Equal(t, "u1", got.UID)
	require.Equal(t, 1, got.Limit)
	require.Equal(t, 1, got.MsgCount)
	require.True(t, got.OnlyUnread)
	require.Equal(t, []RecentConversation{{
		UID: "u1", ChannelID: "g1", ChannelType: 2, Unread: 3, Timestamp: 100,
		LastMsgSeq: 12, LastClientMsgNo: "c12", ReadToMsgSeq: 9, Version: 1000,
		RecentMessages: []Message{{MessageID: 99, MessageSeq: 12, ClientMsgNo: "c12", ChannelID: "g1", ChannelType: 2, FromUID: "u2", Timestamp: 100, Payload: []byte("hello")}},
	}}, got.Items)
}

func TestListRecentConversationsRejectsInvalidRequest(t *testing.T) {
	app := New(Options{Conversations: &fakeRecentConversationSyncer{}})
	maxInt := int(^uint(0) >> 1)
	for _, req := range []RecentConversationsRequest{
		{UID: "", Limit: 1, MsgCount: 0},
		{UID: "u1", Limit: 0, MsgCount: 0},
		{UID: "u1", Limit: maxInt, MsgCount: 0},
		{UID: "u1", Limit: 1, MsgCount: -1},
	} {
		_, err := app.ListRecentConversations(context.Background(), req)
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	}
}

func TestListRecentConversationsReturnsUnavailableWhenSyncerMissing(t *testing.T) {
	app := New(Options{})

	_, err := app.ListRecentConversations(context.Background(), RecentConversationsRequest{UID: "u1", Limit: 1})

	require.ErrorIs(t, err, ErrRecentConversationsUnavailable)
}

func TestListRecentConversationsPropagatesSyncError(t *testing.T) {
	want := errors.New("sync failed")
	app := New(Options{Conversations: &fakeRecentConversationSyncer{err: want}})

	_, err := app.ListRecentConversations(context.Background(), RecentConversationsRequest{UID: "u1", Limit: 1})

	require.ErrorIs(t, err, want)
}

type fakeRecentConversationSyncer struct {
	query  conversationusecase.SyncQuery
	result conversationusecase.SyncResult
	err    error
}

func (f *fakeRecentConversationSyncer) Sync(_ context.Context, query conversationusecase.SyncQuery) (conversationusecase.SyncResult, error) {
	f.query = query
	return f.result, f.err
}
