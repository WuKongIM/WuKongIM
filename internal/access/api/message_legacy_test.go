package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
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
	req := httptest.NewRequest(http.MethodPost, "/channel/messagesync", bytes.NewBufferString(`{"login_uid":"u1","channel_id":"u2","channel_type":1,"start_message_seq":2,"end_message_seq":5,"limit":10,"pull_mode":1,"include_event_meta":1,"event_summary_mode":"compat"}`))
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
	if got.LoginUID != "u1" || got.ChannelID != "u2" || got.ChannelType != frame.ChannelTypePerson || got.StartMessageSeq != 2 || got.EndMessageSeq != 5 || got.Limit != 10 || got.PullMode != messageusecase.PullModeUp || !got.IncludeEventMeta || got.EventSummaryMode != "compat" {
		t.Fatalf("sync query = %#v, want mapped request", got)
	}
}

func TestMessageEventAppendMapsCompatibleRequestToUsecase(t *testing.T) {
	messages := &recordingMessageUsecase{
		appendResult: messageusecase.MessageEventAppendResult{
			ChannelID:   "u2@u1",
			ChannelType: int64(frame.ChannelTypePerson),
			FromUID:     "u1",
			ClientMsgNo: "cmn-1",
			EventID:     "evt-1",
			EventKey:    messageusecase.EventKeyDefault,
			MsgEventSeq: 12,
			Status:      messageusecase.EventStatusOpen,
		},
	}
	srv := New(Options{Messages: messages})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/event", bytes.NewBufferString(`{"channel_id":"u2","channel_type":1,"from_uid":"u1","message_id":99,"client_msg_no":"cmn-1","event_id":"evt-1","event_type":"stream.delta","event_key":"main","visibility":"public","occurred_at":1700000000000,"payload":{"kind":"text","delta":"hi"}}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"status":200,"data":{"client_msg_no":"cmn-1","event_key":"main","event_id":"evt-1","msg_event_seq":12,"stream_status":"open","channel_id":"u2","channel_type":1,"from_uid":"u1"}}`) {
		t.Fatalf("body = %q, want compatible event append response", rec.Body.String())
	}
	if len(messages.appendCalls) != 1 {
		t.Fatalf("append calls = %#v, want one call", messages.appendCalls)
	}
	call := messages.appendCalls[0]
	if call.ChannelID != "u2" || call.ChannelType != int64(frame.ChannelTypePerson) || call.FromUID != "u1" || call.MessageID != 99 {
		t.Fatalf("append command channel/sender = %#v, want mapped request", call)
	}
	if call.ClientMsgNo != "cmn-1" || call.EventID != "evt-1" || call.EventKey != "main" || call.EventType != "stream.delta" || call.Visibility != "public" || call.OccurredAt != 1700000000000 {
		t.Fatalf("append command event fields = %#v, want mapped request", call)
	}
	if string(call.Payload) != `{"kind":"text","delta":"hi"}` {
		t.Fatalf("payload = %q, want raw JSON payload", call.Payload)
	}
}

func TestMessageEventAppendReturnsCompatibleErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		messages MessageUsecase
		body     string
		want     string
	}{
		{name: "invalid json", messages: &recordingMessageUsecase{}, body: `{"channel_id":`, want: `{"msg":"数据格式有误！","status":400}`},
		{name: "missing usecase", body: `{"channel_id":"g1","channel_type":2,"client_msg_no":"cmn","event_id":"evt","event_type":"stream.open"}`, want: `{"msg":"message usecase not configured","status":400}`},
		{name: "unsupported headers", messages: &recordingMessageUsecase{}, body: `{"channel_id":"g1","channel_type":2,"client_msg_no":"cmn","event_id":"evt","event_type":"stream.open","headers":{"x":"y"}}`, want: `{"msg":"message event headers are not supported","status":400}`},
		{name: "usecase error", messages: &recordingMessageUsecase{appendErr: messageusecase.ErrMessageEventClientMsgNoRequired}, body: `{"channel_id":"g1","channel_type":2,"event_id":"evt","event_type":"stream.open"}`, want: `{"msg":"client_msg_no不能为空！","status":400}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Messages: tt.messages})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/message/event", bytes.NewBufferString(tt.body))
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

func TestChannelMessageSyncIncludesEventFields(t *testing.T) {
	messages := &recordingMessageUsecase{
		syncResult: messageusecase.SyncChannelMessagesResult{
			Messages: []messageusecase.SyncedMessage{{
				MessageID:   88,
				MessageSeq:  2,
				ClientMsgNo: "c1",
				FromUID:     "u2",
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				Payload:     []byte("base"),
				End:         1,
				EndReason:   3,
				StreamData:  []byte("hello"),
				EventMeta: &messageusecase.MessageEventMeta{
					HasEvents:       true,
					Completed:       true,
					EventVersion:    2,
					LastMsgEventSeq: 2,
					EventCount:      1,
					Events: []messageusecase.MessageEventKeyMeta{{
						EventKey:        messageusecase.EventKeyDefault,
						Status:          messageusecase.EventStatusClosed,
						LastMsgEventSeq: 2,
						Snapshot:        map[string]any{"text": "hello"},
						EndReason:       3,
					}},
				},
				EventHint: &messageusecase.MessageEventSyncHint{ClientMsgNo: "c1"},
			}},
		},
	}
	srv := New(Options{Messages: messages})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/messagesync", bytes.NewBufferString(`{"login_uid":"u1","channel_id":"g1","channel_type":2,"event_summary_mode":"full"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"start_message_seq":0,"end_message_seq":0,"more":0,"messages":[{"header":{"no_persist":0,"red_dot":0,"sync_once":0},"setting":0,"message_id":88,"message_idstr":"88","client_msg_no":"c1","end":1,"end_reason":3,"stream_data":"aGVsbG8=","event_meta":{"has_events":true,"completed":true,"event_version":2,"last_msg_event_seq":2,"event_count":1,"events":[{"event_key":"main","status":"closed","last_msg_event_seq":2,"snapshot":{"text":"hello"},"end_reason":3}]},"event_sync_hint":{"client_msg_no":"c1","from_msg_event_seq":0},"message_seq":2,"from_uid":"u2","channel_id":"g1","channel_type":2,"expire":0,"timestamp":0,"payload":"YmFzZQ=="}]}`) {
		t.Fatalf("body = %q, want event-enriched messagesync response", rec.Body.String())
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
	sendCalls    []messageusecase.SendCommand
	sendResult   messageusecase.SendResult
	sendErr      error
	appendCalls  []messageusecase.MessageEventAppend
	appendResult messageusecase.MessageEventAppendResult
	appendErr    error
	syncQueries  []messageusecase.SyncChannelMessagesQuery
	syncResult   messageusecase.SyncChannelMessagesResult
	syncErr      error
}

func (r *recordingMessageUsecase) Send(_ context.Context, cmd messageusecase.SendCommand) (messageusecase.SendResult, error) {
	r.sendCalls = append(r.sendCalls, cmd)
	return r.sendResult, r.sendErr
}

func (r *recordingMessageUsecase) AppendMessageEvent(_ context.Context, event messageusecase.MessageEventAppend) (messageusecase.MessageEventAppendResult, error) {
	r.appendCalls = append(r.appendCalls, event)
	return r.appendResult, r.appendErr
}

func (r *recordingMessageUsecase) SyncChannelMessages(_ context.Context, query messageusecase.SyncChannelMessagesQuery) (messageusecase.SyncChannelMessagesResult, error) {
	r.syncQueries = append(r.syncQueries, query)
	return r.syncResult, r.syncErr
}
