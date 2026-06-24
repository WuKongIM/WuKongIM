package webhook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPSenderPostsEventQueryAndBody(t *testing.T) {
	var gotEvent string
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEvent = r.URL.Query().Get("event")
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		gotBody = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender := NewHTTPSender(HTTPSenderOptions{
		Addr:    server.URL + "/webhook",
		Timeout: time.Second,
	})
	if err := sender.Send(context.Background(), SendRequest{Event: EventMsgNotify, Body: []byte(`[{"message_id":1}]`)}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotEvent != EventMsgNotify {
		t.Fatalf("event query = %q, want %q", gotEvent, EventMsgNotify)
	}
	if gotBody != `[{"message_id":1}]` {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestHTTPSenderReturnsErrorOnNonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	sender := NewHTTPSender(HTTPSenderOptions{Addr: server.URL, Timeout: time.Second})
	if err := sender.Send(context.Background(), SendRequest{Event: EventMsgNotify, Body: []byte(`[]`)}); err == nil {
		t.Fatalf("Send() error = nil, want error for non-200 status")
	}
}
