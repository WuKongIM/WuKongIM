package presence

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
)

func (a *App) HeartbeatOnce(ctx context.Context) error {
	groups := a.online.ActiveSlots()
	var err error
	for _, slot := range groups {
		if heartbeatErr := a.heartbeatGroup(ctx, slot); heartbeatErr != nil {
			err = errors.Join(err, heartbeatErr)
		}
	}
	return err
}

func (a *App) heartbeatGroup(ctx context.Context, slot online.SlotSnapshot) error {
	lease := GatewayLease{
		SlotID:         slot.SlotID,
		GatewayNodeID:  a.localNodeID,
		GatewayBootID:  a.gatewayBootID,
		RouteCount:     slot.Count,
		RouteDigest:    slot.Digest,
		LeaseUntilUnix: a.now().Add(a.leaseTTL).Unix(),
	}

	result, err := a.authority.HeartbeatAuthoritative(ctx, HeartbeatAuthoritativeCommand{
		Lease: lease,
	})
	if err != nil {
		return err
	}
	if !result.Mismatch {
		return nil
	}

	routes := make([]Route, 0)
	for _, conn := range a.online.ActiveConnectionsBySlot(slot.SlotID) {
		routes = append(routes, a.routeFromConn(conn))
	}
	return a.authority.ReplayAuthoritative(ctx, ReplayAuthoritativeCommand{
		Lease:  lease,
		Routes: routes,
	})
}
