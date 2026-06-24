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
	var gotMethod string
	var gotContentType string
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEvent = r.URL.Query().Get("event")
		gotToken = r.URL.Query().Get("token")
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		gotBody = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender := NewHTTPSender(HTTPSenderOptions{
		Addr:    server.URL + "/webhook?token=keep",
		Timeout: time.Second,
	})
	if err := sender.Send(context.Background(), SendRequest{Event: EventMsgNotify, Body: []byte(`[{"message_id":1}]`)}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want %q", gotMethod, http.MethodPost)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotEvent != EventMsgNotify {
		t.Fatalf("event query = %q, want %q", gotEvent, EventMsgNotify)
	}
	if gotToken != "keep" {
		t.Fatalf("token query = %q, want keep", gotToken)
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

func TestHTTPSenderTimeoutReturnsPromptly(t *testing.T) {
	release := make(chan struct{})
	released := false
	closeRelease := func() {
		if !released {
			close(release)
			released = true
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer closeRelease()

	sender := NewHTTPSender(HTTPSenderOptions{Addr: server.URL, Timeout: 30 * time.Millisecond})
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- sender.Send(context.Background(), SendRequest{Event: EventMsgNotify, Body: []byte(`[]`)})
	}()
	var err error
	select {
	case err = <-done:
		closeRelease()
	case <-time.After(500 * time.Millisecond):
		closeRelease()
		t.Fatalf("Send() did not return before timeout guard")
	}
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("Send() error = nil, want timeout error")
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("Send() elapsed = %v, want prompt timeout", elapsed)
	}
}
