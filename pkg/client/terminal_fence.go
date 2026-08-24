package client

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type terminalFenceState uint8

const (
	terminalFenceOpen terminalFenceState = iota
	terminalFenceAwaitingAck
	terminalFenceAckObserved
	terminalFenceAcknowledged
	terminalFenceFailed
)

type terminalFenceCut struct {
	conn    net.Conn
	pending *pendingTracker
	epoch   uint64
	nonce   frame.TerminalFenceNonce
	done    <-chan struct{}
}

// SealIngress preserves the legacy no-grant seam and fails closed. A target-
// published TerminalFenceGrant is mandatory for a real remote fence.
func (c *Client) SealIngress(ctx context.Context) error {
	if c == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if c.conn == nil {
		return ErrNotConnected
	}
	return ErrIngressSealUnsupported
}

// SealIngressWithFence quiesces SEND/PING admission, waits for every previously
// admitted SENDACK, writes one bounded request EVENT, and succeeds only after
// the reader decodes the exact server ACK. Socket close and local write
// callbacks never imply success.
func (c *Client) SealIngressWithFence(ctx context.Context, grant frame.TerminalFenceGrant) error {
	if c == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := grant.Validate(); err != nil {
		return fmt.Errorf("%w: invalid grant", ErrTerminalFenceProtocol)
	}

	cut, err := c.beginTerminalFence(grant)
	if err != nil {
		return err
	}
	if cut.done == nil {
		return nil
	}

	if err := cut.pending.waitEmpty(ctx); err != nil {
		return c.failTerminalFence(cut.epoch, cut.nonce, err)
	}
	if err := c.ensureTerminalFenceAwaiting(cut.epoch, cut.nonce); err != nil {
		return err
	}
	request, err := frame.NewTerminalFenceRequest(grant, cut.nonce)
	if err != nil {
		return c.failTerminalFence(cut.epoch, cut.nonce, err)
	}
	if err := c.writeControlToConn(ctx, request, cut.conn, true); err != nil {
		return c.failTerminalFence(cut.epoch, cut.nonce, err)
	}

	select {
	case <-cut.done:
		return c.currentTerminalFenceError(cut.epoch, cut.nonce)
	case <-ctx.Done():
		return c.failTerminalFence(cut.epoch, cut.nonce, ctx.Err())
	}
}

// beginTerminalFence takes one atomic admission cut, then releases the locks
// before waiting for SENDACKs or the remote ACK. New SEND/PING/reconnect calls
// can therefore fail promptly while RECVACK remains independently writable.
func (c *Client) beginTerminalFence(grant frame.TerminalFenceGrant) (terminalFenceCut, error) {
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.terminalFence {
	case terminalFenceAcknowledged:
		if c.terminalFenceEpoch == grant.Epoch {
			return terminalFenceCut{}, nil
		}
		return terminalFenceCut{}, ErrTerminalFenceActive
	case terminalFenceFailed:
		if c.terminalFenceErr != nil {
			return terminalFenceCut{}, c.terminalFenceErr
		}
		return terminalFenceCut{}, ErrTerminalFenceProtocol
	case terminalFenceAwaitingAck, terminalFenceAckObserved:
		return terminalFenceCut{}, ErrTerminalFenceActive
	}
	if c.closed {
		return terminalFenceCut{}, ErrClosed
	}
	if c.conn == nil || c.pending == nil {
		return terminalFenceCut{}, ErrNotConnected
	}
	nonce, err := newTerminalFenceNonce()
	if err != nil {
		return terminalFenceCut{}, fmt.Errorf("%w: create nonce", ErrTerminalFenceProtocol)
	}
	c.terminalFenceEpoch = grant.Epoch
	c.terminalFenceNonce = nonce
	c.terminalFenceDone = make(chan struct{})
	c.terminalFenceErr = nil
	c.terminalFence = terminalFenceAwaitingAck
	return terminalFenceCut{
		conn:    c.conn,
		pending: c.pending,
		epoch:   grant.Epoch,
		nonce:   nonce,
		done:    c.terminalFenceDone,
	}, nil
}

func newTerminalFenceNonce() (frame.TerminalFenceNonce, error) {
	for {
		var nonce frame.TerminalFenceNonce
		if _, err := io.ReadFull(cryptorand.Reader, nonce[:]); err != nil {
			return frame.TerminalFenceNonce{}, err
		}
		if nonce != (frame.TerminalFenceNonce{}) {
			return nonce, nil
		}
	}
}

func (c *Client) ensureTerminalFenceAwaiting(epoch uint64, nonce frame.TerminalFenceNonce) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminalFenceEpoch != epoch || c.terminalFenceNonce != nonce {
		return fmt.Errorf("%w: terminal cut changed", ErrTerminalFenceProtocol)
	}
	if c.terminalFence == terminalFenceAwaitingAck {
		return nil
	}
	if c.terminalFenceErr != nil {
		return c.terminalFenceErr
	}
	return ErrTerminalFenceProtocol
}

func (c *Client) currentTerminalFenceError(epoch uint64, nonce frame.TerminalFenceNonce) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminalFenceEpoch != epoch || c.terminalFenceNonce != nonce {
		return fmt.Errorf("%w: terminal cut changed", ErrTerminalFenceProtocol)
	}
	switch c.terminalFence {
	case terminalFenceAcknowledged:
		return nil
	case terminalFenceFailed:
		if c.terminalFenceErr != nil {
			return c.terminalFenceErr
		}
		return ErrTerminalFenceProtocol
	default:
		return ErrTerminalFenceActive
	}
}

func (c *Client) failTerminalFence(epoch uint64, nonce frame.TerminalFenceNonce, cause error) error {
	if cause == nil {
		cause = ErrTerminalFenceProtocol
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminalFenceEpoch != epoch || c.terminalFenceNonce != nonce {
		return fmt.Errorf("%w: terminal cut changed", ErrTerminalFenceProtocol)
	}
	if c.terminalFence == terminalFenceAcknowledged {
		return nil
	}
	if c.terminalFence == terminalFenceFailed {
		if c.terminalFenceErr != nil {
			return c.terminalFenceErr
		}
		return ErrTerminalFenceProtocol
	}
	err := fmt.Errorf("%w: %v", ErrTerminalFenceProtocol, cause)
	c.terminalFence = terminalFenceFailed
	c.terminalFenceErr = err
	if c.terminalFenceDone != nil {
		close(c.terminalFenceDone)
	}
	return err
}

func (c *Client) acceptTerminalFenceAck(pkt *frame.EventPacket, conn net.Conn) error {
	ack, err := frame.ParseTerminalFenceAck(pkt)
	c.mu.Lock()
	defer c.mu.Unlock()
	if conn != nil && c.conn != conn {
		return nil
	}
	if err != nil {
		return c.failActiveTerminalFenceLocked(err)
	}
	if c.terminalFence != terminalFenceAwaitingAck || !ack.Matches(c.terminalFenceEpoch, c.terminalFenceNonce) {
		err := fmt.Errorf("%w: terminal ACK does not match active cut", ErrTerminalFenceProtocol)
		c.failActiveTerminalFenceLocked(err)
		return err
	}
	// Do not publish success while the reader may already hold post-ACK bytes
	// from the same socket read. The reader completes this state only at its
	// current decoded batch boundary.
	c.terminalFence = terminalFenceAckObserved
	return nil
}

func (c *Client) completeTerminalFenceReadBatch(conn net.Conn, trailingBytes int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if conn != nil && c.conn != conn {
		return nil
	}
	if c.terminalFence != terminalFenceAckObserved {
		return nil
	}
	if trailingBytes != 0 {
		return c.failActiveTerminalFenceLocked(fmt.Errorf("%w: trailing bytes after terminal ACK", ErrTerminalFenceProtocol))
	}
	c.terminalFence = terminalFenceAcknowledged
	if c.terminalFenceDone != nil {
		close(c.terminalFenceDone)
	}
	return nil
}

func (c *Client) rejectFrameAfterTerminalFence(f frame.Frame, conn net.Conn) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if conn != nil && c.conn != conn {
		return nil
	}
	if c.terminalFence != terminalFenceAckObserved && c.terminalFence != terminalFenceAcknowledged {
		return nil
	}
	err := fmt.Errorf("%w: received %s after terminal ACK", ErrTerminalFenceProtocol, f.GetFrameType())
	if c.terminalFence == terminalFenceAckObserved {
		return c.failActiveTerminalFenceLocked(err)
	}
	c.terminalFence = terminalFenceFailed
	c.terminalFenceErr = err
	return err
}

func (c *Client) failActiveTerminalFenceLocked(cause error) error {
	if c.terminalFence == terminalFenceFailed {
		if c.terminalFenceErr != nil {
			return c.terminalFenceErr
		}
		return ErrTerminalFenceProtocol
	}
	if c.terminalFence != terminalFenceAwaitingAck && c.terminalFence != terminalFenceAckObserved {
		return cause
	}
	if cause == nil {
		cause = ErrTerminalFenceProtocol
	}
	err := fmt.Errorf("%w: %v", ErrTerminalFenceProtocol, cause)
	c.terminalFence = terminalFenceFailed
	c.terminalFenceErr = err
	if c.terminalFenceDone != nil {
		close(c.terminalFenceDone)
	}
	return err
}

func (c *Client) failTerminalFenceForRead(conn net.Conn, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if conn != nil && c.conn != conn {
		return
	}
	if c.terminalFence != terminalFenceAwaitingAck && c.terminalFence != terminalFenceAckObserved {
		return
	}
	if err == nil {
		err = net.ErrClosed
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		err = fmt.Errorf("stream ended before terminal ACK: %w", err)
	}
	c.failActiveTerminalFenceLocked(err)
}
