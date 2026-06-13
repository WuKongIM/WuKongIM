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
	state.markOpenComplete()

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

func TestSendExecutorCopiesPayloadWhenAdapterDoesNotOwnDecodedFrames(t *testing.T) {
	executor, err := newSendExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()

	payload := []byte("payload")
	send := &frame.SendPacket{
		ChannelID:   "channel-a",
		ChannelType: 2,
		Payload:     payload,
	}
	state := &sessionState{listener: &listenerRuntime{}}

	if !executor.submit(state, "", send) {
		t.Fatal("submit returned false")
	}
	payload[0] = 'P'

	queued := queuedSendExecutorPacket(t, executor)
	if got, want := string(queued.Payload), "payload"; got != want {
		t.Fatalf("queued payload = %q, want %q", got, want)
	}
	if sameBackingArray(queued.Payload, send.Payload) {
		t.Fatal("queued payload aliases non-owned decoded payload")
	}
}

func TestSendExecutorAdoptsPayloadWhenAdapterOwnsDecodedFrames(t *testing.T) {
	executor, err := newSendExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()

	payload := []byte("owned")
	send := &frame.SendPacket{
		ClientMsgNo: "before",
		ChannelID:   "channel-a",
		ChannelType: 2,
		Payload:     payload,
	}
	state := &sessionState{listener: &listenerRuntime{ownsDecodedFrames: true}}

	if !executor.submit(state, "", send) {
		t.Fatal("submit returned false")
	}
	send.ClientMsgNo = "after"

	queued := queuedSendExecutorPacket(t, executor)
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

func TestSendExecutorUsesRuntimeWorkerCountAndCapacity(t *testing.T) {
	executor, err := newSendExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        4,
		AsyncSendQueueCapacity:  16,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()

	if got, want := executor.workers, 4; got != want {
		t.Fatalf("send executor workers = %d, want %d", got, want)
	}
	if got, want := sendExecutorShardCount(t, executor), 4; got != want {
		t.Fatalf("send executor shards = %d, want %d", got, want)
	}
	if got, want := executor.totalCapacity(), 16; got != want {
		t.Fatalf("send executor capacity = %d, want %d", got, want)
	}
}

func TestSendExecutorBoundsMailboxCapacityAcrossShards(t *testing.T) {
	executor, err := newSendExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        4,
		AsyncSendQueueCapacity:  10,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()

	shardCaps := sendExecutorShardCapacities(t, executor)
	if got, want := len(shardCaps), 4; got != want {
		t.Fatalf("send executor shards = %d, want %d", got, want)
	}
	for i, capacity := range shardCaps {
		if capacity <= 0 {
			t.Fatalf("send executor shard %d capacity = %d, want > 0", i, capacity)
		}
		if capacity != 3 {
			t.Fatalf("send executor shard %d capacity = %d, want ceil(10/4)=3", i, capacity)
		}
	}
	if got, want := executor.totalCapacity(), 10; got != want {
		t.Fatalf("send executor total capacity = %d, want %d", got, want)
	}
}

func TestSendExecutorRejectsWhenShardMailboxFullBeforePayloadClone(t *testing.T) {
	executor, err := newSendExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()

	state := &sessionState{listener: &listenerRuntime{}}
	if !executor.submit(state, "", &frame.SendPacket{Payload: []byte("first")}) {
		t.Fatal("first submit rejected")
	}

	payload := make([]byte, 64*1024)
	packet := &frame.SendPacket{Payload: payload}
	baseline := testing.AllocsPerRun(1000, func() {})
	actual := testing.AllocsPerRun(1000, func() {
		_ = executor.submit(state, "", packet)
	})
	if actual > baseline+0.5 {
		t.Fatalf("allocs = %.2f, want near baseline %.2f; full shard should not clone payload", actual, baseline)
	}
	if got := executor.depth(); got != 1 {
		t.Fatalf("send executor depth = %d, want 1", got)
	}
}

func TestAsyncSendBatchOptionsUseDefaults(t *testing.T) {
	opt := gatewaytypes.NormalizeSessionOptions(gatewaytypes.SessionOptions{})
	if got, want := opt.AsyncSendBatchMaxWait, time.Millisecond; got != want {
		t.Fatalf("AsyncSendBatchMaxWait = %s, want %s", got, want)
	}
	if got, want := opt.AsyncSendBatchMaxRecords, 512; got != want {
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
	if got, want := opt.AsyncSendBatchMaxRecords, 512; got != want {
		t.Fatalf("AsyncSendBatchMaxRecords = %d, want %d", got, want)
	}
	if got, want := opt.AsyncSendBatchMaxBytes, 512*1024; got != want {
		t.Fatalf("AsyncSendBatchMaxBytes = %d, want %d", got, want)
	}
}

func TestAsyncDispatchWorkerCountUsesExplicitOverride(t *testing.T) {
	got := asyncDispatchWorkerCount(gatewaytypes.RuntimeOptions{AsyncSendWorkers: 7})
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
		{name: "low clamps to minimum", gomaxprocs: 1, want: 128},
		{name: "scales by cpu", gomaxprocs: 10, want: 128},
		{name: "high caps maximum", gomaxprocs: 128, want: 256},
		{name: "invalid clamps minimum", gomaxprocs: 0, want: 128},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adaptiveAsyncDispatchWorkerCount(tt.gomaxprocs); got != tt.want {
				t.Fatalf("adaptive worker count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAdaptiveAsyncDispatchWorkerCountKeepsSessionBurstRoom(t *testing.T) {
	workers := adaptiveAsyncDispatchWorkerCount(10)
	if got, want := asyncDispatchQueueCapacityPerShard(workers), asyncDispatchQueuePerWorker; got != want {
		t.Fatalf("default shard capacity = %d, want %d for session bursts", got, want)
	}
}

func TestAsyncSendDispatchShardsBySessionNotChannel(t *testing.T) {
	const shards = 256
	state := &sessionState{session: session.New(session.Config{ID: 42})}

	first := asyncDispatchShardIndex(state, &frame.SendPacket{ChannelID: "channel-a", ChannelType: 2}, shards)
	second := asyncDispatchShardIndex(state, &frame.SendPacket{ChannelID: "channel-b", ChannelType: 2}, shards)

	if first != second {
		t.Fatalf("shard indexes = %d/%d, want same session shard independent of channel", first, second)
	}
	if want := int(state.session.ID() % uint64(shards)); first != want {
		t.Fatalf("shard index = %d, want session shard %d", first, want)
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

func TestSendExecutorObserverTracksQueueWaitAndBatch(t *testing.T) {
	observer := &recordingAsyncSendObserver{}
	handler := &recordingAsyncSendBatchHandler{}
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			Observer: observer,
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        1,
				AsyncSendQueueCapacity:  4,
				AsyncPoolReleaseTimeout: time.Second,
			},
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 8,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, srv.options.Runtime)
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()

	state := &sessionState{
		server:         srv,
		listener:       &listenerRuntime{eventProtocol: "wkproto"},
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}

	if !executor.submit(state, "", &frame.SendPacket{
		ChannelID:   "c1",
		ClientMsgNo: "m1",
		Payload:     []byte("abc"),
	}) {
		t.Fatal("submit rejected")
	}
	srv.observeAsyncSendQueue(executor)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(observer.queueEvents()) >= 2 && len(observer.batchEvents()) == 1 && len(observer.waitEvents()) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if queues, batches, waits := observer.queueEvents(), observer.batchEvents(), observer.waitEvents(); len(queues) < 2 || len(batches) != 1 || len(waits) != 1 {
		t.Fatalf("send observations queue/batch/wait = %d/%d/%d, want at least 2/1/1", len(queues), len(batches), len(waits))
	}
	queueEvents := observer.queueEvents()
	if len(queueEvents) < 2 {
		t.Fatalf("queue events = %d, want enqueue and dequeue observations", len(queueEvents))
	}
	if got, want := queueEvents[0].Depth, 1; got != want {
		t.Fatalf("enqueue queue depth = %d, want %d", got, want)
	}
	if got, want := queueEvents[0].Capacity, 4; got != want {
		t.Fatalf("queue capacity = %d, want %d", got, want)
	}
	if got, want := queueEvents[len(queueEvents)-1].Depth, 0; got != want {
		t.Fatalf("dequeue queue depth = %d, want %d", got, want)
	}

	batches := observer.batchEvents()
	if len(batches) != 1 {
		t.Fatalf("batch events = %d, want 1", len(batches))
	}
	if got, want := batches[0].Records, 1; got != want {
		t.Fatalf("batch records = %d, want %d", got, want)
	}
	if got, want := batches[0].Bytes, 3; got != want {
		t.Fatalf("batch bytes = %d, want %d", got, want)
	}
	if batches[0].Wait <= 0 {
		t.Fatalf("batch wait = %s, want > 0", batches[0].Wait)
	}

	waits := observer.waitEvents()
	if len(waits) != 1 {
		t.Fatalf("dispatch wait events = %d, want 1", len(waits))
	}
	if got, want := waits[0].Protocol, "wkproto"; got != want {
		t.Fatalf("wait protocol = %q, want %q", got, want)
	}
	if waits[0].Duration <= 0 {
		t.Fatalf("dispatch wait = %s, want > 0", waits[0].Duration)
	}
}

func TestAsyncSendReportsAdmissionResults(t *testing.T) {
	observer := &recordingAsyncSendObserver{}
	handler := &countingAsyncFrameHandler{}
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			Observer: observer,
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        1,
				AsyncSendQueueCapacity:  1,
				AsyncPoolReleaseTimeout: time.Second,
			},
		},
	}
	executor, err := newSendExecutor(nil, srv.options.Runtime)
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()

	state := &sessionState{server: srv, key: connKey{connID: 1}}
	send := &frame.SendPacket{ChannelID: "c1", ChannelType: 1}
	if !executor.submit(state, "r1", send) {
		t.Fatal("first submit failed")
	}
	srv.observeAsyncSendAdmission(nil, true)
	if executor.submit(state, "r2", send) {
		t.Fatal("second submit unexpectedly succeeded")
	}
	srv.observeAsyncSendAdmission(nil, false)

	if len(observer.sendAdmissions) != 2 {
		t.Fatalf("send admissions = %d, want 2", len(observer.sendAdmissions))
	}
	if got := observer.sendAdmissions[0].Result; got != "ok" {
		t.Fatalf("first send admission result = %q, want ok", got)
	}
	if got := observer.sendAdmissions[1].Result; got != "full" {
		t.Fatalf("second send admission result = %q, want full", got)
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

func TestSendExecutorDispatchesBatchOnAnts(t *testing.T) {
	handler := &recordingAsyncSendBatchHandler{}
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        1,
				AsyncSendQueueCapacity:  8,
				AsyncPoolReleaseTimeout: time.Second,
			},
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxWait:    time.Millisecond,
				AsyncSendBatchMaxRecords: 8,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, srv.options.Runtime)
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()

	state := &sessionState{
		server:         srv,
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}
	if !executor.submit(state, "r1", &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "m1", Payload: []byte("one")}) {
		t.Fatal("first submit rejected")
	}
	if !executor.submit(state, "r2", &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "m2", Payload: []byte("two")}) {
		t.Fatal("second submit rejected")
	}

	eventually(t, time.Second, func() bool {
		return len(handler.snapshotBatches()) == 1
	}, "send executor did not dispatch batch")
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

func TestSendExecutorFallsBackToFrameHandler(t *testing.T) {
	handler := &countingAsyncFrameHandler{}
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        1,
				AsyncSendQueueCapacity:  8,
				AsyncPoolReleaseTimeout: time.Second,
			},
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 8,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, srv.options.Runtime)
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()

	state := &sessionState{
		server:         srv,
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}
	if !executor.submit(state, "", &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "m1"}) {
		t.Fatal("first submit rejected")
	}
	if !executor.submit(state, "", &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "m2"}) {
		t.Fatal("second submit rejected")
	}

	eventually(t, time.Second, func() bool {
		return handler.frames.Load() == 2
	}, "send executor did not fall back to OnFrame")
	if got, want := handler.frames.Load(), uint64(2); got != want {
		t.Fatalf("OnFrame calls = %d, want %d", got, want)
	}
}

func TestSendExecutorStopIsIdempotentAndRejectsSubmit(t *testing.T) {
	executor, err := newSendExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  2,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}

	if !executor.submit(&sessionState{}, "", &frame.SendPacket{Payload: []byte("queued")}) {
		t.Fatal("submit before stop rejected")
	}
	executor.stop()
	executor.stop()

	if executor.submit(&sessionState{}, "", &frame.SendPacket{Payload: []byte("after")}) {
		t.Fatal("submit accepted after stop")
	}
	if got := executor.depth(); got != 0 {
		t.Fatalf("send executor depth = %d, want 0 after stop drains mailbox", got)
	}
	if sendExecutorShardIsOpen(t, executor, 0) {
		t.Fatal("send executor shard remained open after stop")
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

func queuedSendExecutorPacket(t *testing.T, executor *sendExecutor) *frame.SendPacket {
	t.Helper()
	task := receiveSendExecutorTask(t, executor, 0)
	send, ok := task.frame.(*frame.SendPacket)
	if !ok {
		t.Fatalf("queued frame = %T, want *frame.SendPacket", task.frame)
	}
	return send
}

func receiveSendExecutorTask(t *testing.T, executor *sendExecutor, shardIndex int) asyncDispatchTask {
	t.Helper()
	if executor == nil {
		t.Fatal("send executor is nil")
	}
	if shardIndex < 0 || shardIndex >= len(executor.shards) {
		t.Fatalf("send executor shard index %d out of range", shardIndex)
	}
	select {
	case task, ok := <-executor.shards[shardIndex].tasks:
		if !ok {
			t.Fatal("send executor tasks channel closed before queued send was read")
		}
		return task
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued send")
		return asyncDispatchTask{}
	}
}

func sendExecutorShardCount(t *testing.T, executor *sendExecutor) int {
	t.Helper()
	if executor == nil {
		t.Fatal("send executor is nil")
	}
	return len(executor.shards)
}

func sendExecutorShardCapacities(t *testing.T, executor *sendExecutor) []int {
	t.Helper()
	if executor == nil {
		t.Fatal("send executor is nil")
	}
	capacities := make([]int, 0, len(executor.shards))
	for i, shard := range executor.shards {
		if shard == nil {
			t.Fatalf("send executor shard %d is nil", i)
		}
		capacities = append(capacities, cap(shard.tasks))
	}
	return capacities
}

func sendExecutorShardIsOpen(t *testing.T, executor *sendExecutor, shardIndex int) bool {
	t.Helper()
	if executor == nil {
		t.Fatal("send executor is nil")
	}
	if shardIndex < 0 || shardIndex >= len(executor.shards) {
		t.Fatalf("send executor shard index %d out of range", shardIndex)
	}
	select {
	case _, ok := <-executor.shards[shardIndex].tasks:
		return ok
	default:
		return true
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

type recordingAsyncSendObserver struct {
	mu             sync.Mutex
	queues         []gatewaytypes.AsyncSendQueueEvent
	batches        []gatewaytypes.AsyncSendBatchEvent
	waits          []gatewaytypes.AsyncSendDispatchWaitEvent
	authQueues     []gatewaytypes.AsyncAuthQueueEvent
	authAdmissions []gatewaytypes.AsyncAuthAdmissionEvent
	authWaits      []gatewaytypes.AsyncAuthWaitEvent
	sendAdmissions []gatewaytypes.AsyncSendAdmissionEvent
}

func (o *recordingAsyncSendObserver) OnConnectionOpen(gatewaytypes.ConnectionEvent)  {}
func (o *recordingAsyncSendObserver) OnConnectionClose(gatewaytypes.ConnectionEvent) {}
func (o *recordingAsyncSendObserver) OnAuth(gatewaytypes.AuthEvent)                  {}
func (o *recordingAsyncSendObserver) OnFrameIn(gatewaytypes.FrameEvent)              {}
func (o *recordingAsyncSendObserver) OnFrameOut(gatewaytypes.FrameEvent)             {}
func (o *recordingAsyncSendObserver) OnFrameHandled(gatewaytypes.FrameHandleEvent)   {}
func (o *recordingAsyncSendObserver) OnAsyncSendQueue(event gatewaytypes.AsyncSendQueueEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.queues = append(o.queues, event)
}
func (o *recordingAsyncSendObserver) OnAsyncSendBatch(event gatewaytypes.AsyncSendBatchEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.batches = append(o.batches, event)
}
func (o *recordingAsyncSendObserver) OnAsyncSendDispatchWait(event gatewaytypes.AsyncSendDispatchWaitEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.waits = append(o.waits, event)
}
func (o *recordingAsyncSendObserver) OnAsyncAuthQueue(event gatewaytypes.AsyncAuthQueueEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.authQueues = append(o.authQueues, event)
}
func (o *recordingAsyncSendObserver) OnAsyncAuthAdmission(event gatewaytypes.AsyncAuthAdmissionEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.authAdmissions = append(o.authAdmissions, event)
}
func (o *recordingAsyncSendObserver) OnAsyncAuthWait(event gatewaytypes.AsyncAuthWaitEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.authWaits = append(o.authWaits, event)
}
func (o *recordingAsyncSendObserver) OnAsyncSendAdmission(event gatewaytypes.AsyncSendAdmissionEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sendAdmissions = append(o.sendAdmissions, event)
}

func (o *recordingAsyncSendObserver) queueEvents() []gatewaytypes.AsyncSendQueueEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]gatewaytypes.AsyncSendQueueEvent(nil), o.queues...)
}

func (o *recordingAsyncSendObserver) batchEvents() []gatewaytypes.AsyncSendBatchEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]gatewaytypes.AsyncSendBatchEvent(nil), o.batches...)
}

func (o *recordingAsyncSendObserver) waitEvents() []gatewaytypes.AsyncSendDispatchWaitEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]gatewaytypes.AsyncSendDispatchWaitEvent(nil), o.waits...)
}

func (o *recordingAsyncSendObserver) authWaitEvents() []gatewaytypes.AsyncAuthWaitEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]gatewaytypes.AsyncAuthWaitEvent(nil), o.authWaits...)
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
	state.markOpenComplete()
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
