package transport

import (
	"context"
	"errors"
)

type Factory interface {
	Name() string
	// Build must return one listener per input spec, preserving spec order in the returned slice.
	// If Build returns an error, the factory must not leak transport-owned resources for any
	// listeners or shared transport state allocated during that call.
	Build(specs []ListenerSpec) ([]Listener, error)
}

type Listener interface {
	Start() error
	// Stop must be safe to call on a listener that was built but never successfully started.
	Stop() error
	Addr() string
}

type Conn interface {
	ID() uint64
	// Write sends immutable payload bytes to the peer; callers must not mutate the slice after calling Write.
	Write([]byte) error
	Close() error
	LocalAddr() string
	RemoteAddr() string
}

// CloseWaiter provides physical isolation independently of handler cleanup.
// CloseAndWait fences future writes and returns nil only after physical close
// completes. Cancellation (including an already canceled context) stops only
// this wait; the close is still requested and repeated calls join one receipt.
// A successful Conn.Close or OnClose notification is not a substitute.
type CloseWaiter interface {
	CloseAndWait(context.Context) error
}

var (
	ErrConnectionClosing = errors.New("gateway/transport: connection closing")
	ErrCloseUnproved     = errors.New("gateway/transport: physical close unproved")
)

// PeerAddress preserves the physical TCP peer when RemoteAddr is supplied by a
// trusted proxy. It is optional so existing transport implementations remain valid.
type PeerAddress interface {
	PeerAddr() string
}

// ObservedWriter exposes the physical asynchronous-write completion boundary.
// The completion callback is invoked at most once after transport ownership of
// an accepted payload ends. A caller that does not need this evidence should
// continue to use Conn.Write.
type ObservedWriter interface {
	WriteObserved(data []byte, frameType string, complete func(error)) error
}

// WebSocketMessageType identifies the websocket opcode for an outbound application message.
type WebSocketMessageType uint8

const (
	// WebSocketMessageUnknown lets the transport use its connection-local fallback.
	WebSocketMessageUnknown WebSocketMessageType = iota
	// WebSocketMessageText writes an outbound text message.
	WebSocketMessageText
	// WebSocketMessageBinary writes an outbound binary message.
	WebSocketMessageBinary
)

// WebSocketMessageWriter supports protocol-aware websocket writes without payload sniffing.
type WebSocketMessageWriter interface {
	// WriteWebSocketMessage writes immutable payload bytes with an explicit websocket message type.
	WriteWebSocketMessage(data []byte, messageType WebSocketMessageType) error
}

// ErrOutboundBytesExceeded indicates that transport-owned outbound buffering exceeded its configured limit.
var ErrOutboundBytesExceeded = errors.New("gateway/transport: outbound bytes limit exceeded")

type ConnHandler interface {
	OnOpen(conn Conn) error
	OnData(conn Conn, data []byte) error
	OnClose(conn Conn, err error)
}

type ListenerSpec struct {
	Options ListenerOptions
	Handler ConnHandler
}

// HandshakeRejectionError identifies an expected client handshake rejection,
// not a failure of the listener. Transports still return the HTTP rejection
// and close the connection; observers may sample these diagnostics separately.
type HandshakeRejectionError struct {
	// StatusCode is the HTTP response sent to the rejected client.
	StatusCode int
	// Err is the non-nil, redacted transport diagnostic cause.
	Err error
}

func (e *HandshakeRejectionError) Error() string { return e.Err.Error() }
func (e *HandshakeRejectionError) Unwrap() error { return e.Err }
