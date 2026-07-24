//go:build e2e

package suite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPostJSONNon2xxReturnsHTTPStatusError(t *testing.T) {
	const responseBody = "  rejected by POST  \n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(responseBody))
	}))
	defer server.Close()

	_, err := PostJSON(context.Background(), server.URL+"/message/send", map[string]any{"value": 1}, nil)
	require.Error(t, err)
	var statusErr *HTTPStatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, http.MethodPost, statusErr.Method)
	require.Equal(t, server.URL+"/message/send", statusErr.URL)
	require.Equal(t, http.StatusConflict, statusErr.StatusCode)
	require.Equal(t, responseBody, statusErr.Body)
	require.Equal(t, fmt.Sprintf("POST %s/message/send returned 409: rejected by POST", server.URL), err.Error())
}

func TestGetJSONNon2xxReturnsHTTPStatusError(t *testing.T) {
	const responseBody = "manager unavailable\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(responseBody))
	}))
	defer server.Close()

	_, err := GetJSON(context.Background(), server.URL+"/manager/messages", nil)
	require.Error(t, err)
	var statusErr *HTTPStatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, http.MethodGet, statusErr.Method)
	require.Equal(t, server.URL+"/manager/messages", statusErr.URL)
	require.Equal(t, http.StatusBadGateway, statusErr.StatusCode)
	require.Equal(t, responseBody, statusErr.Body)
	require.Equal(t, fmt.Sprintf("GET %s/manager/messages returned 502: manager unavailable", server.URL), err.Error())
}

func TestIsMessageSendRetryRequired(t *testing.T) {
	t.Run("wrapped exact status", func(t *testing.T) {
		err := fmt.Errorf("wrapped: %w", &HTTPStatusError{
			Method:     http.MethodPost,
			URL:        "http://node/message/send",
			StatusCode: http.StatusServiceUnavailable,
			Body:       " \n{\"error\":\"retry required\"}\t",
		})
		require.True(t, IsMessageSendRetryRequired(err))
	})

	tests := []struct {
		name string
		err  error
	}{
		{name: "nil", err: nil},
		{name: "transport error", err: errors.New("connection refused")},
		{name: "400 exact body", err: &HTTPStatusError{StatusCode: http.StatusBadRequest, Body: `{"error":"retry required"}`}},
		{name: "500 exact body", err: &HTTPStatusError{StatusCode: http.StatusInternalServerError, Body: `{"error":"retry required"}`}},
		{name: "503 other error", err: &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Body: `{"error":"temporarily unavailable"}`}},
		{name: "503 malformed JSON", err: &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Body: `{"error":`}},
		{name: "503 missing error", err: &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Body: `{"message":"retry required"}`}},
		{name: "503 non-string error", err: &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Body: `{"error":503}`}},
		{name: "503 nested error", err: &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Body: `{"result":{"error":"retry required"}}`}},
		{name: "503 title-case error key", err: &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Body: `{"Error":"retry required"}`}},
		{name: "503 upper-case error key", err: &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Body: `{"ERROR":"retry required"}`}},
		{name: "503 imprecise error", err: &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Body: `{"error":"retry required "}`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.False(t, IsMessageSendRetryRequired(tt.err))
		})
	}
}

func TestPostMessageSendEventuallyRetriesExactStatusWithStableBody(t *testing.T) {
	var requestBodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/message/send", r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		requestBody, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		requestBodies = append(requestBodies, append([]byte(nil), requestBody...))
		w.Header().Set("Content-Type", "application/json")
		if len(requestBodies) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"retry required"}`))
			return
		}
		_, _ = w.Write([]byte(`{"message_id":41,"message_seq":7,"reason":1}`))
	}))
	defer server.Close()

	body := map[string]any{
		"from_uid":      "sender-a",
		"channel_id":    "group-a",
		"channel_type":  2,
		"client_msg_no": "stable-client-message-no",
		"payload":       "c3RhYmxlIHBheWxvYWQ=",
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := PostMessageSendEventually(ctx, strings.TrimPrefix(server.URL, "http://"), body)
	require.NoError(t, err)
	require.Equal(t, MessageSendResponse{MessageID: 41, MessageSeq: 7, Reason: 1}, resp)
	require.Len(t, requestBodies, 3)
	require.Equal(t, requestBodies[0], requestBodies[1])
	require.Equal(t, requestBodies[0], requestBodies[2])

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(requestBodies[0], &decoded))
	require.Equal(t, "stable-client-message-no", decoded["client_msg_no"])
	require.Equal(t, "c3RhYmxlIHBheWxvYWQ=", decoded["payload"])
}

func TestPostMessageSendEventuallyDoesNotRetryHTTPStatusVariants(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "400", statusCode: http.StatusBadRequest, body: `{"error":"retry required"}`},
		{name: "500", statusCode: http.StatusInternalServerError, body: `{"error":"retry required"}`},
		{name: "other 503", statusCode: http.StatusServiceUnavailable, body: `{"error":"temporarily unavailable"}`},
		{name: "malformed 503", statusCode: http.StatusServiceUnavailable, body: `{"error":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts++
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			_, err := PostMessageSendEventually(context.Background(), strings.TrimPrefix(server.URL, "http://"), testMessageSendBody())
			require.Error(t, err)
			require.Equal(t, 1, attempts)
			require.False(t, IsMessageSendRetryRequired(err))
		})
	}
}

func TestPostMessageSendEventuallyDoesNotRetryTransportError(t *testing.T) {
	sentinel := errors.New("transport failed")
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return nil, sentinel
	})}

	_, err := postMessageSendEventuallyWithClient(context.Background(), client, "node.invalid", testMessageSendBody())
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 1, attempts)
}

func TestPostMessageSendEventuallyDoesNotRetryDecodeError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message_id":`))
	}))
	defer server.Close()

	_, err := PostMessageSendEventually(context.Background(), strings.TrimPrefix(server.URL, "http://"), testMessageSendBody())
	require.Error(t, err)
	require.ErrorContains(t, err, "decode POST")
	require.Equal(t, 1, attempts)
}

func TestPostMessageSendEventuallyReturnsFirstSuccessfulBusinessResponse(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		_, _ = w.Write([]byte(`{"message_id":0,"message_seq":0,"reason":99}`))
	}))
	defer server.Close()

	resp, err := PostMessageSendEventually(context.Background(), strings.TrimPrefix(server.URL, "http://"), testMessageSendBody())
	require.NoError(t, err)
	require.Equal(t, MessageSendResponse{Reason: 99}, resp)
	require.Equal(t, 1, attempts)
}

func TestPostMessageSendEventuallyContextExpiryRetainsStatusError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body: &cancelOnCloseReadCloser{
				Reader: strings.NewReader(`{"error":"retry required"}`),
				cancel: cancel,
			},
			Request: req,
		}, nil
	})}

	_, err := postMessageSendEventuallyWithClient(ctx, client, "node.invalid", testMessageSendBody())
	require.Error(t, err)
	require.ErrorContains(t, err, "after 1 attempt")
	require.ErrorIs(t, err, context.Canceled)
	var statusErr *HTTPStatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, http.StatusServiceUnavailable, statusErr.StatusCode)
	require.Equal(t, `{"error":"retry required"}`, statusErr.Body)
	require.Equal(t, 1, attempts)
}

func TestPostMessageSendEventuallyDeadlineExpiryRetainsStatusError(t *testing.T) {
	ctx := &controlledDeadlineContext{
		Context: context.Background(),
		done:    make(chan struct{}),
	}
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body: &cancelOnCloseReadCloser{
				Reader: strings.NewReader(`{"error":"retry required"}`),
				cancel: ctx.expire,
			},
			Request: req,
		}, nil
	})}

	_, err := postMessageSendEventuallyWithClient(ctx, client, "node.invalid", testMessageSendBody())
	require.Error(t, err)
	require.ErrorContains(t, err, "after 1 attempt")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	var statusErr *HTTPStatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, http.StatusServiceUnavailable, statusErr.StatusCode)
	require.Equal(t, `{"error":"retry required"}`, statusErr.Body)
	require.Equal(t, 1, attempts)
}

func TestPostMessageSendEventuallyReturnsCurrentTerminalStatusWhenContextAlsoCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		status := http.StatusServiceUnavailable
		body := io.ReadCloser(io.NopCloser(strings.NewReader(`{"error":"retry required"}`)))
		if attempts == 2 {
			status = http.StatusInternalServerError
			body = &cancelOnCloseReadCloser{
				Reader: strings.NewReader(`{"error":"terminal"}`),
				cancel: cancel,
			}
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       body,
			Request:    req,
		}, nil
	})}

	_, err := postMessageSendEventuallyWithClient(ctx, client, "node.invalid", testMessageSendBody())
	require.Error(t, err)
	require.NotErrorIs(t, err, context.Canceled)
	var statusErr *HTTPStatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, http.StatusInternalServerError, statusErr.StatusCode)
	require.Equal(t, `{"error":"terminal"}`, statusErr.Body)
	require.Equal(t, 2, attempts)
}

func testMessageSendBody() map[string]any {
	return map[string]any{
		"from_uid":      "sender-a",
		"channel_id":    "group-a",
		"channel_type":  2,
		"client_msg_no": "client-message-no",
		"payload":       "cGF5bG9hZA==",
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type cancelOnCloseReadCloser struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *cancelOnCloseReadCloser) Close() error {
	r.cancel()
	return nil
}

type controlledDeadlineContext struct {
	context.Context
	done chan struct{}
}

func (c *controlledDeadlineContext) Done() <-chan struct{} {
	return c.done
}

func (c *controlledDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (c *controlledDeadlineContext) expire() {
	close(c.done)
}

func TestPostConversationListDecodesPublicResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/conversation/list", r.URL.Path)

		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "u1", req["uid"])
		require.Equal(t, float64(10), req["limit"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"conversations":[{"channel_id":"g1","channel_type":2,"sparse_active":false,"last_message":{"message_id":7,"message_seq":3,"from_uid":"u2","client_msg_no":"c1","payload":"aGVsbG8="}}],"more":0}`))
	}))
	defer server.Close()

	page, err := PostConversationList(context.Background(), strings.TrimPrefix(server.URL, "http://"), "u1", 10)
	require.NoError(t, err)
	require.Len(t, page.Conversations, 1)
	require.Equal(t, "g1", page.Conversations[0].ChannelID)
	require.Equal(t, []byte("hello"), page.Conversations[0].LastMessage.Payload)
}

func TestGetJSONDecodesPublicResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/manager/slots", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":1}`))
	}))
	defer server.Close()

	var out struct {
		Total int `json:"total"`
	}
	_, err := GetJSON(context.Background(), server.URL+"/manager/slots", &out)
	require.NoError(t, err)
	require.Equal(t, 1, out.Total)
}

func TestFetchTopDeliveryDecodesPublicResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/top/v1/snapshot", r.URL.Path)
		require.Equal(t, "delivery", r.URL.Query().Get("view"))
		require.Equal(t, "2s", r.URL.Query().Get("window"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"delivery":{"ack_bindings":3,"retry_queue_depth":2}}`))
	}))
	defer server.Close()

	delivery, err := FetchTopDelivery(context.Background(), strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	require.Equal(t, int64(3), delivery.AckBindings)
	require.Equal(t, int64(2), delivery.RetryQueueDepth)
}

func TestParseMetricSampleMatchesLabels(t *testing.T) {
	name, labels, value, ok := parseMetricSample(`wukongim_authority_recipient_dispatch_total{phase="conversation",result="ok"} 3`)

	require.True(t, ok)
	require.Equal(t, "wukongim_authority_recipient_dispatch_total", name)
	require.Equal(t, map[string]string{"phase": "conversation", "result": "ok"}, labels)
	require.Equal(t, float64(3), value)
	require.True(t, metricLabelsMatch(labels, map[string]string{"result": "ok"}))
	require.False(t, metricLabelsMatch(labels, map[string]string{"result": "error"}))
}

func TestFetchMetricSamplesReturnsOnePublicSnapshot(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/metrics", r.URL.Path)
		_, _ = w.Write([]byte(`# HELP ignored comment
wukongim_ants_pool_waiting{component="channelappend",pool="advance"} 3
wukongim_delivery_recipient_worker_queue_depth 7
malformed
`))
	}))
	defer server.Close()

	samples, err := FetchMetricSamples(context.Background(), strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	require.Equal(t, 1, requests)
	require.Equal(t, []MetricSample{
		{
			Name:   "wukongim_ants_pool_waiting",
			Labels: map[string]string{"component": "channelappend", "pool": "advance"},
			Value:  3,
		},
		{
			Name:   "wukongim_delivery_recipient_worker_queue_depth",
			Labels: map[string]string{},
			Value:  7,
		},
	}, samples)
}
