package api

import (
	"bytes"
	"context"
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
				ChannelID:      "g1",
				ChannelType:    int64(frame.ChannelTypeGroup),
				LastMessageID:  99,
				LastMessageSeq: 7,
				LastAt:         1234,
				FromUID:        "u2",
				ClientMsgNo:    "c1",
				Payload:        []byte("hello"),
				UpdatedAt:      1235,
			}},
			NextCursor: conversationusecase.Cursor{
				LastAt:         1234,
				LastMessageSeq: 7,
				ChannelID:      "g1",
				ChannelType:    int64(frame.ChannelTypeGroup),
			},
			HasMore:            true,
			Truncated:          true,
			ScannedMemberships: 500,
		},
	}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/list", bytes.NewBufferString(`{
		"uid":"u1",
		"limit":20,
		"cursor":{"last_at":2000,"last_message_seq":9,"channel_id":"g0","channel_type":2}
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
			"last_message_id":99,
			"last_message_idstr":"99",
			"last_message_seq":7,
			"last_at":1234,
			"from_uid":"u2",
			"client_msg_no":"c1",
			"payload":"aGVsbG8=",
			"updated_at":1235
		}],
		"next_cursor":{"last_at":1234,"last_message_seq":7,"channel_id":"g1","channel_type":2},
		"more":1,
		"truncated":true,
		"scanned_memberships":500
	}`) {
		t.Fatalf("body = %q, want conversation list page", rec.Body.String())
	}
	if len(conversations.requests) != 1 {
		t.Fatalf("conversation list requests = %#v, want one", conversations.requests)
	}
	got := conversations.requests[0]
	if got.UID != "u1" || got.Limit != 20 ||
		got.Cursor.LastAt != 2000 || got.Cursor.LastMessageSeq != 9 ||
		got.Cursor.ChannelID != "g0" || got.Cursor.ChannelType != int64(frame.ChannelTypeGroup) {
		t.Fatalf("list request = %#v, want mapped cursor request", got)
	}
}

func TestConversationListReturnsPeerIDForPersonChannel(t *testing.T) {
	conversations := &recordingConversationUsecase{
		result: conversationusecase.ListResult{
			Items: []conversationusecase.Conversation{{
				ChannelID:      "u1@u2",
				ChannelType:    int64(frame.ChannelTypePerson),
				LastMessageID:  100,
				LastMessageSeq: 8,
				LastAt:         2000,
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
			"last_message_id":100,
			"last_message_idstr":"100",
			"last_message_seq":8,
			"last_at":2000,
			"from_uid":"",
			"client_msg_no":"",
			"payload":null,
			"updated_at":0
		}],
		"more":0,
		"truncated":false,
		"scanned_memberships":0
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
				{ChannelID: "g1", ChannelType: int64(frame.ChannelTypeGroup)},
				{ChannelID: "g2", ChannelType: int64(frame.ChannelTypeGroup)},
			},
			Truncated:          true,
			ScannedMemberships: 700,
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
	if got.Result != "ok" || got.ReturnedItems != 2 || got.ScannedMemberships != 700 || !got.Truncated {
		t.Fatalf("observer event = %#v, want page shape", got)
	}
	if got.Duration <= 0 {
		t.Fatalf("observer duration = %v, want positive latency", got.Duration)
	}
}

type recordingConversationUsecase struct {
	requests []conversationusecase.ListRequest
	result   conversationusecase.ListResult
	err      error
}

func (r *recordingConversationUsecase) List(_ context.Context, req conversationusecase.ListRequest) (conversationusecase.ListResult, error) {
	r.requests = append(r.requests, req)
	return r.result, r.err
}

type recordingConversationListObserver struct {
	events []ConversationListObservation
}

func (r *recordingConversationListObserver) ObserveConversationList(event ConversationListObservation) {
	r.events = append(r.events, event)
}
