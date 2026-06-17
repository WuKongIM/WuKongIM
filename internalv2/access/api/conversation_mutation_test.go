package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestConversationClearUnreadMapsLegacyRequestToUsecaseCommand(t *testing.T) {
	conversations := &recordingConversationUsecase{}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversations/clearUnread", bytes.NewBufferString(`{"uid":"u1","channel_id":"u2","channel_type":1,"message_seq":12}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"status":200}`) {
		t.Fatalf("body = %q, want mutation success", rec.Body.String())
	}
	if len(conversations.clearUnreadCommands) != 1 {
		t.Fatalf("clear unread commands = %#v, want one command", conversations.clearUnreadCommands)
	}
	wantChannelID, err := runtimechannelid.NormalizePersonChannel("u1", "u2")
	if err != nil {
		t.Fatalf("NormalizePersonChannel() error = %v", err)
	}
	got := conversations.clearUnreadCommands[0]
	if got.UID != "u1" || got.ChannelID != wantChannelID || got.ChannelType != frame.ChannelTypePerson || got.MessageSeq != 12 {
		t.Fatalf("clear unread command = %#v, want normalized personal channel command", got)
	}
}

func TestConversationSetUnreadMapsLegacyRequestToUsecaseCommand(t *testing.T) {
	conversations := &recordingConversationUsecase{}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversations/setUnread", bytes.NewBufferString(`{"uid":"u1","channel_id":"g1","channel_type":2,"unread":3}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"status":200}`) {
		t.Fatalf("body = %q, want mutation success", rec.Body.String())
	}
	if len(conversations.setUnreadCommands) != 1 {
		t.Fatalf("set unread commands = %#v, want one command", conversations.setUnreadCommands)
	}
	got := conversations.setUnreadCommands[0]
	if got.UID != "u1" || got.ChannelID != "g1" || got.ChannelType != frame.ChannelTypeGroup || got.Unread != 3 {
		t.Fatalf("set unread command = %#v, want group command", got)
	}
}

func TestConversationDeleteMapsLegacyRequestToUsecaseCommand(t *testing.T) {
	conversations := &recordingConversationUsecase{}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversations/delete", bytes.NewBufferString(`{"uid":"u1","channel_id":"u2","channel_type":1,"message_seq":12}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"status":200}`) {
		t.Fatalf("body = %q, want mutation success", rec.Body.String())
	}
	if len(conversations.deleteCommands) != 1 {
		t.Fatalf("delete commands = %#v, want one command", conversations.deleteCommands)
	}
	wantChannelID, err := runtimechannelid.NormalizePersonChannel("u1", "u2")
	if err != nil {
		t.Fatalf("NormalizePersonChannel() error = %v", err)
	}
	got := conversations.deleteCommands[0]
	if got.UID != "u1" || got.ChannelID != wantChannelID || got.ChannelType != frame.ChannelTypePerson || got.MessageSeq != 12 {
		t.Fatalf("delete command = %#v, want normalized personal channel command", got)
	}
}

func TestConversationSetUnreadRejectsInvalidLegacyRequest(t *testing.T) {
	conversations := &recordingConversationUsecase{}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversations/setUnread", bytes.NewBufferString(`{"uid":"","channel_id":"g1","channel_type":2,"unread":3}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"msg":"UID cannot be empty","status":400}`) {
		t.Fatalf("body = %q, want legacy validation error", rec.Body.String())
	}
	if len(conversations.setUnreadCommands) != 0 {
		t.Fatalf("set unread commands = %#v, want none", conversations.setUnreadCommands)
	}
}

func TestConversationDeleteReportsMissingUsecase(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversations/delete", bytes.NewBufferString(`{"uid":"u1","channel_id":"g1","channel_type":2,"message_seq":12}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"msg":"conversation usecase not configured","status":400}`) {
		t.Fatalf("body = %q, want missing usecase error", rec.Body.String())
	}
}
