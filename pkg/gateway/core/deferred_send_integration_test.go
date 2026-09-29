//go:build integration

package core

import (
	"context"
	"errors"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"reflect"
	"sync"
	"testing"
	"time"
)

type deferredProbeCall struct {
	items    []gatewaytypes.SendBatchItem
	publish  func(int, func() error) error
	complete func(error)
}
type deferredProbeHandler struct {
	countingAsyncFrameHandler
	calls        chan deferredProbeCall
	beforeReturn func(deferredProbeCall) error
	onError      func()
}

var _ gatewaytypes.DeferredSendBatchHandler = (*deferredProbeHandler)(nil)

func (h *deferredProbeHandler) OnSessionError(ctx gatewaytypes.Context, err error) {
	h.countingAsyncFrameHandler.OnSessionError(ctx, err)
	if h.onError != nil {
		h.onError()
	}
}

func (h *deferredProbeHandler) OnSendBatchDeferred(items []gatewaytypes.SendBatchItem, publish func(int, func() error) error, complete func(error)) error {
	call := deferredProbeCall{items, publish, complete}
	h.calls <- call
	if h.beforeReturn != nil {
		return h.beforeReturn(call)
	}
	return nil
}
func deferredTestExecutor(t *testing.T, capacity int, h *deferredProbeHandler, batchSize ...int) (*sendExecutor, *Server) {
	t.Helper()
	s := &Server{dispatcher: newDispatcher(h), options: gatewaytypes.Options{DefaultSession: gatewaytypes.SessionOptions{AsyncSendBatchMaxRecords: 1}}}
	if len(batchSize) > 0 {
		s.options.DefaultSession.AsyncSendBatchMaxRecords = batchSize[0]
		s.options.DefaultSession.AsyncSendBatchMaxWait = time.Millisecond
	}
	e, err := newSendExecutor(s, gatewaytypes.RuntimeOptions{AsyncSendWorkers: 1, AsyncSendQueueCapacity: capacity, AsyncPoolReleaseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	s.async.Store(&asyncRuntime{send: e})
	t.Cleanup(e.stop)
	return e, s
}
func deferredTestState(s *Server, id uint64) *sessionState {
	state := asyncSendTestState(s, id)
	state.markOpenDispatched()
	state.markOpenComplete()
	return state
}
func deferredNext(t *testing.T, h *deferredProbeHandler) deferredProbeCall {
	t.Helper()
	select {
	case c := <-h.calls:
		return c
	case <-time.After(3 * time.Second):
		t.Fatal("deferred preparation blocked on prior completion")
		return deferredProbeCall{}
	}
}
func deferredDrain(t *testing.T, e *sendExecutor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := e.drain(ctx); err != nil {
		t.Fatal(err)
	}
	if e.depth() != 0 || e.shardQueued[0].Load() != 0 {
		t.Fatal("retained reservation after drain")
	}
	e.deferred[0].mu.Lock()
	defer e.deferred[0].mu.Unlock()
	if len(e.deferred[0].lanes) != 0 {
		t.Fatal("historical session lanes retained")
	}
}

func TestDeferredSendCrossBatchOrderAndOutstandingBudget(t *testing.T) {
	h := &deferredProbeHandler{calls: make(chan deferredProbeCall, 8)}
	e, s := deferredTestExecutor(t, 3, h)
	a, b := deferredTestState(s, 1), deferredTestState(s, 2)
	submit := func(state *sessionState, id string) deferredProbeCall {
		t.Helper()
		if !e.submit(state, "token-"+id, &frame.SendPacket{ClientMsgNo: id}) {
			t.Fatal("unexpected rejection")
		}
		return deferredNext(t, h)
	}
	first := submit(a, "a1")
	second := submit(a, "a2")
	other := submit(b, "b1")
	if e.depth() != 3 || e.shardQueued[0].Load() != 3 {
		t.Fatalf("dispatch lost ownership: %d/%d", e.depth(), e.shardQueued[0].Load())
	}
	var mu sync.Mutex
	var order []string
	write := func(id string) func() error {
		return func() error { mu.Lock(); defer mu.Unlock(); order = append(order, id); return nil }
	}
	if err := second.publish(0, write("a2")); err != nil {
		t.Fatal(err)
	}
	second.complete(nil)
	if len(order) != 0 || e.depth() != 3 {
		t.Fatal("unpublished result escaped budget or order")
	}
	if e.submit(a, "", &frame.SendPacket{}) {
		t.Fatal("capacity multiplied by async dispatch")
	}
	if err := other.publish(0, write("b1")); err != nil {
		t.Fatal(err)
	}
	other.complete(nil)
	if !reflect.DeepEqual(order, []string{"b1"}) {
		t.Fatal(order)
	}
	if first.items[0].Frame.ClientMsgNo != "a1" || first.items[0].ReplyToken != "token-a1" {
		t.Fatal("workqueue reused deferred descriptors")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(e.drain(ctx), context.Canceled) {
		t.Fatal("drain ignored pending ownership")
	}
	if e.submit(b, "", &frame.SendPacket{}) {
		t.Fatal("drain did not fence admission")
	}
	if err := first.publish(0, write("a1")); err != nil {
		t.Fatal(err)
	}
	first.complete(nil)
	deferredDrain(t, e)
	if !reflect.DeepEqual(order, []string{"b1", "a1", "a2"}) {
		t.Fatal(order)
	}
}

func TestDeferredSendInlineCompletionStillOwnsPreparation(t *testing.T) {
	release := make(chan struct{})
	h := &deferredProbeHandler{calls: make(chan deferredProbeCall, 1), beforeReturn: func(c deferredProbeCall) error {
		_ = c.publish(0, func() error { return nil })
		c.complete(nil)
		<-release
		return nil
	}}
	e, s := deferredTestExecutor(t, 1, h)
	if !e.submit(deferredTestState(s, 1), "", &frame.SendPacket{}) {
		t.Fatal("rejected")
	}
	deferredNext(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(e.drain(ctx), context.Canceled) {
		t.Fatal("drained active preparation")
	}
	if e.depth() != 1 {
		t.Fatal("inline completion released active preparation")
	}
	close(release)
	deferredDrain(t, e)
}

func TestDeferredSendIndependentSessionPublicationDoesNotHoldShardLock(t *testing.T) {
	h := &deferredProbeHandler{calls: make(chan deferredProbeCall, 2)}
	e, s := deferredTestExecutor(t, 2, h)
	if !e.submit(deferredTestState(s, 1), "", &frame.SendPacket{}) {
		t.Fatal("rejected")
	}
	a := deferredNext(t, h)
	if !e.submit(deferredTestState(s, 2), "", &frame.SendPacket{}) {
		t.Fatal("rejected")
	}
	b := deferredNext(t, h)
	entered, release, published := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { _ = a.publish(0, func() error { close(entered); <-release; return nil }); a.complete(nil) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("write did not start")
	}
	go func() { _ = b.publish(0, func() error { close(published); return nil }); b.complete(nil) }()
	select {
	case <-published:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("independent write blocked by shard mutex")
	}
	close(release)
	deferredDrain(t, e)
}

func TestDeferredSendInvalidCompletionFailsClosedAndDrains(t *testing.T) {
	for _, mode := range []string{"missing", "rejected", "error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			sentinel := errors.New("rejected")
			h := &deferredProbeHandler{calls: make(chan deferredProbeCall, 1)}
			if mode == "rejected" {
				h.beforeReturn = func(deferredProbeCall) error { return sentinel }
			}
			e, s := deferredTestExecutor(t, 1, h)
			state := deferredTestState(s, 1)
			if !e.submit(state, "", &frame.SendPacket{}) {
				t.Fatal("rejected")
			}
			c := deferredNext(t, h)
			switch mode {
			case "missing":
				c.complete(nil)
			case "error":
				c.complete(sentinel)
			case "panic":
				_ = c.publish(0, func() error { panic("write panic") })
				c.complete(nil)
			}
			deferredDrain(t, e)
			if !state.isClosed() {
				t.Fatal("invalid completion left session open")
			}
		})
	}
}

func TestDeferredSendDuplicateAndInvalidPublicationDoNotReleaseTwice(t *testing.T) {
	h := &deferredProbeHandler{calls: make(chan deferredProbeCall, 1)}
	e, s := deferredTestExecutor(t, 1, h)
	if !e.submit(deferredTestState(s, 1), "", &frame.SendPacket{}) {
		t.Fatal("rejected")
	}
	c := deferredNext(t, h)
	if err := c.publish(-1, func() error { return nil }); err == nil {
		t.Fatal("invalid index accepted")
	}
	if err := c.publish(0, nil); err == nil {
		t.Fatal("nil publication accepted")
	}
	if err := c.publish(0, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.publish(0, func() error { t.Error("duplicate executed"); return nil }); err == nil {
		t.Fatal("duplicate accepted")
	}
	c.complete(nil)
	deferredDrain(t, e)
}

func TestDeferredSendCompletionErrorHandlingRetainsOwnership(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	h := &deferredProbeHandler{calls: make(chan deferredProbeCall, 1), onError: func() { close(entered); <-release }}
	h.beforeReturn = func(c deferredProbeCall) error {
		_ = c.publish(0, func() error { return nil })
		go c.complete(errors.New("terminal failure"))
		<-entered
		return nil
	}
	e, s := deferredTestExecutor(t, 1, h)
	closeOnError := false
	s.options.DefaultSession.CloseOnHandlerError = &closeOnError
	if !e.submit(deferredTestState(s, 1), "", &frame.SendPacket{}) {
		t.Fatal("rejected")
	}
	deferredNext(t, h)
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := e.drain(ctx)
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("released while completion error callback was running: %v", err)
	}
	deferredDrain(t, e)
}

func TestDeferredSendConcurrentCompletionsPreserveOneSessionOrder(t *testing.T) {
	const n = 32
	h := &deferredProbeHandler{calls: make(chan deferredProbeCall, n)}
	e, s := deferredTestExecutor(t, n, h)
	state := deferredTestState(s, 1)
	calls := make([]deferredProbeCall, n)
	for i := range calls {
		if !e.submit(state, "", &frame.SendPacket{}) {
			t.Fatal("rejected")
		}
		calls[i] = deferredNext(t, h)
	}
	var order []int
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(n)
	for i := n - 1; i >= 0; i-- {
		go func(index int) {
			defer wg.Done()
			if err := calls[index].publish(0, func() error { mu.Lock(); order = append(order, index); mu.Unlock(); return nil }); err != nil {
				t.Error(err)
			}
			calls[index].complete(nil)
		}(i)
	}
	wg.Wait()
	deferredDrain(t, e)
	if len(order) != n {
		t.Fatal(order)
	}
	for i, got := range order {
		if i != got {
			t.Fatal(order)
		}
	}
}

func TestDeferredSendBatchErrorReportsEachSessionOnce(t *testing.T) {
	h := &deferredProbeHandler{calls: make(chan deferredProbeCall, 2)}
	e, s := deferredTestExecutor(t, 2, h, 2)
	closeOnError := false
	s.options.DefaultSession.CloseOnHandlerError = &closeOnError
	state := deferredTestState(s, 1)
	for range 2 {
		if !e.submit(state, "", &frame.SendPacket{}) {
			t.Fatal("rejected")
		}
	}
	c := deferredNext(t, h)
	if len(c.items) != 2 {
		t.Fatal("expected one two-record batch")
	}
	c.complete(errors.New("terminal failure"))
	deferredDrain(t, e)
	if got := len(h.sessionErrors()); got != 1 {
		t.Fatalf("one terminal batch error reported %d times for one session", got)
	}
}
