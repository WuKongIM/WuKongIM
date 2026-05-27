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

// ResolveChannelPlacement returns the Slot leader and peers for id.
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
	return ChannelPlacement{Leader: ch.NodeID(route.Leader), Replicas: replicas, MinISR: r.minISR}, nil
}
