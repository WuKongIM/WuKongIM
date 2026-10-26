package client

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// connectFailure retains a closed diagnostic projection while preserving the
// original error family/text. Its private cause must not become report data.
type connectFailure struct {
	cause       error
	phase       string
	kind        string
	reason      frame.ReasonCode
	reasonKnown bool
	fingerprint [16]byte
}

func (e *connectFailure) Error() string { return e.cause.Error() }
func (e *connectFailure) Unwrap() error { return e.cause }

// ConnectPhase identifies the failing CONNECT boundary, never an endpoint.
func (e *connectFailure) ConnectPhase() string { return e.phase }

// ConnectKind reports a closed kind without parsing the original error text.
func (e *connectFailure) ConnectKind() string { return e.kind }

// ConnackReason reports only a recognized reason from an observed CONNACK.
func (e *connectFailure) ConnackReason() (frame.ReasonCode, bool) {
	return e.reason, e.reasonKnown
}

// ConnectFingerprint is zero before a CONNECT packet has been constructed.
func (e *connectFailure) ConnectFingerprint() [16]byte { return e.fingerprint }

// failConnect adds failure-only metadata and reports the same error to Observer.
// Successful CONNECT and every SEND avoid this allocation and fingerprint work.
func (c *Client) failConnect(opts ConnectOptions, started time.Time, phase string, cause error, packet *frame.ConnectPacket, ack *frame.ConnackPacket) error {
	failure := &connectFailure{cause: cause, phase: phase, kind: connectFailureKind(cause)}
	switch phase {
	case "frame":
		failure.kind = "protocol"
	case "rejected":
		failure.kind = "rejected"
		if ack != nil && ack.ReasonCode <= frame.ReasonSystemBusy {
			failure.reason, failure.reasonKnown = ack.ReasonCode, true
		}
	case "crypto":
		failure.kind = "crypto"
	}
	if packet != nil {
		failure.fingerprint = frame.HandshakeFingerprint(packet.ClientKey, packet.ClientTimestamp)
	}
	c.observeConnect(opts, time.Since(started), failure)
	return failure
}

func connectFailureKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	case errors.Is(err, ErrClosed), errors.Is(err, net.ErrClosed), errors.Is(err, io.ErrClosedPipe):
		return "closed"
	case errors.Is(err, ErrTerminalFenceActive):
		return "fence"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) || networkError != nil {
		return "network"
	}
	return "unknown"
}
