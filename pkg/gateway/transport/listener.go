package transport

import (
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type ListenerOptions struct {
	Name    string
	Network string
	Address string
	Path    string
	// WebSocketSubprotocol requires an exact case-sensitive offered token and
	// selects it in the Upgrade response. Empty leaves subprotocols unselected.
	WebSocketSubprotocol string
	// WebSocketBinaryOnly rejects text data messages, including fragmented text.
	// WebSocket control frames retain their normal transport behavior.
	WebSocketBinaryOnly bool
	// ProxyProtocolTrustedCIDRs optionally limits PROXY address assertions to these
	// actual TCP peer networks. Detection is always enabled; empty accepts any peer
	// without verifying the asserted client address. Direct traffic stays allowed.
	ProxyProtocolTrustedCIDRs []string
	// MaxPendingBytes bounds bytes buffered inside the transport before the gateway core consumes them.
	MaxPendingBytes int
	// MaxOutboundBytes bounds bytes buffered inside the transport after gateway queue dequeue.
	MaxOutboundBytes int64
	// Observer receives aggregate transport pressure observations.
	Observer gatewaytypes.TransportPressureObserver
	OnError  func(error)
	Logger   wklog.Logger
}
