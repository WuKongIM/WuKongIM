package delivery

import (
	"context"
	"sync"
	"time"
)

const recentCompletedMessageCap = 256

// routeStateMapPool reuses per-message route maps after an inflight message completes.
var routeStateMapPool = sync.Pool{
	New: func() any {
		return make(map[RouteKey]RouteDeliveryState, 4)
	},
}

type actor struct {
	mu              sync.Mutex
	shard           *shard
	key             ChannelKey
	lane            ActorLane
	activityCount   int
	nextDispatchSeq uint64
	inflight        map[uint64]*InflightMessage
	resolvable      []resolvableMessageRef
	pendingRouteCnt int
	completed       map[uint64]struct{}
	completedOrder  []uint64
	expiredEvents   []RouteExpiredEvent
	offlineEvents   []OfflineResolvedEvent
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

func (a *actor) handleRetryEntry(ctx context.Context, entry RetryEntry) error {
	switch entry.Kind {
	case RetryEntryResolve:
		return a.handleResolveRetryTick(ctx, entry)
	default:
		return a.handleRetryTick(ctx, RetryTick{Entry: entry})
	}
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

func (a *actor) handleResolveRetryTick(ctx context.Context, entry RetryEntry) error {
	a.touch()
	msg := a.inflight[entry.MessageID]
	if msg == nil || msg.ResolveDone {
		return nil
	}
	if entry.Attempt != msg.ResolveAttempt {
		return nil
	}
	if !msg.ResolveRetryAt.IsZero() && msg.ResolveRetryAt.After(a.shard.manager.clock.Now()) {
		return nil
	}
	return a.resumeResolvable(ctx)
}

func (a *actor) dispatch(ctx context.Context, env CommittedEnvelope) error {
	msg := &InflightMessage{
		MessageID:      env.MessageID,
		MessageSeq:     env.MessageSeq,
		Envelope:       cloneEnvelope(env),
		ResolveAttempt: 1,
		Routes:         acquireRouteStateMap(),
	}
	a.inflight[env.MessageID] = msg
	a.pushResolvable(msg)
	return a.resumeResolvable(ctx)
}

func (a *actor) dispatchObserved(ctx context.Context, env CommittedEnvelope) error {
	if next := env.MessageSeq + 1; next > a.nextDispatchSeq {
		a.nextDispatchSeq = next
	}
	return a.dispatch(ctx, env)
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

		token := msg.ResolveToken
		cursor := msg.NextCursor
		page, err := a.resolvePageOutsideLock(ctx, token, cursor, pageLimit)
		if a.inflight[msg.MessageID] != msg || msg.ResolveDone {
			return nil
		}
		if err != nil {
			return err
		}
		a.recordOfflineResolvedEvents(msg, page, cursor, pageLimit)
		a.notifyOfflineResolvedOutsideLock(ctx)
		if a.inflight[msg.MessageID] != msg || msg.ResolveDone {
			return nil
		}
		routes := page.Routes
		if len(routes) > 0 {
			if err := a.applyPush(ctx, msg, routes, 1); err != nil {
				return err
			}
			if a.inflight[msg.MessageID] != msg {
				return nil
			}
		}
		msg.NextCursor = page.NextCursor
		msg.ResolveDone = page.Done
		if !page.Done && len(routes) == 0 && page.NextCursor == "" {
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
	for len(a.resolvable) > 0 {
		ref := a.resolvable[0]
		msg := a.inflight[ref.MessageID]
		if msg == nil || msg.ResolveDone {
			a.popResolvable()
			continue
		}
		if msg.ResolveInProgress {
			return nil
		}
		if !msg.ResolveRetryAt.IsZero() && msg.ResolveRetryAt.After(now) {
			return nil
		}
		return msg
	}
	return nil
}

func (a *actor) resumeMessage(ctx context.Context, msg *InflightMessage) (bool, error) {
	if msg.ResolveInProgress {
		return false, nil
	}
	msg.ResolveInProgress = true
	defer func() {
		msg.ResolveInProgress = false
	}()

	beforeBegun := msg.ResolveBegun
	beforeDone := msg.ResolveDone
	beforeCursor := msg.NextCursor
	beforePending := msg.PendingRouteCnt

	if !msg.ResolveBegun {
		env := cloneEnvelope(msg.Envelope)
		token, err := a.beginResolveOutsideLock(ctx, env)
		if a.inflight[msg.MessageID] != msg || msg.ResolveDone {
			return false, nil
		}
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
	a.scheduleResolveRetry(msg, nextAttempt)
	return false, nil
}

// beginResolveOutsideLock runs resolver setup without holding the actor lock.
func (a *actor) beginResolveOutsideLock(ctx context.Context, env CommittedEnvelope) (any, error) {
	a.mu.Unlock()
	token, err := a.shard.manager.resolver.BeginResolve(ctx, a.key, env)
	a.mu.Lock()
	return token, err
}

// resolvePageOutsideLock resolves one subscriber page without holding the actor lock.
func (a *actor) resolvePageOutsideLock(ctx context.Context, token any, cursor string, limit int) (ResolvePageResult, error) {
	a.mu.Unlock()
	page, err := a.shard.manager.resolver.ResolvePage(ctx, token, cursor, limit)
	a.mu.Lock()
	if err != nil {
		return ResolvePageResult{}, err
	}
	return page, nil
}

// pushOutsideLock pushes route batches without holding the actor lock.
func (a *actor) pushOutsideLock(ctx context.Context, cmd PushCommand) (PushResult, error) {
	a.mu.Unlock()
	result, err := a.shard.manager.push.Push(ctx, cmd)
	a.mu.Lock()
	return result, err
}

func (a *actor) pushResultStillCurrent(msg *InflightMessage, route RouteKey) bool {
	_, ok := msg.Routes[route]
	return ok
}

func (a *actor) dispatchLate(ctx context.Context, env CommittedEnvelope) error {
	// Owner routing and replay can deliver sparse committed sequences to a node,
	// so a lower seq can legitimately arrive after a higher locally observed one.
	// Deliver it late instead of stalling forever.
	return a.dispatch(ctx, env)
}

func (a *actor) applyPush(ctx context.Context, msg *InflightMessage, routes []RouteKey, attempt int) error {
	pushRoutes := append([]RouteKey(nil), routes...)
	for _, route := range pushRoutes {
		// Bind before I/O so a fast receiver ack cannot beat the owner-side index update.
		a.ensureRouteState(msg, route)
		a.shard.manager.ackIdx.Bind(a.ackBinding(msg, route))
	}
	result, err := a.pushOutsideLock(ctx, PushCommand{
		Envelope: cloneEnvelope(msg.Envelope),
		Routes:   pushRoutes,
		Attempt:  attempt,
	})
	if a.inflight[msg.MessageID] != msg {
		return nil
	}
	if err != nil {
		result = PushResult{Retryable: pushRoutes}
	}
	for _, route := range result.Accepted {
		state, ok := msg.Routes[route]
		if !ok {
			continue
		}
		state.Attempt = attempt
		binding := a.ackBinding(msg, route)
		if state.Accepted {
			if !a.shard.manager.ackIdx.refresh(binding) {
				if err := a.finishRoute(ctx, msg, route); err != nil {
					return err
				}
				continue
			}
		} else {
			state.Accepted = true
			if !a.shard.manager.ackIdx.refresh(binding) {
				a.shard.manager.ackIdx.Bind(binding)
			}
		}
		msg.Routes[route] = state
		if !a.scheduleRetry(msg, route, attempt) {
			if err := a.expireRoute(ctx, msg, route, attempt); err != nil {
				return err
			}
		}
	}
	for _, route := range result.Retryable {
		state, ok := msg.Routes[route]
		if !ok {
			continue
		}
		state.Attempt = attempt
		if !state.Accepted {
			a.shard.manager.ackIdx.RemoveRoute(route.UID, route.SessionID, msg.MessageID)
		}
		msg.Routes[route] = state
		if !a.scheduleRetry(msg, route, attempt) {
			if err := a.expireRoute(ctx, msg, route, attempt); err != nil {
				return err
			}
		}
	}
	for _, route := range result.Dropped {
		if err := a.finishRoute(ctx, msg, route); err != nil {
			return err
		}
	}
	return nil
}

func (a *actor) ackBinding(msg *InflightMessage, route RouteKey) AckBinding {
	return AckBinding{
		SessionID:   route.SessionID,
		MessageID:   msg.MessageID,
		ChannelID:   a.key.ChannelID,
		ChannelType: a.key.ChannelType,
		Route:       route,
	}
}

func (a *actor) expireRoute(ctx context.Context, msg *InflightMessage, route RouteKey, attempt int) error {
	state, ok := msg.Routes[route]
	if !ok {
		return nil
	}
	notify := true
	if state.Accepted {
		if _, ok := a.shard.manager.ackIdx.TakeRoute(route.UID, route.SessionID, msg.MessageID); !ok {
			notify = false
		}
	}
	if err := a.finishRoute(ctx, msg, route); err != nil {
		return err
	}
	if notify {
		a.expiredEvents = append(a.expiredEvents, RouteExpiredEvent{
			ChannelID:   a.key.ChannelID,
			ChannelType: a.key.ChannelType,
			MessageID:   msg.MessageID,
			MessageSeq:  msg.MessageSeq,
			Route:       route,
			Attempt:     attempt,
		})
	}
	return nil
}

func (a *actor) drainExpiredEventsLocked() []RouteExpiredEvent {
	if len(a.expiredEvents) == 0 {
		return nil
	}
	events := append([]RouteExpiredEvent(nil), a.expiredEvents...)
	a.expiredEvents = a.expiredEvents[:0]
	return events
}

func (a *actor) recordOfflineResolvedEvents(msg *InflightMessage, page ResolvePageResult, cursor string, limit int) {
	if msg == nil || len(page.OfflineUIDs) == 0 {
		return
	}
	env := cloneEnvelope(msg.Envelope)
	for _, uid := range page.OfflineUIDs {
		if uid == "" {
			continue
		}
		a.offlineEvents = append(a.offlineEvents, OfflineResolvedEvent{
			Envelope:   env,
			UID:        uid,
			Attempt:    msg.ResolveAttempt,
			PageCursor: cursor,
			NextCursor: page.NextCursor,
			PageLimit:  limit,
			Done:       page.Done,
		})
	}
}

func (a *actor) drainOfflineResolvedEventsLocked() []OfflineResolvedEvent {
	if len(a.offlineEvents) == 0 {
		return nil
	}
	events := append([]OfflineResolvedEvent(nil), a.offlineEvents...)
	a.offlineEvents = a.offlineEvents[:0]
	return events
}

func (a *actor) notifyOfflineResolvedOutsideLock(ctx context.Context) {
	events := a.drainOfflineResolvedEventsLocked()
	if len(events) == 0 {
		return
	}
	a.mu.Unlock()
	a.shard.manager.notifyOfflineResolved(ctx, events)
	a.mu.Lock()
}

func (a *actor) ensureRouteState(msg *InflightMessage, route RouteKey) {
	if msg.Routes == nil {
		msg.Routes = acquireRouteStateMap()
	}
	if _, ok := msg.Routes[route]; ok {
		return
	}
	msg.Routes[route] = RouteDeliveryState{}
	msg.PendingRouteCnt++
	a.pendingRouteCnt++
}

func (a *actor) finishRoute(_ context.Context, msg *InflightMessage, route RouteKey) error {
	if _, ok := msg.Routes[route]; !ok {
		return nil
	}
	delete(msg.Routes, route)
	if msg.PendingRouteCnt > 0 {
		msg.PendingRouteCnt--
	}
	if a.pendingRouteCnt > 0 {
		a.pendingRouteCnt--
	}
	a.shard.manager.ackIdx.RemoveRoute(route.UID, route.SessionID, msg.MessageID)
	if msg.PendingRouteCnt == 0 && msg.ResolveDone {
		a.completeMessage(msg.MessageID)
	}
	return nil
}

func (a *actor) scheduleRetry(msg *InflightMessage, route RouteKey, attempt int) bool {
	delay, ok := a.shard.nextRetryDelay(attempt + 1)
	if !ok {
		return false
	}
	a.shard.wheel.Schedule(RetryEntry{
		When:        a.shard.manager.clock.Now().Add(delay),
		Kind:        RetryEntryRoute,
		ChannelID:   a.key.ChannelID,
		ChannelType: a.key.ChannelType,
		MessageID:   msg.MessageID,
		Route:       route,
		Attempt:     attempt + 1,
	})
	return true
}

func (a *actor) scheduleResolveRetry(msg *InflightMessage, attempt int) {
	if msg == nil || msg.ResolveRetryAt.IsZero() {
		return
	}
	a.shard.wheel.Schedule(RetryEntry{
		When:        msg.ResolveRetryAt,
		Kind:        RetryEntryResolve,
		ChannelID:   a.key.ChannelID,
		ChannelType: a.key.ChannelType,
		MessageID:   msg.MessageID,
		Attempt:     attempt,
	})
}

func (a *actor) completeMessage(messageID uint64) {
	if msg := a.inflight[messageID]; msg != nil {
		releaseRouteStateMap(msg.Routes)
		msg.Routes = nil
	}
	a.rememberCompleted(messageID)
	delete(a.inflight, messageID)
}

func acquireRouteStateMap() map[RouteKey]RouteDeliveryState {
	routes, ok := routeStateMapPool.Get().(map[RouteKey]RouteDeliveryState)
	if !ok || routes == nil {
		return make(map[RouteKey]RouteDeliveryState, 4)
	}
	return routes
}

func releaseRouteStateMap(routes map[RouteKey]RouteDeliveryState) {
	if routes == nil {
		return
	}
	clear(routes)
	routeStateMapPool.Put(routes)
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
	return limit - a.pendingRouteCnt
}

func (a *actor) inflightRouteCount() int {
	return a.pendingRouteCnt
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
	copy(a.completedOrder, a.completedOrder[1:])
	a.completedOrder[len(a.completedOrder)-1] = 0
	a.completedOrder = a.completedOrder[:len(a.completedOrder)-1]
	delete(a.completed, evict)
}

func cloneEnvelope(env CommittedEnvelope) CommittedEnvelope {
	copied := env
	copied.Payload = append([]byte(nil), env.Payload...)
	copied.MessageScopedUIDs = append([]string(nil), env.MessageScopedUIDs...)
	return copied
}

type resolvableMessageRef struct {
	MessageID  uint64
	MessageSeq uint64
}

func (a *actor) pushResolvable(msg *InflightMessage) {
	if msg == nil {
		return
	}
	a.resolvable = append(a.resolvable, resolvableMessageRef{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq})
	resolvableSiftUp(a.resolvable, len(a.resolvable)-1)
}

func (a *actor) popResolvable() {
	last := len(a.resolvable) - 1
	a.resolvable[0] = a.resolvable[last]
	a.resolvable[last] = resolvableMessageRef{}
	a.resolvable = a.resolvable[:last]
	resolvableSiftDown(a.resolvable, 0)
}

func resolvableSiftUp(values []resolvableMessageRef, idx int) {
	for idx > 0 {
		parent := (idx - 1) / 2
		if values[parent].MessageSeq <= values[idx].MessageSeq {
			return
		}
		values[idx], values[parent] = values[parent], values[idx]
		idx = parent
	}
}

func resolvableSiftDown(values []resolvableMessageRef, idx int) {
	for {
		left := 2*idx + 1
		if left >= len(values) {
			return
		}
		smallest := left
		right := left + 1
		if right < len(values) && values[right].MessageSeq < values[left].MessageSeq {
			smallest = right
		}
		if values[idx].MessageSeq <= values[smallest].MessageSeq {
			return
		}
		values[idx], values[smallest] = values[smallest], values[idx]
		idx = smallest
	}
}
