package app

import (
	"context"
	"sync"
	"time"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

type conversationAuthorityRouteLifecycleOptions struct {
	// LocalAuthority receives active/warming marks for targets served by this node.
	LocalAuthority *conversationAuthority
	// LocalNodeID identifies this node when applying route-authority events.
	LocalNodeID uint64
	// Initial returns the currently known route authorities when the lifecycle starts.
	Initial func() []cluster.RouteAuthority
	// Watch creates the route-authority event stream when the lifecycle starts.
	Watch func() <-chan cluster.RouteAuthorityEvent
	// HandoffTimeout bounds local authority drain during route-authority changes.
	HandoffTimeout time.Duration
	// ReconcileInterval controls private pull repair of missed route-authority events.
	ReconcileInterval time.Duration
}

type conversationAuthorityRouteLifecycle struct {
	localAuthority *conversationAuthority
	localNodeID    uint64
	initial        func() []cluster.RouteAuthority
	watch          func() <-chan cluster.RouteAuthorityEvent
	handoffTimeout time.Duration
	reconcileEvery time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
	latest map[uint16]conversationusecase.RouteTarget
}

func newConversationAuthorityRouteLifecycle(opts conversationAuthorityRouteLifecycleOptions) *conversationAuthorityRouteLifecycle {
	if opts.HandoffTimeout <= 0 {
		opts.HandoffTimeout = 3 * time.Second
	}
	if opts.ReconcileInterval <= 0 {
		opts.ReconcileInterval = 5 * time.Second
	}
	return &conversationAuthorityRouteLifecycle{
		localAuthority: opts.LocalAuthority,
		localNodeID:    opts.LocalNodeID,
		initial:        opts.Initial,
		watch:          opts.Watch,
		handoffTimeout: opts.HandoffTimeout,
		reconcileEvery: opts.ReconcileInterval,
		latest:         make(map[uint16]conversationusecase.RouteTarget),
	}
}

func (l *conversationAuthorityRouteLifecycle) Start(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	if l.cancel != nil {
		l.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	var events <-chan cluster.RouteAuthorityEvent
	if l.watch != nil {
		events = l.watch()
	}
	l.cancel = cancel
	if l.latest == nil {
		l.latest = make(map[uint16]conversationusecase.RouteTarget)
	}
	if events != nil {
		l.wg.Add(1)
		go l.watchRouteAuthorities(runCtx, events)
	}
	if l.initial != nil {
		l.wg.Add(1)
		go l.reconcileRouteAuthorities(runCtx)
	}
	l.mu.Unlock()
	l.applyRouteAuthorities(runCtx, l.initialAuthorities())
	return nil
}

func (l *conversationAuthorityRouteLifecycle) Stop(context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	cancel := l.cancel
	l.cancel = nil
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	l.wg.Wait()
	return nil
}

func (l *conversationAuthorityRouteLifecycle) initialAuthorities() []cluster.RouteAuthority {
	if l == nil || l.initial == nil {
		return nil
	}
	return l.initial()
}

func (l *conversationAuthorityRouteLifecycle) watchRouteAuthorities(ctx context.Context, events <-chan cluster.RouteAuthorityEvent) {
	defer l.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			for _, authority := range event.Authorities {
				l.handleRouteAuthority(ctx, authority)
			}
		}
	}
}

func (l *conversationAuthorityRouteLifecycle) reconcileRouteAuthorities(ctx context.Context) {
	defer l.wg.Done()
	ticker := time.NewTicker(l.reconcileEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.applyRouteAuthorities(ctx, l.initialAuthorities())
		}
	}
}

func (l *conversationAuthorityRouteLifecycle) applyRouteAuthorities(ctx context.Context, authorities []cluster.RouteAuthority) {
	if l == nil || l.localAuthority == nil {
		return
	}
	for _, authority := range authorities {
		l.handleRouteAuthority(ctx, authority)
	}
}

func (l *conversationAuthorityRouteLifecycle) handleRouteAuthority(ctx context.Context, authority cluster.RouteAuthority) {
	if l == nil || l.localAuthority == nil {
		return
	}
	target := conversationRouteTarget(authority)
	previous, hadPrevious, accepted := l.acceptRouteAuthorityTarget(target)
	if !accepted {
		return
	}
	switch {
	case target.LeaderNodeID == l.localNodeID:
		l.localAuthority.markActive(target)
	case target.LeaderNodeID == 0:
		if hadPrevious && l.localAuthorityCapable(previous) {
			l.startAuthorityDrain(ctx, previous)
		}
		l.localAuthority.markWarming(target)
	default:
		if hadPrevious && l.localAuthorityCapable(previous) {
			l.startAuthorityDrain(ctx, previous)
		}
	}
}

func (l *conversationAuthorityRouteLifecycle) acceptRouteAuthorityTarget(target conversationusecase.RouteTarget) (conversationusecase.RouteTarget, bool, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.latest == nil {
		l.latest = make(map[uint16]conversationusecase.RouteTarget)
	}
	current, ok := l.latest[target.HashSlot]
	if ok && !conversationAuthorityRouteTargetNewer(target, current) {
		return current, true, false
	}
	l.latest[target.HashSlot] = target
	return current, ok, true
}

func (l *conversationAuthorityRouteLifecycle) localAuthorityCapable(target conversationusecase.RouteTarget) bool {
	return target.LeaderNodeID == 0 || target.LeaderNodeID == l.localNodeID
}

func (l *conversationAuthorityRouteLifecycle) startAuthorityDrain(ctx context.Context, target conversationusecase.RouteTarget) {
	if l == nil || l.localAuthority == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	drainCtx := ctx
	cancel := func() {}
	if l.handoffTimeout > 0 {
		drainCtx, cancel = context.WithTimeout(ctx, l.handoffTimeout)
	}
	result, err := l.localAuthority.beginDrainAuthority(target)
	if err != nil {
		cancel()
		l.localAuthority.observeHandoff(result, err)
		return
	}
	l.mu.Lock()
	if l.cancel == nil {
		l.mu.Unlock()
		cancel()
		return
	}
	l.wg.Add(1)
	l.mu.Unlock()
	go func() {
		defer l.wg.Done()
		defer cancel()
		l.drainAuthorityTarget(drainCtx, target)
	}()
}

func (l *conversationAuthorityRouteLifecycle) drainAuthorityTarget(ctx context.Context, target conversationusecase.RouteTarget) {
	if l == nil || l.localAuthority == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, _ = l.localAuthority.finishDrainingAuthority(ctx, target)
}

func conversationAuthorityRouteTargetNewer(next, current conversationusecase.RouteTarget) bool {
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

func conversationRouteTarget(authority cluster.RouteAuthority) conversationusecase.RouteTarget {
	return conversationusecase.RouteTarget{
		HashSlot:       authority.HashSlot,
		SlotID:         authority.SlotID,
		LeaderNodeID:   authority.LeaderNodeID,
		LeaderTerm:     authority.LeaderTerm,
		ConfigEpoch:    authority.ConfigEpoch,
		RouteRevision:  authority.RouteRevision,
		AuthorityEpoch: authority.AuthorityEpoch,
	}
}
