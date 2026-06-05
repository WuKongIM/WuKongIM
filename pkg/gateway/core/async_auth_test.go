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
