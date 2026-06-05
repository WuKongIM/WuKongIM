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
)

type asyncAuthNoopHandler struct{}

func (asyncAuthNoopHandler) OnListenerError(string, error)                   {}
func (asyncAuthNoopHandler) OnSessionOpen(gatewaytypes.Context) error        { return nil }
func (asyncAuthNoopHandler) OnFrame(gatewaytypes.Context, frame.Frame) error { return nil }
func (asyncAuthNoopHandler) OnSessionClose(gatewaytypes.Context) error       { return nil }
func (asyncAuthNoopHandler) OnSessionError(gatewaytypes.Context, error)      {}

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
	queue := newAsyncAuthQueueWithCapacity(1)
	srv.asyncAuth.Store(queue)
	queue.tasks <- asyncAuthTask{state: &sessionState{}, connect: &frame.ConnectPacket{UID: "queued"}}
	queue.queued.Add(1)

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
	queue.close()
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
	queue := newAsyncAuthQueueWithCapacity(1)
	srv.asyncAuth.Store(queue)
	queue.tasks <- asyncAuthTask{state: &sessionState{}, connect: &frame.ConnectPacket{UID: "queued"}}
	queue.queued.Add(1)

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
	queue.close()
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
	srv.asyncAuth.Store(queue)
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
