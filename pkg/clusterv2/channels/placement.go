package channels

import (
	"context"
	"fmt"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// SlotPlacementResolver derives initial channel placement from Slot routes.
type SlotPlacementResolver struct {
	router PlacementRouter
	minISR int
}

// NewSlotPlacementResolver creates a placement resolver backed by router.
func NewSlotPlacementResolver(router PlacementRouter, minISR int) *SlotPlacementResolver {
	return &SlotPlacementResolver{router: router, minISR: minISR}
}

// ResolveChannelPlacement returns the preferred ChannelV2 leader and peers for id.
func (r *SlotPlacementResolver) ResolveChannelPlacement(ctx context.Context, id ch.ChannelID) (ChannelPlacement, error) {
	if err := ctxErr(ctx); err != nil {
		return ChannelPlacement{}, err
	}
	if r == nil || r.router == nil {
		return ChannelPlacement{}, fmt.Errorf("%w: placement router is nil", ch.ErrInvalidConfig)
	}
	route, err := r.router.RouteKey(id.ID)
	if err != nil {
		return ChannelPlacement{}, err
	}
	replicas := make([]ch.NodeID, 0, len(route.Peers))
	for _, peer := range route.Peers {
		replicas = append(replicas, ch.NodeID(peer))
	}
	leader := route.Leader
	if route.PreferredLeader != 0 && routeHasPeer(route.Peers, route.PreferredLeader) {
		leader = route.PreferredLeader
	}
	return ChannelPlacement{Leader: ch.NodeID(leader), Replicas: replicas, MinISR: r.minISR}, nil
}

func routeHasPeer(peers []uint64, nodeID uint64) bool {
	for _, peer := range peers {
		if peer == nodeID {
			return true
		}
	}
	return false
}
