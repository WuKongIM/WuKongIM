package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerConversationsReturnsRecentList(t *testing.T) {
	var gotReq managementusecase.RecentConversationsRequest
	srv := New(Options{
		Management: managerNodesStub{
			recentConversationsReqSink: &gotReq,
			recentConversations: managementusecase.RecentConversationsResponse{
				UID: "u1", Limit: 50, MsgCount: 1, OnlyUnread: true, Truncated: true,
				Items: []managementusecase.RecentConversation{{
					UID: "u1", ChannelID: "g1", ChannelType: 2, Unread: 4, Timestamp: 100,
					LastMsgSeq: 12, LastClientMsgNo: "c12", ReadToMsgSeq: 8, Version: 1000,
					RecentMessages: []managementusecase.Message{{
						MessageID: 99, MessageSeq: 12, ClientMsgNo: "c12",
						ChannelID: "g1", ChannelType: 2, FromUID: "u2",
						Timestamp: 100, Payload: []byte("hello"),
					}},
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=%20u1%20&limit=50&msg_count=1&only_unread=true", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotReq != (managementusecase.RecentConversationsRequest{UID: "u1", Limit: 50, MsgCount: 1, OnlyUnread: true}) {
		t.Fatalf("request = %#v, want normalized recent conversation query", gotReq)
	}
	if !jsonEqual(rec.Body.String(), `{
		"uid":"u1","limit":50,"msg_count":1,"only_unread":true,"truncated":true,
		"items":[{"uid":"u1","channel_id":"g1","channel_type":2,"unread":4,"timestamp":100,"last_msg_seq":12,"last_client_msg_no":"c12","read_to_msg_seq":8,"version":1000,"recent_messages":[{"message_id":99,"message_seq":12,"client_msg_no":"c12","channel_id":"g1","channel_type":2,"from_uid":"u2","timestamp":100,"payload":"aGVsbG8="}]}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerConversationsRejectsInvalidQuery(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{}})
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
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want %d; body=%s", path, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
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
			srv := New(Options{Management: managerNodesStub{recentConversationsErr: tt.err}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=u1", nil)
			srv.Engine().ServeHTTP(rec, req)
			if rec.Code != tt.code {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.code, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.body) {
				t.Fatalf("body = %s, want %s", rec.Body.String(), tt.body)
			}
		})
	}
}

func TestManagerConversationsRequiresChannelReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{
				Username: "viewer",
				Password: "pw",
				Permissions: []PermissionConfig{{
					Resource: "cluster.user",
					Actions:  []string{"r"},
				}},
			},
			{
				Username: "channel",
				Password: "pw",
				Permissions: []PermissionConfig{{
					Resource: "cluster.channel",
					Actions:  []string{"r"},
				}},
			},
		}),
		Management: managerNodesStub{
			recentConversations: managementusecase.RecentConversationsResponse{UID: "u1", Limit: 50, MsgCount: 1},
		},
	})

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=u1", nil)
	deniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d", denied.Code, http.StatusForbidden)
	}

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodGet, "/manager/conversations?uid=u1", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "channel"))
	srv.Engine().ServeHTTP(allowed, allowedReq)
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed status = %d, want %d; body=%s", allowed.Code, http.StatusOK, allowed.Body.String())
	}
}
