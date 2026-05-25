package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestManagerConversationsReturnsRecentList(t *testing.T) {
	var received managementusecase.RecentConversationsRequest
	srv := New(Options{Management: managementStub{
		recentConversationsReqSink: &received,
		recentConversations: managementusecase.RecentConversationsResponse{
			UID: "u1", Limit: 50, MsgCount: 1, OnlyUnread: true, Truncated: true,
			Items: []managementusecase.RecentConversation{{
				UID: "u1", ChannelID: "g1", ChannelType: 2, Unread: 4, Timestamp: 100,
				LastMsgSeq: 12, LastClientMsgNo: "c12", ReadToMsgSeq: 8, Version: 1000,
				RecentMessages: []managementusecase.Message{{MessageID: 99, MessageSeq: 12, ClientMsgNo: "c12", ChannelID: "g1", ChannelType: 2, FromUID: "u2", Timestamp: 100, Payload: []byte("hello")}},
			}},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=%20u1%20&limit=50&msg_count=1&only_unread=true", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.RecentConversationsRequest{UID: "u1", Limit: 50, MsgCount: 1, OnlyUnread: true}, received)
	require.JSONEq(t, `{
        "uid":"u1","limit":50,"msg_count":1,"only_unread":true,"truncated":true,
        "items":[{"uid":"u1","channel_id":"g1","channel_type":2,"unread":4,"timestamp":100,"last_msg_seq":12,"last_client_msg_no":"c12","read_to_msg_seq":8,"version":1000,"recent_messages":[{"message_id":99,"message_seq":12,"client_msg_no":"c12","channel_id":"g1","channel_type":2,"from_uid":"u2","timestamp":100,"payload":"aGVsbG8="}]}]
    }`, rec.Body.String())
}

func TestManagerConversationsRejectsInvalidQuery(t *testing.T) {
	srv := New(Options{Management: managementStub{}})
	for _, path := range []string{
		"/manager/conversations",
		"/manager/conversations?uid=u1&limit=0",
		"/manager/conversations?uid=u1&limit=201",
		"/manager/conversations?uid=u1&msg_count=-1",
		"/manager/conversations?uid=u1&msg_count=11",
		"/manager/conversations?uid=u1&only_unread=maybe",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, path)
	}
}

func TestManagerConversationsMapsUsecaseErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		body string
	}{
		{name: "invalid", err: metadb.ErrInvalidArgument, code: http.StatusBadRequest, body: `{"error":"bad_request","message":"invalid conversation query"}`},
		{name: "unavailable", err: managementusecase.ErrRecentConversationsUnavailable, code: http.StatusServiceUnavailable, body: `{"error":"service_unavailable","message":"recent conversations unavailable"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: managementStub{recentConversationsErr: tt.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=u1", nil)
			srv.Engine().ServeHTTP(rec, req)
			require.Equal(t, tt.code, rec.Code)
			require.JSONEq(t, tt.body, rec.Body.String())
		})
	}
}

func TestManagerConversationsRequiresChannelReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: AuthConfig{
			On: true, JWTSecret: "secret", JWTIssuer: "test",
			Users: []UserConfig{
				{Username: "viewer", Password: "pw", Permissions: []PermissionConfig{{Resource: "cluster.user", Actions: []string{"r"}}}},
				{Username: "channel", Password: "pw", Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"r"}}}},
			},
		},
		Management: managementStub{recentConversations: managementusecase.RecentConversationsResponse{UID: "u1", Limit: 50, MsgCount: 1}},
	})

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=u1", nil)
	deniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(denied, deniedReq)
	require.Equal(t, http.StatusForbidden, denied.Code)

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=u1", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "channel"))
	srv.Engine().ServeHTTP(allowed, allowedReq)
	require.Equal(t, http.StatusOK, allowed.Code)
}
