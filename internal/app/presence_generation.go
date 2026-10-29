package app

import (
	"context"
	"sync/atomic"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// presenceFullAuthority keeps RPC/client wiring stable across a joined recovery
// cohort replacement. Business work remains in the current presence usecase.
type presenceFullAuthority struct {
	current atomic.Pointer[presence.SlotRecovery]
}

func (a *presenceFullAuthority) RegisterRoute(ctx context.Context, target presence.RouteTarget, route presence.Route) (presence.RegisterResult, error) {
	if current := a.current.Load(); current != nil {
		return current.RegisterRoute(ctx, target, route)
	}
	return presence.RegisterResult{}, authority.ErrSlotIncomplete
}
func (a *presenceFullAuthority) CommitRouteWithReceipt(ctx context.Context, claim presence.GenerationClaim, token presence.PendingRouteToken) (presence.ActivationReceipt, error) {
	if current := a.current.Load(); current != nil {
		return current.CommitRouteWithReceipt(ctx, claim, token)
	}
	return presence.ActivationReceipt{}, authority.ErrSlotIncomplete
}
func (a *presenceFullAuthority) AbortRouteWithClaim(ctx context.Context, claim presence.GenerationClaim, token presence.PendingRouteToken) error {
	if current := a.current.Load(); current != nil {
		return current.AbortRouteWithClaim(ctx, claim, token)
	}
	return authority.ErrSlotIncomplete
}
func (a *presenceFullAuthority) TouchRoutes(ctx context.Context, claim presence.GenerationClaim, routes []presence.Route) error {
	if current := a.current.Load(); current != nil {
		return current.TouchRoutes(ctx, claim, routes)
	}
	return authority.ErrSlotIncomplete
}
func (a *presenceFullAuthority) UnregisterRoute(ctx context.Context, claim presence.GenerationClaim, identity presence.RouteIdentity, seq uint64) error {
	if current := a.current.Load(); current != nil {
		return current.UnregisterRoute(ctx, claim, identity, seq)
	}
	return authority.ErrSlotIncomplete
}

// presenceFullRecovery closes original callers before joining the slot scans
// and suspect rechecks whose physical I/O may outlive canceled caller contexts.
type presenceFullRecovery struct {
	callers *presence.AuthorityRecovery
	slots   *presence.SlotRecovery
}

func (r *presenceFullRecovery) Close(ctx context.Context) error {
	if err := r.callers.Close(ctx); err != nil {
		return err
	}
	return r.slots.Close(ctx)
}
