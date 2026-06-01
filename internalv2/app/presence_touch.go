package app

import (
	"context"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

// presenceTouchLocalRegistry drains owner-local routes with pending activity updates.
type presenceTouchLocalRegistry interface {
	DrainTouched(limit int) []online.OnlineConn
	RequeueTouched([]online.OnlineConn)
}

// presenceOwnerLocalRegistry mutates owner-local real gateway sessions.
type presenceOwnerLocalRegistry interface {
	Connection(sessionID uint64) (online.OnlineConn, bool)
	MarkClosingAndUnregister(sessionID uint64) (online.OnlineConn, bool)
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
	for _, authority := range w.initialAuthorities() {
		w.handleAuthority(authority)
	}
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

func (w *presenceTouchWorker) handleAuthority(authority clusterv2.RouteAuthority) {
	if w.opts.Directory == nil {
		return
	}
	target := presence.RouteTarget{
		HashSlot:       authority.HashSlot,
		SlotID:         authority.SlotID,
		LeaderNodeID:   authority.LeaderNodeID,
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
	if next.AuthorityEpoch != current.AuthorityEpoch {
		if next.LeaderNodeID == 0 || current.LeaderNodeID == 0 {
			return true
		}
		return next.AuthorityEpoch > current.AuthorityEpoch
	}
	return next != current
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
			w.opts.Local.RequeueTouched([]online.OnlineConn{conn})
			continue
		}
		groups[target] = append(groups[target], routeFromOnlineConn(conn))
	}
	for target, routes := range groups {
		if err := ctx.Err(); err != nil {
			w.requeueRouteGroups(groups)
			return
		}
		if err := w.opts.Authority.TouchRoutesTo(ctx, target, routes); err != nil {
			w.opts.Local.RequeueTouched(onlineConnsFromRoutes(routes))
		}
		delete(groups, target)
	}
}

func (w *presenceTouchWorker) requeueRouteGroups(groups map[presence.RouteTarget][]presence.Route) {
	if w.opts.Local == nil {
		return
	}
	for _, routes := range groups {
		w.opts.Local.RequeueTouched(onlineConnsFromRoutes(routes))
	}
}

func routeFromOnlineConn(conn online.OnlineConn) presence.Route {
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

func onlineConnsFromRoutes(routes []presence.Route) []online.OnlineConn {
	conns := make([]online.OnlineConn, 0, len(routes))
	for _, route := range routes {
		conns = append(conns, onlineConnFromRoute(route))
	}
	return conns
}

func onlineConnFromRoute(route presence.Route) online.OnlineConn {
	return online.OnlineConn{
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
