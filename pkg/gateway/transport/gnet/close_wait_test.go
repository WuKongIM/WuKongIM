package gnet

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gnetv2 "github.com/panjf2000/gnet/v2"
)

type closeReceiptConn struct {
	allocTestGnetConn
	submit func(gnetv2.AsyncCallback) error
	calls  atomic.Int32
}

func (c *closeReceiptConn) CloseWithCallback(cb gnetv2.AsyncCallback) error {
	c.calls.Add(1)
	return c.submit(cb)
}

func TestCloseWaitJoinsOnePhysicalReceiptAfterCanceledWait(t *testing.T) {
	callbacks := make(chan gnetv2.AsyncCallback, 1)
	raw := &closeReceiptConn{submit: func(cb gnetv2.AsyncCallback) error { callbacks <- cb; return nil }}
	state := newConnState(1, raw, nil)
	c := state.transport
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.CloseAndWait(ctx) }()
	callback := <-callbacks
	// Neither a failure event nor an ordinary close request is physical proof.
	state.fail(errors.New("local dispatch failure"))
	select {
	case err := <-done:
		t.Fatalf("premature close receipt: %v", err)
	default:
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		go func() { done <- c.CloseAndWait(context.Background()) }()
	}
	if err := c.Write([]byte("late")); !errors.Is(err, transport.ErrConnectionClosing) {
		t.Fatal(err)
	}
	if err := c.WriteObserved(nil, "packet", nil); !errors.Is(err, transport.ErrConnectionClosing) {
		t.Fatal(err)
	}
	if err := c.WriteWebSocketMessage(nil, transport.WebSocketMessageBinary); !errors.Is(err, transport.ErrConnectionClosing) {
		t.Fatal(err)
	}
	if err := callback(raw, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if raw.calls.Load() != 1 {
		t.Fatalf("physical close submissions: %d", raw.calls.Load())
	}
}

func TestCloseWaitRequiresSubmissionAndCallbackSuccess(t *testing.T) {
	for _, which := range []string{"submission", "callback", "callback-before-submission-error"} {
		t.Run(which, func(t *testing.T) {
			failure := errors.New("close failure")
			var cb gnetv2.AsyncCallback
			raw := &closeReceiptConn{}
			raw.submit = func(callback gnetv2.AsyncCallback) error {
				cb = callback
				switch which {
				case "submission":
					return failure
				case "callback":
					_ = callback(raw, failure)
					return nil
				default:
					_ = callback(raw, nil)
					return failure
				}
			}
			c := newConnState(1, raw, nil).transport
			if err := c.CloseAndWait(context.Background()); err == nil {
				t.Fatal("failure became proof")
			}
			_ = cb(raw, nil)
			if err := c.CloseAndWait(context.Background()); err == nil {
				t.Fatal("late nil erased failure")
			}
			if raw.calls.Load() != 1 {
				t.Fatal("retry submitted another close")
			}
		})
	}
}

func TestCloseWaitDoesNotPublishCallbackBeforeSubmissionReturns(t *testing.T) {
	called, release := make(chan struct{}), make(chan struct{})
	raw := &closeReceiptConn{}
	raw.submit = func(cb gnetv2.AsyncCallback) error {
		_ = cb(raw, nil)
		close(called)
		<-release
		return errors.New("ambiguous enqueue failure")
	}
	c := newConnState(1, raw, nil).transport
	first := make(chan error, 1)
	go func() { first <- c.CloseAndWait(context.Background()) }()
	<-called
	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() { second <- c.CloseAndWait(ctx) }()
	cancel()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatalf("premature callback proof: %v", err)
	}
	close(release)
	if err := <-first; err == nil {
		t.Fatal("submission error became success")
	}
}

// Core fences/cancels the request context before asking transport to close. A
// cancellation at that boundary must stop waiting without suppressing the close.
func TestCloseWaitCanceledContextStillStartsOnePhysicalClose(t *testing.T) {
	callbacks := make(chan gnetv2.AsyncCallback, 1)
	raw := &closeReceiptConn{submit: func(cb gnetv2.AsyncCallback) error { callbacks <- cb; return nil }}
	c := newConnState(1, raw, nil).transport
	if err := c.CloseAndWait(nil); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.CloseAndWait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if raw.calls.Load() != 1 {
		t.Fatal("cancellation erased the physical close request")
	}
	if err := c.Write([]byte("late")); !errors.Is(err, transport.ErrConnectionClosing) {
		t.Fatal(err)
	}
	cb := <-callbacks
	_ = cb(raw, nil)
	if err := c.CloseAndWait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if raw.calls.Load() != 1 {
		t.Fatal("retry queued a second close")
	}
}
