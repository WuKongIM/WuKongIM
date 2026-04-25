package transport

import "errors"

var (
	ErrStopped        = errors.New("nodetransport: stopped")
	ErrTimeout        = errors.New("nodetransport: request timeout")
	ErrNodeNotFound   = errors.New("nodetransport: node not found")
	ErrQueueFull      = errors.New("nodetransport: queue full")
	ErrMsgTooLarge    = errors.New("nodetransport: message too large")
	ErrInvalidMsgType = errors.New("nodetransport: invalid message type 0")
)

const (
	// MaxMessageSize is the upper bound for a single wire message body.
	MaxMessageSize = 64 << 20 // 64 MB

	// MaxFrameSize matches the transport frame body limit.
	MaxFrameSize = MaxMessageSize

	// Reserved message types for built-in RPC mechanism.
	MsgTypeRPCRequest  uint8 = 0xFE
	MsgTypeRPCResponse uint8 = 0xFF

	// HeaderSize is [msgType:1][bodyLen:4].
	HeaderSize = 5

	// maxPooledBufCap prevents the buffer pool from retaining huge slices.
	maxPooledBufCap = 64 * 1024

	// msgHeaderSize is [msgType:1][bodyLen:4].
	msgHeaderSize = HeaderSize
)
