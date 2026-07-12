package manager

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerMessagesRejectsChannelFilterWithoutChannelID(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/messages?channel_type=2", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !jsonEqual(rec.Body.String(), `{"error":"bad_request","message":"channel_id is required for message filters"}`) {
		t.Fatalf("status/body = %d %s, want missing channel_id", rec.Code, rec.Body.String())
	}
}

func TestManagerMessagesReturnsClusterLatestByDefault(t *testing.T) {
	var gotReq managementusecase.ListMessagesRequest
	nextCursor := managementusecase.MessageListCursor{BeforeMessageID: 101}
	srv := New(Options{Management: managerNodesStub{
		messagesReqSink: &gotReq,
		messagesPage: managementusecase.ListMessagesResponse{
			Items:   []managementusecase.Message{{MessageID: 101, MessageSeq: 10, ChannelID: "room-1", ChannelType: 2}},
			HasMore: true, NextCursor: nextCursor,
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/messages?limit=50", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if gotReq != (managementusecase.ListMessagesRequest{Limit: 50}) {
		t.Fatalf("request = %#v, want unscoped latest", gotReq)
	}
	wantBody := fmt.Sprintf(`{"items":[{"message_id":"101","message_seq":10,"client_msg_no":"","channel_id":"room-1","channel_type":2,"from_uid":"","timestamp":0,"payload":null}],"has_more":true,"next_cursor":%q}`, mustEncodeMessageCursorForTest(t, nextCursor))
	if !jsonEqual(rec.Body.String(), wantBody) {
		t.Fatalf("body = %s, want %s", rec.Body.String(), wantBody)
	}
}

func TestManagerMessagesReturnsPagedList(t *testing.T) {
	var gotReq managementusecase.ListMessagesRequest
	inputCursor := managementusecase.MessageListCursor{BeforeSeq: 11}
	nextCursor := managementusecase.MessageListCursor{BeforeSeq: 9}
	srv := New(Options{
		Management: managerNodesStub{
			messagesReqSink: &gotReq,
			messagesPage: managementusecase.ListMessagesResponse{
				Items: []managementusecase.Message{{
					MessageID: 101, MessageSeq: 10, ClientMsgNo: "c-101",
					ChannelID: "room-1", ChannelType: 2, FromUID: "u1",
					Timestamp: 1713859200, Payload: []byte("hello"),
				}},
				HasMore: true, NextCursor: nextCursor,
			},
		},
	})

	rec := httptest.NewRecorder()
	path := "/manager/messages?channel_id=room-1&channel_type=2&limit=1&message_id=101&client_msg_no=c-101&cursor=" + mustEncodeMessageCursorForTest(t, inputCursor)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	wantReq := managementusecase.ListMessagesRequest{ChannelID: "room-1", ChannelType: 2, Limit: 1, Cursor: inputCursor, MessageID: 101, ClientMsgNo: "c-101"}
	if gotReq != wantReq {
		t.Fatalf("request = %#v, want %#v", gotReq, wantReq)
	}
	wantBody := fmt.Sprintf(`{"items":[{"message_id":"101","message_seq":10,"client_msg_no":"c-101","channel_id":"room-1","channel_type":2,"from_uid":"u1","timestamp":1713859200,"payload":"aGVsbG8="}],"has_more":true,"next_cursor":%q}`, mustEncodeMessageCursorForTest(t, nextCursor))
	if !jsonEqual(rec.Body.String(), wantBody) {
		t.Fatalf("body = %s, want %s", rec.Body.String(), wantBody)
	}
}

func TestManagerMessagesPreservesUint64MessageIDsForJavaScriptClients(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{
		messagesPage: managementusecase.ListMessagesResponse{
			Items: []managementusecase.Message{
				{MessageID: 2076275923258192000, MessageSeq: 2, ChannelID: "room-1", ChannelType: 2},
				{MessageID: 2076275923258192001, MessageSeq: 2, ChannelID: "room-2", ChannelType: 2},
			},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/messages?limit=50", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	wantBody := `{"items":[{"message_id":"2076275923258192000","message_seq":2,"client_msg_no":"","channel_id":"room-1","channel_type":2,"from_uid":"","timestamp":0,"payload":null},{"message_id":"2076275923258192001","message_seq":2,"client_msg_no":"","channel_id":"room-2","channel_type":2,"from_uid":"","timestamp":0,"payload":null}],"has_more":false}`
	if !jsonEqual(rec.Body.String(), wantBody) {
		t.Fatalf("body = %s, want JavaScript-safe decimal message IDs", rec.Body.String())
	}
}

func TestManagerMessageRetentionRequiresWritePermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader", Password: "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"r"}}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/messages/retention", strings.NewReader(`{"channel_id":"room-1","channel_type":2,"through_seq":9}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestManagerMessageRetentionRejectsInvalidRequest(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "writer", Password: "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"w"}}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/messages/retention", strings.NewReader(`{"channel_id":"room-1","channel_type":2}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "writer"))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !jsonEqual(rec.Body.String(), `{"error":"bad_request","message":"invalid message retention request"}`) {
		t.Fatalf("status/body = %d %s, want invalid request", rec.Code, rec.Body.String())
	}
}

func TestManagerMessageRetentionAdvancesBoundary(t *testing.T) {
	var gotReq managementusecase.AdvanceMessageRetentionRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "writer", Password: "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"w"}}},
		}}),
		Management: managerNodesStub{
			retentionReqSink: &gotReq,
			retentionResult: managementusecase.AdvanceMessageRetentionResponse{
				ChannelID: "room-1", ChannelType: 2, RequestedThroughSeq: 10,
				AdvancedThroughSeq: 8, MinAvailableSeq: 9, Status: managementusecase.MessageRetentionStatusAdvanced,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/messages/retention", strings.NewReader(`{"channel_id":"room-1","channel_type":2,"through_seq":10,"dry_run":true}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "writer"))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	wantReq := managementusecase.AdvanceMessageRetentionRequest{ChannelID: "room-1", ChannelType: 2, ThroughSeq: 10, DryRun: true}
	if gotReq != wantReq {
		t.Fatalf("retention request = %#v, want %#v", gotReq, wantReq)
	}
	if !jsonEqual(rec.Body.String(), `{"channel_id":"room-1","channel_type":2,"requested_through_seq":10,"advanced_through_seq":8,"min_available_seq":9,"status":"advanced"}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerMessageRetentionReturnsBlockedStatus(t *testing.T) {
	srv := New(Options{
		Management: managerNodesStub{
			retentionResult: managementusecase.AdvanceMessageRetentionResponse{
				ChannelID: "room-1", ChannelType: 2, RequestedThroughSeq: 10,
				AdvancedThroughSeq: 4, MinAvailableSeq: 5,
				Status:        managementusecase.MessageRetentionStatusBlocked,
				BlockedReason: managementusecase.MessageRetentionBlockedReasonReplayCursor,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/messages/retention", strings.NewReader(`{"channel_id":"room-1","channel_type":2,"through_seq":10}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !jsonEqual(rec.Body.String(), `{"channel_id":"room-1","channel_type":2,"requested_through_seq":10,"advanced_through_seq":4,"min_available_seq":5,"status":"blocked","blocked_reason":"replay_cursor"}`) {
		t.Fatalf("status/body = %d %s, want blocked response", rec.Code, rec.Body.String())
	}
}

func TestManagerMessageRetentionMapsUnavailable(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{retentionErr: managementusecase.ErrMessageRetentionUnavailable}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/messages/retention", strings.NewReader(`{"channel_id":"room-1","channel_type":2,"through_seq":10}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !jsonEqual(rec.Body.String(), `{"error":"service_unavailable","message":"message retention unavailable"}`) {
		t.Fatalf("status/body = %d %s, want retention unavailable", rec.Code, rec.Body.String())
	}
}

func TestDecodeMessageCursorAcceptsLegacyJSONPayload(t *testing.T) {
	cursor := managementusecase.MessageListCursor{BeforeSeq: 11}

	decoded, err := decodeMessageCursor(mustEncodeLegacyMessageCursorForTest(t, cursor))

	if err != nil || decoded != cursor {
		t.Fatalf("decodeMessageCursor() = %#v, %v; want %#v", decoded, err, cursor)
	}
}

func TestMessageCursorCodecRoundTripsGlobalMessageID(t *testing.T) {
	cursor := managementusecase.MessageListCursor{BeforeMessageID: 101}
	raw := mustEncodeMessageCursorForTest(t, cursor)
	decoded, err := decodeMessageCursor(raw)
	if err != nil || decoded != cursor {
		t.Fatalf("decodeMessageCursor(global) = %#v, %v; want %#v", decoded, err, cursor)
	}
}

func mustEncodeMessageCursorForTest(t *testing.T, cursor managementusecase.MessageListCursor) string {
	t.Helper()
	raw, err := encodeMessageCursor(cursor)
	if err != nil {
		t.Fatalf("encodeMessageCursor() error = %v", err)
	}
	return raw
}

func mustEncodeLegacyMessageCursorForTest(t *testing.T, cursor managementusecase.MessageListCursor) string {
	t.Helper()
	payload, err := json.Marshal(messageCursorPayload{Version: 1, BeforeSeq: cursor.BeforeSeq})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}
