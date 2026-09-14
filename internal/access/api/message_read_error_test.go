package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	conversation "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	message "github.com/WuKongIM/WuKongIM/internal/usecase/message"
)

type failingReadMessages struct {
	*recordingMessageUsecase
	failure error
}

func (f *failingReadMessages) LookupMessages(context.Context, message.LookupMessagesQuery) (message.SyncChannelMessagesResult, error) {
	return message.SyncChannelMessagesResult{}, f.failure
}

func TestMessageReadRoutesRetryableErrors(t *testing.T) {
	for _, failure := range []error{proxy.ErrReadStaleRoute, fmt.Errorf("%w: %w", message.ErrAppendFailed, proxy.ErrReadStaleRoute), transport.RemoteError{Code: transport.RemoteErrorCodeGeneric, Message: proxy.ErrReadStaleRoute.Error()}, io.EOF, fmt.Errorf("%w: %w", message.ErrAppendFailed, io.EOF), context.DeadlineExceeded, message.ErrRouteNotReady, errors.Join(conversation.ErrRouteNotReady, io.EOF)} {
		for _, path := range []string{"/messages", "/channel/messagesync", "/conversation/list", "/conversation/sync"} {
			t.Run(path+"/"+failure.Error(), func(t *testing.T) {
				m := &failingReadMessages{recordingMessageUsecase: &recordingMessageUsecase{syncErr: failure}, failure: failure}
				c := &recordingLegacyConversationSync{recordingConversationUsecase: recordingConversationUsecase{err: failure}, err: failure}
				s := New(Options{Messages: m, Conversations: c})
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"uid":"u","login_uid":"u","channel_id":"g","channel_type":2,"message_ids":[1]}`))
				req.Header.Set("Content-Type", "application/json")
				s.Handler().ServeHTTP(rec, req)
				if rec.Code != 503 {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
				}
				var body struct {
					Code   string `json:"code"`
					Status int    `json:"status"`
					Msg    string `json:"msg"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Code != "unavailable" || body.Status != 503 || body.Msg == "" {
					t.Fatalf("response=%s", rec.Body.String())
				}
			})
		}
	}
}

func TestMessageReadErrorsDoNotRetryBusinessOrUnknownFailures(t *testing.T) {
	for _, failure := range []error{metadb.ErrStaleMeta, message.ErrSyncMembershipRequired, message.ErrSyncChannelDisbanded, message.ErrSyncPageScanBudget, message.ErrAppendFailed, errors.New("disk corruption"), errors.New("EOF"), context.Canceled} {
		m := &failingReadMessages{recordingMessageUsecase: &recordingMessageUsecase{}, failure: failure}
		s := New(Options{Messages: m})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBufferString(`{"login_uid":"u","channel_id":"g","channel_type":2,"message_ids":[1]}`))
		req.Header.Set("Content-Type", "application/json")
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 400 || bytes.Contains(rec.Body.Bytes(), []byte(`"code":"unavailable"`)) {
			t.Fatalf("%v: %d %s", failure, rec.Code, rec.Body.String())
		}
	}
}
