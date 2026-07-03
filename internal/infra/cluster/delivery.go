package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

// DeliveryPusher routes owner-node delivery batches to local or remote pushers.
type DeliveryPusher struct {
	// localNodeID is the node identity used to classify local owner batches.
	localNodeID uint64
	// local accepts delivery batches for sessions owned by this node.
	local runtimedelivery.Pusher
	// remote forwards delivery batches to non-local owner nodes over node RPC.
	remote *accessnode.Client
}

// NewDeliveryPusher creates a delivery pusher that routes by PushCommand.OwnerNodeID.
func NewDeliveryPusher(localNodeID uint64, local runtimedelivery.Pusher, remote *accessnode.Client) *DeliveryPusher {
	return &DeliveryPusher{localNodeID: localNodeID, local: local, remote: remote}
}

// Push routes one owner-node delivery command and classifies unavailable remote owners as retryable.
func (p *DeliveryPusher) Push(ctx context.Context, cmd runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	if p != nil && cmd.OwnerNodeID == p.localNodeID {
		if p.local != nil {
			return p.local.Push(ctx, cmd)
		}
		// A missing local owner pusher means no local session runtime can accept these routes.
		return runtimedelivery.PushResult{Dropped: cloneDeliveryRoutes(cmd.Routes)}, nil
	}
	if p == nil || p.remote == nil {
		return runtimedelivery.PushResult{Retryable: cloneDeliveryRoutes(cmd.Routes)}, nil
	}
	result, err := p.remote.PushBatch(ctx, cmd.OwnerNodeID, cmd)
	if err != nil {
		return runtimedelivery.PushResult{Retryable: cloneDeliveryRoutes(cmd.Routes)}, nil
	}
	return result, nil
}

func cloneDeliveryRoutes(routes []runtimedelivery.Route) []runtimedelivery.Route {
	if len(routes) == 0 {
		return nil
	}
	return append([]runtimedelivery.Route(nil), routes...)
}
