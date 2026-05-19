package transport

import "github.com/WuKongIM/WuKongIM/pkg/wklog"

type ListenerOptions struct {
	Name    string
	Network string
	Address string
	Path    string
	// MaxPendingBytes bounds bytes buffered inside the transport before the gateway core consumes them.
	MaxPendingBytes int
	// MaxOutboundBytes bounds bytes buffered inside the transport after gateway queue dequeue.
	MaxOutboundBytes int64
	OnError          func(error)
	Logger           wklog.Logger
}
