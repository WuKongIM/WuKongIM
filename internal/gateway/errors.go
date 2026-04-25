package gateway

import gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"

var (
	ErrNilHandler               = gatewaytypes.ErrNilHandler
	ErrListenerNameEmpty        = gatewaytypes.ErrListenerNameEmpty
	ErrListenerNameDuplicate    = gatewaytypes.ErrListenerNameDuplicate
	ErrListenerAddressEmpty     = gatewaytypes.ErrListenerAddressEmpty
	ErrListenerAddressDuplicate = gatewaytypes.ErrListenerAddressDuplicate
	ErrListenerNetworkEmpty     = gatewaytypes.ErrListenerNetworkEmpty
	ErrListenerTransportEmpty   = gatewaytypes.ErrListenerTransportEmpty
	ErrListenerProtocolEmpty    = gatewaytypes.ErrListenerProtocolEmpty
	ErrListenerWebsocketPath    = gatewaytypes.ErrListenerWebsocketPath
	ErrGatewayClosed            = gatewaytypes.ErrGatewayClosed
	ErrSessionClosed            = gatewaytypes.ErrSessionClosed
	ErrInboundOverflow          = gatewaytypes.ErrInboundOverflow
	ErrWriteTimeout             = gatewaytypes.ErrWriteTimeout
	ErrIdleTimeout              = gatewaytypes.ErrIdleTimeout
)

type CloseReason = gatewaytypes.CloseReason

const (
	CloseReasonServerStop       = gatewaytypes.CloseReasonServerStop
	CloseReasonPeerClosed       = gatewaytypes.CloseReasonPeerClosed
	CloseReasonProtocolError    = gatewaytypes.CloseReasonProtocolError
	CloseReasonInboundOverflow  = gatewaytypes.CloseReasonInboundOverflow
	CloseReasonPolicyViolation  = gatewaytypes.CloseReasonPolicyViolation
	CloseReasonPolicyTimeout    = gatewaytypes.CloseReasonPolicyTimeout
	CloseReasonWriteQueueFull   = gatewaytypes.CloseReasonWriteQueueFull
	CloseReasonOutboundOverflow = gatewaytypes.CloseReasonOutboundOverflow
	CloseReasonIdleTimeout      = gatewaytypes.CloseReasonIdleTimeout
	CloseReasonHandlerError     = gatewaytypes.CloseReasonHandlerError
)
