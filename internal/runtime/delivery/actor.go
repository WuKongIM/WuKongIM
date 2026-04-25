package delivery

import (
	"context"
	"sync"
	"time"
)

const recentCompletedMessageCap = 256

type actor struct {
	mu              sync.Mutex
	shard           *shard
	key             ChannelKey
	lane            ActorLane
	activityCount   int
	nextDispatchSeq uint64
	inflight        map[uint64]*InflightMessage
	completed       map[uint64]struct{}
	completedOrder  []uint64
	lastActive      int64
}

func newActor(shard *shard, key ChannelKey) *actor {
	return &actor{
		shard:      shard,
		key:        key,
		inflight:   make(map[uint64]*InflightMessage),
		completed:  make(map[uint64]struct{}),
		lastActive: shard.manager.clock.Now().UnixNano(),
	}
}

func (a *actor) handleStartDispatch(ctx context.Context, env CommittedEnvelope) error {
	a.markActivity()
	a.touch()
	if a.hasSeenMessage(env.MessageID) {
		return nil
	}
	if a.nextDispatchSeq == 0 {
		a.nextDispatchSeq = env.MessageSeq
	}
	switch {
	case env.MessageSeq < a.nextDispatchSeq:
		return a.dispatchLate(ctx, env)
	case env.MessageSeq > a.nextDispatchSeq:
		return a.dispatchObserved(ctx, env)
	default:
	}
	return a.dispatchObserved(ctx, env)
}

func (a *actor) handleRouteAck(ctx context.Context, event RouteAcked) error {
	a.touch()
	msg := a.inflight[event.MessageID]
	if msg == nil {
		return nil
	}
	if err := a.finishRoute(ctx, msg, event.Route); err != nil {
		return err
	}
	return a.resumeResolvable(ctx)
}

func (a *actor) handleRouteOffline(ctx context.Context, event RouteOffline) error {
	a.touch()
	msg := a.inflight[event.MessageID]
	if msg == nil {
		return nil
	}
	if err := a.finishRoute(ctx, msg, event.Route); err != nil {
		return err
	}
	return a.resumeResolvable(ctx)
}

func (a *actor) handleRetryTick(ctx context.Context, event RetryTick) error {
	a.touch()
	msg := a.inflight[event.Entry.MessageID]
	if msg == nil {
		return nil
	}
	state, ok := msg.Routes[event.Entry.Route]
	if !ok {
		return nil
	}
	if event.Entry.Attempt <= state.Attempt {
		return nil
	}
	if err := a.applyPush(ctx, msg, []RouteKey{event.Entry.Route}, event.Entry.Attempt); err != nil {
		return err
	}
	return a.resumeResolvable(ctx)
}

func (a *actor) dispatch(ctx context.Context, env CommittedEnvelope) error {
	msg := &InflightMessage{
		MessageID:      env.MessageID,
		MessageSeq:     env.MessageSeq,
		Envelope:       cloneEnvelope(env),
		ResolveAttempt: 1,
		Routes:         make(map[RouteKey]*RouteDeliveryState),
	}
	a.inflight[env.MessageID] = msg
	return a.resumeResolvable(ctx)
}

func (a *actor) dispatchObserved(ctx context.Context, env CommittedEnvelope) error {
	if err := a.dispatch(ctx, env); err != nil {
		return err
	}
	a.nextDispatchSeq = env.MessageSeq + 1
	return nil
}

func (a *actor) resolvePages(ctx context.Context, msg *InflightMessage) error {
	for !msg.ResolveDone {
		remaining := a.routeBudgetRemaining()
		if remaining <= 0 {
			return nil
		}
		pageLimit := a.shard.manager.resolvePageSize
		if pageLimit <= 0 || pageLimit > remaining {
			pageLimit = remaining
		}

		routes, nextCursor, done, err := a.shard.manager.resolver.ResolvePage(ctx, msg.ResolveToken, msg.NextCursor, pageLimit)
		if err != nil {
			return err
		}
		if len(routes) > 0 {
			if err := a.applyPush(ctx, msg, routes, 1); err != nil {
				return err
			}
		}
		msg.NextCursor = nextCursor
		msg.ResolveDone = done
		if !done && len(routes) == 0 && nextCursor == "" {
			msg.ResolveDone = true
		}
	}
	if msg.PendingRouteCnt == 0 {
		a.completeMessage(msg.MessageID)
	}
	return nil
}

func (a *actor) resumeResolvable(ctx context.Context) error {
	for {
		msg := a.nextResolvableMessage(a.shard.manager.clock.Now())
		if msg == nil {
			return nil
		}
		progressed, err := a.resumeMessage(ctx, msg)
		if err != nil {
			return err
		}
		if !progressed {
			return nil
		}
	}
}

func (a *actor) nextResolvableMessage(now time.Time) *InflightMessage {
	var next *InflightMessage
	for _, msg := range a.inflight {
		if msg.ResolveDone {
			continue
		}
		if next == nil || msg.MessageSeq < next.MessageSeq {
			next = msg
		}
	}
	if next == nil {
		return nil
	}
	if !next.ResolveRetryAt.IsZero() && next.ResolveRetryAt.After(now) {
		return nil
	}
	return next
}

func (a *actor) hasDueResolveRetry(now time.Time) bool {
	msg := a.nextResolvableMessage(now)
	return msg != nil && !msg.ResolveRetryAt.IsZero() && !msg.ResolveRetryAt.After(now)
}

func (a *actor) resumeMessage(ctx context.Context, msg *InflightMessage) (bool, error) {
	beforeBegun := msg.ResolveBegun
	beforeDone := msg.ResolveDone
	beforeCursor := msg.NextCursor
	beforePending := msg.PendingRouteCnt

	if !msg.ResolveBegun {
		token, err := a.shard.manager.resolver.BeginResolve(ctx, a.key, msg.Envelope)
		if err != nil {
			return a.handleResolveFailure(ctx, msg)
		}
		msg.ResolveToken = token
		msg.ResolveBegun = true
	}
	msg.ResolveRetryAt = time.Time{}

	if err := a.resolvePages(ctx, msg); err != nil {
		return a.handleResolveFailure(ctx, msg)
	}

	progressed := !beforeBegun && msg.ResolveBegun
	progressed = progressed || beforeDone != msg.ResolveDone
	progressed = progressed || beforeCursor != msg.NextCursor
	progressed = progressed || beforePending != msg.PendingRouteCnt
	return progressed, nil
}

func (a *actor) handleResolveFailure(_ context.Context, msg *InflightMessage) (bool, error) {
	nextAttempt := msg.ResolveAttempt + 1
	delay, ok := a.shard.nextRetryDelay(nextAttempt)
	if !ok {
		msg.ResolveDone = true
		msg.ResolveRetryAt = time.Time{}
		if msg.PendingRouteCnt == 0 {
			a.completeMessage(msg.MessageID)
		}
		return true, nil
	}
	msg.ResolveAttempt = nextAttempt
	msg.ResolveRetryAt = a.shard.manager.clock.Now().Add(delay)
	return false, nil
}

func (a *actor) dispatchLate(ctx context.Context, env CommittedEnvelope) error {
	// preferLocal routing allows each node to observe only a sparse subset of the
	// global channel sequence, so a lower seq can legitimately arrive after a
	// higher locally observed one. Deliver it late instead of stalling forever.
	return a.dispatch(ctx, env)
}

func (a *actor) applyPush(ctx context.Context, msg *InflightMessage, routes []RouteKey, attempt int) error {
	result, err := a.shard.manager.push.Push(ctx, PushCommand{
		Envelope: cloneEnvelope(msg.Envelope),
		Routes:   append([]RouteKey(nil), routes...),
		Attempt:  attempt,
	})
	if err != nil {
		result = PushResult{Retryable: append([]RouteKey(nil), routes...)}
	}
	for _, route := range result.Accepted {
		state := a.ensureRouteState(msg, route)
		state.Attempt = attempt
		state.Accepted = true
		a.shard.manager.ackIdx.Bind(AckBinding{
			SessionID:   route.SessionID,
			MessageID:   msg.MessageID,
			ChannelID:   a.key.ChannelID,
			ChannelType: a.key.ChannelType,
			Route:       route,
		})
		a.scheduleRetry(msg, route, attempt)
	}
	for _, route := range result.Retryable {
		state := a.ensureRouteState(msg, route)
		state.Attempt = attempt
		a.scheduleRetry(msg, route, attempt)
	}
	for _, route := range result.Dropped {
		if err := a.finishRoute(ctx, msg, route); err != nil {
			return err
		}
	}
	return nil
}

func (a *actor) ensureRouteState(msg *InflightMessage, route RouteKey) *RouteDeliveryState {
	state := msg.Routes[route]
	if state != nil {
		return state
	}
	state = &RouteDeliveryState{}
	msg.Routes[route] = state
	msg.PendingRouteCnt++
	return state
}

func (a *actor) finishRoute(_ context.Context, msg *InflightMessage, route RouteKey) error {
	if _, ok := msg.Routes[route]; !ok {
		return nil
	}
	delete(msg.Routes, route)
	if msg.PendingRouteCnt > 0 {
		msg.PendingRouteCnt--
	}
	a.shard.manager.ackIdx.Remove(route.SessionID, msg.MessageID)
	if msg.PendingRouteCnt == 0 && msg.ResolveDone {
		a.completeMessage(msg.MessageID)
	}
	return nil
}

func (a *actor) scheduleRetry(msg *InflightMessage, route RouteKey, attempt int) {
	delay, ok := a.shard.nextRetryDelay(attempt + 1)
	if !ok {
		return
	}
	a.shard.wheel.Schedule(RetryEntry{
		When:        a.shard.manager.clock.Now().Add(delay),
		ChannelID:   a.key.ChannelID,
		ChannelType: a.key.ChannelType,
		MessageID:   msg.MessageID,
		Route:       route,
		Attempt:     attempt + 1,
	})
}

func (a *actor) completeMessage(messageID uint64) {
	a.rememberCompleted(messageID)
	delete(a.inflight, messageID)
}

func (a *actor) isIdle(nowUnixNano int64, idleTimeout int64) bool {
	if len(a.inflight) > 0 {
		return false
	}
	return nowUnixNano-a.lastActive >= idleTimeout
}

func (a *actor) touch() {
	a.lastActive = a.shard.manager.clock.Now().UnixNano()
}

func (a *actor) routeBudgetRemaining() int {
	limit := a.shard.manager.limits.MaxInflightRoutesPerActor
	if limit <= 0 {
		return int(^uint(0) >> 1)
	}
	return limit - a.inflightRouteCount()
}

func (a *actor) inflightRouteCount() int {
	total := 0
	for _, msg := range a.inflight {
		total += msg.PendingRouteCnt
	}
	return total
}

func (a *actor) markActivity() {
	a.activityCount++
	if a.lane == LaneDedicated {
		return
	}
	threshold := a.shard.manager.limits.DedicatedLaneActivityThreshold
	if threshold > 0 && a.activityCount >= threshold {
		a.lane = LaneDedicated
	}
}

func (a *actor) hasSeenMessage(messageID uint64) bool {
	if _, ok := a.inflight[messageID]; ok {
		return true
	}
	_, ok := a.completed[messageID]
	return ok
}

func (a *actor) rememberCompleted(messageID uint64) {
	if messageID == 0 {
		return
	}
	if _, ok := a.completed[messageID]; ok {
		return
	}
	a.completed[messageID] = struct{}{}
	a.completedOrder = append(a.completedOrder, messageID)
	if len(a.completedOrder) <= recentCompletedMessageCap {
		return
	}
	evict := a.completedOrder[0]
	a.completedOrder = append([]uint64(nil), a.completedOrder[1:]...)
	delete(a.completed, evict)
}

func cloneEnvelope(env CommittedEnvelope) CommittedEnvelope {
	copied := env
	copied.Payload = append([]byte(nil), env.Payload...)
	return copied
}
