package client

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestSealIngressLegacySeamFailsClosedAtEveryLifecycleBoundary(t *testing.T) {
	var nilClient *Client
	if err := nilClient.SealIngress(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil SealIngress() = %v, want %v", err, ErrClosed)
	}

	c := newDisconnectedClientOrFatal(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.SealIngress(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("SealIngress(canceled) = %v, want %v", err, context.Canceled)
	}
	if err := c.SealIngress(nil); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("SealIngress(disconnected) = %v, want %v", err, ErrNotConnected)
	}

	c.mu.Lock()
	c.conn = discardConn{}
	c.mu.Unlock()
	if err := c.SealIngress(context.Background()); !errors.Is(err, ErrIngressSealUnsupported) {
		t.Fatalf("SealIngress(connected) = %v, want %v", err, ErrIngressSealUnsupported)
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	if err := c.SealIngress(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("SealIngress(closed) = %v, want %v", err, ErrClosed)
	}
}

func TestSealIngressWithFenceRejectsInvalidCapabilityBeforeStateMutation(t *testing.T) {
	c := newDisconnectedClientOrFatal(t)
	if err := c.SealIngressWithFence(context.Background(), frame.TerminalFenceGrant{}); !errors.Is(err, ErrTerminalFenceProtocol) {
		t.Fatalf("SealIngressWithFence(invalid grant) = %v, want %v", err, ErrTerminalFenceProtocol)
	}
	if c.terminalFence != terminalFenceOpen {
		t.Fatalf("terminal fence state = %v, want open after invalid grant", c.terminalFence)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	grant := frame.TerminalFenceGrant{Epoch: 1, Capability: "capability"}
	if err := c.SealIngressWithFence(ctx, grant); !errors.Is(err, context.Canceled) {
		t.Fatalf("SealIngressWithFence(canceled) = %v, want %v", err, context.Canceled)
	}

	var nilClient *Client
	if err := nilClient.SealIngressWithFence(context.Background(), grant); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil SealIngressWithFence() = %v, want %v", err, ErrClosed)
	}
}

func TestBeginTerminalFenceEnforcesOneTerminalGeneration(t *testing.T) {
	grant := frame.TerminalFenceGrant{Epoch: 21, Capability: "capability"}
	tests := []struct {
		name       string
		configure  func(*Client)
		want       error
		idempotent bool
	}{
		{
			name: "acknowledged same epoch",
			configure: func(c *Client) {
				c.terminalFence = terminalFenceAcknowledged
				c.terminalFenceEpoch = grant.Epoch
			},
			idempotent: true,
		},
		{
			name: "acknowledged other epoch",
			configure: func(c *Client) {
				c.terminalFence = terminalFenceAcknowledged
				c.terminalFenceEpoch = grant.Epoch + 1
			},
			want: ErrTerminalFenceActive,
		},
		{
			name: "failed with retained cause",
			configure: func(c *Client) {
				c.terminalFence = terminalFenceFailed
				c.terminalFenceErr = io.ErrUnexpectedEOF
			},
			want: io.ErrUnexpectedEOF,
		},
		{
			name: "failed without retained cause",
			configure: func(c *Client) {
				c.terminalFence = terminalFenceFailed
			},
			want: ErrTerminalFenceProtocol,
		},
		{
			name: "already awaiting ack",
			configure: func(c *Client) {
				c.terminalFence = terminalFenceAwaitingAck
			},
			want: ErrTerminalFenceActive,
		},
		{
			name: "ack observed",
			configure: func(c *Client) {
				c.terminalFence = terminalFenceAckObserved
			},
			want: ErrTerminalFenceActive,
		},
		{
			name: "closed",
			configure: func(c *Client) {
				c.closed = true
			},
			want: ErrClosed,
		},
		{
			name: "disconnected",
			configure: func(c *Client) {
				c.conn = nil
			},
			want: ErrNotConnected,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newFenceReadyClientOrFatal(t)
			tt.configure(c)
			cut, err := c.beginTerminalFence(grant)
			if !errors.Is(err, tt.want) {
				t.Fatalf("beginTerminalFence() error = %v, want %v", err, tt.want)
			}
			if tt.idempotent && cut.done != nil {
				t.Fatal("idempotent acknowledged fence returned a new wait channel")
			}
		})
	}

	c := newFenceReadyClientOrFatal(t)
	cut, err := c.beginTerminalFence(grant)
	if err != nil {
		t.Fatalf("beginTerminalFence(open) error = %v", err)
	}
	if cut.conn == nil || cut.pending != c.pending || cut.epoch != grant.Epoch || cut.nonce == (frame.TerminalFenceNonce{}) || cut.done == nil {
		t.Fatalf("beginTerminalFence(open) cut = %#v, want complete immutable session cut", cut)
	}
	if c.terminalFence != terminalFenceAwaitingAck {
		t.Fatalf("terminal fence state = %v, want awaiting ack", c.terminalFence)
	}
}

func TestTerminalFenceStateQueriesRejectStaleCutsAndPreserveFailure(t *testing.T) {
	epoch := uint64(31)
	nonce := frame.TerminalFenceNonce{1, 2, 3}
	c := &Client{terminalFenceEpoch: epoch, terminalFenceNonce: nonce}

	if err := c.ensureTerminalFenceAwaiting(epoch+1, nonce); !errors.Is(err, ErrTerminalFenceProtocol) {
		t.Fatalf("ensureTerminalFenceAwaiting(stale epoch) = %v", err)
	}
	c.terminalFence = terminalFenceAwaitingAck
	if err := c.ensureTerminalFenceAwaiting(epoch, nonce); err != nil {
		t.Fatalf("ensureTerminalFenceAwaiting(active) error = %v", err)
	}
	c.terminalFence = terminalFenceFailed
	c.terminalFenceErr = io.ErrUnexpectedEOF
	if err := c.ensureTerminalFenceAwaiting(epoch, nonce); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ensureTerminalFenceAwaiting(failed) = %v, want retained cause", err)
	}
	c.terminalFenceErr = nil
	if err := c.ensureTerminalFenceAwaiting(epoch, nonce); !errors.Is(err, ErrTerminalFenceProtocol) {
		t.Fatalf("ensureTerminalFenceAwaiting(failed without cause) = %v", err)
	}

	if err := c.currentTerminalFenceError(epoch, frame.TerminalFenceNonce{9}); !errors.Is(err, ErrTerminalFenceProtocol) {
		t.Fatalf("currentTerminalFenceError(stale nonce) = %v", err)
	}
	c.terminalFence = terminalFenceAcknowledged
	if err := c.currentTerminalFenceError(epoch, nonce); err != nil {
		t.Fatalf("currentTerminalFenceError(acknowledged) = %v", err)
	}
	c.terminalFence = terminalFenceFailed
	c.terminalFenceErr = io.ErrUnexpectedEOF
	if err := c.currentTerminalFenceError(epoch, nonce); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("currentTerminalFenceError(failed) = %v", err)
	}
	c.terminalFenceErr = nil
	if err := c.currentTerminalFenceError(epoch, nonce); !errors.Is(err, ErrTerminalFenceProtocol) {
		t.Fatalf("currentTerminalFenceError(failed without cause) = %v", err)
	}
	c.terminalFence = terminalFenceAwaitingAck
	if err := c.currentTerminalFenceError(epoch, nonce); !errors.Is(err, ErrTerminalFenceActive) {
		t.Fatalf("currentTerminalFenceError(awaiting) = %v", err)
	}
}

func TestFailTerminalFenceIsIdempotentAndClosesWaiters(t *testing.T) {
	epoch := uint64(41)
	nonce := frame.TerminalFenceNonce{4, 1}
	done := make(chan struct{})
	c := &Client{
		terminalFence:      terminalFenceAwaitingAck,
		terminalFenceEpoch: epoch,
		terminalFenceNonce: nonce,
		terminalFenceDone:  done,
	}

	if err := c.failTerminalFence(epoch+1, nonce, io.EOF); !errors.Is(err, ErrTerminalFenceProtocol) || c.terminalFence != terminalFenceAwaitingAck {
		t.Fatalf("failTerminalFence(stale cut) = %v, state=%v", err, c.terminalFence)
	}
	err := c.failTerminalFence(epoch, nonce, nil)
	if !errors.Is(err, ErrTerminalFenceProtocol) || c.terminalFence != terminalFenceFailed {
		t.Fatalf("failTerminalFence(active) = %v, state=%v", err, c.terminalFence)
	}
	select {
	case <-done:
	default:
		t.Fatal("failTerminalFence() did not release fence waiter")
	}
	if again := c.failTerminalFence(epoch, nonce, io.ErrUnexpectedEOF); again != err {
		t.Fatalf("second failTerminalFence() = %v, want retained first error %v", again, err)
	}

	c = &Client{terminalFence: terminalFenceFailed, terminalFenceEpoch: epoch, terminalFenceNonce: nonce}
	if got := c.failTerminalFence(epoch, nonce, io.EOF); !errors.Is(got, ErrTerminalFenceProtocol) {
		t.Fatalf("failTerminalFence(failed without cause) = %v", got)
	}
	c.terminalFence = terminalFenceAcknowledged
	if got := c.failTerminalFence(epoch, nonce, io.EOF); got != nil {
		t.Fatalf("failTerminalFence(acknowledged) = %v, want nil", got)
	}
}

func TestTerminalFenceReaderFailuresAreScopedAndFailClosed(t *testing.T) {
	current := discardConn{}
	other := &identityConn{id: "other"}
	done := make(chan struct{})
	c := &Client{
		conn:               current,
		terminalFence:      terminalFenceAwaitingAck,
		terminalFenceDone:  done,
		terminalFenceEpoch: 51,
		terminalFenceNonce: frame.TerminalFenceNonce{5, 1},
		terminalFenceErr:   nil,
	}
	c.failTerminalFenceForRead(other, io.EOF)
	if c.terminalFence != terminalFenceAwaitingAck {
		t.Fatalf("stale reader changed terminal state to %v", c.terminalFence)
	}
	c.failTerminalFenceForRead(current, io.EOF)
	if c.terminalFence != terminalFenceFailed || !errors.Is(c.terminalFenceErr, ErrTerminalFenceProtocol) || !strings.Contains(c.terminalFenceErr.Error(), "stream ended before terminal ACK") {
		t.Fatalf("current reader EOF state=%v error=%v", c.terminalFence, c.terminalFenceErr)
	}
	select {
	case <-done:
	default:
		t.Fatal("current reader EOF did not release terminal waiter")
	}

	c = &Client{conn: current, terminalFence: terminalFenceOpen}
	c.failTerminalFenceForRead(current, nil)
	if c.terminalFence != terminalFenceOpen {
		t.Fatalf("open terminal state changed on ordinary reader failure: %v", c.terminalFence)
	}
}

func TestTerminalFenceRejectsMalformedAckAndPostAckFrames(t *testing.T) {
	current := discardConn{}
	done := make(chan struct{})
	c := &Client{
		conn:               current,
		terminalFence:      terminalFenceAwaitingAck,
		terminalFenceDone:  done,
		terminalFenceEpoch: 61,
		terminalFenceNonce: frame.TerminalFenceNonce{6, 1},
	}
	if err := c.acceptTerminalFenceAck(&frame.EventPacket{}, current); !errors.Is(err, ErrTerminalFenceProtocol) {
		t.Fatalf("acceptTerminalFenceAck(malformed) = %v", err)
	}
	if c.terminalFence != terminalFenceFailed {
		t.Fatalf("malformed terminal ACK state = %v, want failed", c.terminalFence)
	}

	c = &Client{conn: current, terminalFence: terminalFenceAcknowledged}
	if err := c.rejectFrameAfterTerminalFence(&frame.PongPacket{}, &identityConn{id: "stale"}); err != nil || c.terminalFence != terminalFenceAcknowledged {
		t.Fatalf("stale post-ACK frame error=%v state=%v", err, c.terminalFence)
	}
	if err := c.rejectFrameAfterTerminalFence(&frame.PongPacket{}, current); !errors.Is(err, ErrTerminalFenceProtocol) {
		t.Fatalf("current post-ACK frame error = %v", err)
	}
	if c.terminalFence != terminalFenceFailed {
		t.Fatalf("current post-ACK frame state = %v, want failed", c.terminalFence)
	}
}

func newDisconnectedClientOrFatal(t *testing.T) *Client {
	t.Helper()
	c, err := New(Config{Addr: "unit-test"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return c
}

func newFenceReadyClientOrFatal(t *testing.T) *Client {
	t.Helper()
	c := newDisconnectedClientOrFatal(t)
	c.conn = discardConn{}
	c.pending = newPendingTracker()
	return c
}

type identityConn struct {
	discardConn
	id string
}
