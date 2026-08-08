package management

import (
	"context"
	"errors"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestListRecentConversationsMapsMembershipResultAndTruncates(t *testing.T) {
	lister := &fakeRecentConversationLister{
		result: conversationusecase.ListResult{Items: []conversationusecase.Conversation{
			{
				ChannelID: "g1", ChannelType: 2, Unread: 3, ReadSeq: 9, UpdatedAt: 1000,
				LastMessage: &conversationusecase.LastMessage{
					MessageID: 99, MessageSeq: 12, ClientMsgNo: "c12",
					FromUID: "u2", ServerTimestampMS: 100000, Payload: []byte("hello"),
				},
			},
			{ChannelID: "g2", ChannelType: 2, Unread: 1, ReadSeq: 8, UpdatedAt: 900},
		}},
	}
	app := New(Options{Conversations: lister})

	got, err := app.ListRecentConversations(context.Background(), RecentConversationsRequest{
		UID: " u1 ", Limit: 1, MsgCount: 1, OnlyUnread: true,
	})

	if err != nil {
		t.Fatalf("ListRecentConversations() error = %v", err)
	}
	if lister.request.UID != "u1" || lister.request.Limit != 2 {
		t.Fatalf("List request = %#v, want uid u1 limit 2", lister.request)
	}
	if !got.Truncated || got.UID != "u1" || got.Limit != 1 || got.MsgCount != 1 || !got.OnlyUnread {
		t.Fatalf("response header = %#v, want truncated u1 limit/msg_count/only_unread", got)
	}
	wantItems := []RecentConversation{{
		UID: "u1", ChannelID: "g1", ChannelType: 2, Unread: 3, Timestamp: 100,
		LastMsgSeq: 12, LastClientMsgNo: "c12", ReadToMsgSeq: 9, Version: 1000,
		RecentMessages: []Message{{
			MessageID: 99, MessageSeq: 12, ClientMsgNo: "c12",
			ChannelID: "g1", ChannelType: 2, FromUID: "u2",
			Timestamp: 100, Payload: []byte("hello"),
		}},
	}}
	if !sameRecentConversations(got.Items, wantItems) {
		t.Fatalf("items = %#v, want %#v", got.Items, wantItems)
	}
}

func TestListRecentConversationsRejectsInvalidRequest(t *testing.T) {
	app := New(Options{Conversations: &fakeRecentConversationLister{}})
	maxInt := int(^uint(0) >> 1)
	for _, req := range []RecentConversationsRequest{
		{UID: "", Limit: 1, MsgCount: 0},
		{UID: "u1", Limit: 0, MsgCount: 0},
		{UID: "u1", Limit: maxInt, MsgCount: 0},
		{UID: "u1", Limit: 1, MsgCount: -1},
	} {
		_, err := app.ListRecentConversations(context.Background(), req)
		if !errors.Is(err, metadb.ErrInvalidArgument) {
			t.Fatalf("ListRecentConversations(%#v) error = %v, want %v", req, err, metadb.ErrInvalidArgument)
		}
	}
}

func TestListRecentConversationsReturnsUnavailableWhenSyncerMissing(t *testing.T) {
	app := New(Options{})

	_, err := app.ListRecentConversations(context.Background(), RecentConversationsRequest{UID: "u1", Limit: 1})

	if !errors.Is(err, ErrRecentConversationsUnavailable) {
		t.Fatalf("ListRecentConversations() error = %v, want %v", err, ErrRecentConversationsUnavailable)
	}
}

func TestListRecentConversationsPropagatesSyncError(t *testing.T) {
	wantErr := errors.New("sync failed")
	app := New(Options{Conversations: &fakeRecentConversationLister{err: wantErr}})

	_, err := app.ListRecentConversations(context.Background(), RecentConversationsRequest{UID: "u1", Limit: 1})

	if !errors.Is(err, wantErr) {
		t.Fatalf("ListRecentConversations() error = %v, want %v", err, wantErr)
	}
}

type fakeRecentConversationLister struct {
	request conversationusecase.ListRequest
	result  conversationusecase.ListResult
	err     error
}

func (f *fakeRecentConversationLister) List(_ context.Context, query conversationusecase.ListRequest) (conversationusecase.ListResult, error) {
	f.request = query
	return f.result, f.err
}

func sameRecentConversations(left, right []RecentConversation) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !sameRecentConversation(left[i], right[i]) {
			return false
		}
	}
	return true
}

func sameRecentConversation(left, right RecentConversation) bool {
	if left.UID != right.UID || left.ChannelID != right.ChannelID || left.ChannelType != right.ChannelType || left.Unread != right.Unread {
		return false
	}
	if left.Timestamp != right.Timestamp || left.LastMsgSeq != right.LastMsgSeq || left.LastClientMsgNo != right.LastClientMsgNo {
		return false
	}
	if left.ReadToMsgSeq != right.ReadToMsgSeq || left.Version != right.Version {
		return false
	}
	return sameMessages(left.RecentMessages, right.RecentMessages)
}

func sameMessages(left, right []Message) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].MessageID != right[i].MessageID || left[i].MessageSeq != right[i].MessageSeq || left[i].ClientMsgNo != right[i].ClientMsgNo {
			return false
		}
		if left[i].ChannelID != right[i].ChannelID || left[i].ChannelType != right[i].ChannelType || left[i].FromUID != right[i].FromUID || left[i].Timestamp != right[i].Timestamp {
			return false
		}
		if string(left[i].Payload) != string(right[i].Payload) {
			return false
		}
	}
	return true
}
