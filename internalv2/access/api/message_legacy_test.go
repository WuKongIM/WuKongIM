package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	messageusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSendMessageMapsCompatibleRequestToMessageUsecase(t *testing.T) {
	messages := &recordingMessageUsecase{
		sendResult: messageusecase.SendResult{MessageID: 99, MessageSeq: 7, Reason: messageusecase.ReasonSuccess},
	}
	srv := New(Options{Messages: messages})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","channel_id":"u2","channel_type":1,"client_msg_no":"c1","payload":"aGk=","header":{"no_persist":1},"sync_once":1,"setting":9,"topic":"topic-a","expire":3600}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-WK-Trace-ID", "ABCDEF0123456789ABCDEF0123456789")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"message_id":99,"message_seq":7,"reason":1}`) {
		t.Fatalf("body = %q, want send response", rec.Body.String())
	}
	if len(messages.sendCalls) != 1 {
		t.Fatalf("send calls = %#v, want one call", messages.sendCalls)
	}
	cmd := messages.sendCalls[0]
	if cmd.FromUID != "u1" || cmd.ChannelID != "u2" || cmd.ChannelType != frame.ChannelTypePerson || cmd.ClientMsgNo != "c1" {
		t.Fatalf("send command identity = %#v, want mapped request", cmd)
	}
	if string(cmd.Payload) != "hi" || !cmd.NoPersist || !cmd.SyncOnce || !cmd.NormalizePersonChannel || cmd.ProtocolVersion != frame.LatestVersion {
		t.Fatalf("send command flags = %#v, want payload, flags, and protocol", cmd)
	}
	if cmd.Setting != 9 || cmd.Topic != "topic-a" || cmd.Expire != 3600 {
		t.Fatalf("legacy message fields = setting:%d topic:%q expire:%d, want 9/topic-a/3600", cmd.Setting, cmd.Topic, cmd.Expire)
	}
	if cmd.TraceID != "abcdef0123456789abcdef0123456789" {
		t.Fatalf("trace id = %q, want normalized header trace", cmd.TraceID)
	}
}

func TestSendMessageMapsSubscribersToRequestScopedCommand(t *testing.T) {
	messages := &recordingMessageUsecase{
		sendResult: messageusecase.SendResult{MessageID: 100, MessageSeq: 1, Reason: messageusecase.ReasonSuccess},
	}
	srv := New(Options{Messages: messages})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"from_uid":"system","payload":"Y21k","subscribers":["u1","u2"],"sync_once":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if len(messages.sendCalls) != 1 {
		t.Fatalf("send calls = %#v, want one call", messages.sendCalls)
	}
	cmd := messages.sendCalls[0]
	if cmd.ChannelID != "" || cmd.ChannelType != 0 || !cmd.SyncOnce {
		t.Fatalf("request-scoped channel fields = %#v, want empty channel with sync_once", cmd)
	}
	if !equalStrings(cmd.MessageScopedUIDs, []string{"u1", "u2"}) {
		t.Fatalf("MessageScopedUIDs = %#v, want u1/u2", cmd.MessageScopedUIDs)
	}
}

func TestSendMessageReturnsCompatibleErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		messages MessageUsecase
		body     string
		status   int
		want     string
	}{
		{name: "invalid json", body: `{"from_uid":`, status: http.StatusBadRequest, want: `{"error":"invalid request"}`},
		{name: "invalid payload", body: `{"from_uid":"u1","channel_id":"g1","channel_type":2,"payload":"?"}`, status: http.StatusBadRequest, want: `{"error":"invalid payload"}`},
		{name: "missing usecase", body: `{"from_uid":"u1","channel_id":"g1","channel_type":2,"payload":"aGk="}`, status: http.StatusInternalServerError, want: `{"error":"message usecase not configured"}`},
		{name: "request scoped needs sync once", messages: &recordingMessageUsecase{sendErr: messageusecase.ErrRequestSubscribersRequireSyncOnce}, body: `{"from_uid":"u1","payload":"aGk=","subscribers":["u2"]}`, status: http.StatusBadRequest, want: `{"error":"request subscribers require sync_once"}`},
		{name: "canceled", messages: &recordingMessageUsecase{sendErr: context.Canceled}, body: `{"from_uid":"u1","channel_id":"g1","channel_type":2,"payload":"aGk="}`, status: http.StatusRequestTimeout, want: `{"error":"request canceled"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Messages: tt.messages})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d body = %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if !jsonEqual(rec.Body.String(), tt.want) {
				t.Fatalf("body = %q, want JSON %s", rec.Body.String(), tt.want)
			}
		})
	}
}

func TestChannelMessageSyncMapsCompatibleRequestToUsecase(t *testing.T) {
	messages := &recordingMessageUsecase{
		syncResult: messageusecase.SyncChannelMessagesResult{
			Messages: []messageusecase.SyncedMessage{{
				Flags:       messageusecase.MessageFlags{NoPersist: true, RedDot: true, SyncOnce: true},
				Setting:     3,
				MessageID:   88,
				MessageSeq:  2,
				ClientMsgNo: "c1",
				FromUID:     "u2",
				ChannelID:   "u1@u2",
				ChannelType: frame.ChannelTypePerson,
				Expire:      60,
				Timestamp:   123,
				Payload:     []byte("hello"),
			}},
			More: true,
		},
	}
	srv := New(Options{Messages: messages})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/messagesync", bytes.NewBufferString(`{"login_uid":"u1","channel_id":"u2","channel_type":1,"start_message_seq":2,"end_message_seq":5,"limit":10,"pull_mode":1,"event_summary_mode":"compat"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"start_message_seq":2,"end_message_seq":5,"more":1,"messages":[{"header":{"no_persist":1,"red_dot":1,"sync_once":1},"setting":3,"message_id":88,"message_idstr":"88","client_msg_no":"c1","message_seq":2,"from_uid":"u2","channel_id":"u2","channel_type":1,"expire":60,"timestamp":123,"payload":"aGVsbG8="}]}`) {
		t.Fatalf("body = %q, want compatible messagesync response", rec.Body.String())
	}
	if len(messages.syncQueries) != 1 {
		t.Fatalf("sync queries = %#v, want one query", messages.syncQueries)
	}
	got := messages.syncQueries[0]
	if got.LoginUID != "u1" || got.ChannelID != "u2" || got.ChannelType != frame.ChannelTypePerson || got.StartMessageSeq != 2 || got.EndMessageSeq != 5 || got.Limit != 10 || got.PullMode != messageusecase.PullModeUp || got.EventSummaryMode != "compat" {
		t.Fatalf("sync query = %#v, want mapped request", got)
	}
}

func TestChannelMessageSyncReturnsCompatibleErrorEnvelope(t *testing.T) {
	srv := New(Options{Messages: &recordingMessageUsecase{syncErr: errors.New("login_uid不能为空！")}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/messagesync", bytes.NewBufferString(`{"channel_id":"g1","channel_type":2}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), "{\"msg\":\"login_uid不能为空！\",\"status\":400}"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

type recordingMessageUsecase struct {
	sendCalls   []messageusecase.SendCommand
	sendResult  messageusecase.SendResult
	sendErr     error
	syncQueries []messageusecase.SyncChannelMessagesQuery
	syncResult  messageusecase.SyncChannelMessagesResult
	syncErr     error
}

func (r *recordingMessageUsecase) Send(_ context.Context, cmd messageusecase.SendCommand) (messageusecase.SendResult, error) {
	r.sendCalls = append(r.sendCalls, cmd)
	return r.sendResult, r.sendErr
}

func (r *recordingMessageUsecase) SyncChannelMessages(_ context.Context, query messageusecase.SyncChannelMessagesQuery) (messageusecase.SyncChannelMessagesResult, error) {
	r.syncQueries = append(r.syncQueries, query)
	return r.syncResult, r.syncErr
}
