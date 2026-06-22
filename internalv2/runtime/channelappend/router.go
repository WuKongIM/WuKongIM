package channelappend

import (
	"context"
	"errors"
	"sync"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
)

const (
	defaultRouterRetryBackoff     = time.Millisecond
	defaultRouterMaxRouteAttempts = 3
)

// AuthorityResolver resolves the append authority for a canonical channel.
type AuthorityResolver interface {
	// ResolveAppendAuthority returns the current fenced channel authority target.
	ResolveAppendAuthority(context.Context, ChannelID) (AuthorityTarget, error)
}

// LocalSubmitter submits sends to the local channel authority runtime.
type LocalSubmitter interface {
	// SubmitLocal admits a batch to the local channel authority.
	SubmitLocal(context.Context, AuthorityTarget, []SendBatchItem) (*Future, error)
}

// RemoteForwarder forwards sends to a remote channel authority.
type RemoteForwarder interface {
	// ForwardSendBatch forwards items to the resolved remote channel authority.
	ForwardSendBatch(context.Context, AuthorityTarget, []SendBatchItem) []SendBatchItemResult
}

// RouterOptions configures channel authority routing.
type RouterOptions struct {
	// LocalNodeID is this node's cluster identity.
	LocalNodeID uint64
	// Resolver resolves canonical channel append authority.
	Resolver AuthorityResolver
	// Local handles sends when this node is the resolved channel authority.
	Local LocalSubmitter
	// Remote forwards sends when another node is the resolved channel authority.
	Remote RemoteForwarder
	// RetryBackoff is the bounded pause between route refresh attempts.
	RetryBackoff time.Duration
	// MaxRouteAttempts bounds retries for items that do not carry deadlines.
	MaxRouteAttempts int
	// MaxOutboundPerNode bounds concurrent remote forwards per leader node. Values <= 0 disable this limit.
	MaxOutboundPerNode int
	// Observer receives foreground routing observations.
	Observer RouterObserver
}

// Router sends commands to the current channel authority.
type Router struct {
	localNodeID uint64
	resolver    AuthorityResolver
	local       LocalSubmitter
	remote      RemoteForwarder

	retryBackoff     time.Duration
	maxRouteAttempts int
	maxOutbound      int
	outbound         map[uint64]int
	outboundMu       sync.Mutex
	observer         RouterObserver
}

// NewRouter creates a channel authority router.
func NewRouter(opts RouterOptions) *Router {
	retryBackoff := opts.RetryBackoff
	if retryBackoff <= 0 {
		retryBackoff = defaultRouterRetryBackoff
	}
	maxRouteAttempts := opts.MaxRouteAttempts
	if maxRouteAttempts <= 0 {
		maxRouteAttempts = defaultRouterMaxRouteAttempts
	}
	return &Router{
		localNodeID:      opts.LocalNodeID,
		resolver:         opts.Resolver,
		local:            opts.Local,
		remote:           opts.Remote,
		retryBackoff:     retryBackoff,
		maxRouteAttempts: maxRouteAttempts,
		maxOutbound:      opts.MaxOutboundPerNode,
		outbound:         make(map[uint64]int),
		observer:         opts.Observer,
	}
}

// Send routes one send command through channel authority.
func (r *Router) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	results := r.SendBatch([]SendBatchItem{{Context: ctx, Command: cmd}})
	if len(results) == 0 {
		return SendResult{}, nil
	}
	return results[0].Result, results[0].Err
}

// SendBatch routes sends and returns item-aligned results.
func (r *Router) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	if len(items) == 1 {
		return []SendBatchItemResult{r.sendSingle(items[0])}
	}
	results := make([]SendBatchItemResult, len(items))
	pending := make([]int, 0, len(items))
	attempts := make([]int, len(items))
	routeChannels := make([]ChannelID, len(items))
	for i, item := range items {
		prepared, routeChannel, result, done := prepareRouterItem(item, time.Now())
		if done {
			results[i] = result
			continue
		}
		items[i] = prepared
		routeChannels[i] = routeChannel
		pending = append(pending, i)
	}

	for len(pending) > 0 {
		groups, nextPending := r.resolvePending(items, routeChannels, results, pending, attempts)
		for _, group := range groups {
			groupResults := r.submitGroup(group)
			for i, result := range normalizeRouterGroupResults(len(group.indexes), groupResults) {
				index := group.indexes[i]
				if shouldRetryRouterError(result.Err) && canRetryRouterItem(items[index], attempts[index], r.maxRouteAttempts, time.Now()) {
					nextPending = append(nextPending, index)
					continue
				}
				results[index] = result
			}
		}
		if len(nextPending) == 0 {
			break
		}
		r.waitBeforeRetry(items, nextPending)
		pending = nextPending
	}
	return results
}

func (r *Router) sendSingle(item SendBatchItem) SendBatchItemResult {
	prepared, routeChannel, result, done := prepareRouterItem(item, time.Now())
	if done {
		return result
	}
	attempts := 0
	for {
		attempts++
		if err := routerItemError(prepared, time.Now()); err != nil {
			return SendBatchItemResult{Err: err}
		}
		if r.resolver == nil {
			return SendBatchItemResult{Err: ErrRouteNotReady}
		}
		target, err := r.resolver.ResolveAppendAuthority(routerItemContext(prepared), routeChannel)
		if err != nil {
			if shouldRetryRouterError(err) && canRetryRouterItem(prepared, attempts, r.maxRouteAttempts, time.Now()) {
				r.waitBeforeRetry([]SendBatchItem{prepared}, []int{0})
				continue
			}
			return SendBatchItemResult{Err: err}
		}
		target, err = normalizeRouterTarget(routeChannel, target)
		if err != nil {
			return SendBatchItemResult{Err: err}
		}
		result = r.submitSingleTarget(target, prepared)
		if shouldRetryRouterError(result.Err) && canRetryRouterItem(prepared, attempts, r.maxRouteAttempts, time.Now()) {
			r.waitBeforeRetry([]SendBatchItem{prepared}, []int{0})
			continue
		}
		return result
	}
}

type routerBatchGroup struct {
	target  AuthorityTarget
	indexes []int
	items   []SendBatchItem
}

func (r *Router) resolvePending(items []SendBatchItem, routeChannels []ChannelID, results []SendBatchItemResult, pending []int, attempts []int) ([]routerBatchGroup, []int) {
	groups := make([]routerBatchGroup, 0, len(pending))
	indexesByChannel := make(map[ChannelID][]int, len(pending))
	channelOrder := make([]ChannelID, 0, len(pending))
	nextPending := make([]int, 0)
	for _, index := range pending {
		attempts[index]++
		item := items[index]
		if err := routerItemError(item, time.Now()); err != nil {
			results[index] = SendBatchItemResult{Err: err}
			continue
		}
		if r.resolver == nil {
			results[index] = SendBatchItemResult{Err: ErrRouteNotReady}
			continue
		}
		channelID := routeChannels[index]
		if _, ok := indexesByChannel[channelID]; !ok {
			channelOrder = append(channelOrder, channelID)
		}
		indexesByChannel[channelID] = append(indexesByChannel[channelID], index)
	}
	for _, channelID := range channelOrder {
		indexes := indexesByChannel[channelID]
		if len(indexes) == 0 {
			continue
		}
		firstIndex := indexes[0]
		target, err := r.resolver.ResolveAppendAuthority(routerItemContext(items[firstIndex]), channelID)
		if err != nil {
			for _, index := range indexes {
				item := items[index]
				if shouldRetryRouterError(err) && canRetryRouterItem(item, attempts[index], r.maxRouteAttempts, time.Now()) {
					nextPending = append(nextPending, index)
					continue
				}
				results[index] = SendBatchItemResult{Err: err}
			}
			continue
		}
		target, err = normalizeRouterTarget(channelID, target)
		if err != nil {
			for _, index := range indexes {
				results[index] = SendBatchItemResult{Err: err}
			}
			continue
		}
		group := routerBatchGroup{target: target}
		for _, index := range indexes {
			group.indexes = append(group.indexes, index)
			group.items = append(group.items, items[index])
		}
		groups = append(groups, group)
	}
	return groups, nextPending
}

func (r *Router) submitGroup(group routerBatchGroup) []SendBatchItemResult {
	startedAt := time.Now()
	path := "remote"
	results := make([]SendBatchItemResult, len(group.items))
	activeItems, activePositions := activeRouterGroupItems(group.items, results, time.Now())
	finish := func() []SendBatchItemResult {
		observeRouterGroup(r.observer, RouterObservation{Path: path, Result: routerResultsClass(results), Items: len(group.items), Duration: time.Since(startedAt)})
		return results
	}
	finishActive := func(activeResults []SendBatchItemResult) []SendBatchItemResult {
		results = mergeActiveRouterResults(activePositions, activeResults, results)
		return finish()
	}
	if len(activeItems) == 0 {
		path = "pre_route"
		return finish()
	}
	ctx, cancel := routerAllItemsContext(activeItems)
	defer cancel()
	if group.target.LeaderNodeID == r.localNodeID {
		path = "local"
		if r.local == nil {
			return finishActive(routerErrorResults(len(activeItems), ErrRouteNotReady))
		}
		future, err := r.local.SubmitLocal(ctx, group.target, activeItems)
		if err != nil {
			return finishActive(routerErrorResults(len(activeItems), err))
		}
		if future == nil {
			return finishActive(routerErrorResults(len(activeItems), ErrAppendResultMissing))
		}
		activeResults, err := future.Wait(ctx)
		if err != nil {
			activeResults = routerErrorResults(len(activeItems), err)
		}
		activeResults = rewriteTerminalRouterErrors(activeItems, activeResults, time.Now())
		return finishActive(activeResults)
	}
	if r.remote == nil {
		return finishActive(routerErrorResults(len(activeItems), ErrRouteNotReady))
	}
	if !r.acquireOutbound(group.target.LeaderNodeID) {
		return finishActive(routerErrorResults(len(activeItems), ErrBackpressured))
	}
	defer r.releaseOutbound(group.target.LeaderNodeID)
	activeResults := r.remote.ForwardSendBatch(ctx, group.target, activeItems)
	activeResults = rewriteTerminalRouterErrors(activeItems, activeResults, time.Now())
	return finishActive(activeResults)
}

func (r *Router) submitSingleTarget(target AuthorityTarget, item SendBatchItem) SendBatchItemResult {
	startedAt := time.Now()
	path := "remote"
	finish := func(result SendBatchItemResult) SendBatchItemResult {
		observeRouterGroup(r.observer, RouterObservation{Path: path, Result: resultClass(result), Items: 1, Duration: time.Since(startedAt)})
		return result
	}
	if err := routerItemError(item, time.Now()); err != nil {
		path = "pre_route"
		return finish(SendBatchItemResult{Err: err})
	}
	ctx, cancel := routerAllItemsContext([]SendBatchItem{item})
	defer cancel()
	if target.LeaderNodeID == r.localNodeID {
		path = "local"
		if r.local == nil {
			return finish(SendBatchItemResult{Err: ErrRouteNotReady})
		}
		future, err := r.local.SubmitLocal(ctx, target, []SendBatchItem{item})
		if err != nil {
			return finish(SendBatchItemResult{Err: err})
		}
		if future == nil {
			return finish(SendBatchItemResult{Err: ErrAppendResultMissing})
		}
		activeResults, err := future.Wait(ctx)
		if err != nil {
			return finish(rewriteTerminalRouterError(item, SendBatchItemResult{Err: err}, time.Now()))
		}
		if len(activeResults) != 1 {
			return finish(SendBatchItemResult{Err: ErrAppendResultMissing})
		}
		result := rewriteTerminalRouterError(item, activeResults[0], time.Now())
		return finish(result)
	}
	if r.remote == nil {
		return finish(SendBatchItemResult{Err: ErrRouteNotReady})
	}
	if !r.acquireOutbound(target.LeaderNodeID) {
		return finish(SendBatchItemResult{Err: ErrBackpressured})
	}
	defer r.releaseOutbound(target.LeaderNodeID)
	activeResults := r.remote.ForwardSendBatch(ctx, target, []SendBatchItem{item})
	if len(activeResults) != 1 {
		return finish(SendBatchItemResult{Err: ErrAppendResultMissing})
	}
	result := rewriteTerminalRouterError(item, activeResults[0], time.Now())
	return finish(result)
}

func prepareRouterItem(item SendBatchItem, now time.Time) (SendBatchItem, ChannelID, SendBatchItemResult, bool) {
	if item.Context == nil {
		item.Context = context.Background()
	}
	if err := routerItemError(item, now); err != nil {
		return item, ChannelID{}, SendBatchItemResult{Err: err}, true
	}
	routeChannel, result, done := preRouteChannel(item.Command)
	return item, routeChannel, result, done
}

func preRouteChannel(cmd SendCommand) (ChannelID, SendBatchItemResult, bool) {
	if cmd.FromUID == "" {
		return ChannelID{}, SendBatchItemResult{Result: SendResult{Reason: ReasonAuthFail}}, true
	}
	if cmd.RequestScoped || (len(cmd.MessageScopedUIDs) > 0 && cmd.ChannelID == "") {
		return preRouteRequestScopedChannel(cmd)
	}
	if cmd.ChannelID == "" || cmd.ChannelType == 0 || len(cmd.Payload) == 0 {
		return ChannelID{}, SendBatchItemResult{Result: SendResult{Reason: ReasonInvalidRequest}}, true
	}
	if cmd.NoPersist {
		return preRouteNoPersistChannel(cmd)
	}
	if cmd.NormalizePersonChannel && cmd.ChannelType == channelTypePerson {
		channelID, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
		if err != nil {
			return ChannelID{}, SendBatchItemResult{Err: err}, true
		}
		return ChannelID{ID: channelID, Type: cmd.ChannelType}, SendBatchItemResult{}, false
	}
	return ChannelID{ID: cmd.ChannelID, Type: cmd.ChannelType}, SendBatchItemResult{}, false
}

func preRouteNoPersistChannel(cmd SendCommand) (ChannelID, SendBatchItemResult, bool) {
	sourceChannelID, alreadyCommandChannel := runtimechannelid.FromCommandChannel(cmd.ChannelID)
	cmd.ChannelID = sourceChannelID
	if !cmd.SyncOnce && !alreadyCommandChannel {
		return ChannelID{}, SendBatchItemResult{Result: SendResult{Reason: ReasonSuccess}}, true
	}
	if cmd.NormalizePersonChannel && cmd.ChannelType == channelTypePerson {
		channelID, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
		if err != nil {
			return ChannelID{}, SendBatchItemResult{Err: err}, true
		}
		cmd.ChannelID = channelID
	}
	return ChannelID{ID: runtimechannelid.ToCommandChannel(cmd.ChannelID), Type: cmd.ChannelType}, SendBatchItemResult{}, false
}

func preRouteRequestScopedChannel(cmd SendCommand) (ChannelID, SendBatchItemResult, bool) {
	if len(cmd.Payload) == 0 {
		return ChannelID{}, SendBatchItemResult{Result: SendResult{Reason: ReasonInvalidRequest}}, true
	}
	if !cmd.SyncOnce {
		return ChannelID{}, SendBatchItemResult{Err: ErrRequestSubscribersRequireSyncOnce}, true
	}
	if cmd.ChannelID != "" {
		return ChannelID{}, SendBatchItemResult{Err: ErrRequestSubscribersConflictChannel}, true
	}
	scoped, err := runtimechannelid.RequestSubscriberChannelFor(cmd.MessageScopedUIDs)
	if err != nil {
		if errors.Is(err, runtimechannelid.ErrRequestSubscribersRequired) {
			return ChannelID{}, SendBatchItemResult{Err: ErrRequestSubscribersRequired}, true
		}
		return ChannelID{}, SendBatchItemResult{Err: err}, true
	}
	if cmd.NoPersist {
		return ChannelID{ID: scoped.CommandChannelID, Type: scoped.ChannelType}, SendBatchItemResult{}, false
	}
	return ChannelID{ID: scoped.CommandChannelID, Type: scoped.ChannelType}, SendBatchItemResult{}, false
}

func routerItemError(item SendBatchItem, now time.Time) error {
	if item.Context != nil {
		if err := item.Context.Err(); err != nil {
			return err
		}
	}
	if !item.Deadline.IsZero() && !item.Deadline.After(now) {
		return context.DeadlineExceeded
	}
	return nil
}

func routerItemContext(item SendBatchItem) context.Context {
	if item.Context != nil {
		return item.Context
	}
	return context.Background()
}

func normalizeRouterTarget(id ChannelID, target AuthorityTarget) (AuthorityTarget, error) {
	if target.ChannelID != id {
		return AuthorityTarget{}, ErrStaleRoute
	}
	if target.ChannelKey == "" {
		target.ChannelKey = channelKey(target.ChannelID)
	}
	if target.LeaderNodeID == 0 {
		return AuthorityTarget{}, ErrRouteNotReady
	}
	return target, nil
}

func shouldRetryRouterError(err error) bool {
	return errors.Is(err, ErrStaleRoute) ||
		errors.Is(err, ErrNotChannelAuthority) ||
		errors.Is(err, ErrNotLeader) ||
		errors.Is(err, ErrRouteNotReady)
}

func canRetryRouterItem(item SendBatchItem, attempts int, maxAttempts int, now time.Time) bool {
	if err := routerItemError(item, now); err != nil {
		return false
	}
	if hasRouterDeadline(item) {
		return true
	}
	return attempts < maxAttempts
}

func hasRouterDeadline(item SendBatchItem) bool {
	if !item.Deadline.IsZero() {
		return true
	}
	if item.Context != nil {
		_, ok := item.Context.Deadline()
		return ok
	}
	return false
}

func (r *Router) waitBeforeRetry(items []SendBatchItem, pending []int) {
	delay := retryDelayForRouterItems(items, pending, r.retryBackoff, time.Now())
	if delay <= 0 {
		return
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	wake, cleanup := routerPendingWakeSignal(items, pending)
	defer cleanup()
	select {
	case <-timer.C:
	case <-wake:
	}
}

func retryDelayForRouterItems(items []SendBatchItem, pending []int, base time.Duration, now time.Time) time.Duration {
	if base <= 0 {
		return 0
	}
	delay := base
	for _, index := range pending {
		deadline, ok := routerItemDeadline(items[index])
		if !ok {
			continue
		}
		remaining := deadline.Sub(now)
		if remaining <= 0 {
			return 0
		}
		if remaining < delay {
			delay = remaining
		}
	}
	return delay
}

func routerItemDeadline(item SendBatchItem) (time.Time, bool) {
	deadline := item.Deadline
	ok := !deadline.IsZero()
	if item.Context != nil {
		if ctxDeadline, ctxOK := item.Context.Deadline(); ctxOK && (!ok || ctxDeadline.Before(deadline)) {
			deadline = ctxDeadline
			ok = true
		}
	}
	return deadline, ok
}

func activeRouterGroupItems(items []SendBatchItem, results []SendBatchItemResult, now time.Time) ([]SendBatchItem, []int) {
	activeItems := make([]SendBatchItem, 0, len(items))
	activePositions := make([]int, 0, len(items))
	for i, item := range items {
		if err := routerItemError(item, now); err != nil {
			results[i] = SendBatchItemResult{Err: err}
			continue
		}
		activePositions = append(activePositions, i)
		activeItems = append(activeItems, item)
	}
	return activeItems, activePositions
}

func mergeActiveRouterResults(activePositions []int, activeResults []SendBatchItemResult, results []SendBatchItemResult) []SendBatchItemResult {
	activeResults = normalizeRouterGroupResults(len(activePositions), activeResults)
	for i, result := range activeResults {
		position := activePositions[i]
		results[position] = result
	}
	return results
}

func routerAllItemsContext(items []SendBatchItem) (context.Context, context.CancelFunc) {
	if len(items) == 0 {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, cancel
	}
	if len(items) == 1 {
		base := routerItemContext(items[0])
		if deadline, ok := routerItemDeadline(items[0]); ok {
			return context.WithDeadline(base, deadline)
		}
		return context.WithCancel(base)
	}
	for _, item := range items {
		if !routerItemHasTerminalSignal(item) {
			return context.WithCancel(context.Background())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cleanup := watchRouterItemTerminals(items, cancel, true)
	return ctx, func() {
		cleanup()
		cancel()
	}
}

func routerPendingWakeSignal(items []SendBatchItem, pending []int) (<-chan struct{}, func()) {
	wake := make(chan struct{})
	var wakeOnce sync.Once
	signal := func() {
		wakeOnce.Do(func() { close(wake) })
	}
	watched := make([]SendBatchItem, 0, len(pending))
	for _, index := range pending {
		item := items[index]
		if err := routerItemError(item, time.Now()); err != nil {
			signal()
			return wake, func() {}
		}
		if !routerItemHasTerminalSignal(item) {
			continue
		}
		watched = append(watched, item)
	}
	cleanup := watchRouterItemTerminals(watched, signal, false)
	return wake, cleanup
}

func routerItemHasTerminalSignal(item SendBatchItem) bool {
	if _, ok := routerItemDeadline(item); ok {
		return true
	}
	return item.Context != nil && item.Context.Done() != nil
}

func watchRouterItemTerminals(items []SendBatchItem, signal func(), requireAll bool) func() {
	if len(items) == 0 {
		return func() {}
	}
	var mu sync.Mutex
	stopped := false
	remaining := len(items)
	fire := func() {
		shouldSignal := false
		mu.Lock()
		if !stopped {
			if requireAll {
				remaining--
				shouldSignal = remaining == 0
			} else {
				shouldSignal = true
			}
			if shouldSignal {
				stopped = true
			}
		}
		mu.Unlock()
		if shouldSignal {
			signal()
		}
	}
	cleanups := make([]func(), 0, len(items)*2)
	for _, item := range items {
		cleanups = append(cleanups, watchRouterItemTerminal(item, fire)...)
	}
	return func() {
		mu.Lock()
		stopped = true
		mu.Unlock()
		for _, cleanup := range cleanups {
			cleanup()
		}
	}
}

func watchRouterItemTerminal(item SendBatchItem, signal func()) []func() {
	var once sync.Once
	fire := func() { once.Do(signal) }
	if err := routerItemError(item, time.Now()); err != nil {
		fire()
		return nil
	}
	cleanups := make([]func(), 0, 2)
	if item.Context != nil && item.Context.Done() != nil {
		stop := context.AfterFunc(item.Context, fire)
		cleanups = append(cleanups, func() { stop() })
	}
	if !item.Deadline.IsZero() {
		delay := time.Until(item.Deadline)
		if delay <= 0 {
			fire()
			return cleanups
		}
		timer := time.AfterFunc(delay, fire)
		cleanups = append(cleanups, func() { timer.Stop() })
	}
	return cleanups
}

func normalizeRouterGroupResults(want int, got []SendBatchItemResult) []SendBatchItemResult {
	if len(got) != want {
		return routerErrorResults(want, ErrAppendResultMissing)
	}
	return got
}

func routerErrorResults(n int, err error) []SendBatchItemResult {
	results := make([]SendBatchItemResult, n)
	for i := range results {
		results[i].Err = err
	}
	return results
}

func rewriteTerminalRouterErrors(items []SendBatchItem, results []SendBatchItemResult, now time.Time) []SendBatchItemResult {
	if len(items) == 0 || len(items) != len(results) {
		return results
	}
	for i := range results {
		if results[i].Err == nil {
			continue
		}
		results[i] = rewriteTerminalRouterError(items[i], results[i], now)
	}
	return results
}

func rewriteTerminalRouterError(item SendBatchItem, result SendBatchItemResult, now time.Time) SendBatchItemResult {
	if errors.Is(result.Err, context.Canceled) {
		if err := routerItemError(item, now); errors.Is(err, context.DeadlineExceeded) {
			result.Err = err
		}
	}
	return result
}

func (r *Router) acquireOutbound(nodeID uint64) bool {
	if r.maxOutbound <= 0 {
		return true
	}
	r.outboundMu.Lock()
	defer r.outboundMu.Unlock()
	if r.outbound[nodeID] >= r.maxOutbound {
		return false
	}
	r.outbound[nodeID]++
	return true
}

func (r *Router) releaseOutbound(nodeID uint64) {
	if r.maxOutbound <= 0 {
		return
	}
	r.outboundMu.Lock()
	defer r.outboundMu.Unlock()
	if r.outbound[nodeID] <= 1 {
		delete(r.outbound, nodeID)
		return
	}
	r.outbound[nodeID]--
}
