package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// presenceTouchLocalRegistry drains owner-local routes with pending activity updates.
type presenceTouchLocalRegistry interface {
	DrainTouched(limit int) []online.OwnerRoute
	RequeueTouched([]online.OwnerRoute)
}

// presenceOwnerLocalRegistry exposes owner-local sessions for conflict actions.
type presenceOwnerLocalRegistry interface {
	LocalSession(sessionID uint64) (online.LocalSession, bool)
	MarkClosingAndUnregister(sessionID uint64) (online.OwnerRoute, bool)
}

// presenceTouchAuthority routes batched activity refreshes to UID authorities.
type presenceTouchAuthority interface {
	ResolveRouteTargets(context.Context, []string) []presence.RouteTargetResult
	TouchRoutesTo(context.Context, presence.RouteTarget, []presence.Route) error
}

// presenceTouchDirectory owns local authority state and TTL expiry.
type presenceTouchDirectory interface {
	BecomeAuthority(presence.RouteTarget)
	LoseAuthority(uint16)
	ExpireRoutes(time.Time, time.Duration) int
}

type presenceTouchWorkerOptions struct {
	// NodeID identifies the local node so authority events can update only local state.
	NodeID uint64
	// Events provides an already-created route authority stream for tests.
	Events <-chan cluster.RouteAuthorityEvent
	// Watch creates the route authority stream during Start.
	Watch func() <-chan cluster.RouteAuthorityEvent
	// Initial returns visible authorities to seed local state after startup.
	Initial func() []cluster.RouteAuthority
	// Local stores owner-local active sessions with pending touch updates.
	Local presenceTouchLocalRegistry
	// Authority sends grouped touches to the observed authority target.
	Authority presenceTouchAuthority
	// Directory installs or clears local authority state and expires stale routes.
	Directory presenceTouchDirectory
	// FlushInterval controls periodic dirty route flushes.
	FlushInterval time.Duration
	// BatchSize bounds routes drained in one chunk.
	BatchSize int
	// MaxRoutesPerFlush bounds routes drained across all chunks in one flush.
	MaxRoutesPerFlush int
	// RouteTTL bounds authority-side route liveness since the latest touch.
	RouteTTL time.Duration
	// Logger records background touch failures that are retried by requeueing.
	Logger wklog.Logger
}

// presenceTouchWorker watches authority changes and periodically flushes owner touches.
type presenceTouchWorker struct {
	opts presenceTouchWorkerOptions

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
	latest map[uint16]presence.RouteTarget
}

func newPresenceTouchWorker(opts presenceTouchWorkerOptions) *presenceTouchWorker {
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = time.Second
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 512
	}
	if opts.MaxRoutesPerFlush <= 0 {
		opts.MaxRoutesPerFlush = 65536
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	return &presenceTouchWorker{
		opts:   opts,
		latest: make(map[uint16]presence.RouteTarget),
	}
}

// Start begins authority watching and periodic touch flushing.
func (w *presenceTouchWorker) Start(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	if w.cancel != nil {
		w.mu.Unlock()
		return nil
	}
	events := w.opts.Events
	if events == nil && w.opts.Watch != nil {
		events = w.opts.Watch()
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.wg.Add(2)
	w.mu.Unlock()

	go w.watch(runCtx, events)
	go w.tick(runCtx)
	w.reconcileAuthorities()
	return nil
}

// Stop cancels authority watching and touch flushing.
func (w *presenceTouchWorker) Stop(context.Context) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	cancel := w.cancel
	w.cancel = nil
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.wg.Wait()
	return nil
}

func (w *presenceTouchWorker) watch(ctx context.Context, events <-chan cluster.RouteAuthorityEvent) {
	defer w.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				if ctx.Err() == nil {
					w.logger().Warn("presence authority watch closed",
						wklog.Event("internal.app.presence_authority_watch_closed"),
					)
				}
				return
			}
			for _, authority := range event.Authorities {
				w.handleAuthority(authority)
			}
		}
	}
}

func (w *presenceTouchWorker) tick(ctx context.Context) {
	defer w.wg.Done()
	ticker := time.NewTicker(w.opts.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			w.reconcileAuthorities()
			w.flushOnce(ctx, now)
		}
	}
}

func (w *presenceTouchWorker) initialAuthorities() []cluster.RouteAuthority {
	if w.opts.Initial == nil {
		return nil
	}
	return w.opts.Initial()
}

func (w *presenceTouchWorker) reconcileAuthorities() {
	for _, authority := range w.initialAuthorities() {
		w.handleAuthority(authority)
	}
}

func (w *presenceTouchWorker) handleAuthority(authority cluster.RouteAuthority) {
	if w.opts.Directory == nil {
		return
	}
	target := presence.RouteTarget{
		HashSlot:       authority.HashSlot,
		SlotID:         authority.SlotID,
		LeaderNodeID:   authority.LeaderNodeID,
		LeaderTerm:     authority.LeaderTerm,
		ConfigEpoch:    authority.ConfigEpoch,
		RouteRevision:  authority.RouteRevision,
		AuthorityEpoch: authority.AuthorityEpoch,
	}
	if !w.acceptAuthority(target) {
		return
	}
	if authority.LeaderNodeID == 0 {
		w.opts.Directory.LoseAuthority(authority.HashSlot)
		return
	}
	if authority.LeaderNodeID == w.opts.NodeID {
		w.opts.Directory.BecomeAuthority(target)
		return
	}
	w.opts.Directory.LoseAuthority(authority.HashSlot)
}

func (w *presenceTouchWorker) acceptAuthority(target presence.RouteTarget) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.latest == nil {
		w.latest = make(map[uint16]presence.RouteTarget)
	}
	current, ok := w.latest[target.HashSlot]
	if ok && !authorityTargetNewer(target, current) {
		return false
	}
	w.latest[target.HashSlot] = target
	return true
}

func authorityTargetNewer(next, current presence.RouteTarget) bool {
	if next.RouteRevision != current.RouteRevision {
		return next.RouteRevision > current.RouteRevision
	}
	if next.ConfigEpoch != current.ConfigEpoch {
		return next.ConfigEpoch > current.ConfigEpoch
	}
	if next.LeaderTerm != current.LeaderTerm {
		return next.LeaderTerm > current.LeaderTerm
	}
	return next.AuthorityEpoch > current.AuthorityEpoch
}

func (w *presenceTouchWorker) flushOnce(ctx context.Context, now time.Time) {
	if w == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return
	}
	if w.opts.Directory != nil && w.opts.RouteTTL > 0 {
		w.opts.Directory.ExpireRoutes(now, w.opts.RouteTTL)
	}
	if w.opts.Local == nil || w.opts.Authority == nil {
		return
	}
	remaining := w.opts.MaxRoutesPerFlush
	requeue := make([]online.OwnerRoute, 0)
	defer func() {
		if len(requeue) > 0 {
			w.opts.Local.RequeueTouched(requeue)
		}
	}()

	for remaining > 0 {
		if ctx.Err() != nil {
			return
		}
		request := min(w.opts.BatchSize, remaining)
		touched := w.opts.Local.DrainTouched(request)
		if len(touched) == 0 {
			return
		}
		remaining -= len(touched)
		if ctx.Err() != nil {
			requeue = append(requeue, touched...)
			return
		}

		uids := uniqueOwnerRouteUIDs(touched)
		results := w.opts.Authority.ResolveRouteTargets(ctx, uids)
		if ctx.Err() != nil {
			requeue = append(requeue, touched...)
			return
		}

		resolved := make(map[string]presence.RouteTarget, len(uids))
		failed := make(map[string]error)
		for i, uid := range uids {
			if i >= len(results) {
				failed[uid] = fmt.Errorf("presence touch route target result missing at index %d", i)
				continue
			}
			if results[i].Err != nil {
				failed[uid] = results[i].Err
				continue
			}
			resolved[uid] = results[i].Target
		}

		groups := make([]presenceTouchRouteGroup, 0)
		groupIndex := make(map[presence.RouteTarget]int)
		loggedFailure := make(map[string]struct{}, len(failed))
		for _, conn := range touched {
			if err, ok := failed[conn.UID]; ok {
				requeue = append(requeue, conn)
				if _, logged := loggedFailure[conn.UID]; !logged {
					loggedFailure[conn.UID] = struct{}{}
					w.logger().Warn("presence touch route target resolve failed",
						wklog.Event("internal.app.presence_touch_resolve_failed"),
						wklog.UID(conn.UID),
						wklog.Int("hashSlot", int(conn.HashSlot)),
						wklog.SessionID(conn.SessionID),
						wklog.Error(err),
					)
				}
				continue
			}
			target := resolved[conn.UID]
			index, ok := groupIndex[target]
			if !ok {
				index = len(groups)
				groupIndex[target] = index
				groups = append(groups, presenceTouchRouteGroup{target: target})
			}
			groups[index].ownerRoutes = append(groups[index].ownerRoutes, conn)
			groups[index].routes = append(groups[index].routes, routeFromOwnerRoute(conn))
		}

		for i, group := range groups {
			if ctx.Err() != nil {
				for _, unsent := range groups[i:] {
					requeue = append(requeue, unsent.ownerRoutes...)
				}
				return
			}
			if err := w.opts.Authority.TouchRoutesTo(ctx, group.target, group.routes); err != nil {
				requeue = append(requeue, group.ownerRoutes...)
				w.logger().Warn("presence touch flush failed",
					wklog.Event("internal.app.presence_touch_failed"),
					wklog.Int("hashSlot", int(group.target.HashSlot)),
					wklog.Uint64("slotID", uint64(group.target.SlotID)),
					wklog.LeaderNodeID(group.target.LeaderNodeID),
					wklog.Uint64("routeRevision", group.target.RouteRevision),
					wklog.Uint64("authorityEpoch", group.target.AuthorityEpoch),
					wklog.Int("routes", len(group.routes)),
					wklog.Error(err),
				)
			}
		}
		if len(touched) < request {
			return
		}
	}
}

func (w *presenceTouchWorker) logger() wklog.Logger {
	if w == nil || w.opts.Logger == nil {
		return wklog.NewNop()
	}
	return w.opts.Logger
}

type presenceTouchRouteGroup struct {
	target      presence.RouteTarget
	routes      []presence.Route
	ownerRoutes []online.OwnerRoute
}

func uniqueOwnerRouteUIDs(routes []online.OwnerRoute) []string {
	seen := make(map[string]struct{}, len(routes))
	uids := make([]string, 0, len(routes))
	for _, route := range routes {
		if _, ok := seen[route.UID]; ok {
			continue
		}
		seen[route.UID] = struct{}{}
		uids = append(uids, route.UID)
	}
	return uids
}

func routeFromOwnerRoute(conn online.OwnerRoute) presence.Route {
	return presence.Route{
		UID:           conn.UID,
		OwnerNodeID:   conn.OwnerNodeID,
		OwnerBootID:   conn.OwnerBootID,
		OwnerSeq:      conn.OwnerSeq,
		SessionID:     conn.SessionID,
		DeviceID:      conn.DeviceID,
		DeviceFlag:    conn.DeviceFlag,
		DeviceLevel:   conn.DeviceLevel,
		Listener:      conn.Listener,
		ConnectedUnix: conn.ConnectedUnix,
		LastSeenUnix:  conn.LastActivityUnix,
	}
}

func ownerRouteFromRoute(route presence.Route) online.OwnerRoute {
	return online.OwnerRoute{
		UID:              route.UID,
		OwnerNodeID:      route.OwnerNodeID,
		OwnerBootID:      route.OwnerBootID,
		OwnerSeq:         route.OwnerSeq,
		SessionID:        route.SessionID,
		DeviceID:         route.DeviceID,
		DeviceFlag:       route.DeviceFlag,
		DeviceLevel:      route.DeviceLevel,
		Listener:         route.Listener,
		ConnectedUnix:    route.ConnectedUnix,
		LastActivityUnix: route.LastSeenUnix,
	}
}
