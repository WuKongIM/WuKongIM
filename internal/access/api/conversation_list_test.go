package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
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
			HasMore:    true,
			Done:       false,
			Deletes:    []conversationusecase.ConversationKey{{ChannelID: "g-deleted", ChannelType: int64(frame.ChannelTypeGroup)}},
			Unresolved: []conversationusecase.ConversationKey{{ChannelID: "g-retry", ChannelType: int64(frame.ChannelTypeGroup)}},
			Coverage:   2001, TombstonesRetainedSince: 1000, ResetRequired: true,
		},
	}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/list", bytes.NewBufferString(`{
		"uid":"u1",
		"limit":20,
		"completed_coverage":900,
		"cursor":"AQAAAAAAAAfQAAAAAAAAAAIAAmcw"
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
		"deletes":[{"channel_id":"g-deleted","channel_type":2}],
		"unresolved":[{"channel_id":"g-retry","channel_type":2}],
		"next_cursor":"AQAAAAAAAATSAAAAAAAAAAIAAmcx",
		"done":false,
		"coverage":2001,
		"tombstones_retained_since":1000,
		"reset_required":true
	}`) {
		t.Fatalf("body = %q, want conversation list page", rec.Body.String())
	}
	assertJSONFieldAbsent(t, rec.Body.Bytes(), "truncated")
	assertJSONFieldAbsent(t, rec.Body.Bytes(), "scanned_memberships")
	if len(conversations.requests) != 1 {
		t.Fatalf("conversation list requests = %#v, want one", conversations.requests)
	}
	got := conversations.requests[0]
	if got.UID != "u1" || got.Limit != 20 || got.CompletedCoverage != 900 ||
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
			Done: true,
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
			"unread":0,
			"last_message":null
		}],
		"deletes":[],
		"unresolved":[],
		"done":true,
		"coverage":0,
		"tombstones_retained_since":0,
		"reset_required":false
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
			Done: true,
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
		"deletes":[],
		"unresolved":[],
		"done":true,
		"coverage":0,
		"tombstones_retained_since":0,
		"reset_required":false
	}`) {
		t.Fatalf("body = %q, want person peer channel id", rec.Body.String())
	}
}

func TestConversationRetryNormalizesKeysAndReturnsPartialResults(t *testing.T) {
	conversations := &recordingConversationUsecase{retryResult: conversationusecase.ListResult{
		Items:      []conversationusecase.Conversation{{ChannelID: "alice@bob", ChannelType: 1}},
		Deletes:    []conversationusecase.ConversationKey{{ChannelID: "gone", ChannelType: 2}},
		Unresolved: []conversationusecase.ConversationKey{{ChannelID: "later", ChannelType: 2}},
		Done:       true,
	}}
	srv := New(Options{Conversations: conversations})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/retry", bytes.NewBufferString(`{
		"uid":"alice",
		"channels":[
			{"channel_id":"bob","channel_type":1},
			{"channel_id":"group","channel_type":2}
		]
	}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(conversations.retryRequests) != 1 {
		t.Fatalf("retry requests = %#v", conversations.retryRequests)
	}
	got := conversations.retryRequests[0]
	if got.UID != "alice" || len(got.Keys) != 2 || got.Keys[0].ChannelID != "bob@alice" || got.Keys[1].ChannelID != "group" {
		t.Fatalf("retry request = %#v, want normalized person and group keys", got)
	}
	if !jsonEqual(rec.Body.String(), `{"conversations":[{"channel_id":"bob","channel_type":1,"active_at":0,"read_seq":0,"deleted_to_seq":0,"unread":0,"last_message":null}],"deletes":[{"channel_id":"gone","channel_type":2}],"unresolved":[{"channel_id":"later","channel_type":2}],"done":true,"coverage":0,"tombstones_retained_since":0,"reset_required":false}`) {
		t.Fatalf("body = %s", rec.Body.String())
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
			ScannedCandidates: 5,
			Items: []conversationusecase.Conversation{
				{ChannelID: "g1", ChannelType: int64(frame.ChannelTypeGroup), LastMessage: &conversationusecase.LastMessage{MessageID: 1}},
				{ChannelID: "g2", ChannelType: int64(frame.ChannelTypeGroup)},
			},
			Deletes:    []conversationusecase.ConversationKey{{ChannelID: "gone", ChannelType: 2}},
			Unresolved: []conversationusecase.ConversationKey{{ChannelID: "retry", ChannelType: 2}},
			HasMore:    true,
			Done:       false,
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
	if got.Result != "ok" || got.ScannedCandidates != 5 || got.ReturnedItems != 2 ||
		got.Deletes != 1 || got.Unresolved != 1 || got.Done {
		t.Fatalf("observer event = %#v, want page shape", got)
	}
	if got.Duration <= 0 {
		t.Fatalf("observer duration = %v, want positive latency", got.Duration)
	}
}

func BenchmarkConversationListResponse200LargePayload(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 32767)
	result := conversationusecase.ListResult{Items: make([]conversationusecase.Conversation, 200), Done: true}
	for i := range result.Items {
		result.Items[i] = conversationusecase.Conversation{
			ChannelID: "benchmark-group", ChannelType: int64(frame.ChannelTypeGroup),
			LastMessage: &conversationusecase.LastMessage{MessageID: uint64(i + 1), MessageSeq: uint64(i + 1), Payload: payload},
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(payload) * len(result.Items)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded, err := json.Marshal(newConversationListResponse("u1", result))
		if err != nil {
			b.Fatal(err)
		}
		conversationListBenchmarkSink = encoded
	}
}

var conversationListBenchmarkSink []byte

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
	retryRequests         []conversationusecase.RetryRequest
	retryResult           conversationusecase.ListResult
	retryErr              error
	clearUnreadCommands   []conversationusecase.ClearUnreadCommand
	setUnreadCommands     []conversationusecase.SetUnreadCommand
	deleteCommands        []conversationusecase.DeleteConversationCommand
	activateCommands      []conversationusecase.ActivateConversationCommand
	clearUnreadErr        error
	setUnreadErr          error
	deleteConversationErr error
}

func (r *recordingConversationUsecase) List(_ context.Context, req conversationusecase.ListRequest) (conversationusecase.ListResult, error) {
	r.requests = append(r.requests, req)
	return r.result, r.err
}

func (r *recordingConversationUsecase) Retry(_ context.Context, req conversationusecase.RetryRequest) (conversationusecase.ListResult, error) {
	r.retryRequests = append(r.retryRequests, req)
	return r.retryResult, r.retryErr
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

func (r *recordingConversationUsecase) ActivateConversation(_ context.Context, cmd conversationusecase.ActivateConversationCommand) error {
	r.activateCommands = append(r.activateCommands, cmd)
	return nil
}

type recordingConversationListObserver struct {
	events []ConversationListObservation
}

func (r *recordingConversationListObserver) ObserveConversationList(event ConversationListObservation) {
	r.events = append(r.events, event)
}
