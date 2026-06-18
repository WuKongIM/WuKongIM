package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestConversationSyncMapsLegacyRequestAndResponse(t *testing.T) {
	personChannel, err := runtimechannelid.NormalizePersonChannel("u1", "u2")
	if err != nil {
		t.Fatalf("NormalizePersonChannel() error = %v", err)
	}
	conversations := &recordingConversationUsecase{
		syncResult: conversationusecase.SyncResult{
			Conversations: []conversationusecase.SyncConversation{{
				ChannelID:       personChannel,
				ChannelType:     frame.ChannelTypePerson,
				Unread:          5,
				Timestamp:       123,
				LastMsgSeq:      8,
				LastClientMsgNo: "last-client",
				ReadToMsgSeq:    3,
				Version:         456,
				Recents: []conversationusecase.SyncMessage{{
					MessageID:         99,
					MessageSeq:        8,
					FromUID:           "u2",
					ChannelID:         personChannel,
					ChannelType:       frame.ChannelTypePerson,
					ClientMsgNo:       "recent-client",
					ServerTimestampMS: 123000,
					Payload:           []byte("hello"),
				}},
			}},
		},
	}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{
		"uid":"u1",
		"version":9,
		"last_msg_seqs":"u2:1:3|g2:2:4",
		"msg_count":2,
		"only_unread":1,
		"exclude_channel_types":[3],
		"limit":20
	}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `[{
		"channel_id":"u2",
		"channel_type":1,
		"unread":5,
		"timestamp":123,
		"last_msg_seq":8,
		"last_client_msg_no":"last-client",
		"offset_msg_seq":0,
		"readed_to_msg_seq":3,
		"version":456,
		"recents":[{
			"header":{"no_persist":0,"red_dot":0,"sync_once":0},
			"setting":0,
			"message_id":99,
			"message_idstr":"99",
			"client_msg_no":"recent-client",
			"message_seq":8,
			"from_uid":"u2",
			"channel_id":"u2",
			"channel_type":1,
			"expire":0,
			"timestamp":123,
			"payload":"aGVsbG8="
		}]
	}]`) {
		t.Fatalf("body = %q, want compatible conversation sync response", rec.Body.String())
	}
	if len(conversations.syncQueries) != 1 {
		t.Fatalf("sync queries = %#v, want one query", conversations.syncQueries)
	}
	got := conversations.syncQueries[0]
	if got.UID != "u1" || got.Version != 9 || got.MsgCount != 2 || !got.OnlyUnread || got.Limit != 20 {
		t.Fatalf("sync query = %#v, want mapped scalar fields", got)
	}
	if len(got.ExcludeChannelTypes) != 1 || got.ExcludeChannelTypes[0] != 3 {
		t.Fatalf("exclude channel types = %#v, want [3]", got.ExcludeChannelTypes)
	}
	if got.LastMsgSeqs[conversationusecase.ConversationKey{ChannelID: personChannel, ChannelType: int64(frame.ChannelTypePerson)}] != 3 ||
		got.LastMsgSeqs[conversationusecase.ConversationKey{ChannelID: "g2", ChannelType: int64(frame.ChannelTypeGroup)}] != 4 {
		t.Fatalf("last msg seqs = %#v, want normalized person and group keys", got.LastMsgSeqs)
	}
}

func TestConversationSyncRejectsInvalidLegacyLastMsgSeqs(t *testing.T) {
	srv := New(Options{Conversations: &recordingConversationUsecase{}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{"uid":"u1","last_msg_seqs":"bad"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"msg":"invalid last_msg_seqs","status":400}`) {
		t.Fatalf("body = %q, want invalid last_msg_seqs error", rec.Body.String())
	}
}

func TestConversationSyncObserverRecordsShapeAndLatency(t *testing.T) {
	conversations := &recordingConversationUsecase{
		syncResult: conversationusecase.SyncResult{
			Conversations: []conversationusecase.SyncConversation{{
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				LastMsgSeq:  3,
			}},
			OverlayItems:       2,
			RecentLoadDuration: 3 * time.Millisecond,
		},
	}
	observer := &recordingConversationSyncObserver{}
	srv := New(Options{Conversations: conversations, ConversationSyncObserver: observer})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{
		"uid":"u1",
		"last_msg_seqs":"g1:2:3|g2:2:4",
		"msg_count":1,
		"only_unread":1
	}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if len(observer.events) != 1 {
		t.Fatalf("events = %#v, want one event", observer.events)
	}
	got := observer.events[0]
	if got.Result != "ok" {
		t.Fatalf("Result = %q, want ok", got.Result)
	}
	if !got.OnlyUnread {
		t.Fatalf("OnlyUnread = false, want true")
	}
	if !got.WithRecents {
		t.Fatalf("WithRecents = false, want true")
	}
	if got.ReturnedItems != 1 {
		t.Fatalf("ReturnedItems = %d, want 1", got.ReturnedItems)
	}
	if got.OverlayItems != 2 {
		t.Fatalf("OverlayItems = %d, want 2", got.OverlayItems)
	}
	if got.RecentLoadDuration != 3*time.Millisecond {
		t.Fatalf("RecentLoadDuration = %v, want 3ms", got.RecentLoadDuration)
	}
	if got.Duration <= 0 {
		t.Fatalf("Duration = %v, want positive duration", got.Duration)
	}
}

func TestConversationSyncObserverRecordsFailures(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		conversations ConversationUsecase
		wantResult    string
	}{
		{
			name:          "invalid json",
			body:          `{`,
			conversations: &recordingConversationUsecase{},
			wantResult:    "invalid_request",
		},
		{
			name:          "missing uid",
			body:          `{}`,
			conversations: &recordingConversationUsecase{},
			wantResult:    "invalid_request",
		},
		{
			name:       "missing usecase",
			body:       `{"uid":"u1"}`,
			wantResult: "not_configured",
		},
		{
			name:          "parse last msg seqs",
			body:          `{"uid":"u1","last_msg_seqs":"bad"}`,
			conversations: &recordingConversationUsecase{},
			wantResult:    "parse_last_msg_seqs_error",
		},
		{
			name:          "usecase error",
			body:          `{"uid":"u1"}`,
			conversations: &recordingConversationUsecase{syncErr: errors.New("sync failed")},
			wantResult:    "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer := &recordingConversationSyncObserver{}
			srv := New(Options{Conversations: tt.conversations, ConversationSyncObserver: observer})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Handler().ServeHTTP(rec, req)

			if len(observer.events) != 1 {
				t.Fatalf("events = %#v, want one event", observer.events)
			}
			got := observer.events[0]
			if got.Result != tt.wantResult {
				t.Fatalf("Result = %q, want %q", got.Result, tt.wantResult)
			}
			if got.Duration <= 0 {
				t.Fatalf("Duration = %v, want positive duration", got.Duration)
			}
		})
	}
}

type recordingConversationSyncObserver struct {
	events []ConversationSyncObservation
}

func (r *recordingConversationSyncObserver) ObserveConversationSync(event ConversationSyncObservation) {
	r.events = append(r.events, event)
}
