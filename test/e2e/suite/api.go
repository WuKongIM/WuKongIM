//go:build e2e

package suite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// HTTPStatusError retains a public HTTP API's non-2xx response details.
type HTTPStatusError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
}

// Error returns the same concise non-2xx diagnostic used by the e2e helpers.
func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("%s %s returned %d: %s", e.Method, e.URL, e.StatusCode, strings.TrimSpace(e.Body))
}

// MessageSendResponse is the public /message/send response model used by e2e tests.
type MessageSendResponse struct {
	MessageID  int64  `json:"message_id"`
	MessageSeq uint64 `json:"message_seq"`
	Reason     uint8  `json:"reason"`
}

// ConversationListPage is the public /conversation/list response page.
type ConversationListPage struct {
	Conversations []ConversationListItem `json:"conversations"`
	More          int                    `json:"more"`
}

// ConversationListItem is one recent-conversation row returned by /conversation/list.
type ConversationListItem struct {
	ChannelID    string                   `json:"channel_id"`
	ChannelType  int64                    `json:"channel_type"`
	ActiveAt     int64                    `json:"active_at"`
	SparseActive bool                     `json:"sparse_active"`
	Unread       uint64                   `json:"unread"`
	LastMessage  *ConversationLastMessage `json:"last_message"`
}

// ConversationLastMessage is the hydrated tail message on a conversation row.
type ConversationLastMessage struct {
	MessageID   uint64 `json:"message_id"`
	MessageSeq  uint64 `json:"message_seq"`
	FromUID     string `json:"from_uid"`
	ClientMsgNo string `json:"client_msg_no"`
	Payload     []byte `json:"payload"`
}

// PostJSON posts one JSON body to a public API URL and optionally decodes the JSON response.
func PostJSON(ctx context.Context, url string, body any, out any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return postJSONBytes(ctx, url, data, out)
}

func postJSONBytes(ctx context.Context, url string, data []byte, out any) ([]byte, error) {
	return postJSONBytesWithClient(ctx, http.DefaultClient, url, data, out)
}

func postJSONBytesWithClient(ctx context.Context, client *http.Client, url string, data []byte, out any) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPStatusError{
			Method:     http.MethodPost,
			URL:        url,
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
		}
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return nil, fmt.Errorf("decode POST %s: %w body=%s", url, err, strings.TrimSpace(string(respBody)))
		}
	}
	return respBody, nil
}

// GetJSON fetches one JSON response from a public HTTP URL.
func GetJSON(ctx context.Context, url string, out any) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPStatusError{
			Method:     http.MethodGet,
			URL:        url,
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
		}
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return nil, fmt.Errorf("decode GET %s: %w body=%s", url, err, strings.TrimSpace(string(respBody)))
		}
	}
	return respBody, nil
}

// PostChannel upserts a legacy-compatible channel through the public HTTP API.
func PostChannel(ctx context.Context, apiAddr string, body map[string]any) error {
	_, err := PostJSON(ctx, "http://"+apiAddr+"/channel", body, nil)
	return err
}

// PostMessageSend sends one message through the public HTTP API.
func PostMessageSend(ctx context.Context, apiAddr string, body map[string]any) (MessageSendResponse, error) {
	var out MessageSendResponse
	_, err := PostJSON(ctx, "http://"+apiAddr+"/message/send", body, &out)
	return out, err
}

// IsMessageSendRetryRequired reports whether err is the public message-send recovery signal.
func IsMessageSendRetryRequired(err error) bool {
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr == nil || statusErr.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	var response struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(statusErr.Body), &response); err != nil {
		return false
	}
	return response.Error == "retry required"
}

// PostMessageSendEventually retries only the public recovery signal with one stable JSON body.
func PostMessageSendEventually(ctx context.Context, apiAddr string, body map[string]any) (MessageSendResponse, error) {
	return postMessageSendEventuallyWithClient(ctx, http.DefaultClient, apiAddr, body)
}

func postMessageSendEventuallyWithClient(ctx context.Context, client *http.Client, apiAddr string, body map[string]any) (MessageSendResponse, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return MessageSendResponse{}, err
	}
	url := "http://" + apiAddr + "/message/send"
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	attempts := 0
	var lastStatusErr error
	for {
		attempts++
		var out MessageSendResponse
		_, err = postJSONBytesWithClient(ctx, client, url, data, &out)
		if err == nil {
			return out, nil
		}
		if IsMessageSendRetryRequired(err) {
			lastStatusErr = err
		} else {
			contextErr := ctx.Err()
			if lastStatusErr != nil && contextErr != nil && errors.Is(err, contextErr) {
				return MessageSendResponse{}, messageSendRetryExhaustedError(url, attempts, contextErr, lastStatusErr)
			}
			return MessageSendResponse{}, err
		}

		select {
		case <-ctx.Done():
			return MessageSendResponse{}, messageSendRetryExhaustedError(url, attempts, ctx.Err(), lastStatusErr)
		case <-ticker.C:
		}
	}
}

func messageSendRetryExhaustedError(url string, attempts int, contextErr, lastStatusErr error) error {
	attemptLabel := "attempts"
	if attempts == 1 {
		attemptLabel = "attempt"
	}
	return fmt.Errorf("POST %s retry exhausted after %d %s: %w", url, attempts, attemptLabel, errors.Join(contextErr, lastStatusErr))
}

// PostConversationList fetches one public /conversation/list page.
func PostConversationList(ctx context.Context, apiAddr, uid string, limit int) (ConversationListPage, error) {
	var page ConversationListPage
	_, err := PostJSON(ctx, "http://"+apiAddr+"/conversation/list", map[string]any{
		"uid":   uid,
		"limit": limit,
	}, &page)
	return page, err
}

// RequireConversationEventually waits for a conversation row to satisfy a scenario assertion.
func RequireConversationEventually(t *testing.T, node StartedNode, uid, channelID string, check func(ConversationListItem) error) ConversationListPage {
	t.Helper()
	return RequireConversationEventuallyWithin(t, node, uid, channelID, 5*time.Second, check)
}

// RequireConversationEventuallyWithin waits up to timeout for a conversation row to satisfy a scenario assertion.
func RequireConversationEventuallyWithin(t *testing.T, node StartedNode, uid, channelID string, timeout time.Duration, check func(ConversationListItem) error) ConversationListPage {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastPage ConversationListPage
	var lastErr error
	for {
		page, err := PostConversationList(ctx, node.APIAddr(), uid, 10)
		if err == nil {
			lastPage = page
			if item, ok := FindConversation(page, channelID); ok {
				if checkErr := check(item); checkErr == nil {
					return page
				} else {
					lastErr = checkErr
				}
			} else {
				lastErr = fmt.Errorf("conversation %s not found in %#v", channelID, page.Conversations)
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("conversation %s for uid %s timed out: lastPage=%#v lastErr=%v\n%s", channelID, uid, lastPage, lastErr, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

// RequireConversationAbsent fails when uid already has a row for channelID.
func RequireConversationAbsent(t *testing.T, ctx context.Context, node StartedNode, uid, channelID string) {
	t.Helper()
	page, err := PostConversationList(ctx, node.APIAddr(), uid, 10)
	require.NoError(t, err)
	if item, ok := FindConversation(page, channelID); ok {
		t.Fatalf("uid %s unexpectedly has non-subscriber conversation: %#v\n%s", uid, item, node.DumpDiagnostics())
	}
}

// FindConversation returns the first page item for channelID.
func FindConversation(page ConversationListPage, channelID string) (ConversationListItem, bool) {
	for _, item := range page.Conversations {
		if item.ChannelID == channelID {
			return item, true
		}
	}
	return ConversationListItem{}, false
}
