package transport

import "errors"

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
