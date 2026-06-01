package app

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

// presenceLocalRegistry pages and mutates owner-local active routes by hash slot.
type presenceLocalRegistry interface {
	VisitActiveByHashSlot(uint16, online.RouteCursor, int, func(online.OnlineConn) bool) (online.RouteCursor, bool)
	Connection(sessionID uint64) (online.OnlineConn, bool)
	MarkClosingAndUnregister(sessionID uint64) (online.OnlineConn, bool)
}

// presenceAuthorityDirectory installs and clears locally owned authority epochs.
type presenceAuthorityDirectory interface {
	BecomeAuthority(presence.RouteTarget)
	LoseAuthority(uint16)
}

// presenceRehydrateAuthority replays owner routes into an authority epoch.
type presenceRehydrateAuthority interface {
	RehydrateRoutesTo(context.Context, presence.RouteTarget, []presence.Route) ([]presence.RehydrateResult, error)
	CommitRehydratedRoute(context.Context, presence.RouteTarget, string) error
	AbortRehydratedRoute(context.Context, presence.RouteTarget, string) error
}

// presenceRouteActioner applies route conflict actions on owner nodes.
type presenceRouteActioner interface {
	ApplyRouteAction(context.Context, presence.RouteAction) error
}

type presenceRehydrateWorkerOptions struct {
	// NodeID identifies the local node so the worker can distinguish local leadership.
	NodeID uint64
	// Events provides an already-created route authority stream for tests.
	Events <-chan clusterv2.RouteAuthorityEvent
	// Watch creates the route authority stream during Start.
	Watch func() <-chan clusterv2.RouteAuthorityEvent
	// Initial returns the currently visible authorities to seed local state after startup.
	Initial func() []clusterv2.RouteAuthority
	// Local stores owner-local active sessions to replay.
	Local presenceLocalRegistry
	// Authority receives rehydrated routes for locally led hash slots.
	Authority presenceRehydrateAuthority
	// Actions applies conflict actions returned by the authority.
	Actions presenceRouteActioner
	// Directory installs or clears local authority state as leadership changes.
	Directory presenceAuthorityDirectory
	// BatchSize bounds routes sent in one rehydrate call.
	BatchSize int
	// MaxInflightPerTarget is accepted only as 1; this worker keeps one task per hash slot.
	MaxInflightPerTarget int
}

// presenceRehydrateWorker follows route authority changes and replays local active routes.
type presenceRehydrateWorker struct {
	opts presenceRehydrateWorkerOptions

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
	slots  map[uint16]presenceSlotWork
	latest map[uint16]presence.RouteTarget
}

type presenceSlotWork struct {
	// target is the authority epoch this work belongs to.
	target presence.RouteTarget
	// cancel stops older epoch work when a newer authority event arrives.
	cancel context.CancelFunc
}

func newPresenceRehydrateWorker(opts presenceRehydrateWorkerOptions) *presenceRehydrateWorker {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 512
	}
	if opts.MaxInflightPerTarget <= 0 {
		opts.MaxInflightPerTarget = 1
	}
	return &presenceRehydrateWorker{
		opts:   opts,
		slots:  make(map[uint16]presenceSlotWork),
		latest: make(map[uint16]presence.RouteTarget),
	}
}

// Start begins watching route authority changes.
func (w *presenceRehydrateWorker) Start(ctx context.Context) error {
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
	w.mu.Unlock()

	w.wg.Add(1)
	go w.watch(runCtx, events)
	for _, authority := range w.initialAuthorities() {
		w.handleAuthority(runCtx, authority)
	}
	return nil
}

// Stop cancels authority watching and any in-flight hash-slot rehydrate work.
func (w *presenceRehydrateWorker) Stop(context.Context) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	cancel := w.cancel
	w.cancel = nil
	for hashSlot, work := range w.slots {
		work.cancel()
		delete(w.slots, hashSlot)
	}
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	w.wg.Wait()
	return nil
}

func (w *presenceRehydrateWorker) watch(ctx context.Context, events <-chan clusterv2.RouteAuthorityEvent) {
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
				w.handleAuthority(ctx, authority)
			}
		}
	}
}

func (w *presenceRehydrateWorker) initialAuthorities() []clusterv2.RouteAuthority {
	if w.opts.Initial == nil {
		return nil
	}
	return w.opts.Initial()
}

func (w *presenceRehydrateWorker) handleAuthority(ctx context.Context, authority clusterv2.RouteAuthority) {
	target := presence.RouteTarget{
		HashSlot:       authority.HashSlot,
		SlotID:         authority.SlotID,
		LeaderNodeID:   authority.LeaderNodeID,
		RouteRevision:  authority.RouteRevision,
		AuthorityEpoch: authority.AuthorityEpoch,
	}
	if authority.LeaderNodeID == 0 {
		if !w.acceptAuthority(target) {
			return
		}
		if w.opts.Directory != nil {
			w.opts.Directory.LoseAuthority(authority.HashSlot)
		}
		w.cancelHashSlot(authority.HashSlot)
		return
	}
	if !w.acceptAuthority(target) {
		return
	}
	if w.opts.Directory != nil && authority.LeaderNodeID == w.opts.NodeID {
		w.opts.Directory.BecomeAuthority(target)
	}
	if w.opts.Directory != nil && authority.LeaderNodeID != w.opts.NodeID {
		w.opts.Directory.LoseAuthority(authority.HashSlot)
	}
	w.startHashSlot(ctx, target)
}

func (w *presenceRehydrateWorker) startHashSlot(ctx context.Context, target presence.RouteTarget) {
	if w.opts.Local == nil || w.opts.Authority == nil {
		return
	}
	workCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	if w.cancel == nil {
		w.mu.Unlock()
		cancel()
		return
	}
	if existing, ok := w.slots[target.HashSlot]; ok {
		existing.cancel()
	}
	w.slots[target.HashSlot] = presenceSlotWork{target: target, cancel: cancel}
	w.wg.Add(1)
	w.mu.Unlock()

	go func() {
		defer w.wg.Done()
		defer w.finishHashSlot(target)
		w.rehydrateHashSlot(workCtx, target)
	}()
}

func (w *presenceRehydrateWorker) cancelHashSlot(hashSlot uint16) {
	w.mu.Lock()
	if existing, ok := w.slots[hashSlot]; ok {
		existing.cancel()
		delete(w.slots, hashSlot)
	}
	w.mu.Unlock()
}

func (w *presenceRehydrateWorker) finishHashSlot(target presence.RouteTarget) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if existing, ok := w.slots[target.HashSlot]; ok && existing.target == target {
		delete(w.slots, target.HashSlot)
	}
}

func (w *presenceRehydrateWorker) acceptAuthority(target presence.RouteTarget) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	current, ok := w.latest[target.HashSlot]
	if ok && !authorityTargetNewer(target, current) {
		return false
	}
	w.latest[target.HashSlot] = target
	return true
}

func authorityTargetNewer(next, current presence.RouteTarget) bool {
	if next.AuthorityEpoch != current.AuthorityEpoch {
		return next.AuthorityEpoch > current.AuthorityEpoch
	}
	if next.RouteRevision != current.RouteRevision {
		return next.RouteRevision > current.RouteRevision
	}
	return next != current
}

func (w *presenceRehydrateWorker) rehydrateHashSlot(ctx context.Context, target presence.RouteTarget) {
	cursor := online.RouteCursor{}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		routes := make([]presence.Route, 0, w.opts.BatchSize)
		next, more := w.opts.Local.VisitActiveByHashSlot(target.HashSlot, cursor, w.opts.BatchSize, func(conn online.OnlineConn) bool {
			routes = append(routes, routeFromOnlineConn(conn))
			return true
		})
		if len(routes) > 0 {
			results, err := w.opts.Authority.RehydrateRoutesTo(ctx, target, routes)
			if err != nil {
				return
			}
			if err := w.applyRehydrateResults(ctx, target, results); err != nil {
				return
			}
		}
		cursor = next
		if !more {
			return
		}
	}
}

func (w *presenceRehydrateWorker) applyRehydrateResults(ctx context.Context, target presence.RouteTarget, results []presence.RehydrateResult) error {
	for _, result := range results {
		if result.Error != "" {
			continue
		}
		if err := w.applyRouteActions(ctx, result.Actions); err != nil {
			if result.PendingToken != "" && w.opts.Authority != nil {
				_ = w.opts.Authority.AbortRehydratedRoute(ctx, target, string(result.PendingToken))
			}
			return err
		}
		if result.PendingToken != "" && w.opts.Authority != nil {
			if err := w.opts.Authority.CommitRehydratedRoute(ctx, target, string(result.PendingToken)); err != nil {
				_ = w.opts.Authority.AbortRehydratedRoute(ctx, target, string(result.PendingToken))
				return err
			}
		}
	}
	return nil
}

func (w *presenceRehydrateWorker) applyRouteActions(ctx context.Context, actions []presence.RouteAction) error {
	if len(actions) == 0 {
		return nil
	}
	if w.opts.Actions == nil {
		return authoritypresence.ErrRouteNotReady
	}
	for _, action := range actions {
		if err := w.opts.Actions.ApplyRouteAction(ctx, action); err != nil {
			return err
		}
	}
	return nil
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
	}
}
