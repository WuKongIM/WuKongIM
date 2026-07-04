package rpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

type recordingObserver struct {
	mu     sync.Mutex
	events []core.Event
}

func (o *recordingObserver) ObserveTransport(event core.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingObserver) snapshot() []core.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]core.Event(nil), o.events...)
}

func TestServiceObservesQueueAdmissionInflightAndTask(t *testing.T) {
	observer := &recordingObserver{}
	started := make(chan struct{})
	release := make(chan struct{})
	svc := NewService(42, func(context.Context, []byte) ([]byte, error) {
		close(started)
		<-release
		return []byte("ok"), nil
	}, core.ServiceOptions{Alias: "answer service", Concurrency: 1, QueueSize: 2, MaxQueueBytes: 16}, observer)
	defer svc.Stop()

	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("work")), Reply: reply}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	waitClosed(t, started)
	waitForEvent(t, observer, func(event core.Event) bool {
		return event.Name == "service_inflight" && event.ServiceID == 42 && event.Inflight == 1
	})
	close(release)

	resp := waitResponse(t, reply)
	if resp.Err != nil {
		t.Fatalf("reply err = %v", resp.Err)
	}
	if string(resp.Payload) != "ok" {
		t.Fatalf("reply payload = %q, want ok", resp.Payload)
	}

	events := waitForEvent(t, observer, func(event core.Event) bool {
		return event.Name == "service_task" && event.ServiceID == 42 && event.Result == "ok"
	})
	okAdmission := findEvent(events, "service_admission", "ok")
	if okAdmission == nil {
		t.Fatalf("missing service_admission ok event: %#v", events)
	}
	if okAdmission.ServiceID != 42 || okAdmission.ServiceAlias != "answer service" || okAdmission.Bytes != 4 || okAdmission.Items != 1 ||
		okAdmission.Capacity != 2 || okAdmission.BytesCapacity != 16 {
		t.Fatalf("service_admission ok = %+v, want service queue snapshot", *okAdmission)
	}

	queueEvent := findEvent(events, "service_queue", "ok")
	if queueEvent == nil {
		t.Fatalf("missing service_queue ok event: %#v", events)
	}
	if queueEvent.ServiceID != 42 || queueEvent.ServiceAlias != "answer service" || queueEvent.Capacity != 2 || queueEvent.BytesCapacity != 16 {
		t.Fatalf("service_queue ok = %+v, want bounded queue dimensions", *queueEvent)
	}

	inflightStarted := findInflight(events, 42, 1)
	if inflightStarted == nil {
		t.Fatalf("missing service_inflight started event: %#v", events)
	}
	if inflightStarted.Capacity != 1 {
		t.Fatalf("service_inflight started capacity = %d, want worker concurrency 1", inflightStarted.Capacity)
	}
	if inflightStarted.PoolCapacity != 1 || inflightStarted.PoolRunning <= 0 || inflightStarted.PoolWaiting != 0 {
		t.Fatalf("service_inflight started pool stats = %+v, want direct executor pool occupancy", *inflightStarted)
	}

	inflightDone := findInflight(events, 42, 0)
	if inflightDone == nil {
		t.Fatalf("missing service_inflight zero event: %#v", events)
	}
	if inflightDone.Capacity != 1 {
		t.Fatalf("service_inflight zero capacity = %d, want worker concurrency 1", inflightDone.Capacity)
	}
	if inflightDone.PoolCapacity != 1 || inflightDone.PoolWaiting != 0 {
		t.Fatalf("service_inflight zero pool stats = %+v, want direct executor pool capacity", *inflightDone)
	}

	taskEvent := findEvent(events, "service_task", "ok")
	if taskEvent == nil {
		t.Fatalf("missing service_task ok event: %#v", events)
	}
	if taskEvent.ServiceID != 42 || taskEvent.ServiceAlias != "answer service" || taskEvent.Bytes != 4 || taskEvent.Duration < 0 {
		t.Fatalf("service_task ok = %+v, want service id, bytes, and non-negative duration", *taskEvent)
	}
}

func TestServiceQueueFullReturnsBusy(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		close(started)
		<-block
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024}, nil)
	defer func() {
		close(block)
		svc.Stop()
	}()

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("running"))}); err != nil {
		t.Fatalf("Enqueue(running) error = %v", err)
	}
	waitClosed(t, started)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("queued"))}); err != nil {
		t.Fatalf("Enqueue(queued) error = %v", err)
	}

	var released atomic.Int32
	err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("full"), func([]byte) {
		released.Add(1)
	})})
	if !errors.Is(err, core.ErrBusy) {
		t.Fatalf("Enqueue(full) error = %v, want %v", err, core.ErrBusy)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("released = %d, want 1", got)
	}
}

func TestServiceAdmissionUsesChannelCapacityDuringDequeueWindow(t *testing.T) {
	observer := &recordingObserver{}
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024}, observer)
	defer svc.Stop()

	req := Request{Payload: core.CopyOwnedBuffer([]byte("queued"))}
	svc.queue <- req
	svc.mu.Lock()
	svc.queuedItems = 1
	svc.queuedBytes = int64(req.Payload.Len())
	svc.mu.Unlock()
	received := <-svc.queue
	defer received.Payload.Release()

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("next"))}); err != nil {
		t.Fatalf("Enqueue() error = %v, want accepted while channel has capacity", err)
	}
	events := waitForEvent(t, observer, func(event core.Event) bool {
		return event.Name == "service_queue" && event.Result == "ok"
	})
	queueEvent := findEvent(events, "service_queue", "ok")
	if queueEvent == nil {
		t.Fatalf("missing service_queue ok event: %#v", events)
	}
	if queueEvent.Items > queueEvent.Capacity {
		t.Fatalf("service_queue items = %d, capacity = %d; want depth capped to capacity", queueEvent.Items, queueEvent.Capacity)
	}
}

func TestServiceTimeout(t *testing.T) {
	svc := NewService(1, func(ctx context.Context, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024, Timeout: 10 * time.Millisecond}, nil)
	defer svc.Stop()

	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("slow")), Reply: reply}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	resp := waitResponse(t, reply)
	if !errors.Is(resp.Err, core.ErrTimeout) {
		t.Fatalf("response err = %v, want %v", resp.Err, core.ErrTimeout)
	}
}

func TestServiceHandlerPanicRepliesAndReleasesPayload(t *testing.T) {
	var released atomic.Int32
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		panic("boom")
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024}, nil)
	defer svc.Stop()

	reply := make(chan Response, 1)
	err := svc.Enqueue(Request{
		Payload: core.NewOwnedBuffer([]byte("panic"), func([]byte) {
			released.Add(1)
		}),
		Reply: reply,
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	resp := waitResponse(t, reply)
	if resp.Err == nil {
		t.Fatal("reply err is nil, want panic error")
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("released = %d, want 1", got)
	}
}

func TestServiceConcurrencyRemainsBounded(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	var calls atomic.Int32

	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		call := calls.Add(1)
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
			return nil, nil
		}
		close(secondStarted)
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 2, MaxQueueBytes: 1024}, nil)
	defer svc.Stop()

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("first"))}); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}
	waitClosed(t, firstStarted)

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("second"))}); err != nil {
		t.Fatalf("Enqueue(second) error = %v", err)
	}

	select {
	case <-secondStarted:
		t.Fatal("second handler started while first handler still holds concurrency slot")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseFirst)
	waitClosed(t, secondStarted)
}

func TestServiceRetriesExecutorOverloadWithoutReplyingBusy(t *testing.T) {
	executor, err := NewExecutor(1, nil)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	defer func() {
		if err := executor.Stop(); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	svc := NewServiceWithExecutor(1, func(context.Context, []byte) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			return []byte("first"), nil
		}
		return []byte("second"), nil
	}, core.ServiceOptions{Concurrency: 2, QueueSize: 2, MaxQueueBytes: 1024}, nil, executor)
	defer svc.Stop()

	firstReply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("first")), Reply: firstReply}); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}
	waitClosed(t, firstStarted)

	secondReply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("second")), Reply: secondReply}); err != nil {
		t.Fatalf("Enqueue(second) error = %v", err)
	}

	time.AfterFunc(20*time.Millisecond, func() {
		close(releaseFirst)
	})
	resp := waitResponse(t, secondReply)
	if resp.Err != nil {
		t.Fatalf("second reply err = %v, want nil", resp.Err)
	}
	if got := string(resp.Payload); got != "second" {
		t.Fatalf("second reply payload = %q, want second", got)
	}
	_ = waitResponse(t, firstReply)
}

func TestServiceMaxQueueBytesReturnsBusy(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		close(started)
		<-block
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 4, MaxQueueBytes: 7}, nil)
	defer func() {
		close(block)
		svc.Stop()
	}()

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("run"))}); err != nil {
		t.Fatalf("Enqueue(running) error = %v", err)
	}
	waitClosed(t, started)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("1234"))}); err != nil {
		t.Fatalf("Enqueue(queued) error = %v", err)
	}

	var released atomic.Int32
	err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("5678"), func([]byte) {
		released.Add(1)
	})})
	if !errors.Is(err, core.ErrBusy) {
		t.Fatalf("Enqueue(over bytes) error = %v, want %v", err, core.ErrBusy)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("released = %d, want 1", got)
	}
}

func TestServiceStopDrainsQueuedPayload(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	svc := NewService(1, func(ctx context.Context, _ []byte) ([]byte, error) {
		close(started)
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 4, MaxQueueBytes: 1024}, nil)

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("running"))}); err != nil {
		t.Fatalf("Enqueue(running) error = %v", err)
	}
	waitClosed(t, started)

	var released atomic.Int32
	if err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("queued"), func([]byte) {
		released.Add(1)
	})}); err != nil {
		t.Fatalf("Enqueue(queued) error = %v", err)
	}

	svc.Stop()
	close(block)

	if got := released.Load(); got != 1 {
		t.Fatalf("released = %d, want 1 queued release", got)
	}
}

func TestServiceStopReturnsWhenHandlerIgnoresContext(t *testing.T) {
	started := make(chan struct{})
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		close(started)
		select {}
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024}, nil)

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("stuck"))}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	waitClosed(t, started)

	done := make(chan struct{})
	go func() {
		svc.Stop()
		close(done)
	}()
	waitClosed(t, done)
}

func TestServiceStopRepliesErrStoppedForQueuedRequest(t *testing.T) {
	started := make(chan struct{})
	svc := NewService(1, func(ctx context.Context, _ []byte) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 2, MaxQueueBytes: 1024}, nil)

	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("running"))}); err != nil {
		t.Fatalf("Enqueue(running) error = %v", err)
	}
	waitClosed(t, started)

	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("queued")), Reply: reply}); err != nil {
		t.Fatalf("Enqueue(queued) error = %v", err)
	}

	svc.Stop()
	resp := waitResponse(t, reply)
	if !errors.Is(resp.Err, core.ErrStopped) {
		t.Fatalf("reply err = %v, want %v", resp.Err, core.ErrStopped)
	}
}

func TestServiceMaxPayloadReleases(t *testing.T) {
	var released atomic.Int32
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		t.Fatal("handler should not run for oversized payload")
		return nil, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024, MaxPayload: 3}, nil)
	defer svc.Stop()

	err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("large"), func([]byte) {
		released.Add(1)
	})})
	if !errors.Is(err, core.ErrMsgTooLarge) {
		t.Fatalf("Enqueue() error = %v, want %v", err, core.ErrMsgTooLarge)
	}
	if got := released.Load(); got != 1 {
		t.Fatalf("released = %d, want 1", got)
	}
}

func TestServiceReplyPayloadIsCopied(t *testing.T) {
	response := []byte("hello")
	svc := NewService(1, func(context.Context, []byte) ([]byte, error) {
		return response, nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024}, nil)
	defer svc.Stop()

	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("request")), Reply: reply}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	resp := waitResponse(t, reply)

	response[0] = 'x'
	if got := string(resp.Payload); got != "hello" {
		t.Fatalf("reply payload = %q, want copied hello", got)
	}
}

func TestServiceInvokesRespondCallback(t *testing.T) {
	svc := NewService(7, func(ctx context.Context, payload []byte) ([]byte, error) {
		return append([]byte("echo:"), payload...), nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 4, MaxQueueBytes: 1 << 20}, nil)
	defer svc.Stop()

	got := make(chan Response, 1)
	err := svc.Enqueue(Request{
		Payload: core.CopyOwnedBuffer([]byte("hi")),
		Respond: func(resp Response) {
			got <- resp
		},
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	select {
	case resp := <-got:
		if resp.Err != nil || string(resp.Payload) != "echo:hi" {
			t.Fatalf("Respond got %+v, want echo:hi", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Respond callback not invoked")
	}
}

func TestServiceWithExecutorNormalizesOptionsAndRuns(t *testing.T) {
	executor, err := NewExecutor(1, nil)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	defer func() {
		if err := executor.Stop(); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}()

	svc := NewServiceWithExecutor(1, func(context.Context, []byte) ([]byte, error) {
		return []byte("ok"), nil
	}, core.ServiceOptions{}, nil, executor)
	defer svc.Stop()

	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("r")), Reply: reply}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	resp := waitResponse(t, reply)
	if resp.Err != nil {
		t.Fatalf("reply err = %v", resp.Err)
	}
	if got := string(resp.Payload); got != "ok" {
		t.Fatalf("reply payload = %q, want ok", got)
	}
}

func waitResponse(t *testing.T, ch <-chan Response) Response {
	t.Helper()

	select {
	case resp := <-ch:
		return resp
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for response")
		return Response{}
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for close")
	}
}

func waitForEvent(t *testing.T, observer *recordingObserver, predicate func(core.Event) bool) []core.Event {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		events := observer.snapshot()
		for _, event := range events {
			if predicate(event) {
				return events
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for event; events=%#v", events)
		case <-ticker.C:
		}
	}
}

func findEvent(events []core.Event, name, result string) *core.Event {
	for i := range events {
		if events[i].Name == name && events[i].Result == result {
			return &events[i]
		}
	}
	return nil
}

func findInflight(events []core.Event, serviceID uint16, inflight int) *core.Event {
	for i := range events {
		if events[i].Name == "service_inflight" && events[i].ServiceID == serviceID && events[i].Inflight == inflight {
			return &events[i]
		}
	}
	return nil
}
