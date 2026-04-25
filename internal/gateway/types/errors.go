package types

import "errors"

var (
	ErrNilHandler               = errors.New("gateway: nil handler")
	ErrListenerNameEmpty        = errors.New("gateway: listener name is empty")
	ErrListenerNameDuplicate    = errors.New("gateway: duplicate listener name")
	ErrListenerAddressEmpty     = errors.New("gateway: listener address is empty")
	ErrListenerAddressDuplicate = errors.New("gateway: duplicate listener address")
	ErrListenerNetworkEmpty     = errors.New("gateway: listener network is empty")
	ErrListenerTransportEmpty   = errors.New("gateway: listener transport is empty")
	ErrListenerProtocolEmpty    = errors.New("gateway: listener protocol is empty")
	ErrListenerWebsocketPath    = errors.New("gateway: websocket listener path is required")
	ErrGatewayClosed            = errors.New("gateway: gateway is closed")
	ErrSessionClosed            = errors.New("gateway: session is closed")
	ErrInboundOverflow          = errors.New("gateway: inbound bytes limit exceeded")
	ErrWriteTimeout             = errors.New("gateway: write timeout")
	ErrIdleTimeout              = errors.New("gateway: idle timeout")
)

type CloseReason string

const (
	CloseReasonServerStop       CloseReason = "server_stop"
	CloseReasonPeerClosed       CloseReason = "peer_closed"
	CloseReasonProtocolError    CloseReason = "protocol_error"
	CloseReasonInboundOverflow  CloseReason = "inbound_overflow"
	CloseReasonPolicyViolation  CloseReason = "policy_violation"
	CloseReasonPolicyTimeout    CloseReason = "policy_timeout"
	CloseReasonWriteQueueFull   CloseReason = "write_queue_full"
	CloseReasonOutboundOverflow CloseReason = "outbound_overflow"
	CloseReasonIdleTimeout      CloseReason = "idle_timeout"
	CloseReasonHandlerError     CloseReason = "handler_error"
)
