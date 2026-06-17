package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestConversationListMapsRequestToUsecaseAndReturnsPage(t *testing.T) {
	conversations := &recordingConversationUsecase{
		result: conversationusecase.ListResult{
			Items: []conversationusecase.Conversation{{
				ChannelID:    "g1",
				ChannelType:  int64(frame.ChannelTypeGroup),
				ActiveAt:     1234,
				ReadSeq:      3,
				DeletedToSeq: 2,
				SparseActive: true,
				UpdatedAt:    1235,
				Unread:       4,
				LastMessage: &conversationusecase.LastMessage{
					MessageID:         99,
					MessageSeq:        7,
					FromUID:           "u2",
					ClientMsgNo:       "c1",
					ServerTimestampMS: 1236,
					Payload:           []byte("hello"),
				},
			}},
			NextCursor: conversationusecase.Cursor{
				ActiveAt:    1234,
				ChannelID:   "g1",
				ChannelType: int64(frame.ChannelTypeGroup),
			},
			HasMore: true,
		},
	}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/list", bytes.NewBufferString(`{
		"uid":"u1",
		"limit":20,
		"cursor":{"active_at":2000,"channel_id":"g0","channel_type":2}
	}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{
		"conversations":[{
			"channel_id":"g1",
			"channel_type":2,
			"active_at":1234,
			"read_seq":3,
			"deleted_to_seq":2,
			"sparse_active":true,
			"unread":4,
			"last_message":{
				"message_id":99,
				"message_idstr":"99",
				"message_seq":7,
				"from_uid":"u2",
				"client_msg_no":"c1",
				"server_timestamp_ms":1236,
				"payload":"aGVsbG8="
			}
		}],
		"next_cursor":{"active_at":1234,"channel_id":"g1","channel_type":2},
		"more":1
	}`) {
		t.Fatalf("body = %q, want conversation list page", rec.Body.String())
	}
	assertJSONFieldAbsent(t, rec.Body.Bytes(), "truncated")
	assertJSONFieldAbsent(t, rec.Body.Bytes(), "scanned_memberships")
	if len(conversations.requests) != 1 {
		t.Fatalf("conversation list requests = %#v, want one", conversations.requests)
	}
	got := conversations.requests[0]
	if got.UID != "u1" || got.Limit != 20 ||
		got.Cursor.ActiveAt != 2000 ||
		got.Cursor.ChannelID != "g0" || got.Cursor.ChannelType != int64(frame.ChannelTypeGroup) {
		t.Fatalf("list request = %#v, want mapped cursor request", got)
	}
}

func TestConversationListOmitsMissingLastMessage(t *testing.T) {
	conversations := &recordingConversationUsecase{
		result: conversationusecase.ListResult{
			Items: []conversationusecase.Conversation{{
				ChannelID:    "g-empty",
				ChannelType:  int64(frame.ChannelTypeGroup),
				ActiveAt:     3000,
				ReadSeq:      5,
				DeletedToSeq: 5,
				UpdatedAt:    3001,
			}},
		},
	}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/list", bytes.NewBufferString(`{"uid":"u1","limit":10}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{
		"conversations":[{
			"channel_id":"g-empty",
			"channel_type":2,
			"active_at":3000,
			"read_seq":5,
			"deleted_to_seq":5,
			"sparse_active":false,
			"unread":0,
			"last_message":null
		}],
		"more":0
	}`) {
		t.Fatalf("body = %q, want row without last_message", rec.Body.String())
	}
	var decoded struct {
		Conversations []map[string]any `json:"conversations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.Conversations) != 1 {
		t.Fatalf("conversations = %#v, want one", decoded.Conversations)
	}
	if value, ok := decoded.Conversations[0]["last_message"]; !ok || value != nil {
		t.Fatalf("last_message = %#v present=%t in %s, want null", value, ok, rec.Body.String())
	}
}

func TestConversationListReturnsPeerIDForPersonChannel(t *testing.T) {
	conversations := &recordingConversationUsecase{
		result: conversationusecase.ListResult{
			Items: []conversationusecase.Conversation{{
				ChannelID:   "u1@u2",
				ChannelType: int64(frame.ChannelTypePerson),
				ActiveAt:    2000,
				LastMessage: &conversationusecase.LastMessage{
					MessageID:  100,
					MessageSeq: 8,
				},
			}},
		},
	}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/list", bytes.NewBufferString(`{"uid":"u1","limit":10}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{
		"conversations":[{
			"channel_id":"u2",
			"channel_type":1,
			"active_at":2000,
			"read_seq":0,
			"deleted_to_seq":0,
			"sparse_active":false,
			"unread":0,
			"last_message":{
				"message_id":100,
				"message_idstr":"100",
				"message_seq":8,
				"from_uid":"",
				"client_msg_no":"",
				"server_timestamp_ms":0,
				"payload":null
			}
		}],
		"more":0
	}`) {
		t.Fatalf("body = %q, want person peer channel id", rec.Body.String())
	}
}

func TestConversationListReturnsCompatibleErrors(t *testing.T) {
	for _, tt := range []struct {
		name          string
		conversations ConversationUsecase
		body          string
		want          string
	}{
		{name: "invalid json", conversations: &recordingConversationUsecase{}, body: `{"uid":`, want: `{"msg":"数据格式有误！","status":400}`},
		{name: "missing uid", conversations: &recordingConversationUsecase{}, body: `{"limit":10}`, want: `{"msg":"uid不能为空！","status":400}`},
		{name: "missing usecase", body: `{"uid":"u1"}`, want: `{"msg":"conversation usecase not configured","status":400}`},
		{name: "usecase error", conversations: &recordingConversationUsecase{err: errors.New("conversation list failed")}, body: `{"uid":"u1"}`, want: `{"msg":"conversation list failed","status":400}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Conversations: tt.conversations})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/conversation/list", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.want) {
				t.Fatalf("body = %q, want JSON %s", rec.Body.String(), tt.want)
			}
		})
	}
}

func TestConversationListObserverRecordsPageShapeAndLatency(t *testing.T) {
	conversations := &recordingConversationUsecase{
		result: conversationusecase.ListResult{
			Items: []conversationusecase.Conversation{
				{ChannelID: "g1", ChannelType: int64(frame.ChannelTypeGroup), SparseActive: true, LastMessage: &conversationusecase.LastMessage{MessageID: 1}},
				{ChannelID: "g2", ChannelType: int64(frame.ChannelTypeGroup)},
			},
			HasMore: true,
		},
	}
	observer := &recordingConversationListObserver{}
	srv := New(Options{Conversations: conversations, ConversationListObserver: observer})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/list", bytes.NewBufferString(`{"uid":"u1","limit":10}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if len(observer.events) != 1 {
		t.Fatalf("observer events = %#v, want one", observer.events)
	}
	got := observer.events[0]
	if got.Result != "ok" || got.ReturnedItems != 2 || got.SparseItems != 1 || got.LastMessageLoads != 2 ||
		got.LastMessageErrors != 0 || got.ActiveIndexStaleSkips != 0 || !got.More {
		t.Fatalf("observer event = %#v, want page shape", got)
	}
	if got.Duration <= 0 {
		t.Fatalf("observer duration = %v, want positive latency", got.Duration)
	}
}

func assertJSONFieldAbsent(t *testing.T, body []byte, field string) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := decoded[field]; ok {
		t.Fatalf("%s present in %s, want absent", field, string(body))
	}
}

type recordingConversationUsecase struct {
	requests              []conversationusecase.ListRequest
	result                conversationusecase.ListResult
	err                   error
	syncQueries           []conversationusecase.SyncQuery
	syncResult            conversationusecase.SyncResult
	syncErr               error
	clearUnreadCommands   []conversationusecase.ClearUnreadCommand
	setUnreadCommands     []conversationusecase.SetUnreadCommand
	deleteCommands        []conversationusecase.DeleteConversationCommand
	clearUnreadErr        error
	setUnreadErr          error
	deleteConversationErr error
}

func (r *recordingConversationUsecase) List(_ context.Context, req conversationusecase.ListRequest) (conversationusecase.ListResult, error) {
	r.requests = append(r.requests, req)
	return r.result, r.err
}

func (r *recordingConversationUsecase) Sync(_ context.Context, req conversationusecase.SyncQuery) (conversationusecase.SyncResult, error) {
	r.syncQueries = append(r.syncQueries, req)
	return r.syncResult, r.syncErr
}

func (r *recordingConversationUsecase) ClearUnread(_ context.Context, cmd conversationusecase.ClearUnreadCommand) error {
	r.clearUnreadCommands = append(r.clearUnreadCommands, cmd)
	return r.clearUnreadErr
}

func (r *recordingConversationUsecase) SetUnread(_ context.Context, cmd conversationusecase.SetUnreadCommand) error {
	r.setUnreadCommands = append(r.setUnreadCommands, cmd)
	return r.setUnreadErr
}

func (r *recordingConversationUsecase) DeleteConversation(_ context.Context, cmd conversationusecase.DeleteConversationCommand) error {
	r.deleteCommands = append(r.deleteCommands, cmd)
	return r.deleteConversationErr
}

type recordingConversationListObserver struct {
	events []ConversationListObservation
}

func (r *recordingConversationListObserver) ObserveConversationList(event ConversationListObservation) {
	r.events = append(r.events, event)
}
