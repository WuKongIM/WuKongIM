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
