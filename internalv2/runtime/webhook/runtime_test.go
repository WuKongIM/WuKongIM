package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeBatchesNotifyMessages(t *testing.T) {
	sender := &recordingSender{requests: make(chan SendRequest, 1)}
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
	defer rt.Stop(context.Background())
	defer close(sender.release)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	rt.Notify(context.Background(), Message{MessageID: 1, MessageSeq: 1})
	<-sender.started
	rt.Notify(context.Background(), Message{MessageID: 2, MessageSeq: 2})
	rt.Notify(context.Background(), Message{MessageID: 3, MessageSeq: 3})
	if !observer.hasResult(EventMsgNotify, "full") {
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
	waitUntil(t, time.Second, func() bool { return observer.hasResult(EventMsgNotify, "retry_exhausted") })
	if got := sender.calls.Load(); got != 2 {
		t.Fatalf("sender calls = %d, want 2", got)
	}
}

func TestRuntimeNegativeRetryMaxAttemptsAttemptsOnce(t *testing.T) {
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
		RetryMaxAttempts:    -1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rt.Stop(context.Background())
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	rt.Notify(context.Background(), Message{MessageID: 1, MessageSeq: 1})
	waitUntil(t, time.Second, func() bool { return observer.hasResult(EventMsgNotify, "retry_exhausted") })
	if got := sender.calls.Load(); got != 1 {
		t.Fatalf("sender calls = %d, want 1", got)
	}
}

func TestRuntimeRejectsZeroRequestTimeout(t *testing.T) {
	_, err := New(RuntimeOptions{
		Sender:              &recordingSender{requests: make(chan SendRequest, 1)},
		QueueSize:           4,
		Workers:             1,
		NotifyBatchMaxItems: 1,
		NotifyBatchMaxWait:  time.Millisecond,
		OnlineBatchMaxItems: 1,
		OnlineBatchMaxWait:  time.Millisecond,
		OfflineUIDBatchSize: 1,
		RequestTimeout:      0,
		RetryMaxAttempts:    1,
	})
	if err == nil {
		t.Fatalf("New() error = nil, want error")
	}
}

func TestRuntimeFocusEventsFiltersDisabledEvents(t *testing.T) {
	sender := &recordingSender{requests: make(chan SendRequest, 2)}
	observer := &recordingObserver{}
	rt, err := New(RuntimeOptions{
		Sender:              sender,
		Observer:            observer,
		QueueSize:           4,
		Workers:             1,
		FocusEvents:         []string{EventMsgNotify},
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
	defer rt.Stop(context.Background())
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	rt.Offline(context.Background(), OfflineMessage{
		Message: Message{MessageID: 1, MessageSeq: 1, ChannelID: "c1"},
		ToUIDs:  []string{"u1"},
	})
	rt.OnlineStatus(context.Background(), OnlineStatus{Value: "u1-1"})
	rt.Notify(context.Background(), Message{MessageID: 2, MessageSeq: 2})

	req := sender.wait(t)
	if req.Event != EventMsgNotify {
		t.Fatalf("event = %q, want %q", req.Event, EventMsgNotify)
	}
	if observer.hasResult(EventMsgOffline, "accepted") || observer.hasResult(EventUserOnlineStatus, "accepted") {
		t.Fatalf("disabled events were admitted: %#v", observer.snapshot())
	}
}

func TestRuntimeAdmissionBeforeStartAndStopAreSafe(t *testing.T) {
	var nilRuntime *Runtime
	if err := nilRuntime.Stop(context.Background()); err != nil {
		t.Fatalf("nil Stop() error = %v", err)
	}
	var zeroRuntime Runtime
	if err := zeroRuntime.Stop(context.Background()); err != nil {
		t.Fatalf("zero-value Stop() error = %v", err)
	}

	observer := &recordingObserver{}
	rt, err := New(RuntimeOptions{
		Sender:              &recordingSender{requests: make(chan SendRequest, 1)},
		Observer:            observer,
		QueueSize:           4,
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
	rt.Notify(context.Background(), Message{MessageID: 1, MessageSeq: 1})
	rt.Offline(context.Background(), OfflineMessage{Message: Message{MessageID: 1, MessageSeq: 1, ChannelID: "c1"}, ToUIDs: []string{"u1"}})
	rt.OnlineStatus(context.Background(), OnlineStatus{Value: "u1-1"})
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() before Start error = %v", err)
	}
	if !observer.hasResult(EventMsgNotify, "closed") {
		t.Fatalf("pre-start admission was not observed as closed: %#v", observer.snapshot())
	}
}

func TestRuntimeRejectsStartAndAdmissionAfterStop(t *testing.T) {
	observer := &recordingObserver{}
	rt, err := New(RuntimeOptions{
		Sender:              &recordingSender{requests: make(chan SendRequest, 1)},
		Observer:            observer,
		QueueSize:           4,
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
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := rt.Start(context.Background()); err == nil {
		t.Fatalf("Start() after Stop error = nil, want error")
	}

	rt.Notify(context.Background(), Message{MessageID: 1, MessageSeq: 1})
	if !observer.hasResult(EventMsgNotify, "closed") {
		t.Fatalf("post-stop admission was not observed as closed: %#v", observer.snapshot())
	}
}

func TestRuntimeSendsOfflineAndOnlineStatusEvents(t *testing.T) {
	sender := &recordingSender{requests: make(chan SendRequest, 2)}
	rt, err := New(RuntimeOptions{
		Sender:              sender,
		QueueSize:           8,
		Workers:             1,
		NotifyBatchMaxItems: 1,
		NotifyBatchMaxWait:  time.Millisecond,
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
	rt.Offline(context.Background(), OfflineMessage{
		Message: Message{MessageID: 1, MessageSeq: 1, ChannelID: "c1"},
		ToUIDs:  []string{"u1", "u2"},
	})
	rt.OnlineStatus(context.Background(), OnlineStatus{Value: "u1-1"})
	rt.OnlineStatus(context.Background(), OnlineStatus{Value: "u2-0"})

	got := map[string]SendRequest{}
	for len(got) < 2 {
		req := sender.wait(t)
		got[req.Event] = req
	}
	if _, ok := got[EventMsgOffline]; !ok {
		t.Fatalf("missing offline event: %#v", got)
	}
	if _, ok := got[EventUserOnlineStatus]; !ok {
		t.Fatalf("missing online status event: %#v", got)
	}
	var statuses []string
	if err := json.Unmarshal(got[EventUserOnlineStatus].Body, &statuses); err != nil {
		t.Fatalf("json.Unmarshal(online) error = %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("len(statuses) = %d, want 2", len(statuses))
	}
}

func TestOfflineMailboxSizingNeverExceedsQueueSize(t *testing.T) {
	for _, tt := range []struct {
		queueSize int
		wantTotal int
	}{
		{queueSize: 1, wantTotal: 1},
		{queueSize: 2, wantTotal: 2},
		{queueSize: 255, wantTotal: 255},
		{queueSize: 256, wantTotal: 256},
		{queueSize: 257, wantTotal: 257},
		{queueSize: 512, wantTotal: 512},
		{queueSize: 1024, wantTotal: 1024},
		{queueSize: 4096, wantTotal: 4096},
	} {
		shards, perShard := offlineMailboxSizing(tt.queueSize)
		if shards <= 0 {
			t.Fatalf("queueSize=%d shards=%d, want positive", tt.queueSize, shards)
		}
		if perShard <= 0 {
			t.Fatalf("queueSize=%d perShard=%d, want positive", tt.queueSize, perShard)
		}
		if got := shards * perShard; got > tt.queueSize {
			t.Fatalf("queueSize=%d aggregate capacity=%d, want <= queueSize", tt.queueSize, got)
		}
		if got := shards * perShard; got != tt.wantTotal {
			t.Fatalf("queueSize=%d aggregate capacity=%d, want %d", tt.queueSize, got, tt.wantTotal)
		}
	}
}

func TestRuntimeStopsRetryingWhenParentContextIsCanceled(t *testing.T) {
	parentCtx, cancel := context.WithCancel(context.Background())
	sender := &cancelingSender{cancel: cancel}
	rt, err := New(RuntimeOptions{
		Sender:              sender,
		QueueSize:           4,
		Workers:             1,
		NotifyBatchMaxItems: 1,
		NotifyBatchMaxWait:  time.Millisecond,
		OnlineBatchMaxItems: 1,
		OnlineBatchMaxWait:  time.Millisecond,
		OfflineUIDBatchSize: 1,
		RequestTimeout:      time.Second,
		RetryMaxAttempts:    3,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	rt.sendWithRetry(parentCtx, EventMsgNotify, []byte(`[]`), 1)
	if got := sender.calls.Load(); got != 1 {
		t.Fatalf("sender calls = %d, want 1", got)
	}
}

func TestRuntimeRetriesPerAttemptTimeouts(t *testing.T) {
	sender := &timeoutSender{}
	rt, err := New(RuntimeOptions{
		Sender:              sender,
		QueueSize:           4,
		Workers:             1,
		NotifyBatchMaxItems: 1,
		NotifyBatchMaxWait:  time.Millisecond,
		OnlineBatchMaxItems: 1,
		OnlineBatchMaxWait:  time.Millisecond,
		OfflineUIDBatchSize: 1,
		RequestTimeout:      time.Millisecond,
		RetryMaxAttempts:    2,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	rt.sendWithRetry(context.Background(), EventMsgNotify, []byte(`[]`), 1)
	if got := sender.calls.Load(); got != 2 {
		t.Fatalf("sender calls = %d, want 2", got)
	}
}

type recordingSender struct {
	requests chan SendRequest
}

func (s *recordingSender) Send(_ context.Context, req SendRequest) error {
	s.requests <- SendRequest{
		Event: req.Event,
		Body:  append([]byte(nil), req.Body...),
	}
	return nil
}

func (s *recordingSender) wait(t *testing.T) SendRequest {
	t.Helper()
	select {
	case req := <-s.requests:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for send request")
		return SendRequest{}
	}
}

type blockingSender struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingSender) Send(ctx context.Context, _ SendRequest) error {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type failingSender struct {
	calls atomic.Int64
}

func (s *failingSender) Send(context.Context, SendRequest) error {
	s.calls.Add(1)
	return errors.New("send failed")
}

type cancelingSender struct {
	calls  atomic.Int64
	cancel context.CancelFunc
}

func (s *cancelingSender) Send(context.Context, SendRequest) error {
	s.calls.Add(1)
	s.cancel()
	return errors.New("parent canceled")
}

type timeoutSender struct {
	calls atomic.Int64
}

func (s *timeoutSender) Send(ctx context.Context, _ SendRequest) error {
	s.calls.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

type recordingObserver struct {
	mu           sync.Mutex
	observations []Observation
}

func (o *recordingObserver) ObserveWebhook(obs Observation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.observations = append(o.observations, obs)
}

func (o *recordingObserver) hasResult(event string, result string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, obs := range o.observations {
		if obs.Event == event && obs.Result == result {
			return true
		}
	}
	return false
}

func (o *recordingObserver) snapshot() []Observation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]Observation(nil), o.observations...)
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition did not become true within %v", timeout)
}
