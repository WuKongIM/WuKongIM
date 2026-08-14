package client

import (
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var (
	// ErrMissingAddr reports a client config without a target address.
	ErrMissingAddr = errors.New("client: addr is required")
	// ErrNotConnected reports an operation that requires an active connection.
	ErrNotConnected = errors.New("client: not connected")
	// ErrClosed reports an operation after the client has closed.
	ErrClosed = errors.New("client: closed")
	// ErrIngressSealUnsupported reports that the current WKProto transport has
	// no server-confirmed flush fence and therefore cannot prove a clean
	// pre-close receive boundary.
	ErrIngressSealUnsupported = errors.New("client: ingress seal unsupported")
	// ErrTerminalFenceActive reports a SEND, PING, or reconnect attempted after
	// terminal quiescing began. RECVACK remains allowed until shutdown.
	ErrTerminalFenceActive = errors.New("client: terminal fence active")
	// ErrTerminalFenceProtocol reports a missing, malformed, mismatched, or
	// violated terminal fence acknowledgement.
	ErrTerminalFenceProtocol = errors.New("client: terminal fence protocol violation")
	// ErrPayloadTooLarge reports a SEND payload larger than the configured batch limit.
	ErrPayloadTooLarge = errors.New("client: payload too large")
	// ErrSendQueueFull reports a SEND admission failure because the local queue is full.
	ErrSendQueueFull = errors.New("client: send queue full")
	// ErrAckTimeout reports a SEND that did not receive SENDACK before its deadline.
	ErrAckTimeout = errors.New("client: sendack timeout")
	// ErrDuplicatePendingSend reports a SEND admitted with an already pending key.
	ErrDuplicatePendingSend = errors.New("client: duplicate pending send")
	// ErrClientSeqExhausted reports that the client sequence generator has no values left.
	ErrClientSeqExhausted = errors.New("client: client sequence exhausted")
	// ErrInvalidMessage reports an outbound message that cannot be encoded as WKProto SEND.
	ErrInvalidMessage = errors.New("client: invalid message")
)

type sessionReadError struct {
	cause error
}

func (e *sessionReadError) Error() string {
	return e.cause.Error()
}

func (e *sessionReadError) Unwrap() error {
	return e.cause
}

// IsSessionReadError reports a pending SEND failure caused by the session
// reader's terminal error. ReadFrame returns the original terminal cause.
func IsSessionReadError(err error) bool {
	var target *sessionReadError
	return errors.As(err, &target)
}

func wrapSessionReadError(err error) error {
	if err == nil {
		err = ErrClosed
	}
	return &sessionReadError{cause: err}
}

// SendError reports a non-success SENDACK for one SEND item.
type SendError struct {
	// ClientSeq is the client sequence echoed by the server.
	ClientSeq uint64
	// ClientMsgNo is the client message number echoed by the server.
	ClientMsgNo string
	// ReasonCode is the server SENDACK reason.
	ReasonCode frame.ReasonCode
}

func (e SendError) Error() string {
	return fmt.Sprintf("client: sendack reason=%s client_seq=%d client_msg_no=%q", e.ReasonCode, e.ClientSeq, e.ClientMsgNo)
}
