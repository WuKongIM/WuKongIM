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
