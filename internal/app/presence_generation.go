package app

import (
	"context"
	"sync/atomic"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
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

// preparePresenceFoundation composes the finite recovery pass before business
// entries open. It is not an all-cluster readiness gate: unavailable targets
// remain explicit and retain their ordinary fail-closed on-demand recovery.
func (a *App) preparePresenceFoundation(ctx context.Context, startedAt time.Time) error {
	full, ok := a.presenceRecovery.(*presenceFullRecovery)
	if !ok || a.restoreMaintenance.Load() || a.seedJoinPreActivationMode(ctx) {
		return ctx.Err()
	}
	// The constructed cluster resolves top-level/default identities; the nested
	// configuration may intentionally leave NodeID unset.
	node, ok := a.cluster.(interface{ NodeID() uint64 })
	if !ok {
		return ErrInvalidConfig
	}
	targets := make([]presence.RouteTarget, 0, 256)
	unknownRoutes, pendingLocal := 0, 0
	routes, available := a.cluster.(clusterWriteReadyRuntime)
	coverageKnown := false
	if available {
		count := routes.Snapshot().HashSlotCount
		coverageKnown = count != 0
		for hash := uint16(0); hash < count; hash++ {
			route, err := routes.RouteHashSlot(hash)
			if err != nil || route.HashSlot != hash || route.Leader == 0 {
				unknownRoutes++
				continue
			}
			if route.Leader != node.NodeID() {
				continue
			}
			if len(targets) == 256 {
				pendingLocal++
				continue
			}
			targets = append(targets, presence.RouteTarget{HashSlot: route.HashSlot, SlotID: route.SlotID, LeaderNodeID: route.Leader, LeaderTerm: route.LeaderTerm, ConfigEpoch: route.ConfigEpoch, RouteRevision: route.Revision, AuthorityEpoch: route.AuthorityEpoch})
		}
	}
	// Preparation consumes the remaining initial control-readiness budget; it
	// neither refreshes activation deadlines nor retries failed physical rounds.
	timeout := a.cfg.Cluster.Timeouts.Start
	if timeout <= 0 {
		timeout = defaultClusterWriteReadyTimeout
	}
	initial, cancel := context.WithDeadline(ctx, startedAt.Add(timeout))
	defer cancel()
	report, _ := full.callers.Prepare(initial, targets)
	a.lifecycleLogger().Info("Presence initial preparation finished",
		wklog.Event("internal.app.presence_initial_preparation"),
		wklog.Int("attempted", report.Attempted), wklog.Int("prepared", report.Prepared), wklog.Int("pending", report.Pending+pendingLocal),
		wklog.Int("unknown_routes", unknownRoutes), wklog.Bool("route_coverage_known", coverageKnown))
	return ctx.Err()
}
