package core

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestServerAsyncSendDispatchRejectsWhenQueueFull(t *testing.T) {
	handler := &countingAsyncFrameHandler{}
	srv := &Server{dispatcher: newDispatcher(handler)}
	queue := newAsyncDispatchQueueWithCapacity(1, 1)
	srv.asyncDispatch.Store(queue)
	queue.shards[0].tasks <- asyncDispatchTask{}
	state := &sessionState{
		server:         srv,
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}
	state.markOpenDispatched()

	done := make(chan struct{})
	go func() {
		srv.dispatchSendFrameAsync(state, "", &frame.SendPacket{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Millisecond):
		<-queue.shards[0].tasks
		<-done
		t.Fatal("dispatchSendFrameAsync blocked when async queue was full")
	}

	if got := handler.frames.Load(); got != 0 {
		t.Fatalf("handler frames = %d, want async queue full rejection without synchronous fallback", got)
	}
	if !state.isClosed() {
		t.Fatal("state was not closed after async queue overflow")
	}
	errs := handler.sessionErrors()
	if len(errs) != 1 || !errors.Is(errs[0], gatewaytypes.ErrAsyncDispatchQueueFull) {
		t.Fatalf("session errors = %v, want ErrAsyncDispatchQueueFull", errs)
	}
	reasons := handler.closeReasons()
	if len(reasons) == 0 || reasons[0] != gatewaytypes.CloseReasonAsyncDispatchQueueFull {
		t.Fatalf("close reasons = %v, want %q", reasons, gatewaytypes.CloseReasonAsyncDispatchQueueFull)
	}
}

func TestServerAsyncSendDispatchRejectsFullQueueBeforePayloadClone(t *testing.T) {
	srv := &Server{dispatcher: newDispatcher(&countingAsyncFrameHandler{})}
	queue := newAsyncDispatchQueueWithCapacity(1, 1)
	srv.asyncDispatch.Store(queue)
	queue.shards[0].tasks <- asyncDispatchTask{}

	payload := make([]byte, 64*1024)
	packet := &frame.SendPacket{Payload: payload}
	newState := func() *sessionState {
		return &sessionState{
			server:         srv,
			closedCh:       make(chan struct{}),
			requestContext: context.Background(),
		}
	}

	baseline := testing.AllocsPerRun(1000, func() {
		newState().close(gatewaytypes.CloseReasonAsyncDispatchQueueFull, gatewaytypes.ErrAsyncDispatchQueueFull)
	})
	actual := testing.AllocsPerRun(1000, func() {
		srv.dispatchSendFrameAsync(newState(), "", packet)
	})
	if actual > baseline+0.5 {
		t.Fatalf("allocs = %.2f, want near close baseline %.2f; full async queue should not clone payload", actual, baseline)
	}
}

func TestAsyncDispatchQueueCopiesPayloadWhenAdapterDoesNotOwnDecodedFrames(t *testing.T) {
	queue := newAsyncDispatchQueueWithCapacity(1, 1)
	payload := []byte("payload")
	send := &frame.SendPacket{
		ChannelID:   "channel-a",
		ChannelType: 2,
		Payload:     payload,
	}
	state := &sessionState{listener: &listenerRuntime{}}

	if !queue.submitSend(state, "", send) {
		t.Fatal("submitSend returned false")
	}
	payload[0] = 'P'

	queued := queuedAsyncSendPacket(t, queue)
	if got, want := string(queued.Payload), "payload"; got != want {
		t.Fatalf("queued payload = %q, want %q", got, want)
	}
	if sameBackingArray(queued.Payload, send.Payload) {
		t.Fatal("queued payload aliases non-owned decoded payload")
	}
}

func TestAsyncDispatchQueueAdoptsPayloadWhenAdapterOwnsDecodedFrames(t *testing.T) {
	queue := newAsyncDispatchQueueWithCapacity(1, 1)
	payload := []byte("owned")
	send := &frame.SendPacket{
		ClientMsgNo: "before",
		ChannelID:   "channel-a",
		ChannelType: 2,
		Payload:     payload,
	}
	state := &sessionState{listener: &listenerRuntime{ownsDecodedFrames: true}}

	if !queue.submitSend(state, "", send) {
		t.Fatal("submitSend returned false")
	}
	send.ClientMsgNo = "after"

	queued := queuedAsyncSendPacket(t, queue)
	if !sameBackingArray(queued.Payload, send.Payload) {
		t.Fatal("queued payload did not adopt owned decoded payload")
	}
	if got, want := cap(queued.Payload), len(queued.Payload); got != want {
		t.Fatalf("queued payload cap = %d, want %d to prevent appending into adjacent decoded bytes", got, want)
	}
	if got, want := queued.ClientMsgNo, "before"; got != want {
		t.Fatalf("queued ClientMsgNo = %q, want copied metadata %q", got, want)
	}
}

func TestServerAsyncSendDispatchUsesConfiguredWorkerCount(t *testing.T) {
	srv := &Server{
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendDispatchWorkers: 4,
			},
		},
	}

	srv.startAsyncDispatcher()
	queue := srv.asyncDispatcher()
	if queue == nil {
		t.Fatal("async dispatcher was not started")
	}
	if got, want := len(queue.shards), 4; got != want {
		t.Fatalf("async dispatch shards = %d, want %d", got, want)
	}
	for i, shard := range queue.shards {
		if got, want := cap(shard.tasks), asyncDispatchQueuePerWorker; got != want {
			t.Fatalf("async dispatch shard %d capacity = %d, want %d", i, got, want)
		}
	}

	queue.close()
	srv.workerWG.Wait()
}

func TestAsyncDispatchQueueBoundsBufferedCapacityForManyWorkers(t *testing.T) {
	queue := newAsyncDispatchQueue(maxAsyncDispatchWorkers)
	if queue == nil {
		t.Fatal("async dispatch queue was nil")
	}

	totalCapacity := 0
	for i, shard := range queue.shards {
		if got := cap(shard.tasks); got != asyncDispatchMinQueuePerWorker {
			t.Fatalf("async dispatch shard %d capacity = %d, want %d", i, got, asyncDispatchMinQueuePerWorker)
		}
		totalCapacity += cap(shard.tasks)
	}
	if totalCapacity > asyncDispatchMaxBufferedTasks {
		t.Fatalf("async dispatch total capacity = %d, want <= %d", totalCapacity, asyncDispatchMaxBufferedTasks)
	}
}

func TestAsyncSendBatchOptionsUseDefaults(t *testing.T) {
	opt := gatewaytypes.NormalizeSessionOptions(gatewaytypes.SessionOptions{})
	if got, want := opt.AsyncSendBatchMaxWait, 500*time.Microsecond; got != want {
		t.Fatalf("AsyncSendBatchMaxWait = %s, want %s", got, want)
	}
	if got, want := opt.AsyncSendBatchMaxRecords, 128; got != want {
		t.Fatalf("AsyncSendBatchMaxRecords = %d, want %d", got, want)
	}
	if got, want := opt.AsyncSendBatchMaxBytes, 512*1024; got != want {
		t.Fatalf("AsyncSendBatchMaxBytes = %d, want %d", got, want)
	}
}

func TestAsyncSendBatchOptionsCanDisableWaitButNotBounds(t *testing.T) {
	opt := gatewaytypes.NormalizeSessionOptions(gatewaytypes.SessionOptions{
		AsyncSendBatchMaxWait:    -time.Microsecond,
		AsyncSendBatchMaxRecords: -1,
		AsyncSendBatchMaxBytes:   -1,
	})
	if opt.AsyncSendBatchMaxWait != 0 {
		t.Fatalf("AsyncSendBatchMaxWait = %s, want 0", opt.AsyncSendBatchMaxWait)
	}
	if got, want := opt.AsyncSendBatchMaxRecords, 128; got != want {
		t.Fatalf("AsyncSendBatchMaxRecords = %d, want %d", got, want)
	}
	if got, want := opt.AsyncSendBatchMaxBytes, 512*1024; got != want {
		t.Fatalf("AsyncSendBatchMaxBytes = %d, want %d", got, want)
	}
}

func TestAsyncDispatchWorkerCountUsesExplicitOverride(t *testing.T) {
	got := asyncDispatchWorkerCount(gatewaytypes.SessionOptions{AsyncSendDispatchWorkers: 7})
	if got != 7 {
		t.Fatalf("worker count = %d, want explicit override 7", got)
	}
}

func TestAdaptiveAsyncDispatchWorkerCountScalesAndClamps(t *testing.T) {
	tests := []struct {
		name       string
		gomaxprocs int
		want       int
	}{
		{name: "low clamps to minimum", gomaxprocs: 1, want: 64},
		{name: "scales by cpu", gomaxprocs: 10, want: 640},
		{name: "high caps maximum", gomaxprocs: 128, want: 1024},
		{name: "invalid clamps minimum", gomaxprocs: 0, want: 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adaptiveAsyncDispatchWorkerCount(tt.gomaxprocs); got != tt.want {
				t.Fatalf("adaptive worker count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRecordAsyncDispatchWaitIncludesSendClientMsgNo(t *testing.T) {
	sink := &recordingSendTraceSink{}
	restore := sendtrace.SetSink(sink)
	defer restore()

	recordAsyncDispatchWait(asyncDispatchTask{
		enqueuedAt: time.Now().Add(-time.Millisecond),
		frame: &frame.SendPacket{
			ClientMsgNo: "async-wait-1",
		},
	})

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("recorded events = %d, want 1", len(events))
	}
	if got := events[0].Stage; got != sendtrace.StageGatewayAsyncDispatchWait {
		t.Fatalf("stage = %s, want %s", got, sendtrace.StageGatewayAsyncDispatchWait)
	}
	if got := events[0].ClientMsgNo; got != "async-wait-1" {
		t.Fatalf("client msg no = %q, want async-wait-1", got)
	}
	if events[0].Duration <= 0 {
		t.Fatalf("duration = %s, want > 0", events[0].Duration)
	}
}

func TestAsyncSendBatchCollectHonorsMaxRecordsAndOrder(t *testing.T) {
	tasks := make(chan asyncDispatchTask, 3)
	tasks <- asyncSendBatchTestTask("m1", 1)
	tasks <- asyncSendBatchTestTask("m2", 1)
	tasks <- asyncSendBatchTestTask("m3", 1)

	collector := newAsyncSendBatchCollector(tasks, asyncSendBatchLimits{
		maxRecords: 2,
		maxBytes:   1024,
	})
	batch, ok := collector.nextBatch()
	if !ok {
		t.Fatal("nextBatch returned ok=false")
	}
	if got, want := asyncSendBatchClientMsgNos(batch), []string{"m1", "m2"}; !equalStrings(got, want) {
		t.Fatalf("batch client msg nos = %v, want %v", got, want)
	}
	batch, ok = collector.nextBatch()
	if !ok {
		t.Fatal("second nextBatch returned ok=false")
	}
	if got, want := asyncSendBatchClientMsgNos(batch), []string{"m3"}; !equalStrings(got, want) {
		t.Fatalf("second batch client msg nos = %v, want %v", got, want)
	}
}

func TestAsyncSendBatchCollectStopsBeforeMaxBytesAndPreservesPending(t *testing.T) {
	tasks := make(chan asyncDispatchTask, 3)
	tasks <- asyncSendBatchTestTask("m1", 4)
	tasks <- asyncSendBatchTestTask("m2", 6)
	tasks <- asyncSendBatchTestTask("m3", 2)

	collector := newAsyncSendBatchCollector(tasks, asyncSendBatchLimits{
		maxRecords: 8,
		maxBytes:   8,
	})
	batch, ok := collector.nextBatch()
	if !ok {
		t.Fatal("nextBatch returned ok=false")
	}
	if got, want := asyncSendBatchClientMsgNos(batch), []string{"m1"}; !equalStrings(got, want) {
		t.Fatalf("batch client msg nos = %v, want %v", got, want)
	}
	batch, ok = collector.nextBatch()
	if !ok {
		t.Fatal("second nextBatch returned ok=false")
	}
	if got, want := asyncSendBatchClientMsgNos(batch), []string{"m2", "m3"}; !equalStrings(got, want) {
		t.Fatalf("second batch client msg nos = %v, want %v", got, want)
	}
}

func TestAsyncSendBatchCollectWithZeroWaitDrainsOnlyReadyTasks(t *testing.T) {
	tasks := make(chan asyncDispatchTask, 2)
	tasks <- asyncSendBatchTestTask("ready", 1)
	collector := newAsyncSendBatchCollector(tasks, asyncSendBatchLimits{
		maxRecords: 8,
		maxBytes:   1024,
	})
	go func() {
		time.Sleep(10 * time.Millisecond)
		tasks <- asyncSendBatchTestTask("late", 1)
	}()

	batch, ok := collector.nextBatch()
	if !ok {
		t.Fatal("nextBatch returned ok=false")
	}
	if got, want := asyncSendBatchClientMsgNos(batch), []string{"ready"}; !equalStrings(got, want) {
		t.Fatalf("batch client msg nos = %v, want %v", got, want)
	}
}

func TestAsyncSendBatchCollectWaitsForNearFutureTask(t *testing.T) {
	tasks := make(chan asyncDispatchTask, 2)
	tasks <- asyncSendBatchTestTask("first", 1)
	collector := newAsyncSendBatchCollector(tasks, asyncSendBatchLimits{
		maxWait:    50 * time.Millisecond,
		maxRecords: 8,
		maxBytes:   1024,
	})
	go func() {
		time.Sleep(time.Millisecond)
		tasks <- asyncSendBatchTestTask("second", 1)
	}()

	batch, ok := collector.nextBatch()
	if !ok {
		t.Fatal("nextBatch returned ok=false")
	}
	if got, want := asyncSendBatchClientMsgNos(batch), []string{"first", "second"}; !equalStrings(got, want) {
		t.Fatalf("batch client msg nos = %v, want %v", got, want)
	}
}

func TestAsyncSendDispatchUsesBatchHandler(t *testing.T) {
	handler := &recordingAsyncSendBatchHandler{}
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{DefaultSession: gatewaytypes.SessionOptions{
			AsyncSendBatchMaxWait:    time.Millisecond,
			AsyncSendBatchMaxRecords: 8,
			AsyncSendBatchMaxBytes:   1024,
		}},
	}
	state := &sessionState{
		server:         srv,
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}
	tasks := make(chan asyncDispatchTask, 2)
	tasks <- asyncDispatchTask{state: state, replyToken: "r1", frame: &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "m1", Payload: []byte("one")}, enqueuedAt: time.Now()}
	tasks <- asyncDispatchTask{state: state, replyToken: "r2", frame: &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "m2", Payload: []byte("two")}, enqueuedAt: time.Now()}
	close(tasks)

	srv.workerWG.Add(1)
	srv.runAsyncDispatchWorker(tasks)

	if got := handler.frameCalls.Load(); got != 0 {
		t.Fatalf("OnFrame calls = %d, want 0 when batch handler is available", got)
	}
	batches := handler.snapshotBatches()
	if len(batches) != 1 {
		t.Fatalf("batch calls = %d, want 1", len(batches))
	}
	if got := len(batches[0]); got != 2 {
		t.Fatalf("batch size = %d, want 2", got)
	}
	if got, want := []string{batches[0][0].Frame.ClientMsgNo, batches[0][1].Frame.ClientMsgNo}, []string{"m1", "m2"}; !equalStrings(got, want) {
		t.Fatalf("batch client msg nos = %v, want %v", got, want)
	}
	if got, want := []string{batches[0][0].ReplyToken, batches[0][1].ReplyToken}, []string{"r1", "r2"}; !equalStrings(got, want) {
		t.Fatalf("reply tokens = %v, want %v", got, want)
	}
}

func TestAsyncSendDispatchBatchItemStoresContextByValue(t *testing.T) {
	handler := &recordingAsyncSendBatchHandler{}
	srv := &Server{dispatcher: newDispatcher(handler)}
	requestContext := context.WithValue(context.Background(), struct{}{}, "batch-context")
	state := &sessionState{
		server: srv,
		listener: &listenerRuntime{options: gatewaytypes.ListenerOptions{
			Name:      "listener-a",
			Network:   "tcp",
			Transport: "gnet",
			Protocol:  "wkproto",
		}},
		session:        session.New(session.Config{ID: 1, Listener: "listener-a"}),
		closedCh:       make(chan struct{}),
		requestContext: requestContext,
	}

	handled := srv.dispatchSendBatch([]asyncDispatchTask{{
		state:      state,
		replyToken: "reply-1",
		frame:      &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "m1"},
		enqueuedAt: time.Now(),
	}})
	if !handled {
		t.Fatal("dispatchSendBatch returned false")
	}

	batches := handler.snapshotBatches()
	if len(batches) != 1 || len(batches[0]) != 1 {
		t.Fatalf("batches = %#v, want one batch with one item", batches)
	}
	item := batches[0][0]
	if got := reflect.TypeOf(item.Context).Kind(); got != reflect.Struct {
		t.Fatalf("SendBatchItem.Context kind = %s, want struct value", got)
	}
	if item.Context.Session == nil {
		t.Fatal("batch context did not include session")
	}
	if got := item.Context.Listener; got != "listener-a" {
		t.Fatalf("listener = %q, want listener-a", got)
	}
	if got := item.Context.ReplyToken; got != "reply-1" {
		t.Fatalf("context reply token = %q, want reply-1", got)
	}
	if got := item.ReplyToken; got != "reply-1" {
		t.Fatalf("item reply token = %q, want reply-1", got)
	}
	if item.Context.RequestContext != requestContext {
		t.Fatal("batch context did not preserve request context")
	}
}

func TestAsyncSendDispatchFallsBackToFrameHandler(t *testing.T) {
	handler := &countingAsyncFrameHandler{}
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{DefaultSession: gatewaytypes.SessionOptions{
			AsyncSendBatchMaxRecords: 8,
			AsyncSendBatchMaxBytes:   1024,
		}},
	}
	state := &sessionState{
		server:         srv,
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}
	tasks := make(chan asyncDispatchTask, 2)
	tasks <- asyncDispatchTask{state: state, frame: &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "m1"}}
	tasks <- asyncDispatchTask{state: state, frame: &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "m2"}}
	close(tasks)

	srv.workerWG.Add(1)
	srv.runAsyncDispatchWorker(tasks)

	if got, want := handler.frames.Load(), uint64(2); got != want {
		t.Fatalf("OnFrame calls = %d, want %d", got, want)
	}
}

func asyncSendBatchTestTask(clientMsgNo string, payloadBytes int) asyncDispatchTask {
	return asyncDispatchTask{
		frame: &frame.SendPacket{
			ClientMsgNo: clientMsgNo,
			Payload:     make([]byte, payloadBytes),
		},
	}
}

func queuedAsyncSendPacket(t *testing.T, queue *asyncDispatchQueue) *frame.SendPacket {
	t.Helper()
	select {
	case task := <-queue.shards[0].tasks:
		send, ok := task.frame.(*frame.SendPacket)
		if !ok {
			t.Fatalf("queued frame = %T, want *frame.SendPacket", task.frame)
		}
		return send
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued send")
		return nil
	}
}

func sameBackingArray(a, b []byte) bool {
	return len(a) > 0 && len(b) > 0 && &a[0] == &b[0]
}

func asyncSendBatchClientMsgNos(batch []asyncDispatchTask) []string {
	out := make([]string, 0, len(batch))
	for _, task := range batch {
		send, _ := task.frame.(*frame.SendPacket)
		if send == nil {
			out = append(out, "")
			continue
		}
		out = append(out, send.ClientMsgNo)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type recordingAsyncSendBatchHandler struct {
	frameCalls atomic.Uint64

	mu      sync.Mutex
	batches [][]gatewaytypes.SendBatchItem
}

func (h *recordingAsyncSendBatchHandler) OnListenerError(string, error) {}
func (h *recordingAsyncSendBatchHandler) OnSessionOpen(gatewaytypes.Context) error {
	return nil
}
func (h *recordingAsyncSendBatchHandler) OnFrame(gatewaytypes.Context, frame.Frame) error {
	h.frameCalls.Add(1)
	return nil
}
func (h *recordingAsyncSendBatchHandler) OnSendBatch(items []gatewaytypes.SendBatchItem) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.batches = append(h.batches, append([]gatewaytypes.SendBatchItem(nil), items...))
	return nil
}
func (h *recordingAsyncSendBatchHandler) OnSessionClose(gatewaytypes.Context) error {
	return nil
}
func (h *recordingAsyncSendBatchHandler) OnSessionError(gatewaytypes.Context, error) {}

func (h *recordingAsyncSendBatchHandler) snapshotBatches() [][]gatewaytypes.SendBatchItem {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([][]gatewaytypes.SendBatchItem, len(h.batches))
	for i := range h.batches {
		out[i] = append([]gatewaytypes.SendBatchItem(nil), h.batches[i]...)
	}
	return out
}

type countingAsyncFrameHandler struct {
	frames atomic.Uint64

	mu          sync.Mutex
	sessionErrs []error
	closeLog    []gatewaytypes.CloseReason
}

type recordingSendTraceSink struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *recordingSendTraceSink) RecordSendTrace(event sendtrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingSendTraceSink) snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendtrace.Event(nil), s.events...)
}

func (h *countingAsyncFrameHandler) OnListenerError(string, error) {}
func (h *countingAsyncFrameHandler) OnSessionOpen(gatewaytypes.Context) error {
	return nil
}
func (h *countingAsyncFrameHandler) OnFrame(gatewaytypes.Context, frame.Frame) error {
	h.frames.Add(1)
	return nil
}
func (h *countingAsyncFrameHandler) OnSessionClose(ctx gatewaytypes.Context) error {
	if ctx.CloseReason != "" {
		h.mu.Lock()
		h.closeLog = append(h.closeLog, ctx.CloseReason)
		h.mu.Unlock()
	}
	return nil
}
func (h *countingAsyncFrameHandler) OnSessionError(ctx gatewaytypes.Context, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionErrs = append(h.sessionErrs, err)
	if ctx.CloseReason != "" {
		h.closeLog = append(h.closeLog, ctx.CloseReason)
	}
}

func (h *countingAsyncFrameHandler) sessionErrors() []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]error(nil), h.sessionErrs...)
}

func (h *countingAsyncFrameHandler) closeReasons() []gatewaytypes.CloseReason {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]gatewaytypes.CloseReason(nil), h.closeLog...)
}

func BenchmarkServerAsyncSendDispatchQueueFullReject(b *testing.B) {
	handler := &countingAsyncFrameHandler{}
	srv := &Server{dispatcher: newDispatcher(handler)}
	queue := newAsyncDispatchQueueWithCapacity(1, 1)
	srv.asyncDispatch.Store(queue)
	queue.shards[0].tasks <- asyncDispatchTask{}
	state := &sessionState{
		server:         srv,
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}
	state.markOpenDispatched()
	packet := &frame.SendPacket{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		srv.dispatchSendFrameAsync(state, "", packet)
	}
	b.StopTimer()
	if got := handler.frames.Load(); got != 0 {
		b.Fatalf("handler frames = %d, want 0", got)
	}
}
