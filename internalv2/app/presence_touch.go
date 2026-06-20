package app

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
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
	ResolveRouteTarget(uid string) (presence.RouteTarget, error)
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
	Events <-chan clusterv2.RouteAuthorityEvent
	// Watch creates the route authority stream during Start.
	Watch func() <-chan clusterv2.RouteAuthorityEvent
	// Initial returns visible authorities to seed local state after startup.
	Initial func() []clusterv2.RouteAuthority
	// Local stores owner-local active sessions with pending touch updates.
	Local presenceTouchLocalRegistry
	// Authority sends grouped touches to the observed authority target.
	Authority presenceTouchAuthority
	// Directory installs or clears local authority state and expires stale routes.
	Directory presenceTouchDirectory
	// FlushInterval controls periodic dirty route flushes.
	FlushInterval time.Duration
	// BatchSize bounds routes drained in one flush.
	BatchSize int
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
	events := w.opts.Events
	if events == nil && w.opts.Watch != nil {
		events = w.opts.Watch()
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	if w.cancel != nil {
		w.mu.Unlock()
		cancel()
		return nil
	}
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

func (w *presenceTouchWorker) watch(ctx context.Context, events <-chan clusterv2.RouteAuthorityEvent) {
	defer w.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				if ctx.Err() == nil {
					w.logger().Warn("presence authority watch closed",
						wklog.Event("internalv2.app.presence_authority_watch_closed"),
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

func (w *presenceTouchWorker) initialAuthorities() []clusterv2.RouteAuthority {
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

func (w *presenceTouchWorker) handleAuthority(authority clusterv2.RouteAuthority) {
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
	touched := w.opts.Local.DrainTouched(w.opts.BatchSize)
	if len(touched) == 0 {
		return
	}
	groups := make(map[presence.RouteTarget][]presence.Route)
	for _, conn := range touched {
		target, err := w.opts.Authority.ResolveRouteTarget(conn.UID)
		if err != nil {
			w.opts.Local.RequeueTouched([]online.OwnerRoute{conn})
			w.logger().Warn("presence touch route target resolve failed",
				wklog.Event("internalv2.app.presence_touch_resolve_failed"),
				wklog.UID(conn.UID),
				wklog.Int("hashSlot", int(conn.HashSlot)),
				wklog.SessionID(conn.SessionID),
				wklog.Error(err),
			)
			continue
		}
		groups[target] = append(groups[target], routeFromOwnerRoute(conn))
	}
	for target, routes := range groups {
		if err := ctx.Err(); err != nil {
			w.requeueRouteGroups(groups)
			return
		}
		if err := w.opts.Authority.TouchRoutesTo(ctx, target, routes); err != nil {
			w.opts.Local.RequeueTouched(ownerRoutesFromRoutes(routes))
			w.logger().Warn("presence touch flush failed",
				wklog.Event("internalv2.app.presence_touch_failed"),
				wklog.Int("hashSlot", int(target.HashSlot)),
				wklog.Uint64("slotID", uint64(target.SlotID)),
				wklog.LeaderNodeID(target.LeaderNodeID),
				wklog.Uint64("routeRevision", target.RouteRevision),
				wklog.Uint64("authorityEpoch", target.AuthorityEpoch),
				wklog.Int("routes", len(routes)),
				wklog.Error(err),
			)
		}
		delete(groups, target)
	}
}

func (w *presenceTouchWorker) logger() wklog.Logger {
	if w == nil || w.opts.Logger == nil {
		return wklog.NewNop()
	}
	return w.opts.Logger
}

func (w *presenceTouchWorker) requeueRouteGroups(groups map[presence.RouteTarget][]presence.Route) {
	if w.opts.Local == nil {
		return
	}
	for _, routes := range groups {
		w.opts.Local.RequeueTouched(ownerRoutesFromRoutes(routes))
	}
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

func ownerRoutesFromRoutes(routes []presence.Route) []online.OwnerRoute {
	conns := make([]online.OwnerRoute, 0, len(routes))
	for _, route := range routes {
		conns = append(conns, ownerRouteFromRoute(route))
	}
	return conns
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
