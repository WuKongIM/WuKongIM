package app

import (
	"sync/atomic"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	deliveryinfra "github.com/WuKongIM/WuKongIM/internal/infra/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// wireMessageEvents uses existing request admission/lifecycle rather than an
// asynchronous token queue. All fanouts finish inside the accepted API request.
func (a *App) wireMessageEvents(opts *message.Options) {
	if opts.EventStore == nil || opts.LookupReader == nil || a.presence == nil || a.online == nil {
		return
	}
	subscribers, ok := a.cluster.(message.UpdateSubscribers)
	if !ok {
		return
	}
	peers, ok := a.cluster.(deliveryinfra.HintPeerCaller)
	if !ok {
		return
	}
	registrar, ok := a.cluster.(nodeRPCRegistrar)
	if !ok {
		return
	}
	events := &deliveryinfra.MessageEvents{Online: a.online, Presence: a.presence, Peers: peers, NodeID: a.cfg.NodeID}
	opts.EventNotifications = events
	opts.EventSubscribers = subscribers
	var last atomic.Int64
	opts.EventNotificationResult = func(err error) {
		if err != nil && a.logger != nil {
			now := time.Now().Unix()
			prev := last.Load()
			if now-prev >= 60 && last.CompareAndSwap(prev, now) {
				a.logger.Warn("stream online delivery incomplete; history remains authoritative", wklog.Event("internal.app.stream_event_delivery_incomplete"))
			}
		}
	}
	registrar.RegisterRPC(clusternet.RPCMessageEventDelivery, accessnode.MessageEventRPC{Writer: events})
}
