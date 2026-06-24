# internalv2 webhook workqueue migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the legacy webhook behavior into `internalv2` as a node-local, bounded, best-effort runtime using `pkg/workqueue` for high-throughput event admission and batching.

**Architecture:** Add `internalv2/runtime/webhook` as a typed runtime that owns webhook DTOs, bounded queues, HTTP delivery, finite retry, and low-cardinality observations. Wire it only from `internalv2/app`, with adapters from channel append post-commit effects and presence activation/deactivation; keep `access/*` protocol adapters thin and keep channel append SENDACK independent from webhook success.

**Tech Stack:** Go, `pkg/workqueue.BoundedBatchPool`, `pkg/workqueue.ShardedMailbox`, `net/http`, `encoding/json`, `compress/gzip`, existing `internalv2` app lifecycle and channelappend post-commit hooks.

---

## Scope

This plan implements these legacy webhook events for `internalv2`:

- `msg.notify`: committed durable message batches.
- `msg.offline`: offline recipient chunks after presence resolution.
- `user.onlinestatus`: batched online/offline route status strings.

Reliability boundary:

- Best-effort, bounded, in-memory queues.
- Finite retry inside the webhook runtime.
- Queue-full and retry-exhausted events are dropped with observation/logging.
- Webhook failure never changes durable append success, SENDACK, conversation active admission, or owner delivery.
- Process crash replay is out of scope for this plan.

Transport boundary:

- HTTP delivery is implemented in this plan.
- The runtime defines a `Sender` interface so gRPC can be added in a separate plan after introducing a root-level `pkg/wkhook` protocol package and the `google.golang.org/grpc` dependency. The current root repo has no `pkg/wkhook` package and no gRPC dependency.

## File Structure

- Create: `internalv2/runtime/webhook/FLOW.md`
  - Documents runtime ownership, event flow, bounded admission, retry, and performance rules.
- Create: `internalv2/runtime/webhook/types.go`
  - Defines `Event`, `Message`, `OfflineMessage`, `OnlineStatus`, `Options`, and observer contracts.
- Create: `internalv2/runtime/webhook/sender.go`
  - Defines `Sender`, `SendRequest`, `HTTPSender`, event constants, JSON encoding, and HTTP success rules.
- Create: `internalv2/runtime/webhook/runtime.go`
  - Owns `BoundedBatchPool` for notify and online status, `ShardedMailbox` for offline chunks, lifecycle, admission, retry, and queue snapshots.
- Create: `internalv2/runtime/webhook/mapper.go`
  - Builds legacy-compatible JSON shapes from runtime event DTOs.
- Create: `internalv2/runtime/webhook/runtime_test.go`
  - Covers batching, queue overflow, retry exhaustion, close/drain, and event filtering.
- Create: `internalv2/runtime/webhook/sender_test.go`
  - Covers HTTP URL event query, JSON body shape, success/failure classification, and request timeout.
- Create: `internalv2/runtime/webhook/mapper_test.go`
  - Covers `MessageResp`-compatible JSON and offline UID compression.
- Modify: `internalv2/runtime/channelappend/contracts.go`
  - Add a batch offline observer interface without removing the existing single-recipient interface.
- Modify: `internalv2/runtime/channelappend/delivery.go`
  - Use the batch observer path when available and preserve single-recipient behavior for existing plugin receive hooks.
- Modify: `internalv2/runtime/channelappend/delivery_test.go`
  - Add regression tests proving offline observer batching does not enqueue one item per UID for large recipient sets.
- Modify: `internalv2/usecase/presence/app.go`
  - Add an optional observer dependency.
- Modify: `internalv2/usecase/presence/ports.go`
  - Extend the local registry port with active local-session reads needed for low-cost online status counts.
- Modify: `internalv2/usecase/presence/types.go`
  - Define route status observation DTOs.
- Modify: `internalv2/usecase/presence/activate.go`
  - Emit online observation only after local route activation succeeds.
- Modify: `internalv2/usecase/presence/deactivate.go`
  - Emit offline observation after the route is removed locally and the route metadata is still available.
- Modify: `internalv2/usecase/presence/*_test.go`
  - Add activation/deactivation observer tests.
- Create: `internalv2/app/webhook.go`
  - Adapts channelappend and presence events into webhook runtime inputs, creates the runtime, and composes hook fanout.
- Modify: `internalv2/app/app.go`
  - Add webhook fields and lifecycle started state.
- Modify: `internalv2/app/config.go`
  - Add `WebhookConfig`, defaults, and validation.
- Modify: `internalv2/app/wiring.go`
  - Call `wireWebhook`, compose `PersistAfterEnqueuer`, compose offline observers, and pass the presence observer into `presence.New`.
- Modify: `internalv2/app/lifecycle.go`
  - Start webhook worker before `channelappend`, stop it after `channelappend`, matching plugin hook semantics.
- Modify: `internalv2/app/*webhook*_test.go` or `internalv2/app/app_test.go`
  - Cover disabled wiring, enabled wiring, lifecycle order, plugin coexistence, and composed hook fanout.
- Modify: `cmd/wukongimv2/config.go`
  - Add `WK_WEBHOOK_*` supported keys and parse them into `app.WebhookConfig`.
- Modify: `cmd/wukongimv2/config_test.go`
  - Cover config/env parsing and invalid values.
- Modify: `wukongim.conf.example`
  - Document new `WK_WEBHOOK_*` keys with English comments.
- Modify: `internalv2/app/FLOW.md`
  - Document webhook placement in the app composition and lifecycle.
- Modify: `internalv2/runtime/channelappend/FLOW.md`
  - Document batch offline observer behavior and the no-SENDACK-impact rule.

## Task 1: Add webhook config defaults and validation

**Files:**
- Modify: `internalv2/app/config.go`
- Test: `internalv2/app/config_test.go`

- [ ] **Step 1: Write failing config default test**

Add this test near the existing app config default tests:

```go
func TestWebhookConfigDefaultsWhenEndpointConfigured(t *testing.T) {
	cfg, err := NormalizeWebhookConfig(WebhookConfig{
		HTTPAddr: "http://127.0.0.1:18080/hook",
	})
	if err != nil {
		t.Fatalf("NormalizeWebhookConfig() error = %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("Enabled = false, want true when HTTPAddr is configured")
	}
	if cfg.QueueSize != 1024 {
		t.Fatalf("QueueSize = %d, want 1024", cfg.QueueSize)
	}
	if cfg.Workers != 16 {
		t.Fatalf("Workers = %d, want 16", cfg.Workers)
	}
	if cfg.NotifyBatchMaxItems != 100 {
		t.Fatalf("NotifyBatchMaxItems = %d, want 100", cfg.NotifyBatchMaxItems)
	}
	if cfg.NotifyBatchMaxWait != 500*time.Millisecond {
		t.Fatalf("NotifyBatchMaxWait = %v, want 500ms", cfg.NotifyBatchMaxWait)
	}
	if cfg.OfflineUIDBatchSize != 512 {
		t.Fatalf("OfflineUIDBatchSize = %d, want 512", cfg.OfflineUIDBatchSize)
	}
	if cfg.RequestTimeout != 5*time.Second {
		t.Fatalf("RequestTimeout = %v, want 5s", cfg.RequestTimeout)
	}
	if cfg.RetryMaxAttempts != 3 {
		t.Fatalf("RetryMaxAttempts = %d, want 3", cfg.RetryMaxAttempts)
	}
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'TestWebhookConfigDefaultsWhenEndpointConfigured' -count=1
```

Expected: compile failure because `WebhookConfig` and `NormalizeWebhookConfig` do not exist.

- [ ] **Step 3: Add config type and defaults**

Add `Webhook WebhookConfig` to `Config` after `Delivery DeliveryConfig`.

Add this type and functions near `DeliveryConfig`:

```go
// WebhookConfig controls node-local best-effort webhook delivery.
type WebhookConfig struct {
	// Enabled starts the webhook runtime when at least one endpoint is configured.
	Enabled bool
	// HTTPAddr receives JSON webhook POST requests as {HTTPAddr}?event=<event>.
	HTTPAddr string
	// FocusEvents limits delivered event names. Empty means all supported webhook events are delivered.
	FocusEvents []string
	// QueueSize bounds accepted webhook events waiting in memory before worker execution.
	QueueSize int
	// Workers bounds concurrent webhook sender calls.
	Workers int
	// NotifyBatchMaxItems limits msg.notify messages sent in one webhook request.
	NotifyBatchMaxItems int
	// NotifyBatchMaxWait bounds how long msg.notify waits for adjacent messages before sending a partial batch.
	NotifyBatchMaxWait time.Duration
	// OnlineBatchMaxItems limits user.onlinestatus records sent in one webhook request.
	OnlineBatchMaxItems int
	// OnlineBatchMaxWait bounds how long user.onlinestatus waits for adjacent records before sending a partial batch.
	OnlineBatchMaxWait time.Duration
	// OfflineUIDBatchSize limits offline recipient UIDs sent in one msg.offline request.
	OfflineUIDBatchSize int
	// RequestTimeout bounds one outbound webhook request attempt.
	RequestTimeout time.Duration
	// RetryMaxAttempts bounds attempts for one admitted webhook batch before it is dropped.
	RetryMaxAttempts int
}

func NormalizeWebhookConfig(cfg WebhookConfig) (WebhookConfig, error) {
	cfg = defaultWebhookConfig(cfg)
	if err := validateWebhookConfig(cfg); err != nil {
		return WebhookConfig{}, err
	}
	return cfg, nil
}

func defaultWebhookConfig(cfg WebhookConfig) WebhookConfig {
	if cfg.HTTPAddr != "" {
		cfg.Enabled = true
	}
	if cfg.QueueSize == 0 {
		cfg.QueueSize = 1024
	}
	if cfg.Workers == 0 {
		cfg.Workers = 16
	}
	if cfg.NotifyBatchMaxItems == 0 {
		cfg.NotifyBatchMaxItems = 100
	}
	if cfg.NotifyBatchMaxWait == 0 {
		cfg.NotifyBatchMaxWait = 500 * time.Millisecond
	}
	if cfg.OnlineBatchMaxItems == 0 {
		cfg.OnlineBatchMaxItems = 512
	}
	if cfg.OnlineBatchMaxWait == 0 {
		cfg.OnlineBatchMaxWait = 2 * time.Second
	}
	if cfg.OfflineUIDBatchSize == 0 {
		cfg.OfflineUIDBatchSize = 512
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 5 * time.Second
	}
	if cfg.RetryMaxAttempts == 0 {
		cfg.RetryMaxAttempts = 3
	}
	return cfg
}

func validateWebhookConfig(cfg WebhookConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.HTTPAddr == "" {
		return fmt.Errorf("%w: webhook HTTPAddr is required when webhook is enabled", ErrInvalidConfig)
	}
	if cfg.QueueSize < 0 {
		return fmt.Errorf("%w: webhook QueueSize must be >= 0", ErrInvalidConfig)
	}
	if cfg.Workers < 0 {
		return fmt.Errorf("%w: webhook Workers must be >= 0", ErrInvalidConfig)
	}
	if cfg.NotifyBatchMaxItems < 0 || cfg.OnlineBatchMaxItems < 0 || cfg.OfflineUIDBatchSize < 0 {
		return fmt.Errorf("%w: webhook batch sizes must be >= 0", ErrInvalidConfig)
	}
	if cfg.NotifyBatchMaxWait < 0 || cfg.OnlineBatchMaxWait < 0 || cfg.RequestTimeout < 0 {
		return fmt.Errorf("%w: webhook durations must be >= 0", ErrInvalidConfig)
	}
	if cfg.RetryMaxAttempts < 0 {
		return fmt.Errorf("%w: webhook RetryMaxAttempts must be >= 0", ErrInvalidConfig)
	}
	return nil
}
```

Wire it in `applyConfigDefaults` before `Plugin`:

```go
var err error
a.cfg.Webhook, err = NormalizeWebhookConfig(a.cfg.Webhook)
if err != nil {
	return err
}
```

- [ ] **Step 4: Run config test**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'TestWebhookConfigDefaultsWhenEndpointConfigured' -count=1
```

Expected: PASS.

- [ ] **Step 5: Add invalid config tests**

Add table tests for negative values and enabled-without-endpoint:

```go
func TestWebhookConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  WebhookConfig
	}{
		{name: "enabled without endpoint", cfg: WebhookConfig{Enabled: true}},
		{name: "negative queue", cfg: WebhookConfig{HTTPAddr: "http://127.0.0.1/hook", QueueSize: -1}},
		{name: "negative workers", cfg: WebhookConfig{HTTPAddr: "http://127.0.0.1/hook", Workers: -1}},
		{name: "negative notify batch", cfg: WebhookConfig{HTTPAddr: "http://127.0.0.1/hook", NotifyBatchMaxItems: -1}},
		{name: "negative online wait", cfg: WebhookConfig{HTTPAddr: "http://127.0.0.1/hook", OnlineBatchMaxWait: -1}},
		{name: "negative retry", cfg: WebhookConfig{HTTPAddr: "http://127.0.0.1/hook", RetryMaxAttempts: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NormalizeWebhookConfig(tt.cfg); err == nil {
				t.Fatalf("NormalizeWebhookConfig() error = nil, want error")
			}
		})
	}
}
```

- [ ] **Step 6: Run app config tests**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'WebhookConfig' -count=1
```

Expected: PASS.

## Task 2: Implement webhook runtime types, mapper, and HTTP sender

**Files:**
- Create: `internalv2/runtime/webhook/FLOW.md`
- Create: `internalv2/runtime/webhook/types.go`
- Create: `internalv2/runtime/webhook/mapper.go`
- Create: `internalv2/runtime/webhook/sender.go`
- Test: `internalv2/runtime/webhook/mapper_test.go`
- Test: `internalv2/runtime/webhook/sender_test.go`

- [ ] **Step 1: Write mapper tests**

Create `internalv2/runtime/webhook/mapper_test.go`:

```go
package webhook

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestBuildNotifyBodyMapsCommittedMessages(t *testing.T) {
	body, err := buildNotifyBody([]Message{{
		MessageID:         42,
		MessageSeq:        7,
		ChannelID:         "group-a",
		ChannelType:       2,
		FromUID:           "alice",
		ClientMsgNo:       "client-1",
		ServerTimestampMS: time.Unix(10, 0).UnixMilli(),
		Payload:           []byte("hello"),
		RedDot:            true,
	}})
	if err != nil {
		t.Fatalf("buildNotifyBody() error = %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(body))
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	msg := got[0]
	if msg["message_id"] != float64(42) || msg["message_seq"] != float64(7) {
		t.Fatalf("message id/seq = %#v", msg)
	}
	if msg["channel_id"] != "group-a" || msg["from_uid"] != "alice" {
		t.Fatalf("channel/from = %#v", msg)
	}
	if msg["payload"] != base64.StdEncoding.EncodeToString([]byte("hello")) {
		t.Fatalf("payload = %v", msg["payload"])
	}
	header, ok := msg["header"].(map[string]any)
	if !ok {
		t.Fatalf("header = %#v, want object", msg["header"])
	}
	if header["red_dot"] != float64(1) || header["sync_once"] != float64(0) || header["no_persist"] != float64(0) {
		t.Fatalf("header = %#v", header)
	}
}

func TestBuildOfflineBodyChunksAndCompressesUIDs(t *testing.T) {
	body, err := buildOfflineBody(OfflineMessage{
		Message: Message{
			MessageID:         10,
			MessageSeq:        11,
			ChannelID:         "group-a",
			ChannelType:       2,
			FromUID:           "alice",
			ServerTimestampMS: time.Unix(10, 0).UnixMilli(),
			Payload:           []byte("payload"),
		},
		ToUIDs: []string{"u1", "u2", "u3"},
	}, 2)
	if err != nil {
		t.Fatalf("buildOfflineBody() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v body=%s", err, string(body))
	}
	if got["compress"] != "gzip" {
		t.Fatalf("compress = %v, want gzip", got["compress"])
	}
	if got["compress_to_uids"] == "" {
		t.Fatalf("compress_to_uids is empty")
	}
	if _, exists := got["to_uids"]; exists {
		t.Fatalf("to_uids exists for compressed body: %#v", got)
	}
}
```

- [ ] **Step 2: Write HTTP sender tests**

Create `internalv2/runtime/webhook/sender_test.go`:

```go
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
```

- [ ] **Step 3: Run failing package tests**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/webhook -count=1
```

Expected: compile failure because runtime package code does not exist.

- [ ] **Step 4: Add runtime type definitions**

Create `internalv2/runtime/webhook/types.go`:

```go
package webhook

import (
	"context"
	"time"
)

const (
	// EventMsgNotify reports committed durable messages.
	EventMsgNotify = "msg.notify"
	// EventMsgOffline reports offline recipients for one committed message chunk.
	EventMsgOffline = "msg.offline"
	// EventUserOnlineStatus reports online/offline route status strings.
	EventUserOnlineStatus = "user.onlinestatus"
)

// Message is the event-neutral committed message shape accepted by the webhook runtime.
type Message struct {
	MessageID         uint64
	MessageSeq        uint64
	ChannelID         string
	ChannelType       uint8
	FromUID           string
	ClientMsgNo        string
	ServerTimestampMS int64
	Payload           []byte
	RedDot            bool
	SyncOnce          bool
}

// OfflineMessage carries one committed message plus one bounded recipient UID chunk.
type OfflineMessage struct {
	Message Message
	ToUIDs  []string
}

// OnlineStatus records one legacy-compatible user online status string.
type OnlineStatus struct {
	Value string
}

// SendRequest is one encoded webhook request.
type SendRequest struct {
	Event string
	Body  []byte
}

// Sender delivers one encoded webhook request to an external endpoint.
type Sender interface {
	Send(context.Context, SendRequest) error
}

// Observer receives low-cardinality webhook runtime observations.
type Observer interface {
	ObserveWebhook(Observation)
}

// Observation describes one webhook admission, send, retry, or drop event.
type Observation struct {
	Queue       string
	Event       string
	Result      string
	Items       int
	QueueDepth  int
	QueueSize   int
	Attempt     int
	Duration    time.Duration
	Err          error
}
```

- [ ] **Step 5: Add mapper implementation**

Create `internalv2/runtime/webhook/mapper.go`:

```go
package webhook

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"time"
)

type messageResp struct {
	Header       messageHeader `json:"header"`
	Setting      uint8         `json:"setting"`
	MessageID    uint64        `json:"message_id"`
	MessageIDStr string        `json:"message_idstr"`
	ClientMsgNo  string        `json:"client_msg_no"`
	MessageSeq   uint64        `json:"message_seq"`
	FromUID      string        `json:"from_uid"`
	ChannelID    string        `json:"channel_id"`
	ChannelType  uint8         `json:"channel_type"`
	Timestamp    int32         `json:"timestamp"`
	Payload      []byte        `json:"payload"`
}

type messageHeader struct {
	NoPersist uint8 `json:"no_persist"`
	RedDot    uint8 `json:"red_dot"`
	SyncOnce  uint8 `json:"sync_once"`
}

type offlineResp struct {
	MessageResp
	ToUIDs         []string `json:"to_uids,omitempty"`
	Compress       string   `json:"compress,omitempty"`
	CompressToUIDs string   `json:"compress_to_uids,omitempty"`
	SourceID       int64    `json:"source_id,omitempty"`
}

type MessageResp = messageResp

func buildNotifyBody(messages []Message) ([]byte, error) {
	out := make([]messageResp, 0, len(messages))
	for _, msg := range messages {
		out = append(out, messageRespFromMessage(msg))
	}
	return json.Marshal(out)
}

func buildOfflineBody(message OfflineMessage, compressThreshold int) ([]byte, error) {
	resp := offlineResp{MessageResp: messageRespFromMessage(message.Message)}
	if compressThreshold > 0 && len(message.ToUIDs) >= compressThreshold {
		compressed, err := gzipJSONStringSlice(message.ToUIDs)
		if err != nil {
			return nil, err
		}
		resp.Compress = "gzip"
		resp.CompressToUIDs = base64.StdEncoding.EncodeToString(compressed)
	} else {
		resp.ToUIDs = append([]string(nil), message.ToUIDs...)
	}
	return json.Marshal(resp)
}

func buildOnlineStatusBody(statuses []OnlineStatus) ([]byte, error) {
	values := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.Value != "" {
			values = append(values, status.Value)
		}
	}
	return json.Marshal(values)
}

func messageRespFromMessage(msg Message) messageResp {
	return messageResp{
		Header: messageHeader{
			NoPersist: 0,
			RedDot:    boolToUint8(msg.RedDot),
			SyncOnce:  boolToUint8(msg.SyncOnce),
		},
		MessageID:    msg.MessageID,
		MessageIDStr: uint64String(msg.MessageID),
		ClientMsgNo:  msg.ClientMsgNo,
		MessageSeq:   msg.MessageSeq,
		FromUID:      msg.FromUID,
		ChannelID:    msg.ChannelID,
		ChannelType:  msg.ChannelType,
		Timestamp:    int32(time.UnixMilli(msg.ServerTimestampMS).Unix()),
		Payload:      append([]byte(nil), msg.Payload...),
	}
}

func gzipJSONStringSlice(values []string) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(values); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func boolToUint8(v bool) uint8 {
	if v {
		return 1
	}
	return 0
}
```

Use `strconv.FormatUint` in `uint64String`.

- [ ] **Step 6: Add HTTP sender implementation**

Create `internalv2/runtime/webhook/sender.go`:

```go
package webhook

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// HTTPSenderOptions configures outbound HTTP webhook delivery.
type HTTPSenderOptions struct {
	// Addr is the base webhook URL. The event query parameter is added per request.
	Addr string
	// Timeout bounds one outbound HTTP request attempt.
	Timeout time.Duration
	// Client optionally supplies a shared HTTP client for tests or custom transports.
	Client *http.Client
}

// HTTPSender posts JSON webhook requests to one configured endpoint.
type HTTPSender struct {
	addr    string
	timeout time.Duration
	client  *http.Client
}

// NewHTTPSender creates an HTTP webhook sender.
func NewHTTPSender(opts HTTPSenderOptions) *HTTPSender {
	client := opts.Client
	if client == nil {
		client = &http.Client{}
	}
	return &HTTPSender{addr: opts.Addr, timeout: opts.Timeout, client: client}
}

// Send posts the encoded webhook body as JSON. Only HTTP 200 is classified as success.
func (s *HTTPSender) Send(ctx context.Context, req SendRequest) error {
	if s == nil || s.addr == "" {
		return fmt.Errorf("webhook: http addr is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	target, err := url.Parse(s.addr)
	if err != nil {
		return err
	}
	query := target.Query()
	query.Set("event", req.Event)
	target.RawQuery = query.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(req.Body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook: http status %s", strconv.Itoa(resp.StatusCode))
	}
	return nil
}
```

- [ ] **Step 7: Add `uint64String` and run tests**

Add to `mapper.go`:

```go
func uint64String(v uint64) string {
	return strconv.FormatUint(v, 10)
}
```

Add `strconv` to the imports.

Run:

```bash
GOWORK=off go test ./internalv2/runtime/webhook -run 'TestBuild|TestHTTPSender' -count=1
```

Expected: PASS.

- [ ] **Step 8: Add runtime FLOW**

Create `internalv2/runtime/webhook/FLOW.md` with this content:

```markdown
# internalv2/runtime/webhook Flow

`internalv2/runtime/webhook` owns node-local best-effort webhook delivery for
events produced by `internalv2/app` adapters. It wraps `pkg/workqueue` behind a
typed API so queue pressure, retry, JSON mapping, and endpoint delivery stay out
of access adapters and usecases.

It does not own message durability, subscriber scans, presence authority, plugin
hooks, channel append ordering, or crash replay.

## Event Flow

`msg.notify`
  -> app adapter receives a durable channelappend committed envelope
  -> webhook runtime admits a `Message` into a bounded notify batch pool
  -> worker sends one JSON array to `{HTTPAddr}?event=msg.notify`

`msg.offline`
  -> channelappend recipient processor classifies offline recipients
  -> batch observer passes bounded UID chunks to the app adapter
  -> webhook runtime admits `OfflineMessage` chunks into a sharded mailbox
  -> worker sends one JSON object to `{HTTPAddr}?event=msg.offline`

`user.onlinestatus`
  -> presence usecase observes successful route activation/deactivation
  -> app adapter formats the legacy status string
  -> webhook runtime admits it into a bounded online-status batch pool
  -> worker sends one JSON array to `{HTTPAddr}?event=user.onlinestatus`

## Performance Rules

Webhook admission is bounded and best-effort. Queue-full, closed, canceled, and
retry-exhausted events are observed and dropped. Webhook delivery never changes
SENDACK success, durable append success, conversation-active admission, or owner
delivery.

Large offline fanout must use recipient batches. Do not enqueue one webhook item
per UID for large groups.
```

- [ ] **Step 9: Run package tests**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/webhook -count=1
```

Expected: PASS.

## Task 3: Implement webhook runtime queues and retry

**Files:**
- Modify: `internalv2/runtime/webhook/runtime.go`
- Test: `internalv2/runtime/webhook/runtime_test.go`

- [ ] **Step 1: Write runtime tests**

Create `internalv2/runtime/webhook/runtime_test.go` with tests covering:

```go
func TestRuntimeBatchesNotifyMessages(t *testing.T) {
	sender := &recordingSender{}
	rt, err := New(RuntimeOptions{
		Sender:              sender,
		QueueSize:           16,
		Workers:             1,
		NotifyBatchMaxItems: 2,
		NotifyBatchMaxWait:  time.Hour,
		OnlineBatchMaxItems: 2,
		OnlineBatchMaxWait:  time.Hour,
		OfflineUIDBatchSize: 2,
		RequestTimeout:      time.Second,
		RetryMaxAttempts:    1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rt.Stop(context.Background())
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for i := 0; i < 2; i++ {
		rt.Notify(context.Background(), Message{MessageID: uint64(i + 1), MessageSeq: uint64(i + 1)})
	}
	req := sender.wait(t)
	if req.Event != EventMsgNotify {
		t.Fatalf("event = %q, want %q", req.Event, EventMsgNotify)
	}
	var got []MessageResp
	if err := json.Unmarshal(req.Body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
}

func TestRuntimeDropsOnFullQueueWithoutBlocking(t *testing.T) {
	sender := &blockingSender{started: make(chan struct{}), release: make(chan struct{})}
	observer := &recordingObserver{}
	rt, err := New(RuntimeOptions{
		Sender:              sender,
		Observer:            observer,
		QueueSize:           1,
		Workers:             1,
		NotifyBatchMaxItems: 1,
		NotifyBatchMaxWait:  time.Millisecond,
		OnlineBatchMaxItems: 1,
		OnlineBatchMaxWait:  time.Millisecond,
		OfflineUIDBatchSize: 1,
		RequestTimeout:      time.Second,
		RetryMaxAttempts:    1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer close(sender.release)
	defer rt.Stop(context.Background())
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	rt.Notify(context.Background(), Message{MessageID: 1, MessageSeq: 1})
	<-sender.started
	rt.Notify(context.Background(), Message{MessageID: 2, MessageSeq: 2})
	rt.Notify(context.Background(), Message{MessageID: 3, MessageSeq: 3})
	if !observer.hasResult("msg.notify", "full") {
		t.Fatalf("observer did not record queue full: %#v", observer.snapshot())
	}
}

func TestRuntimeRetriesThenDrops(t *testing.T) {
	sender := &failingSender{}
	observer := &recordingObserver{}
	rt, err := New(RuntimeOptions{
		Sender:              sender,
		Observer:            observer,
		QueueSize:           4,
		Workers:             1,
		NotifyBatchMaxItems: 1,
		NotifyBatchMaxWait:  time.Millisecond,
		OnlineBatchMaxItems: 1,
		OnlineBatchMaxWait:  time.Millisecond,
		OfflineUIDBatchSize: 1,
		RequestTimeout:      time.Second,
		RetryMaxAttempts:    2,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rt.Stop(context.Background())
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	rt.Notify(context.Background(), Message{MessageID: 1, MessageSeq: 1})
	waitUntil(t, time.Second, func() bool { return observer.hasResult("msg.notify", "retry_exhausted") })
	if got := sender.calls.Load(); got != 2 {
		t.Fatalf("sender calls = %d, want 2", got)
	}
}
```

Implement small test fakes inside the same test file: `recordingSender`, `blockingSender`, `failingSender`, `recordingObserver`, and `waitUntil`.

- [ ] **Step 2: Run failing runtime tests**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/webhook -run 'TestRuntime' -count=1
```

Expected: compile failure because `RuntimeOptions`, `New`, and runtime methods do not exist.

- [ ] **Step 3: Implement runtime options and lifecycle**

Create `internalv2/runtime/webhook/runtime.go`:

```go
package webhook

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

// RuntimeOptions configures the bounded webhook runtime.
type RuntimeOptions struct {
	Sender              Sender
	Observer            Observer
	QueueSize           int
	Workers             int
	NotifyBatchMaxItems int
	NotifyBatchMaxWait  time.Duration
	OnlineBatchMaxItems int
	OnlineBatchMaxWait  time.Duration
	OfflineUIDBatchSize int
	RequestTimeout      time.Duration
	RetryMaxAttempts    int
	FocusEvents         []string
}

// Runtime owns bounded webhook event admission and delivery.
type Runtime struct {
	opts    RuntimeOptions
	sender  Sender
	focus   map[string]struct{}
	notify  *workqueue.BoundedBatchPool[Message]
	online  *workqueue.BoundedBatchPool[OnlineStatus]
	offline *workqueue.ShardedMailbox[OfflineMessage]
}

// New creates a webhook runtime. Start opens the queues.
func New(opts RuntimeOptions) (*Runtime, error) {
	if opts.Sender == nil || opts.QueueSize <= 0 || opts.Workers <= 0 {
		return nil, workqueue.ErrInvalidConfig
	}
	rt := &Runtime{opts: opts, sender: opts.Sender, focus: map[string]struct{}{}}
	for _, event := range opts.FocusEvents {
		if event != "" {
			rt.focus[event] = struct{}{}
		}
	}
	return rt, nil
}
```

- [ ] **Step 4: Implement Start/Stop and queue creation**

Add:

```go
// Start opens bounded queue admission.
func (r *Runtime) Start(context.Context) error {
	if r == nil {
		return workqueue.ErrInvalidConfig
	}
	notify, err := workqueue.NewBoundedBatchPool[Message](workqueue.BoundedBatchPoolConfig[Message]{
		Name:      "webhook_notify",
		Workers:   r.opts.Workers,
		QueueSize: r.opts.QueueSize,
		Policy: func(Message) workqueue.BatchOptions {
			return workqueue.BatchOptions{MaxItems: r.opts.NotifyBatchMaxItems, MaxWait: r.opts.NotifyBatchMaxWait}
		},
	}, r.handleNotifyBatch)
	if err != nil {
		return err
	}
	online, err := workqueue.NewBoundedBatchPool[OnlineStatus](workqueue.BoundedBatchPoolConfig[OnlineStatus]{
		Name:      "webhook_online_status",
		Workers:   r.opts.Workers,
		QueueSize: r.opts.QueueSize,
		Policy: func(OnlineStatus) workqueue.BatchOptions {
			return workqueue.BatchOptions{MaxItems: r.opts.OnlineBatchMaxItems, MaxWait: r.opts.OnlineBatchMaxWait}
		},
	}, r.handleOnlineBatch)
	if err != nil {
		_ = notify.Close(context.Background())
		return err
	}
	offline, err := workqueue.NewShardedMailbox[OfflineMessage](workqueue.ShardedMailboxConfig{
		Name:              "webhook_offline",
		Shards:            r.opts.Workers,
		Workers:           r.opts.Workers,
		QueueSizePerShard: r.opts.QueueSize,
		BatchMaxItems:     1,
		BatchMaxWait:      0,
	}, r.handleOfflineBatch)
	if err != nil {
		_ = online.Close(context.Background())
		_ = notify.Close(context.Background())
		return err
	}
	r.notify = notify
	r.online = online
	r.offline = offline
	return nil
}

// Stop closes admission and drains accepted webhook work until ctx expires.
func (r *Runtime) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	return errors.Join(
		r.notify.Close(ctx),
		r.online.Close(ctx),
		r.offline.Close(ctx),
	)
}
```

- [ ] **Step 5: Implement admission methods**

Add:

```go
// Notify admits one committed message for msg.notify delivery.
func (r *Runtime) Notify(ctx context.Context, msg Message) {
	if r == nil || !r.enabled(EventMsgNotify) {
		return
	}
	err := r.notify.Submit(ctx, cloneMessage(msg))
	r.observeAdmission(EventMsgNotify, "webhook_notify", err, r.notify.QueueDepth(), r.notify.QueueCapacity())
}

// Offline admits one bounded recipient chunk for msg.offline delivery.
func (r *Runtime) Offline(ctx context.Context, msg OfflineMessage) {
	if r == nil || !r.enabled(EventMsgOffline) {
		return
	}
	msg = cloneOfflineMessage(msg)
	err := r.offline.Submit(ctx, msg.Message.ChannelID, msg)
	r.observeAdmission(EventMsgOffline, "webhook_offline", err, r.offline.QueueDepth(), r.offline.QueueCapacity())
}

// OnlineStatus admits one legacy-compatible status record.
func (r *Runtime) OnlineStatus(ctx context.Context, status OnlineStatus) {
	if r == nil || !r.enabled(EventUserOnlineStatus) || status.Value == "" {
		return
	}
	err := r.online.Submit(ctx, status)
	r.observeAdmission(EventUserOnlineStatus, "webhook_online_status", err, r.online.QueueDepth(), r.online.QueueCapacity())
}
```

Classify `workqueue.ErrFull` as `"full"`, `workqueue.ErrClosed` as `"closed"`, context errors as `"canceled"` or `"timeout"`, nil as `"accepted"`.

- [ ] **Step 6: Implement send handlers and finite retry**

Add handlers:

```go
func (r *Runtime) handleNotifyBatch(ctx context.Context, batch []Message) error {
	body, err := buildNotifyBody(batch)
	if err != nil {
		r.observeSend(EventMsgNotify, "encode_error", len(batch), 0, err, 0)
		return nil
	}
	r.sendWithRetry(ctx, EventMsgNotify, body, len(batch))
	return nil
}

func (r *Runtime) handleOnlineBatch(ctx context.Context, batch []OnlineStatus) error {
	body, err := buildOnlineStatusBody(batch)
	if err != nil {
		r.observeSend(EventUserOnlineStatus, "encode_error", len(batch), 0, err, 0)
		return nil
	}
	r.sendWithRetry(ctx, EventUserOnlineStatus, body, len(batch))
	return nil
}

func (r *Runtime) handleOfflineBatch(ctx context.Context, batch workqueue.MailboxBatch[OfflineMessage]) error {
	for _, item := range batch.Items {
		body, err := buildOfflineBody(item, r.opts.OfflineUIDBatchSize)
		if err != nil {
			r.observeSend(EventMsgOffline, "encode_error", len(item.ToUIDs), 0, err, 0)
			continue
		}
		r.sendWithRetry(ctx, EventMsgOffline, body, len(item.ToUIDs))
	}
	return nil
}
```

Add `sendWithRetry` with no unbounded loop:

```go
func (r *Runtime) sendWithRetry(ctx context.Context, event string, body []byte, items int) {
	attempts := r.opts.RetryMaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		started := time.Now()
		err := r.sender.Send(ctx, SendRequest{Event: event, Body: body})
		if err == nil {
			r.observeSend(event, "ok", items, attempt, nil, time.Since(started))
			return
		}
		if attempt == attempts {
			r.observeSend(event, "retry_exhausted", items, attempt, err, time.Since(started))
			return
		}
		r.observeSend(event, "retry", items, attempt, err, time.Since(started))
	}
}
```

Keep retry sleeps out of v0 unless tests show tight retry loops cause CPU pressure. A bounded backoff can be added with a single `RetryBackoff` field after measuring.

- [ ] **Step 7: Add clone helpers and observations**

Add clone helpers:

```go
func cloneMessage(msg Message) Message {
	msg.Payload = append([]byte(nil), msg.Payload...)
	return msg
}

func cloneOfflineMessage(msg OfflineMessage) OfflineMessage {
	msg.Message = cloneMessage(msg.Message)
	msg.ToUIDs = append([]string(nil), msg.ToUIDs...)
	return msg
}
```

Add `enabled`, `observeAdmission`, and `observeSend` helpers. Do not include UID, channel id, session id, or endpoint address in observations.

- [ ] **Step 8: Run runtime tests**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/webhook -count=1
```

Expected: PASS.

- [ ] **Step 9: Run workqueue regression tests**

Run:

```bash
GOWORK=off go test ./pkg/workqueue ./internalv2/runtime/webhook -count=1
```

Expected: PASS.

## Task 4: Add batch offline recipient observer to channelappend

**Files:**
- Modify: `internalv2/runtime/channelappend/contracts.go`
- Modify: `internalv2/runtime/channelappend/delivery.go`
- Test: `internalv2/runtime/channelappend/delivery_test.go`

- [ ] **Step 1: Write failing batch observer test**

Add to `delivery_test.go`:

```go
func TestRecipientProcessorUsesBatchOfflineObserverWhenAvailable(t *testing.T) {
	observer := &batchOfflineObserver{}
	ports := recipientPorts{
		presence:                 fakePresenceResolver{},
		offlineRecipientObserver: observer,
	}
	recipients := make([]Recipient, 0, 1000)
	for i := 0; i < 1000; i++ {
		recipients = append(recipients, Recipient{UID: fmt.Sprintf("u-%d", i)})
	}
	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 1, MessageSeq: 1, ChannelID: "group-a", ChannelType: 2},
		Recipients: recipients,
	}, ports)
	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if observer.singleCalls != 0 {
		t.Fatalf("singleCalls = %d, want 0", observer.singleCalls)
	}
	if observer.batchCalls != 1 {
		t.Fatalf("batchCalls = %d, want 1", observer.batchCalls)
	}
	if len(observer.uids) != 1000 {
		t.Fatalf("len(uids) = %d, want 1000", len(observer.uids))
	}
}

type batchOfflineObserver struct {
	singleCalls int
	batchCalls  int
	uids        []string
}

func (o *batchOfflineObserver) ObserveOfflineRecipient(context.Context, OfflineRecipientEvent) {
	o.singleCalls++
}

func (o *batchOfflineObserver) ObserveOfflineRecipients(_ context.Context, event OfflineRecipientsEvent) {
	o.batchCalls++
	o.uids = append(o.uids, event.UIDs...)
}
```

Use the existing fake presence resolver style in the file. If the file has a differently named resolver fake, reuse it and set every UID offline by returning no routes.

- [ ] **Step 2: Run failing test**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend -run 'TestRecipientProcessorUsesBatchOfflineObserverWhenAvailable' -count=1
```

Expected: compile failure because `OfflineRecipientsEvent` and `ObserveOfflineRecipients` do not exist.

- [ ] **Step 3: Add batch interface**

In `contracts.go`, add:

```go
// OfflineRecipientsEvent reports one durable message with a bounded offline UID slice.
type OfflineRecipientsEvent struct {
	// Event is the committed message whose recipients were classified offline.
	Event CommittedEnvelope
	// UIDs are recipient user identifiers without an online route.
	UIDs []string
}

// OfflineRecipientsObserver receives offline recipient candidates in bounded batches.
type OfflineRecipientsObserver interface {
	// ObserveOfflineRecipients records durable recipients with no online route.
	ObserveOfflineRecipients(context.Context, OfflineRecipientsEvent)
}
```

- [ ] **Step 4: Route offline observations through batch path**

Update `observeOfflineRecipients` in `delivery.go`:

```go
	offlineUIDs := make([]string, 0, len(uids))
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		if _, ok := online[uid]; ok {
			continue
		}
		if _, ok := seenOffline[uid]; ok {
			continue
		}
		seenOffline[uid] = struct{}{}
		offlineUIDs = append(offlineUIDs, uid)
	}
	if len(offlineUIDs) == 0 {
		return
	}
	if batchObserver, ok := observer.(OfflineRecipientsObserver); ok {
		batchObserver.ObserveOfflineRecipients(ctx, OfflineRecipientsEvent{
			Event: batch.Event,
			UIDs:  append([]string(nil), offlineUIDs...),
		})
		return
	}
	for _, uid := range offlineUIDs {
		observer.ObserveOfflineRecipient(ctx, OfflineRecipientEvent{Event: batch.Event, UID: uid})
	}
```

- [ ] **Step 5: Preserve existing single observer behavior**

Add or keep a test proving a plain `OfflineRecipientObserver` still receives one call per offline UID:

```go
func TestRecipientProcessorKeepsSingleOfflineObserverFallback(t *testing.T) {
	observer := &singleOfflineObserver{}
	ports := recipientPorts{
		presence:                 fakePresenceResolver{},
		offlineRecipientObserver: observer,
	}
	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 1, MessageSeq: 1, ChannelID: "group-a", ChannelType: 2},
		Recipients: []Recipient{{UID: "u1"}, {UID: "u2"}},
	}, ports)
	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got := len(observer.events); got != 2 {
		t.Fatalf("events = %d, want 2", got)
	}
}
```

- [ ] **Step 6: Run channelappend delivery tests**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/channelappend -run 'OfflineObserver|OfflineRecipient|RecipientProcessor' -count=1
```

Expected: PASS.

## Task 5: Add presence status observer

**Files:**
- Modify: `internalv2/usecase/presence/types.go`
- Modify: `internalv2/usecase/presence/ports.go`
- Modify: `internalv2/usecase/presence/app.go`
- Modify: `internalv2/usecase/presence/activate.go`
- Modify: `internalv2/usecase/presence/deactivate.go`
- Test: `internalv2/usecase/presence/*_test.go`

- [ ] **Step 1: Write presence observer tests**

Add tests:

```go
func TestActivateObservesOnlineAfterLocalActivation(t *testing.T) {
	observer := &recordingPresenceObserver{}
	app := newPresenceAppForTest(t, presenceTestOptions{Observer: observer})
	err := app.Activate(context.Background(), ActivateCommand{
		UID: "u1", DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1,
		ConnectedUnix: 10, SessionID: 100,
	})
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if len(observer.events) != 1 {
		t.Fatalf("events = %d, want 1", len(observer.events))
	}
	event := observer.events[0]
	if event.Kind != PresenceOnline || event.Route.UID != "u1" || event.Route.SessionID != 100 {
		t.Fatalf("event = %#v", event)
	}
}

func TestDeactivateObservesOfflineWithRouteMetadata(t *testing.T) {
	observer := &recordingPresenceObserver{}
	app := newPresenceAppForTest(t, presenceTestOptions{Observer: observer})
	err := app.Activate(context.Background(), ActivateCommand{
		UID: "u1", DeviceID: "d1", DeviceFlag: 1, DeviceLevel: 1,
		ConnectedUnix: 10, SessionID: 100,
	})
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	observer.events = nil
	err = app.Deactivate(context.Background(), DeactivateCommand{UID: "u1", SessionID: 100})
	if err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	if len(observer.events) != 1 {
		t.Fatalf("events = %d, want 1", len(observer.events))
	}
	event := observer.events[0]
	if event.Kind != PresenceOffline || event.Route.DeviceFlag != 1 || event.Route.SessionID != 100 {
		t.Fatalf("event = %#v", event)
	}
}
```

Adapt helper names to existing presence tests; do not move gateway-specific data into the usecase.

- [ ] **Step 2: Run failing tests**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/presence -run 'TestActivateObservesOnline|TestDeactivateObservesOffline' -count=1
```

Expected: compile failure because observer types do not exist.

- [ ] **Step 3: Add observer DTOs**

In `types.go`, add:

```go
// PresenceStatus identifies a route status transition observed by the presence usecase.
type PresenceStatus string

const (
	// PresenceOnline reports a route that became locally active after authority registration.
	PresenceOnline PresenceStatus = "online"
	// PresenceOffline reports a route removed from the owner-local registry.
	PresenceOffline PresenceStatus = "offline"
)

// PresenceStatusEvent describes one successful owner-local route status transition.
type PresenceStatusEvent struct {
	// Kind is online or offline.
	Kind PresenceStatus
	// Route is the owner-local route metadata available at the transition point.
	Route OwnerRoute
	// DeviceOnlineCount is the number of active local sessions for this UID and device flag after the transition.
	DeviceOnlineCount int
	// TotalOnlineCount is the number of active local sessions for this UID after the transition.
	TotalOnlineCount int
}

// PresenceObserver receives successful owner-local route status transitions.
type PresenceObserver interface {
	// ObservePresenceStatus records one route status transition.
	ObservePresenceStatus(PresenceStatusEvent)
}
```

Add `Observer PresenceObserver` to `Options` and `observer PresenceObserver` to `App`.

- [ ] **Step 4: Extend local registry port for online counts**

In `ports.go`, add the existing `online.Registry` method to the `LocalRegistry` interface:

```go
// LocalSessionsByUID returns copies of local sessions currently indexed for uid.
LocalSessionsByUID(uid string) []LocalSession
```

Update presence test fakes to store active sessions and return copies from `LocalSessionsByUID`.

- [ ] **Step 5: Emit observations**

After `MarkActive` succeeds in `Activate`, fetch counts from the local registry and observe:

```go
if a.observer != nil {
	a.observer.ObservePresenceStatus(a.presenceStatusEvent(PresenceOnline, routeProjection))
}
```

After `MarkClosingAndUnregister` succeeds in `Deactivate`, observe with the removed route:

```go
if a.observer != nil {
	a.observer.ObservePresenceStatus(a.presenceStatusEvent(PresenceOffline, conn))
}
```

Add helper:

```go
func (a *App) presenceStatusEvent(kind PresenceStatus, route OwnerRoute) PresenceStatusEvent {
	sessions := a.local.LocalSessionsByUID(route.UID)
	total := 0
	device := 0
	for _, session := range sessions {
		if session.Route.State != RouteStateActive {
			continue
		}
		total++
		if session.Route.DeviceFlag == route.DeviceFlag {
			device++
		}
	}
	return PresenceStatusEvent{
		Kind:              kind,
		Route:             route,
		DeviceOnlineCount: device,
		TotalOnlineCount:  total,
	}
}
```

- [ ] **Step 6: Run presence tests**

Run:

```bash
GOWORK=off go test ./internalv2/usecase/presence -count=1
```

Expected: PASS.

## Task 6: Wire webhook into app and compose existing plugin hooks

**Files:**
- Create: `internalv2/app/webhook.go`
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/lifecycle.go`
- Test: `internalv2/app/app_test.go`

- [ ] **Step 1: Write app wiring tests**

Add tests:

```go
func TestWebhookWiringCreatesRuntimeWhenHTTPAddrConfigured(t *testing.T) {
	app := newTestApp(t, Config{
		NodeID:  1,
		DataDir: t.TempDir(),
		Webhook: WebhookConfig{
			HTTPAddr: "http://127.0.0.1:18080/webhook",
		},
	})
	if app.webhook == nil {
		t.Fatalf("webhook runtime is nil")
	}
	if app.webhookNotify == nil {
		t.Fatalf("webhook notify adapter is nil")
	}
	if app.webhookOffline == nil {
		t.Fatalf("webhook offline adapter is nil")
	}
}

func TestWebhookPluginHooksAreComposed(t *testing.T) {
	pluginPersist := recordingPersistAfterEnqueuer{}
	webhookPersist := recordingPersistAfterEnqueuer{}
	composed := composePersistAfterEnqueuers(pluginPersist, webhookPersist)
	composed.EnqueuePersistAfter(context.Background(), channelappend.CommittedEnvelope{MessageID: 1, MessageSeq: 1})
	if pluginPersist.count.Load() != 1 || webhookPersist.count.Load() != 1 {
		t.Fatalf("composed persist counts plugin=%d webhook=%d", pluginPersist.count.Load(), webhookPersist.count.Load())
	}
}

func TestWebhookOfflineBatchAdapterChunksUIDs(t *testing.T) {
	worker := &recordingWebhookRuntime{}
	observer := webhookOfflineObserver{runtime: worker, uidBatchSize: 2}
	observer.ObserveOfflineRecipients(context.Background(), channelappend.OfflineRecipientsEvent{
		Event: channelappend.CommittedEnvelope{MessageID: 1, MessageSeq: 1, ChannelID: "group-a", ChannelType: 2},
		UIDs:  []string{"u1", "u2", "u3"},
	})
	if got := len(worker.offline); got != 2 {
		t.Fatalf("offline chunks = %d, want 2", got)
	}
	if len(worker.offline[0].ToUIDs) != 2 || len(worker.offline[1].ToUIDs) != 1 {
		t.Fatalf("offline chunks = %#v", worker.offline)
	}
}
```

Reuse existing test helpers where possible and add small recording fakes in `app_test.go`.

- [ ] **Step 2: Run failing app tests**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'TestWebhook' -count=1
```

Expected: compile failure because app webhook fields/adapters do not exist.

- [ ] **Step 3: Add app fields**

In `App`, add fields:

```go
// webhook owns node-local best-effort webhook delivery.
webhook WorkerRuntime
// webhookNotify adapts committed envelopes into msg.notify events.
webhookNotify channelappend.PersistAfterEnqueuer
// webhookOffline adapts offline recipient batches into msg.offline events.
webhookOffline channelappend.OfflineRecipientObserver
// webhookPresence adapts presence status observations into user.onlinestatus events.
webhookPresence presence.PresenceObserver
webhookStarted bool
```

- [ ] **Step 4: Add app webhook adapters**

Create `internalv2/app/webhook.go`:

```go
package app

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	runtimewebhook "github.com/WuKongIM/WuKongIM/internalv2/runtime/webhook"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
)

type webhookRuntime interface {
	Notify(context.Context, runtimewebhook.Message)
	Offline(context.Context, runtimewebhook.OfflineMessage)
	OnlineStatus(context.Context, runtimewebhook.OnlineStatus)
}

type webhookNotifyEnqueuer struct {
	runtime webhookRuntime
}

type webhookOfflineObserver struct {
	runtime      webhookRuntime
	uidBatchSize int
}

type webhookPresenceObserver struct {
	runtime webhookRuntime
}

func (e webhookNotifyEnqueuer) EnqueuePersistAfter(ctx context.Context, event channelappend.CommittedEnvelope) {
	if e.runtime == nil || event.MessageSeq == 0 {
		return
	}
	e.runtime.Notify(ctx, webhookMessageFromEnvelope(event))
}

func (o webhookOfflineObserver) ObserveOfflineRecipient(ctx context.Context, event channelappend.OfflineRecipientEvent) {
	o.ObserveOfflineRecipients(ctx, channelappend.OfflineRecipientsEvent{Event: event.Event, UIDs: []string{event.UID}})
}

func (o webhookOfflineObserver) ObserveOfflineRecipients(ctx context.Context, event channelappend.OfflineRecipientsEvent) {
	if o.runtime == nil || len(event.UIDs) == 0 {
		return
	}
	size := o.uidBatchSize
	if size <= 0 {
		size = 512
	}
	for start := 0; start < len(event.UIDs); start += size {
		end := start + size
		if end > len(event.UIDs) {
			end = len(event.UIDs)
		}
		o.runtime.Offline(ctx, runtimewebhook.OfflineMessage{
			Message: webhookMessageFromEnvelope(event.Event),
			ToUIDs:  append([]string(nil), event.UIDs[start:end]...),
		})
	}
}

func (o webhookPresenceObserver) ObservePresenceStatus(event presence.PresenceStatusEvent) {
	if o.runtime == nil || event.Route.UID == "" {
		return
	}
	value := fmt.Sprintf("%s-%d-%s-%d-%d-%d",
		event.Route.UID,
		event.Route.DeviceFlag,
		string(event.Kind),
		event.Route.SessionID,
		event.DeviceOnlineCount,
		event.TotalOnlineCount,
	)
	o.runtime.OnlineStatus(context.Background(), runtimewebhook.OnlineStatus{Value: value})
}

func webhookMessageFromEnvelope(event channelappend.CommittedEnvelope) runtimewebhook.Message {
	return runtimewebhook.Message{
		MessageID:         event.MessageID,
		MessageSeq:        event.MessageSeq,
		ChannelID:         event.ChannelID,
		ChannelType:       event.ChannelType,
		FromUID:           event.FromUID,
		ClientMsgNo:       event.ClientMsgNo,
		ServerTimestampMS: event.ServerTimestampMS,
		Payload:           append([]byte(nil), event.Payload...),
		RedDot:            event.RedDot,
		SyncOnce:          event.SyncOnce,
	}
}
```

- [ ] **Step 5: Add hook composition helpers**

In `webhook.go`, add:

```go
type persistAfterFanout []channelappend.PersistAfterEnqueuer

func (f persistAfterFanout) EnqueuePersistAfter(ctx context.Context, event channelappend.CommittedEnvelope) {
	for _, next := range f {
		if next != nil {
			next.EnqueuePersistAfter(ctx, event)
		}
	}
}

func composePersistAfterEnqueuers(items ...channelappend.PersistAfterEnqueuer) channelappend.PersistAfterEnqueuer {
	out := make(persistAfterFanout, 0, len(items))
	for _, item := range items {
		if item != nil {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return nil
	}
	if len(out) == 1 {
		return out[0]
	}
	return out
}
```

Add an offline fanout that implements both single and batch observer interfaces:

```go
type offlineObserverFanout []channelappend.OfflineRecipientObserver

func (f offlineObserverFanout) ObserveOfflineRecipient(ctx context.Context, event channelappend.OfflineRecipientEvent) {
	for _, next := range f {
		if next != nil {
			next.ObserveOfflineRecipient(ctx, event)
		}
	}
}

func (f offlineObserverFanout) ObserveOfflineRecipients(ctx context.Context, event channelappend.OfflineRecipientsEvent) {
	for _, next := range f {
		if batch, ok := next.(channelappend.OfflineRecipientsObserver); ok {
			batch.ObserveOfflineRecipients(ctx, event)
			continue
		}
		for _, uid := range event.UIDs {
			next.ObserveOfflineRecipient(ctx, channelappend.OfflineRecipientEvent{Event: event.Event, UID: uid})
		}
	}
}
```

- [ ] **Step 6: Wire runtime construction**

Add `wireWebhook` in `webhook.go`:

```go
func (a *App) wireWebhook() error {
	if !a.cfg.Webhook.Enabled || a.webhook != nil {
		return nil
	}
	sender := runtimewebhook.NewHTTPSender(runtimewebhook.HTTPSenderOptions{
		Addr:    a.cfg.Webhook.HTTPAddr,
		Timeout: a.cfg.Webhook.RequestTimeout,
	})
	rt, err := runtimewebhook.New(runtimewebhook.RuntimeOptions{
		Sender:              sender,
		QueueSize:           a.cfg.Webhook.QueueSize,
		Workers:             a.cfg.Webhook.Workers,
		NotifyBatchMaxItems: a.cfg.Webhook.NotifyBatchMaxItems,
		NotifyBatchMaxWait:  a.cfg.Webhook.NotifyBatchMaxWait,
		OnlineBatchMaxItems: a.cfg.Webhook.OnlineBatchMaxItems,
		OnlineBatchMaxWait:  a.cfg.Webhook.OnlineBatchMaxWait,
		OfflineUIDBatchSize: a.cfg.Webhook.OfflineUIDBatchSize,
		RequestTimeout:      a.cfg.Webhook.RequestTimeout,
		RetryMaxAttempts:    a.cfg.Webhook.RetryMaxAttempts,
		FocusEvents:         a.cfg.Webhook.FocusEvents,
	})
	if err != nil {
		return fmt.Errorf("internalv2/app: create webhook runtime: %w", err)
	}
	a.webhook = rt
	a.webhookNotify = webhookNotifyEnqueuer{runtime: rt}
	a.webhookOffline = webhookOfflineObserver{runtime: rt, uidBatchSize: a.cfg.Webhook.OfflineUIDBatchSize}
	a.webhookPresence = webhookPresenceObserver{runtime: rt}
	return nil
}
```

- [ ] **Step 7: Call wireWebhook before presence and channelappend wiring**

In `New`, call `wireWebhook` after `ensureOnlineRegistry()` and before `wirePresence()`:

```go
if err := app.wireWebhook(); err != nil {
	return nil, err
}
```

In `wirePresence`, pass:

```go
Observer: a.webhookPresence,
```

In `wireChannelAppend`, set:

```go
opts.PersistAfterEnqueuer = composePersistAfterEnqueuers(a.pluginPersistAfter, a.webhookNotify)
```

For recipient processor:

```go
OfflineRecipientObserver: composeOfflineRecipientObservers(a.pluginReceive, a.webhookOffline),
```

- [ ] **Step 8: Add lifecycle ordering**

Start webhook after plugin hook and before delivery/channelappend:

```go
if a.webhook != nil {
	if err := a.webhook.Start(ctx); err != nil {
		a.logLifecycleError("webhook", "start", err)
		stopErr := a.rollbackStarted(ctx)
		return errors.Join(err, stopErr)
	}
	a.webhookStarted = true
}
```

Stop webhook after channelappend and delivery workers have drained:

```go
if a.webhookStarted && a.webhook != nil {
	if stopErr := a.webhook.Stop(ctx); stopErr != nil {
		a.logLifecycleWarn("webhook", "stop", stopErr)
		err = errors.Join(err, stopErr)
	} else {
		a.webhookStarted = false
	}
}
```

Update rollback with the same order.

- [ ] **Step 9: Run app webhook tests**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'TestWebhook|Test.*Lifecycle' -count=1
```

Expected: PASS.

## Task 7: Parse WK_WEBHOOK config in cmd/wukongimv2 and update example config

**Files:**
- Modify: `cmd/wukongimv2/config.go`
- Test: `cmd/wukongimv2/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Write config parser tests**

Add tests:

```go
func TestBuildConfigParsesWebhook(t *testing.T) {
	values := minimalConfigValues()
	values["WK_WEBHOOK_HTTP_ADDR"] = "http://127.0.0.1:19090/webhook"
	values["WK_WEBHOOK_FOCUS_EVENTS"] = `["msg.notify","msg.offline"]`
	values["WK_WEBHOOK_QUEUE_SIZE"] = "2048"
	values["WK_WEBHOOK_WORKERS"] = "32"
	values["WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_ITEMS"] = "200"
	values["WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_WAIT"] = "250ms"
	values["WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_ITEMS"] = "300"
	values["WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_WAIT"] = "1s"
	values["WK_WEBHOOK_OFFLINE_UID_BATCH_SIZE"] = "1000"
	values["WK_WEBHOOK_REQUEST_TIMEOUT"] = "2s"
	values["WK_WEBHOOK_RETRY_MAX_ATTEMPTS"] = "4"

	cfg, err := buildConfig(values)
	if err != nil {
		t.Fatalf("buildConfig() error = %v", err)
	}
	if !cfg.Webhook.Enabled {
		t.Fatalf("Webhook.Enabled = false, want true")
	}
	if cfg.Webhook.HTTPAddr != "http://127.0.0.1:19090/webhook" {
		t.Fatalf("HTTPAddr = %q", cfg.Webhook.HTTPAddr)
	}
	if got := cfg.Webhook.FocusEvents; len(got) != 2 || got[0] != "msg.notify" || got[1] != "msg.offline" {
		t.Fatalf("FocusEvents = %#v", got)
	}
	if cfg.Webhook.QueueSize != 2048 || cfg.Webhook.Workers != 32 {
		t.Fatalf("queue/workers = %d/%d", cfg.Webhook.QueueSize, cfg.Webhook.Workers)
	}
}
```

- [ ] **Step 2: Run failing config parser test**

Run:

```bash
GOWORK=off go test ./cmd/wukongimv2 -run 'TestBuildConfigParsesWebhook' -count=1
```

Expected: fail or compile error because keys are not parsed.

- [ ] **Step 3: Add supported keys**

Add to `supportedConfigKeys` near delivery/plugin keys:

```go
"WK_WEBHOOK_HTTP_ADDR",
"WK_WEBHOOK_FOCUS_EVENTS",
"WK_WEBHOOK_QUEUE_SIZE",
"WK_WEBHOOK_WORKERS",
"WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_ITEMS",
"WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_WAIT",
"WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_ITEMS",
"WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_WAIT",
"WK_WEBHOOK_OFFLINE_UID_BATCH_SIZE",
"WK_WEBHOOK_REQUEST_TIMEOUT",
"WK_WEBHOOK_RETRY_MAX_ATTEMPTS",
```

- [ ] **Step 4: Parse config values**

In `buildConfig`, after delivery parsing and before plugin parsing, parse each key into `cfg.Webhook`.

Use existing helpers:

```go
if raw := configValue(values, "WK_WEBHOOK_HTTP_ADDR"); raw != "" {
	cfg.Webhook.HTTPAddr = raw
}
if raw := configValue(values, "WK_WEBHOOK_FOCUS_EVENTS"); raw != "" {
	if err := json.Unmarshal([]byte(raw), &cfg.Webhook.FocusEvents); err != nil {
		return app.Config{}, fmt.Errorf("parse WK_WEBHOOK_FOCUS_EVENTS: %w", err)
	}
}
```

For numeric fields, reject `< 0` with the same message style used by delivery config:

```go
if raw := configValue(values, "WK_WEBHOOK_QUEUE_SIZE"); raw != "" {
	queueSize, err := parseInt("WK_WEBHOOK_QUEUE_SIZE", raw)
	if err != nil {
		return app.Config{}, err
	}
	if queueSize < 0 {
		return app.Config{}, fmt.Errorf("parse WK_WEBHOOK_QUEUE_SIZE: value must be >= 0")
	}
	cfg.Webhook.QueueSize = queueSize
}
```

Repeat for workers, batch sizes, timeout, waits, and retry attempts.

- [ ] **Step 5: Update example config**

Add a webhook section near the delivery settings in `wukongim.conf.example`:

```text
# Enables node-local best-effort webhook delivery when an endpoint is configured.
# The runtime uses bounded in-memory queues; webhook failure never changes SENDACK or durable append success.
# WK_WEBHOOK_HTTP_ADDR=http://127.0.0.1:19090/webhook

# JSON array of event names to deliver. Empty means all supported events: msg.notify, msg.offline, user.onlinestatus.
# WK_WEBHOOK_FOCUS_EVENTS=["msg.notify","msg.offline","user.onlinestatus"]

# Maximum queued webhook events retained in memory per queue before new events are dropped.
# WK_WEBHOOK_QUEUE_SIZE=1024

# Maximum concurrent webhook sender workers per event queue.
# WK_WEBHOOK_WORKERS=16

# Maximum msg.notify messages sent in one webhook request.
# WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_ITEMS=100

# Maximum wait used to collect adjacent msg.notify messages into one request.
# WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_WAIT=500ms

# Maximum user.onlinestatus records sent in one webhook request.
# WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_ITEMS=512

# Maximum wait used to collect adjacent user.onlinestatus records into one request.
# WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_WAIT=2s

# Maximum offline recipient UIDs sent in one msg.offline request.
# WK_WEBHOOK_OFFLINE_UID_BATCH_SIZE=512

# Timeout for one outbound webhook request attempt.
# WK_WEBHOOK_REQUEST_TIMEOUT=5s

# Maximum attempts for one admitted webhook batch before it is dropped.
# WK_WEBHOOK_RETRY_MAX_ATTEMPTS=3
```

- [ ] **Step 6: Run config tests**

Run:

```bash
GOWORK=off go test ./cmd/wukongimv2 -run 'Webhook|Config' -count=1
```

Expected: PASS.

## Task 8: Add app and runtime flow documentation

**Files:**
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/runtime/channelappend/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Update `internalv2/app/FLOW.md`**

Add a short section near plugin/delivery wiring:

```markdown
Webhook runtime:

  -> create optional node-local webhook runtime from `WebhookConfig`
  -> start webhook queues before channelappend starts producing post-commit effects
  -> compose webhook `msg.notify` with plugin PersistAfter instead of replacing it
  -> compose webhook `msg.offline` with plugin Receive instead of replacing it
  -> presence usecase emits route status observations for `user.onlinestatus`

Webhook delivery is best-effort and bounded. Queue-full, closed, and retry-
exhausted events are dropped after observation. Webhook delivery never affects
SENDACK, durable append success, conversation active admission, or owner
delivery.
```

- [ ] **Step 2: Update `internalv2/runtime/channelappend/FLOW.md`**

Add:

```markdown
Offline recipient observers may implement the batch observer interface. The
recipient processor prefers the batch interface when available so large groups
do not enqueue one external side-effect per UID. Plain single-recipient
observers still receive per-UID calls for existing adapters.
```

- [ ] **Step 3: Add project knowledge**

Append to `docs/development/PROJECT_KNOWLEDGE.md`:

```markdown
- internalv2 webhook delivery is a best-effort post-commit side effect: bounded queues and finite retry protect SENDACK, channelappend, conversation active admission, and owner delivery from slow external endpoints.
```

- [ ] **Step 4: Run markdown grep checks**

Run:

```bash
rg -n "webhook|Webhook|Offline recipient observers" internalv2/app/FLOW.md internalv2/runtime/channelappend/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
```

Expected: output includes the new sections.

## Task 9: Add focused verification and performance guard

**Files:**
- Test-only commands unless a benchmark needs a new file.
- Optional create: `internalv2/runtime/webhook/benchmark_test.go`

- [ ] **Step 1: Add webhook runtime microbenchmarks**

Create `benchmark_test.go`:

```go
package webhook

import (
	"context"
	"testing"
	"time"
)

func BenchmarkRuntimeNotifyAdmission(b *testing.B) {
	rt, err := New(RuntimeOptions{
		Sender:              discardSender{},
		QueueSize:           65536,
		Workers:             16,
		NotifyBatchMaxItems: 100,
		NotifyBatchMaxWait:  time.Millisecond,
		OnlineBatchMaxItems: 512,
		OnlineBatchMaxWait:  time.Millisecond,
		OfflineUIDBatchSize: 512,
		RequestTimeout:      time.Second,
		RetryMaxAttempts:    1,
	})
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	if err := rt.Start(context.Background()); err != nil {
		b.Fatalf("Start() error = %v", err)
	}
	defer rt.Stop(context.Background())
	msg := Message{MessageID: 1, MessageSeq: 1, ChannelID: "group-a", ChannelType: 2, Payload: []byte("payload")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		msg.MessageID = uint64(i + 1)
		rt.Notify(context.Background(), msg)
	}
}
```

Add:

```go
type discardSender struct{}

func (discardSender) Send(context.Context, SendRequest) error { return nil }
```

- [ ] **Step 2: Run focused unit tests**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/webhook ./internalv2/runtime/channelappend ./internalv2/usecase/presence ./internalv2/app ./cmd/wukongimv2 -count=1
```

Expected: PASS.

- [ ] **Step 3: Run race tests for new concurrency**

Run:

```bash
GOWORK=off go test -race -count=1 ./internalv2/runtime/webhook ./internalv2/runtime/channelappend ./internalv2/usecase/presence
```

Expected: PASS.

- [ ] **Step 4: Run webhook benchmark**

Run:

```bash
GOWORK=off go test ./internalv2/runtime/webhook -run '^$' -bench 'BenchmarkRuntimeNotifyAdmission' -benchmem -benchtime=500ms -count=5
```

Expected: benchmark completes without unbounded allocation growth or blocking. Record results in the final implementation report.

- [ ] **Step 5: Run config and docs checks**

Run:

```bash
git diff --check
GOWORK=off go test ./cmd/wukongimv2 -count=1
```

Expected: PASS.

## Self-Review

Spec coverage:

- `msg.notify` covered by Tasks 2, 3, and 6 through committed envelope adapter and notify batch pool.
- `msg.offline` covered by Tasks 3, 4, and 6 through batch offline observer, UID chunking, and sharded mailbox.
- `user.onlinestatus` covered by Tasks 5 and 6 through presence observer and online status batch pool.
- Bounded workqueue usage covered by Task 3.
- Plugin coexistence covered by Task 6.
- Config and `wukongim.conf.example` coverage is Task 7.
- FLOW and project knowledge coverage is Task 8.
- Performance verification is Task 9.

Placeholder scan:

- No placeholder marker is present.
- No unbounded retry loop is specified.
- No high-cardinality observer labels are specified.

Type consistency:

- App adapters use `runtimewebhook.Message`, `OfflineMessage`, and `OnlineStatus`.
- Channelappend batch type is `OfflineRecipientsEvent`.
- Presence observer type is `PresenceStatusEvent`.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-24-internalv2-webhook-workqueue-migration.md`. Two execution options:

**1. Subagent-Driven (recommended)** - Dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints.
