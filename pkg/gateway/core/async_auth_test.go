package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type asyncAuthNoopHandler struct{}

func (asyncAuthNoopHandler) OnListenerError(string, error)                   {}
func (asyncAuthNoopHandler) OnSessionOpen(gatewaytypes.Context) error        { return nil }
func (asyncAuthNoopHandler) OnFrame(gatewaytypes.Context, frame.Frame) error { return nil }
func (asyncAuthNoopHandler) OnSessionClose(gatewaytypes.Context) error       { return nil }
func (asyncAuthNoopHandler) OnSessionError(gatewaytypes.Context, error)      {}

func TestAuthExecutorSubmitRejectsWhenFull(t *testing.T) {
	executor, err := newAuthExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncAuthWorkers:        1,
		AsyncAuthQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new auth executor: %v", err)
	}
	defer executor.stop()

	state := &sessionState{}
	if !executor.submit(asyncAuthTask{
		state:   state,
		connect: &frame.ConnectPacket{UID: "u1"},
	}) {
		t.Fatal("first auth submit rejected")
	}
	if executor.submit(asyncAuthTask{
		state:   state,
		connect: &frame.ConnectPacket{UID: "u2"},
	}) {
		t.Fatal("second auth submit accepted when executor queue is full")
	}
	if got := executor.depth(); got != 1 {
		t.Fatalf("executor depth = %d, want 1", got)
	}
	if got := executor.totalCapacity(); got != 1 {
		t.Fatalf("executor capacity = %d, want 1", got)
	}
}

func TestAuthExecutorRunsQueuedTaskOnAnts(t *testing.T) {
	authCalled := make(chan struct{}, 1)
	handler := asyncAuthNoopHandler{}
	srv := &Server{
		options: gatewaytypes.Options{
			Authenticator: gatewaytypes.AuthenticatorFunc(func(*gatewaytypes.Context, *frame.ConnectPacket) (*gatewaytypes.AuthResult, error) {
				authCalled <- struct{}{}
				return &gatewaytypes.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			}),
			Handler: handler,
		},
		dispatcher: newDispatcher(handler),
	}
	executor, err := newAuthExecutor(srv, gatewaytypes.RuntimeOptions{
		AsyncAuthWorkers:        1,
		AsyncAuthQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new auth executor: %v", err)
	}
	defer executor.stop()

	state := &sessionState{
		server:   srv,
		listener: &listenerRuntime{adapter: asyncAuthEncodeOnlyProtocol{}},
		session:  session.New(session.Config{ID: 1}),
		closedCh: make(chan struct{}),
	}
	conn := &asyncAuthRecordingConn{}
	state.conn = conn
	state.requestContext, state.cancelRequestContext = context.WithCancel(context.Background())
	state.setAuthPending(true)

	if !executor.submit(asyncAuthTask{
		state:   state,
		connect: &frame.ConnectPacket{UID: "u1", DeviceID: "d-1", DeviceFlag: frame.APP},
	}) {
		t.Fatal("auth submit rejected")
	}

	select {
	case <-authCalled:
	case panicValue := <-executor.panicC:
		t.Fatalf("auth executor worker panicked: %v", panicValue)
	case <-time.After(time.Second):
		t.Fatal("authenticator was not called")
	}

	eventually(t, time.Second, func() bool {
		return executor.depth() == 0
	}, "auth executor depth returned to zero")
	eventually(t, time.Second, func() bool {
		return conn.writes.Load() > 0 && state.openWasDispatched()
	}, "auth task completed success path")
	select {
	case panicValue := <-executor.panicC:
		t.Fatalf("auth executor worker panicked: %v", panicValue)
	default:
	}
	if state.session.Value(gatewaytypes.SessionValueDeviceID) != "d-1" {
		t.Fatalf("session device id = %v, want d-1", state.session.Value(gatewaytypes.SessionValueDeviceID))
	}
}

func TestAuthExecutorStopIsIdempotentAndRejectsSubmit(t *testing.T) {
	executor, err := newAuthExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncAuthWorkers:        1,
		AsyncAuthQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new auth executor: %v", err)
	}

	executor.stop()
	executor.stop()

	if executor.submit(asyncAuthTask{
		state:   &sessionState{},
		connect: &frame.ConnectPacket{UID: "u1"},
	}) {
		t.Fatal("auth submit accepted after stop")
	}
}

func TestAuthExecutorStopDrainsBufferedDepth(t *testing.T) {
	executor, err := newAuthExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncAuthWorkers:        1,
		AsyncAuthQueueCapacity:  2,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new auth executor: %v", err)
	}

	state := &sessionState{}
	if !executor.submit(asyncAuthTask{state: state, connect: &frame.ConnectPacket{UID: "u1"}}) {
		t.Fatal("first auth submit rejected")
	}
	if !executor.submit(asyncAuthTask{state: state, connect: &frame.ConnectPacket{UID: "u2"}}) {
		t.Fatal("second auth submit rejected")
	}
	if got := executor.depth(); got != 2 {
		t.Fatalf("executor depth before stop = %d, want 2", got)
	}

	executor.stop()
	executor.stop()

	if got := executor.depth(); got != 0 {
		t.Fatalf("executor depth after stop = %d, want 0", got)
	}
}

func TestAuthExecutorPanicClearsAuthPending(t *testing.T) {
	logger := newAsyncAuthRecordingLogger()
	handler := asyncAuthNoopHandler{}
	srv := &Server{
		options: gatewaytypes.Options{
			Authenticator: gatewaytypes.AuthenticatorFunc(func(*gatewaytypes.Context, *frame.ConnectPacket) (*gatewaytypes.AuthResult, error) {
				return &gatewaytypes.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			}),
			Handler:  handler,
			Observer: asyncAuthPanicObserver{},
			Logger:   logger,
		},
		dispatcher: newDispatcher(handler),
	}
	executor, err := newAuthExecutor(srv, gatewaytypes.RuntimeOptions{
		AsyncAuthWorkers:        1,
		AsyncAuthQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new auth executor: %v", err)
	}
	defer executor.stop()

	state := &sessionState{
		server:   srv,
		closedCh: make(chan struct{}),
	}
	state.setAuthPending(true)

	if !executor.submit(asyncAuthTask{state: state, connect: &frame.ConnectPacket{UID: "u1"}}) {
		t.Fatal("auth submit rejected")
	}

	select {
	case <-executor.panicC:
	case <-time.After(time.Second):
		t.Fatal("auth executor panic was not observed")
	}
	if state.isAuthPending() {
		t.Fatal("auth pending remained true after executor panic")
	}
	if got := executor.depth(); got != 0 {
		t.Fatalf("executor depth after panic = %d, want 0", got)
	}
	select {
	case msg := <-logger.warnC:
		if msg != "gateway async auth task panic" {
			t.Fatalf("warning message = %q, want gateway async auth task panic", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("panic warning was not logged")
	}
}

func TestAuthExecutorPanicLoggingIsBestEffort(t *testing.T) {
	handler := asyncAuthNoopHandler{}
	srv := &Server{
		options: gatewaytypes.Options{
			Authenticator: gatewaytypes.AuthenticatorFunc(func(*gatewaytypes.Context, *frame.ConnectPacket) (*gatewaytypes.AuthResult, error) {
				return &gatewaytypes.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			}),
			Handler:  handler,
			Observer: asyncAuthPanicObserver{},
			Logger:   asyncAuthPanicLogger{},
		},
		dispatcher: newDispatcher(handler),
	}
	executor, err := newAuthExecutor(srv, gatewaytypes.RuntimeOptions{
		AsyncAuthWorkers:        1,
		AsyncAuthQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new auth executor: %v", err)
	}
	defer executor.stop()

	state := &sessionState{
		server:   srv,
		closedCh: make(chan struct{}),
	}
	state.setAuthPending(true)

	if !executor.submit(asyncAuthTask{state: state, connect: &frame.ConnectPacket{UID: "u1"}}) {
		t.Fatal("auth submit rejected")
	}

	select {
	case got := <-executor.panicC:
		if got != "async auth queue observer panic" {
			t.Fatalf("panic value = %v, want async auth queue observer panic", got)
		}
	case <-time.After(time.Second):
		t.Fatal("auth executor panic was not observed")
	}
	if state.isAuthPending() {
		t.Fatal("auth pending remained true after executor panic")
	}
	if got := executor.depth(); got != 0 {
		t.Fatalf("executor depth after panic = %d, want 0", got)
	}
}

func TestAsyncAuthQueueSubmitRejectsWhenFull(t *testing.T) {
	queue := newAsyncAuthQueueWithCapacity(1)
	state := &sessionState{}

	if !queue.submit(asyncAuthTask{
		state:      state,
		replyToken: "r1",
		connect:    &frame.ConnectPacket{UID: "u1"},
	}) {
		t.Fatal("first auth submit rejected")
	}
	if queue.submit(asyncAuthTask{
		state:      state,
		replyToken: "r2",
		connect:    &frame.ConnectPacket{UID: "u2"},
	}) {
		t.Fatal("second auth submit accepted when queue is full")
	}
	queue.close()
}

func TestAsyncAuthQueueConsumeBalancesQueuedDepth(t *testing.T) {
	queue := newAsyncAuthQueueWithCapacity(1)
	if !queue.submit(asyncAuthTask{
		state:   &sessionState{},
		connect: &frame.ConnectPacket{UID: "u1"},
	}) {
		t.Fatal("auth submit rejected")
	}
	task := <-queue.tasks
	if task.connect == nil {
		t.Fatal("queued auth task has nil connect packet")
	}
	queue.consume(1)
	if got := queue.queued.Load(); got != 0 {
		t.Fatalf("queued depth = %d, want 0", got)
	}
	queue.close()
}

func TestAsyncAuthWorkerCountBounds(t *testing.T) {
	if got, want := adaptiveAsyncAuthWorkerCount(0), minAsyncAuthWorkers; got != want {
		t.Fatalf("worker count with zero GOMAXPROCS = %d, want %d", got, want)
	}
	if got, want := adaptiveAsyncAuthWorkerCount(1), minAsyncAuthWorkers; got != want {
		t.Fatalf("worker count with one GOMAXPROCS = %d, want %d", got, want)
	}
	if got, want := adaptiveAsyncAuthWorkerCount(128), maxAsyncAuthWorkers; got != want {
		t.Fatalf("worker count with large GOMAXPROCS = %d, want %d", got, want)
	}
}

func TestCloseReasonForAsyncAuthQueueFull(t *testing.T) {
	if got := closeReasonForError(gatewaytypes.ErrAsyncAuthQueueFull, gatewaytypes.CloseReasonPolicyViolation); got != gatewaytypes.CloseReasonAsyncAuthQueueFull {
		t.Fatalf("close reason = %q, want %q", got, gatewaytypes.CloseReasonAsyncAuthQueueFull)
	}
}

func TestServerAsyncAuthRejectsWhenQueueFull(t *testing.T) {
	var authCalls atomic.Uint64
	handler := asyncAuthNoopHandler{}
	srv := &Server{
		options: gatewaytypes.Options{
			Authenticator: gatewaytypes.AuthenticatorFunc(func(*gatewaytypes.Context, *frame.ConnectPacket) (*gatewaytypes.AuthResult, error) {
				authCalls.Add(1)
				return &gatewaytypes.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			}),
			Handler: handler,
		},
		dispatcher: newDispatcher(handler),
	}
	executor, err := newAuthExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncAuthWorkers:        1,
		AsyncAuthQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new auth executor: %v", err)
	}
	defer executor.stop()
	srv.async.Store(&asyncRuntime{auth: executor})
	if !executor.submit(asyncAuthTask{state: &sessionState{}, connect: &frame.ConnectPacket{UID: "queued"}}) {
		t.Fatal("prefill auth executor failed")
	}

	state := &sessionState{
		server:   srv,
		closedCh: make(chan struct{}),
	}
	state.requestContext, state.cancelRequestContext = context.WithCancel(context.Background())
	state.setAuthRequired(true)

	srv.handleAuthFrame(state, "", &frame.ConnectPacket{UID: "u1"}, false)

	if got := authCalls.Load(); got != 0 {
		t.Fatalf("auth calls = %d, want 0 when queue is full", got)
	}
	if !state.isClosed() {
		t.Fatal("state was not closed after auth queue overflow")
	}
	if got := state.closeReason(); got != gatewaytypes.CloseReasonAsyncAuthQueueFull {
		t.Fatalf("close reason = %q, want %q", got, gatewaytypes.CloseReasonAsyncAuthQueueFull)
	}
}

func TestServerAsyncAuthQueueFullKeepsCloseReasonWhenConnackWriteFails(t *testing.T) {
	handler := asyncAuthNoopHandler{}
	srv := &Server{
		options: gatewaytypes.Options{
			Authenticator: gatewaytypes.AuthenticatorFunc(func(*gatewaytypes.Context, *frame.ConnectPacket) (*gatewaytypes.AuthResult, error) {
				return &gatewaytypes.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			}),
			Handler: handler,
		},
		dispatcher: newDispatcher(handler),
	}
	executor, err := newAuthExecutor(nil, gatewaytypes.RuntimeOptions{
		AsyncAuthWorkers:        1,
		AsyncAuthQueueCapacity:  1,
		AsyncPoolReleaseTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new auth executor: %v", err)
	}
	defer executor.stop()
	srv.async.Store(&asyncRuntime{auth: executor})
	if !executor.submit(asyncAuthTask{state: &sessionState{}, connect: &frame.ConnectPacket{UID: "queued"}}) {
		t.Fatal("prefill auth executor failed")
	}

	state := &sessionState{
		server:   srv,
		listener: &listenerRuntime{adapter: asyncAuthEncodeOnlyProtocol{}},
		conn:     asyncAuthWriteErrConn{err: transport.ErrOutboundBytesExceeded},
		closedCh: make(chan struct{}),
	}
	state.requestContext, state.cancelRequestContext = context.WithCancel(context.Background())
	state.setAuthRequired(true)

	srv.handleAuthFrame(state, "", &frame.ConnectPacket{UID: "u1"}, false)

	if got := state.closeReason(); got != gatewaytypes.CloseReasonAsyncAuthQueueFull {
		t.Fatalf("close reason = %q, want %q", got, gatewaytypes.CloseReasonAsyncAuthQueueFull)
	}
}

func TestServerAsyncAuthRollsBackWhenConnackWriteSucceedsButSessionClosesBeforeOpen(t *testing.T) {
	handler := &asyncAuthActivatingHandler{}
	srv := &Server{
		options: gatewaytypes.Options{
			Authenticator: gatewaytypes.AuthenticatorFunc(func(*gatewaytypes.Context, *frame.ConnectPacket) (*gatewaytypes.AuthResult, error) {
				return &gatewaytypes.AuthResult{Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}}, nil
			}),
			Handler: handler,
		},
		dispatcher: newDispatcher(handler),
	}
	state := &sessionState{
		server:   srv,
		listener: &listenerRuntime{adapter: asyncAuthEncodeOnlyProtocol{}},
		closedCh: make(chan struct{}),
	}
	state.requestContext, state.cancelRequestContext = context.WithCancel(context.Background())
	state.session = session.New(session.Config{ID: 1})
	state.conn = asyncAuthCloseOnWriteConn{onWrite: func() {
		state.close(gatewaytypes.CloseReasonPeerClosed, nil)
	}}
	state.setAuthPending(true)

	srv.runAuthTask(asyncAuthTask{
		state:      state,
		connect:    &frame.ConnectPacket{UID: "u1", DeviceID: "d-1", DeviceFlag: frame.APP},
		enqueuedAt: time.Now(),
	})

	if !handler.rollbackCalled.Load() {
		t.Fatal("activation rollback was not called after close before session open")
	}
	if handler.openCalled.Load() {
		t.Fatal("session open called after close before open dispatch")
	}
}

func TestAsyncAuthQueueCloseStopsWorker(t *testing.T) {
	srv := &Server{}
	queue := newAsyncAuthQueueWithCapacity(1)
	done := make(chan struct{})

	srv.workerWG.Add(1)
	go func() {
		srv.runAsyncAuthWorker(queue)
		close(done)
	}()

	queue.close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("auth worker did not stop after queue close")
	}
	srv.workerWG.Wait()
}

func TestAsyncAuthReportsQueueAndAdmission(t *testing.T) {
	observer := &recordingAsyncSendObserver{}
	srv := &Server{options: gatewaytypes.Options{Observer: observer}}
	queue := newAsyncAuthQueueWithCapacity(1)
	state := &sessionState{}

	if !queue.submit(asyncAuthTask{state: state, connect: &frame.ConnectPacket{UID: "u1"}}) {
		t.Fatal("first async auth submit failed")
	}
	srv.observeAsyncAuthQueue(queue)
	srv.observeAsyncAuthAdmission(queue, true)
	if queue.submit(asyncAuthTask{state: state, connect: &frame.ConnectPacket{UID: "u2"}}) {
		t.Fatal("second async auth submit unexpectedly succeeded")
	}
	srv.observeAsyncAuthAdmission(queue, false)

	if len(observer.authQueues) != 1 {
		t.Fatalf("auth queue events = %d, want 1", len(observer.authQueues))
	}
	if got := observer.authQueues[0].Depth; got != 1 {
		t.Fatalf("auth queue depth = %d, want 1", got)
	}
	if got := observer.authQueues[0].Capacity; got != 1 {
		t.Fatalf("auth queue capacity = %d, want 1", got)
	}
	if got := observer.authQueues[0].Workers; got != 1 {
		t.Fatalf("auth queue workers = %d, want 1", got)
	}
	if len(observer.authAdmissions) != 2 {
		t.Fatalf("auth admissions = %d, want 2", len(observer.authAdmissions))
	}
	if got := observer.authAdmissions[0].Result; got != "ok" {
		t.Fatalf("first auth admission result = %q, want ok", got)
	}
	if got := observer.authAdmissions[1].Result; got != "full" {
		t.Fatalf("second auth admission result = %q, want full", got)
	}
}

func TestAsyncAuthReportsWaitWithNonNegativeDuration(t *testing.T) {
	observer := &recordingAsyncSendObserver{}
	srv := &Server{options: gatewaytypes.Options{Observer: observer}}
	state := &sessionState{listener: &listenerRuntime{options: gatewaytypes.ListenerOptions{Name: "tcp", Network: "tcp"}}}

	srv.observeAsyncAuthWait(asyncAuthTask{
		state:      state,
		enqueuedAt: time.Now().Add(24 * time.Hour),
	})

	events := observer.authWaitEvents()
	if len(events) != 1 {
		t.Fatalf("auth wait events = %d, want 1", len(events))
	}
	if got := events[0].Duration; got != 0 {
		t.Fatalf("auth wait duration = %v, want 0", got)
	}
	if got := events[0].Listener; got != "tcp" {
		t.Fatalf("auth wait listener = %q, want tcp", got)
	}
}

type asyncAuthEncodeOnlyProtocol struct{}

func (asyncAuthEncodeOnlyProtocol) Name() string { return "wkproto" }

func (asyncAuthEncodeOnlyProtocol) Decode(session.Session, []byte) ([]frame.Frame, int, error) {
	return nil, 0, nil
}

func (asyncAuthEncodeOnlyProtocol) Encode(session.Session, frame.Frame, session.OutboundMeta) ([]byte, error) {
	return []byte("connack-system"), nil
}

func (asyncAuthEncodeOnlyProtocol) OnOpen(session.Session) error  { return nil }
func (asyncAuthEncodeOnlyProtocol) OnClose(session.Session) error { return nil }

type asyncAuthWriteErrConn struct {
	err error
}

func (c asyncAuthWriteErrConn) ID() uint64 { return 1 }

func (c asyncAuthWriteErrConn) Write([]byte) error {
	return c.err
}

func (c asyncAuthWriteErrConn) Close() error { return nil }

func (c asyncAuthWriteErrConn) LocalAddr() string  { return "local" }
func (c asyncAuthWriteErrConn) RemoteAddr() string { return "remote" }

type asyncAuthCloseOnWriteConn struct {
	onWrite func()
}

func (c asyncAuthCloseOnWriteConn) ID() uint64 { return 1 }

func (c asyncAuthCloseOnWriteConn) Write([]byte) error {
	if c.onWrite != nil {
		c.onWrite()
	}
	return nil
}

func (c asyncAuthCloseOnWriteConn) Close() error       { return nil }
func (c asyncAuthCloseOnWriteConn) LocalAddr() string  { return "local" }
func (c asyncAuthCloseOnWriteConn) RemoteAddr() string { return "remote" }

type asyncAuthRecordingConn struct {
	writes atomic.Uint64
}

func (c *asyncAuthRecordingConn) ID() uint64 { return 1 }

func (c *asyncAuthRecordingConn) Write([]byte) error {
	c.writes.Add(1)
	return nil
}

func (c *asyncAuthRecordingConn) Close() error       { return nil }
func (c *asyncAuthRecordingConn) LocalAddr() string  { return "local" }
func (c *asyncAuthRecordingConn) RemoteAddr() string { return "remote" }

func eventually(t *testing.T, timeout time.Duration, condition func() bool, msg string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !condition() {
		t.Fatal(msg)
	}
}

type asyncAuthPanicObserver struct{}

func (asyncAuthPanicObserver) OnConnectionOpen(gatewaytypes.ConnectionEvent)  {}
func (asyncAuthPanicObserver) OnConnectionClose(gatewaytypes.ConnectionEvent) {}
func (asyncAuthPanicObserver) OnAuth(gatewaytypes.AuthEvent)                  {}
func (asyncAuthPanicObserver) OnFrameIn(gatewaytypes.FrameEvent)              {}
func (asyncAuthPanicObserver) OnFrameOut(gatewaytypes.FrameEvent)             {}
func (asyncAuthPanicObserver) OnFrameHandled(gatewaytypes.FrameHandleEvent)   {}
func (asyncAuthPanicObserver) OnAsyncAuthQueue(gatewaytypes.AsyncAuthQueueEvent) {
	panic("async auth queue observer panic")
}
func (asyncAuthPanicObserver) OnAsyncAuthAdmission(gatewaytypes.AsyncAuthAdmissionEvent) {}
func (asyncAuthPanicObserver) OnAsyncAuthWait(gatewaytypes.AsyncAuthWaitEvent)           {}

type asyncAuthRecordingLogger struct {
	warnC chan string
}

func newAsyncAuthRecordingLogger() *asyncAuthRecordingLogger {
	return &asyncAuthRecordingLogger{warnC: make(chan string, 4)}
}

func (l *asyncAuthRecordingLogger) Debug(string, ...wklog.Field) {}
func (l *asyncAuthRecordingLogger) Info(string, ...wklog.Field)  {}
func (l *asyncAuthRecordingLogger) Warn(msg string, _ ...wklog.Field) {
	select {
	case l.warnC <- msg:
	default:
	}
}
func (l *asyncAuthRecordingLogger) Error(string, ...wklog.Field) {}
func (l *asyncAuthRecordingLogger) Fatal(string, ...wklog.Field) {}
func (l *asyncAuthRecordingLogger) Named(string) wklog.Logger    { return l }
func (l *asyncAuthRecordingLogger) With(...wklog.Field) wklog.Logger {
	return l
}
func (l *asyncAuthRecordingLogger) Sync() error { return nil }

type asyncAuthPanicLogger struct{}

func (asyncAuthPanicLogger) Debug(string, ...wklog.Field) {}
func (asyncAuthPanicLogger) Info(string, ...wklog.Field)  {}
func (asyncAuthPanicLogger) Warn(string, ...wklog.Field)  { panic("logger warn panic") }
func (asyncAuthPanicLogger) Error(string, ...wklog.Field) {}
func (asyncAuthPanicLogger) Fatal(string, ...wklog.Field) {}
func (asyncAuthPanicLogger) Named(string) wklog.Logger    { return asyncAuthPanicLogger{} }
func (asyncAuthPanicLogger) With(...wklog.Field) wklog.Logger {
	return asyncAuthPanicLogger{}
}
func (asyncAuthPanicLogger) Sync() error { return nil }

type asyncAuthActivatingHandler struct {
	rollbackCalled atomic.Bool
	openCalled     atomic.Bool
}

func (h *asyncAuthActivatingHandler) OnListenerError(string, error) {}

func (h *asyncAuthActivatingHandler) OnSessionOpen(gatewaytypes.Context) error {
	h.openCalled.Store(true)
	return nil
}

func (h *asyncAuthActivatingHandler) OnFrame(gatewaytypes.Context, frame.Frame) error { return nil }
func (h *asyncAuthActivatingHandler) OnSessionClose(gatewaytypes.Context) error       { return nil }
func (h *asyncAuthActivatingHandler) OnSessionError(gatewaytypes.Context, error)      {}

func (h *asyncAuthActivatingHandler) OnSessionActivate(*gatewaytypes.Context) (*frame.ConnackPacket, error) {
	return nil, nil
}

func (h *asyncAuthActivatingHandler) OnSessionActivateRollback(gatewaytypes.Context, error) {
	h.rollbackCalled.Store(true)
}
