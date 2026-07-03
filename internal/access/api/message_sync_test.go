package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestMessageSyncMapsLegacyRequestAndResponse(t *testing.T) {
	cmdSync := &recordingCMDSyncUsecase{
		syncResult: cmdsync.SyncResult{Messages: []cmdsync.SyncedMessage{{
			MessageID: 99, MessageSeq: 7, FromUID: "system", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup,
			ClientMsgNo: "cmd-1", ServerTimestampMS: 123000, Payload: []byte("cmd"),
		}}},
	}
	srv := New(Options{CMDSync: cmdSync})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/sync", bytes.NewBufferString(`{"uid":"u1","message_seq":3,"limit":20}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `[{"header":{"no_persist":0,"red_dot":0,"sync_once":0},"setting":0,"message_id":99,"message_idstr":"99","client_msg_no":"cmd-1","message_seq":7,"from_uid":"system","channel_id":"g1","channel_type":2,"expire":0,"timestamp":123,"payload":"Y21k"}]`) {
		t.Fatalf("body = %q, want legacy CMD sync messages", rec.Body.String())
	}
	if len(cmdSync.syncQueries) != 1 || cmdSync.syncQueries[0] != (cmdsync.SyncQuery{UID: "u1", MessageSeq: 3, Limit: 20}) {
		t.Fatalf("sync queries = %#v, want mapped legacy request", cmdSync.syncQueries)
	}
}

func TestMessageSyncReturnsLegacyValidationErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{name: "invalid json", body: `{"uid":`, want: `{"msg":"数据格式有误！","status":400}`},
		{name: "missing uid", body: `{"uid":"","message_seq":1}`, want: `{"msg":"uid不能为空！","status":400}`},
		{name: "negative limit", body: `{"uid":"u1","limit":-1}`, want: `{"msg":"limit不能为负数！","status":400}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmdSync := &recordingCMDSyncUsecase{}
			srv := New(Options{CMDSync: cmdSync})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/message/sync", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.want) {
				t.Fatalf("body = %q, want JSON %s", rec.Body.String(), tt.want)
			}
			if len(cmdSync.syncQueries) != 0 {
				t.Fatalf("sync queries = %#v, want none", cmdSync.syncQueries)
			}
		})
	}
}

func TestMessageSyncAckMapsLegacyRequestToUsecase(t *testing.T) {
	cmdSync := &recordingCMDSyncUsecase{}
	srv := New(Options{CMDSync: cmdSync})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/syncack", bytes.NewBufferString(`{"uid":"u1","last_message_seq":9}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"status":200}`) {
		t.Fatalf("body = %q, want success envelope", rec.Body.String())
	}
	if len(cmdSync.acks) != 1 || cmdSync.acks[0] != (cmdsync.SyncAckCommand{UID: "u1", LastMessageSeq: 9}) {
		t.Fatalf("acks = %#v, want mapped syncack", cmdSync.acks)
	}
}

func TestMessageSyncAckRejectsLegacyValidationErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{name: "invalid json", body: `{"uid":`, want: `{"msg":"数据格式有误！","status":400}`},
		{name: "missing uid", body: `{"uid":"","last_message_seq":9}`, want: `{"msg":"uid不能为空！","status":400}`},
		{name: "missing last message seq", body: `{"uid":"u1","last_message_seq":0}`, want: `{"msg":"last_message_seq不能为空！","status":400}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmdSync := &recordingCMDSyncUsecase{}
			srv := New(Options{CMDSync: cmdSync})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/message/syncack", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.want) {
				t.Fatalf("body = %q, want JSON %s", rec.Body.String(), tt.want)
			}
			if len(cmdSync.acks) != 0 {
				t.Fatalf("acks = %#v, want none", cmdSync.acks)
			}
		})
	}
}

type recordingCMDSyncUsecase struct {
	syncQueries []cmdsync.SyncQuery
	acks        []cmdsync.SyncAckCommand
	syncResult  cmdsync.SyncResult
	syncErr     error
	ackErr      error
}

func (r *recordingCMDSyncUsecase) Sync(_ context.Context, query cmdsync.SyncQuery) (cmdsync.SyncResult, error) {
	r.syncQueries = append(r.syncQueries, query)
	return r.syncResult, r.syncErr
}

func (r *recordingCMDSyncUsecase) SyncAck(_ context.Context, cmd cmdsync.SyncAckCommand) error {
	r.acks = append(r.acks, cmd)
	return r.ackErr
}
