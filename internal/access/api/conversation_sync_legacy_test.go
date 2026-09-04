package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestConversationSyncLegacyRouteIsRegistered(t *testing.T) {
	srv := New(Options{Conversations: &recordingConversationUsecase{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{"uid":"u1","msg_count":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want registered compatibility route", rec.Code, rec.Body.String())
	}
}

func TestConversationSyncLegacyMapsOldParametersAndResponse(t *testing.T) {
	conversations := &recordingLegacyConversationSync{
		result: conversationusecase.LegacySyncResult{Items: []conversationusecase.LegacyConversation{{
			ChannelID:        "u1@u2",
			ChannelType:      1,
			Unread:           2,
			Timestamp:        1_700_000_000,
			LastMessageSeq:   9,
			LastClientMsgNo:  "client-9",
			ReadToMessageSeq: 7,
			Version:          1_700_000_000_000_000_000,
			Recents: []conversationusecase.LegacyRecentMessage{{
				MessageID: 99, MessageSeq: 9, ChannelID: "u1@u2", ChannelType: 1,
				FromUID: "u2", ClientMsgNo: "client-9", Timestamp: 1_700_000_000,
				Payload: []byte("hello"), End: 1, EndReason: 3, StreamData: []byte("done"),
				EventMeta: &conversationusecase.LegacyMessageEventMeta{
					HasEvents: true, Completed: true, EventVersion: 2, LastMsgEventSeq: 2,
					EventCount: 1, Events: []conversationusecase.LegacyMessageEventKeyMeta{{
						EventKey: "main", Status: "closed", LastMsgEventSeq: 2,
						EndReason: 3, Snapshot: map[string]any{"text": "done"},
					}},
				},
				EventHint: &conversationusecase.LegacyMessageEventSyncHint{ClientMsgNo: "client-9"},
			}},
		}}},
	}
	srv := New(Options{Conversations: conversations})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{
		"uid":"u1",
		"version":123,
		"last_msg_seqs":"u2:1:7|g1:2:8|g2:3:not-a-sequence|invalid",
		"msg_count":3,
		"only_unread":1,
		"exclude_channel_types":[3,4],
		"page":2,
		"page_size":50
	}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if len(conversations.requests) != 1 {
		t.Fatalf("legacy requests = %#v, want one", conversations.requests)
	}
	got := conversations.requests[0]
	if got.UID != "u1" || got.Version != 123 || got.MessageCount != 3 || !got.OnlyUnread || got.Page != 2 || got.PageSize != 50 {
		t.Fatalf("legacy request = %#v, want old scalar parameters", got)
	}
	if len(got.ExcludeChannelTypes) != 2 || got.ExcludeChannelTypes[0] != 3 || got.ExcludeChannelTypes[1] != 4 {
		t.Fatalf("exclude channel types = %#v", got.ExcludeChannelTypes)
	}
	if len(got.ClientLastMessageSeqs) != 3 || got.ClientLastMessageSeqs[0] != (conversationusecase.LegacyConversationCursor{ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: 1, LastMessageSeq: 7}) || got.ClientLastMessageSeqs[1] != (conversationusecase.LegacyConversationCursor{ChannelID: "g1", ChannelType: 2, LastMessageSeq: 8}) || got.ClientLastMessageSeqs[2] != (conversationusecase.LegacyConversationCursor{ChannelID: "g2", ChannelType: 3, LastMessageSeq: 0}) {
		t.Fatalf("last message cursors = %#v", got.ClientLastMessageSeqs)
	}
	if !jsonEqual(rec.Body.String(), `[{
		"channel_id":"u2",
		"channel_type":1,
		"unread":2,
		"timestamp":1700000000,
		"last_msg_seq":9,
		"last_client_msg_no":"client-9",
		"offset_msg_seq":0,
		"readed_to_msg_seq":7,
		"version":1700000000000000000,
		"recents":[{
			"header":{"no_persist":0,"red_dot":0,"sync_once":0},
			"setting":0,
			"message_id":99,
			"message_idstr":"99",
			"client_msg_no":"client-9",
			"end":1,
			"end_reason":3,
			"stream_data":"ZG9uZQ==",
			"event_meta":{"has_events":true,"completed":true,"event_version":2,"last_msg_event_seq":2,"event_count":1,"events":[{"event_key":"main","status":"closed","last_msg_event_seq":2,"snapshot":{"text":"done"},"end_reason":3}]},
			"event_sync_hint":{"client_msg_no":"client-9","from_msg_event_seq":0},
			"message_seq":9,
			"from_uid":"u2",
			"channel_id":"u2",
			"channel_type":1,
			"expire":0,
			"timestamp":1700000000,
			"payload":"aGVsbG8="
		}]
	}]`) {
		t.Fatalf("body = %s, want legacy conversation array", rec.Body.String())
	}
}

func TestConversationSyncLegacyPreservesOldSystemUIDProjection(t *testing.T) {
	conversations := &recordingLegacyConversationSync{result: conversationusecase.LegacySyncResult{
		Items: []conversationusecase.LegacyConversation{
			{
				ChannelID:   runtimechannelid.EncodePersonChannel("u1", userusecase.DefaultSystemUID),
				ChannelType: 1,
				Recents: []conversationusecase.LegacyRecentMessage{{
					MessageSeq: 1, ChannelID: runtimechannelid.EncodePersonChannel("u1", userusecase.DefaultSystemUID), ChannelType: 1,
				}},
			},
			{
				ChannelID: "g1", ChannelType: 2,
				Recents: []conversationusecase.LegacyRecentMessage{{
					MessageSeq: 2, ChannelID: "g1", ChannelType: 2, FromUID: userusecase.DefaultSystemUID,
				}},
			},
		},
	}}
	srv := New(Options{Conversations: conversations})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{"uid":"u1","msg_count":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	var rows []conversationSyncLegacyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(rows) != 1 || rows[0].ChannelID != "g1" || len(rows[0].Recents) != 1 || rows[0].Recents[0].FromUID != "" {
		t.Fatalf("rows = %#v, want system conversation omitted and system sender blanked", rows)
	}
}

func TestConversationSyncLegacyUsesConfiguredSystemUIDProjection(t *testing.T) {
	const systemUID = "custom-system"
	conversations := &recordingLegacyConversationSync{result: conversationusecase.LegacySyncResult{
		Items: []conversationusecase.LegacyConversation{
			{
				ChannelID:   runtimechannelid.EncodePersonChannel("u1", systemUID),
				ChannelType: 1,
			},
			{
				ChannelID: "g1", ChannelType: 2,
				Recents: []conversationusecase.LegacyRecentMessage{{
					MessageSeq: 2, ChannelID: "g1", ChannelType: 2, FromUID: systemUID,
				}},
			},
		},
	}}
	srv := New(Options{Conversations: conversations, SystemUID: systemUID})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{"uid":"u1","msg_count":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	var rows []conversationSyncLegacyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(rows) != 1 || rows[0].ChannelID != "g1" || len(rows[0].Recents) != 1 || rows[0].Recents[0].FromUID != "" {
		t.Fatalf("rows = %#v, want configured system conversation omitted and sender blanked", rows)
	}
}

type recordingLegacyConversationSync struct {
	recordingConversationUsecase
	requests []conversationusecase.LegacySyncRequest
	result   conversationusecase.LegacySyncResult
	err      error
}

func (r *recordingLegacyConversationSync) SyncLegacy(_ context.Context, req conversationusecase.LegacySyncRequest) (conversationusecase.LegacySyncResult, error) {
	r.requests = append(r.requests, req)
	return r.result, r.err
}
