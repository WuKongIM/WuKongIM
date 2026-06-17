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
	handler := newBlockingAsyncSendFrameHandler()
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 1,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer func() {
		close(handler.release)
		executor.stop()
	}()
	srv.async.Store(&asyncRuntime{send: executor})

	if !executor.submit(asyncSendTestState(srv, 1), "", &frame.SendPacket{ClientMsgNo: "running"}) {
		t.Fatal("running submit rejected")
	}
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("blocking handler did not start")
	}
	if !executor.submit(asyncSendTestState(srv, 1), "", &frame.SendPacket{ClientMsgNo: "queued"}) {
		t.Fatal("queued submit rejected")
	}

	state := asyncSendTestState(srv, 1)
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
		<-done
		t.Fatal("dispatchSendFrameAsync blocked when async queue was full")
	}

	if got := handler.frames(); got != 1 {
		t.Fatalf("handler frames = %d, want async queue full rejection without synchronous fallback", got)
	}
	if !state.isClosed() {
		t.Fatal("state was not closed after async queue overflow")
	}
	errs := handler.counting.sessionErrors()
	if len(errs) != 1 || !errors.Is(errs[0], gatewaytypes.ErrAsyncDispatchQueueFull) {
		t.Fatalf("session errors = %v, want ErrAsyncDispatchQueueFull", errs)
	}
	reasons := handler.counting.closeReasons()
	if len(reasons) == 0 || reasons[0] != gatewaytypes.CloseReasonAsyncDispatchQueueFull {
		t.Fatalf("close reasons = %v, want %q", reasons, gatewaytypes.CloseReasonAsyncDispatchQueueFull)
	}
}

func TestServerAsyncSendDispatchRejectsFullQueueBeforePayloadClone(t *testing.T) {
	handler := newBlockingAsyncSendFrameHandler()
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 1,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer func() {
		close(handler.release)
		executor.stop()
	}()
	srv.async.Store(&asyncRuntime{send: executor})
	if !executor.submit(asyncSendTestState(srv, 1), "", &frame.SendPacket{ClientMsgNo: "running"}) {
		t.Fatal("running submit rejected")
	}
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("blocking handler did not start")
	}
	if !executor.submit(asyncSendTestState(srv, 1), "", &frame.SendPacket{ClientMsgNo: "queued"}) {
		t.Fatal("queued submit rejected")
	}

	payload := make([]byte, 64*1024)
	packet := &frame.SendPacket{Payload: payload}
	newState := func() *sessionState {
		return asyncSendTestState(srv, 1)
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
	handler := newBlockingRecordingAsyncSendBatchHandler()
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 1,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer func() {
		handler.releaseAll()
		executor.stop()
	}()

	payload := []byte("payload")
	send := &frame.SendPacket{
		ChannelID:   "channel-a",
		ChannelType: 2,
		Payload:     payload,
	}
	state := asyncSendTestState(srv, 1)
	state.listener.ownsDecodedFrames = false

	if !executor.submit(state, "", send) {
		t.Fatal("submit returned false")
	}
	payload[0] = 'P'
	handler.releaseAll()

	queued := handler.onlySend(t)
	if got, want := string(queued.Payload), "payload"; got != want {
		t.Fatalf("queued payload = %q, want %q", got, want)
	}
	if sameBackingArray(queued.Payload, send.Payload) {
		t.Fatal("queued payload aliases non-owned decoded payload")
	}
}

func TestSendExecutorAdoptsPayloadWhenAdapterOwnsDecodedFrames(t *testing.T) {
	handler := newBlockingRecordingAsyncSendBatchHandler()
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 1,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer func() {
		handler.releaseAll()
		executor.stop()
	}()

	payload := []byte("owned")
	send := &frame.SendPacket{
		ClientMsgNo: "before",
		ChannelID:   "channel-a",
		ChannelType: 2,
		Payload:     payload,
	}
	state := asyncSendTestState(srv, 1)
	state.listener.ownsDecodedFrames = true

	if !executor.submit(state, "", send) {
		t.Fatal("submit returned false")
	}
	send.ClientMsgNo = "after"
	handler.releaseAll()

	queued := handler.onlySend(t)
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
	if got, want := executor.workers, 4; got != want {
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

	if got, want := executor.workers, 4; got != want {
		t.Fatalf("send executor shards = %d, want %d", got, want)
	}
	if got, want := executor.shardCapacity, 3; got != want {
		t.Fatalf("send executor shard capacity = %d, want ceil(10/4)=3", got)
	}
	if got, want := executor.totalCapacity(), 10; got != want {
		t.Fatalf("send executor total capacity = %d, want %d", got, want)
	}
}

func TestSendExecutorRejectsWhenShardMailboxFullBeforePayloadClone(t *testing.T) {
	handler := newBlockingAsyncSendFrameHandler()
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 1,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer func() {
		close(handler.release)
		executor.stop()
	}()

	state := asyncSendTestState(srv, 1)
	if !executor.submit(state, "", &frame.SendPacket{Payload: []byte("running")}) {
		t.Fatal("running submit rejected")
	}
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("blocking handler did not start")
	}
	if !executor.submit(state, "", &frame.SendPacket{Payload: []byte("queued")}) {
		t.Fatal("queued submit rejected")
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

func TestSendExecutorRejectsWhenGlobalCapacityFullAcrossShards(t *testing.T) {
	handler := newBlockingAsyncSendFrameHandler()
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 1,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  3,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer func() {
		close(handler.release)
		executor.stop()
	}()

	if !executor.submit(asyncSendTestState(srv, 1), "", &frame.SendPacket{Payload: []byte("running")}) {
		t.Fatal("running submit rejected")
	}
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("blocking handler did not start")
	}
	for i := 0; i < 3; i++ {
		if !executor.submit(asyncSendTestState(srv, 1), "", &frame.SendPacket{Payload: []byte{byte(i)}}) {
			t.Fatalf("submit %d rejected before global capacity was full", i+1)
		}
	}

	targetWithShardSpace := asyncSendTestState(srv, 1)
	payload := make([]byte, 64*1024)
	baseline := testing.AllocsPerRun(1000, func() {})
	actual := testing.AllocsPerRun(1000, func() {
		if executor.submit(targetWithShardSpace, "", &frame.SendPacket{Payload: payload}) {
			t.Fatal("submit accepted after global capacity was full")
		}
	})
	if actual > baseline+0.5 {
		t.Fatalf("allocs = %.2f, want near baseline %.2f; global capacity rejection should not clone payload", actual, baseline)
	}
	if got, want := executor.depth(), 3; got != want {
		t.Fatalf("send executor depth = %d, want %d", got, want)
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

func TestAsyncSendDispatchShardsBySessionNotChannel(t *testing.T) {
	const shards = 256
	state := &sessionState{session: session.New(session.Config{ID: 42})}

	first := asyncSendShardIndex(state, &frame.SendPacket{ChannelID: "channel-a", ChannelType: 2}, shards)
	second := asyncSendShardIndex(state, &frame.SendPacket{ChannelID: "channel-b", ChannelType: 2}, shards)

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
	handler := newBlockingAsyncSendFrameHandler()
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			Observer: observer,
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        1,
				AsyncSendQueueCapacity:  1,
				AsyncPoolReleaseTimeout: time.Second,
			},
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 1,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, srv.options.Runtime)
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer func() {
		close(handler.release)
		executor.stop()
	}()

	state := asyncSendTestState(srv, 1)
	send := &frame.SendPacket{ChannelID: "c1", ChannelType: 1}
	if !executor.submit(state, "r1", send) {
		t.Fatal("first submit failed")
	}
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("blocking handler did not start")
	}
	if !executor.submit(state, "r2", send) {
		t.Fatal("queued submit failed")
	}
	srv.observeAsyncSendAdmission(nil, true)
	if executor.submit(state, "r3", send) {
		t.Fatal("third submit unexpectedly succeeded")
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

func TestAsyncSendDispatchMailboxBatchHonorsMaxRecordsAndOrder(t *testing.T) {
	handler := &recordingAsyncSendBatchHandler{}
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 2,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor := &sendExecutor{server: srv}

	executor.dispatchMailboxBatch([]asyncDispatchTask{
		asyncSendBatchTestTask("m1", 1),
		asyncSendBatchTestTask("m2", 1),
		asyncSendBatchTestTask("m3", 1),
	})

	batches := handler.snapshotBatches()
	if len(batches) != 2 {
		t.Fatalf("batch count = %d, want 2", len(batches))
	}
	if got, want := sendBatchClientMsgNos(batches[0]), []string{"m1", "m2"}; !equalStrings(got, want) {
		t.Fatalf("first batch client msg nos = %v, want %v", got, want)
	}
	if got, want := sendBatchClientMsgNos(batches[1]), []string{"m3"}; !equalStrings(got, want) {
		t.Fatalf("second batch client msg nos = %v, want %v", got, want)
	}
}

func TestAsyncSendDispatchMailboxBatchSplitsBeforeMaxBytes(t *testing.T) {
	handler := &recordingAsyncSendBatchHandler{}
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 8,
				AsyncSendBatchMaxBytes:   8,
			},
		},
	}
	executor := &sendExecutor{server: srv}

	executor.dispatchMailboxBatch([]asyncDispatchTask{
		asyncSendBatchTestTask("m1", 4),
		asyncSendBatchTestTask("m2", 6),
		asyncSendBatchTestTask("m3", 2),
	})

	batches := handler.snapshotBatches()
	if len(batches) != 2 {
		t.Fatalf("batch count = %d, want 2", len(batches))
	}
	if got, want := sendBatchClientMsgNos(batches[0]), []string{"m1"}; !equalStrings(got, want) {
		t.Fatalf("first batch client msg nos = %v, want %v", got, want)
	}
	if got, want := sendBatchClientMsgNos(batches[1]), []string{"m2", "m3"}; !equalStrings(got, want) {
		t.Fatalf("second batch client msg nos = %v, want %v", got, want)
	}
}

func TestSendExecutorDispatchesBatchOnWorkqueue(t *testing.T) {
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
}

func TestSendExecutorStopDispatchesBufferedTasks(t *testing.T) {
	handler := &recordingAsyncSendBatchHandler{}
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 8,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  2,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}

	state := &sessionState{server: srv, closedCh: make(chan struct{}), requestContext: context.Background()}
	if !executor.submit(state, "r1", &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "m1", Payload: []byte("one")}) {
		t.Fatal("first submit rejected")
	}
	if !executor.submit(state, "r2", &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "m2", Payload: []byte("two")}) {
		t.Fatal("second submit rejected")
	}

	executor.stop()
	executor.stop()

	batches := handler.snapshotBatches()
	if len(batches) != 1 {
		t.Fatalf("batch calls = %d, want 1", len(batches))
	}
	if got, want := []string{batches[0][0].Frame.ClientMsgNo, batches[0][1].Frame.ClientMsgNo}, []string{"m1", "m2"}; !equalStrings(got, want) {
		t.Fatalf("batch client msg nos = %v, want %v", got, want)
	}
	if got := executor.depth(); got != 0 {
		t.Fatalf("send executor depth = %d, want 0 after stop dispatches buffered tasks", got)
	}
}

func TestSendExecutorStopHonorsReleaseTimeoutWhenHandlerBlocks(t *testing.T) {
	handler := newBlockingAsyncSendFrameHandler()
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        1,
				AsyncSendQueueCapacity:  1,
				AsyncPoolReleaseTimeout: 20 * time.Millisecond,
			},
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 1,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, srv.options.Runtime)
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	state := &sessionState{server: srv, closedCh: make(chan struct{}), requestContext: context.Background()}
	if !executor.submit(state, "", &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "blocked"}) {
		t.Fatal("submit rejected")
	}
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("blocked handler did not start")
	}

	done := make(chan struct{})
	start := time.Now()
	go func() {
		executor.stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		close(handler.release)
		t.Fatal("send executor stop did not honor release timeout")
	}
	close(handler.release)
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("send executor stop took %s, want under 1s", elapsed)
	}
}

func TestSendExecutorPanicRearmsShardBacklog(t *testing.T) {
	handler := newPanicThenRecordingFrameHandler()
	srv := &Server{
		dispatcher: newDispatcher(handler),
		options: gatewaytypes.Options{
			Runtime: gatewaytypes.RuntimeOptions{
				AsyncSendWorkers:        1,
				AsyncSendQueueCapacity:  2,
				AsyncPoolReleaseTimeout: time.Second,
			},
			DefaultSession: gatewaytypes.SessionOptions{
				AsyncSendBatchMaxRecords: 1,
				AsyncSendBatchMaxBytes:   1024,
			},
		},
	}
	executor, err := newSendExecutor(srv, srv.options.Runtime)
	if err != nil {
		t.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()

	state := &sessionState{server: srv, closedCh: make(chan struct{}), requestContext: context.Background()}
	if !executor.submit(state, "", &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "panic-first"}) {
		t.Fatal("first submit rejected")
	}
	select {
	case <-handler.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first SEND task did not start")
	}
	if !executor.submit(state, "", &frame.SendPacket{ChannelID: "c1", ClientMsgNo: "after-panic"}) {
		t.Fatal("second submit rejected")
	}
	close(handler.releaseFirst)

	select {
	case panicValue := <-executor.panicC:
		if panicValue == nil {
			t.Fatal("panicC delivered nil panic value")
		}
	case <-time.After(time.Second):
		t.Fatal("send executor did not record handler panic")
	}
	eventually(t, time.Second, func() bool {
		return equalStrings(handler.clientMsgNos(), []string{"after-panic"})
	}, "send executor did not rearm shard backlog after panic")
	if got := executor.depth(); got != 0 {
		t.Fatalf("send executor depth = %d, want 0 after panic backlog drains", got)
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

func sameBackingArray(a, b []byte) bool {
	return len(a) > 0 && len(b) > 0 && &a[0] == &b[0]
}

func sendBatchClientMsgNos(batch []gatewaytypes.SendBatchItem) []string {
	out := make([]string, 0, len(batch))
	for _, item := range batch {
		if item.Frame == nil {
			out = append(out, "")
			continue
		}
		out = append(out, item.Frame.ClientMsgNo)
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

func asyncSendTestState(srv *Server, id uint64) *sessionState {
	return &sessionState{
		server: srv,
		key:    connKey{connID: id},
		listener: &listenerRuntime{
			options: gatewaytypes.ListenerOptions{Name: "send-test", Network: "tcp"},
			adapter: asyncAuthEncodeOnlyProtocol{},
		},
		session:        session.New(session.Config{ID: id}),
		closedCh:       make(chan struct{}),
		requestContext: context.Background(),
	}
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

type panicThenRecordingFrameHandler struct {
	firstStarted chan struct{}
	releaseFirst chan struct{}

	panicOnce sync.Once
	mu        sync.Mutex
	seen      []string
}

func newPanicThenRecordingFrameHandler() *panicThenRecordingFrameHandler {
	return &panicThenRecordingFrameHandler{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
}

type blockingAsyncSendFrameHandler struct {
	counting  countingAsyncFrameHandler
	startOnce sync.Once
	started   chan struct{}
	release   chan struct{}
}

func newBlockingAsyncSendFrameHandler() *blockingAsyncSendFrameHandler {
	return &blockingAsyncSendFrameHandler{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
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

func (h *panicThenRecordingFrameHandler) OnListenerError(string, error) {}
func (h *panicThenRecordingFrameHandler) OnSessionOpen(gatewaytypes.Context) error {
	return nil
}
func (h *panicThenRecordingFrameHandler) OnFrame(_ gatewaytypes.Context, f frame.Frame) error {
	didPanic := false
	h.panicOnce.Do(func() {
		didPanic = true
		close(h.firstStarted)
		<-h.releaseFirst
		panic("send frame panic")
	})
	if didPanic {
		return nil
	}
	send, _ := f.(*frame.SendPacket)
	h.mu.Lock()
	defer h.mu.Unlock()
	if send == nil {
		h.seen = append(h.seen, "")
		return nil
	}
	h.seen = append(h.seen, send.ClientMsgNo)
	return nil
}
func (h *panicThenRecordingFrameHandler) OnSessionClose(gatewaytypes.Context) error {
	return nil
}
func (h *panicThenRecordingFrameHandler) OnSessionError(gatewaytypes.Context, error) {}

func (h *panicThenRecordingFrameHandler) clientMsgNos() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}

func (h *blockingAsyncSendFrameHandler) OnListenerError(string, error) {}
func (h *blockingAsyncSendFrameHandler) OnSessionOpen(gatewaytypes.Context) error {
	return nil
}
func (h *blockingAsyncSendFrameHandler) OnFrame(ctx gatewaytypes.Context, f frame.Frame) error {
	if err := h.counting.OnFrame(ctx, f); err != nil {
		return err
	}
	h.startOnce.Do(func() {
		close(h.started)
	})
	<-h.release
	return nil
}
func (h *blockingAsyncSendFrameHandler) OnSessionClose(ctx gatewaytypes.Context) error {
	return h.counting.OnSessionClose(ctx)
}
func (h *blockingAsyncSendFrameHandler) OnSessionError(ctx gatewaytypes.Context, err error) {
	h.counting.OnSessionError(ctx, err)
}

func (h *blockingAsyncSendFrameHandler) frames() uint64 {
	return h.counting.frames.Load()
}

type blockingRecordingAsyncSendBatchHandler struct {
	recordingAsyncSendBatchHandler
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func newBlockingRecordingAsyncSendBatchHandler() *blockingRecordingAsyncSendBatchHandler {
	return &blockingRecordingAsyncSendBatchHandler{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (h *blockingRecordingAsyncSendBatchHandler) OnSendBatch(items []gatewaytypes.SendBatchItem) error {
	h.once.Do(func() {
		close(h.started)
	})
	<-h.release
	return h.recordingAsyncSendBatchHandler.OnSendBatch(items)
}

func (h *blockingRecordingAsyncSendBatchHandler) releaseAll() {
	select {
	case <-h.release:
	default:
		close(h.release)
	}
}

func (h *blockingRecordingAsyncSendBatchHandler) onlySend(t *testing.T) *frame.SendPacket {
	t.Helper()
	eventually(t, time.Second, func() bool {
		batches := h.snapshotBatches()
		return len(batches) == 1 && len(batches[0]) == 1
	}, "blocking batch handler did not record one send")
	batches := h.snapshotBatches()
	return batches[0][0].Frame
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
	executor, err := newSendExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncSendWorkers:        1,
		AsyncSendQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		b.Fatalf("new send executor: %v", err)
	}
	defer executor.stop()
	srv.async.Store(&asyncRuntime{send: executor})
	if !executor.submit(&sessionState{}, "", &frame.SendPacket{}) {
		b.Fatal("prefill send executor failed")
	}
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
