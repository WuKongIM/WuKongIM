package channelwrite

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

type routerBatchGroup struct {
	target  AuthorityTarget
	indexes []int
	items   []SendBatchItem
}

func (r *Router) resolvePending(items []SendBatchItem, routeChannels []ChannelID, results []SendBatchItemResult, pending []int, attempts []int) ([]routerBatchGroup, []int) {
	groups := make([]routerBatchGroup, 0, len(pending))
	groupIndexes := make(map[AuthorityTarget]int, len(pending))
	nextPending := make([]int, 0)
	for _, index := range pending {
		attempts[index]++
		item := items[index]
		if err := routerItemError(item, time.Now()); err != nil {
			results[index] = SendBatchItemResult{Err: err}
			continue
		}
		if r == nil || r.resolver == nil {
			results[index] = SendBatchItemResult{Err: ErrRouteNotReady}
			continue
		}
		channelID := routeChannels[index]
		target, err := r.resolver.ResolveAppendAuthority(routerItemContext(item), channelID)
		if err != nil {
			if shouldRetryRouterError(err) && canRetryRouterItem(item, attempts[index], r.maxRouteAttempts, time.Now()) {
				nextPending = append(nextPending, index)
				continue
			}
			results[index] = SendBatchItemResult{Err: err}
			continue
		}
		target, err = normalizeRouterTarget(channelID, target)
		if err != nil {
			results[index] = SendBatchItemResult{Err: err}
			continue
		}
		groupIndex, ok := groupIndexes[target]
		if !ok {
			groupIndex = len(groups)
			groupIndexes[target] = groupIndex
			groups = append(groups, routerBatchGroup{target: target})
		}
		groups[groupIndex].indexes = append(groups[groupIndex].indexes, index)
		groups[groupIndex].items = append(groups[groupIndex].items, item)
	}
	return groups, nextPending
}

func (r *Router) submitGroup(group routerBatchGroup) []SendBatchItemResult {
	results := make([]SendBatchItemResult, len(group.items))
	activeItems, activePositions := activeRouterGroupItems(group.items, results, time.Now())
	if len(activeItems) == 0 {
		return results
	}
	localNodeID := uint64(0)
	if r != nil {
		localNodeID = r.localNodeID
	}
	ctx, cancel := routerAllItemsContext(activeItems)
	defer cancel()
	var activeResults []SendBatchItemResult
	if group.target.LeaderNodeID == localNodeID {
		if r == nil || r.local == nil {
			activeResults = routerErrorResults(len(activeItems), ErrRouteNotReady)
			return mergeActiveRouterResults(group.items, activePositions, activeResults, results)
		}
		future, err := r.local.SubmitLocal(ctx, group.target, activeItems)
		if err != nil {
			activeResults = routerErrorResults(len(activeItems), err)
			return mergeActiveRouterResults(group.items, activePositions, activeResults, results)
		}
		if future == nil {
			activeResults = routerErrorResults(len(activeItems), ErrAppendResultMissing)
			return mergeActiveRouterResults(group.items, activePositions, activeResults, results)
		}
		activeResults, err = future.Wait(ctx)
		if err != nil {
			activeResults = routerErrorResults(len(activeItems), err)
		}
		return mergeActiveRouterResults(group.items, activePositions, activeResults, results)
	}
	if r == nil || r.remote == nil {
		activeResults = routerErrorResults(len(activeItems), ErrRouteNotReady)
		return mergeActiveRouterResults(group.items, activePositions, activeResults, results)
	}
	if !r.acquireOutbound(group.target.LeaderNodeID) {
		activeResults = routerErrorResults(len(activeItems), ErrBackpressured)
		return mergeActiveRouterResults(group.items, activePositions, activeResults, results)
	}
	defer r.releaseOutbound(group.target.LeaderNodeID)
	activeResults = r.remote.ForwardSendBatch(ctx, group.target, activeItems)
	return mergeActiveRouterResults(group.items, activePositions, activeResults, results)
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
		return ChannelID{}, SendBatchItemResult{Result: SendResult{Reason: ReasonUnsupported}}, true
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
		return ChannelID{}, SendBatchItemResult{Result: SendResult{Reason: ReasonUnsupported}}, true
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

func mergeActiveRouterResults(items []SendBatchItem, activePositions []int, activeResults []SendBatchItemResult, results []SendBatchItemResult) []SendBatchItemResult {
	activeResults = normalizeRouterGroupResults(len(activePositions), activeResults)
	for i, result := range activeResults {
		position := activePositions[i]
		if err := routerItemError(items[position], time.Now()); err != nil {
			results[position] = SendBatchItemResult{Err: err}
			continue
		}
		results[position] = result
	}
	return results
}

func routerAllItemsContext(items []SendBatchItem) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	if len(items) == 0 {
		cancel()
		return ctx, cancel
	}
	stop := make(chan struct{})
	var stopOnce sync.Once
	stopAll := func() {
		stopOnce.Do(func() {
			close(stop)
			cancel()
		})
	}
	done := make(chan struct{}, len(items))
	for _, item := range items {
		go func(item SendBatchItem) {
			waitRouterItemTerminal(item, stop)
			select {
			case done <- struct{}{}:
			case <-stop:
			}
		}(item)
	}
	go func() {
		for range items {
			select {
			case <-done:
			case <-stop:
				return
			}
		}
		cancel()
	}()
	return ctx, stopAll
}

func routerPendingWakeSignal(items []SendBatchItem, pending []int) (<-chan struct{}, func()) {
	wake := make(chan struct{})
	stop := make(chan struct{})
	var wakeOnce sync.Once
	signal := func() {
		wakeOnce.Do(func() { close(wake) })
	}
	var stopOnce sync.Once
	cleanup := func() {
		stopOnce.Do(func() { close(stop) })
	}
	for _, index := range pending {
		item := items[index]
		if err := routerItemError(item, time.Now()); err != nil {
			signal()
			break
		}
		go func(item SendBatchItem) {
			waitRouterItemTerminal(item, stop)
			signal()
		}(item)
	}
	return wake, cleanup
}

func waitRouterItemTerminal(item SendBatchItem, stop <-chan struct{}) {
	deadline, hasDeadline := routerItemDeadline(item)
	var timer *time.Timer
	var timerC <-chan time.Time
	if hasDeadline {
		delay := time.Until(deadline)
		if delay <= 0 {
			return
		}
		timer = time.NewTimer(delay)
		timerC = timer.C
		defer timer.Stop()
	}
	var ctxDone <-chan struct{}
	if item.Context != nil {
		ctxDone = item.Context.Done()
	}
	if ctxDone == nil && timerC == nil {
		<-stop
		return
	}
	select {
	case <-ctxDone:
	case <-timerC:
	case <-stop:
	}
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

func (r *Router) acquireOutbound(nodeID uint64) bool {
	if r == nil || r.maxOutbound <= 0 {
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
	if r == nil || r.maxOutbound <= 0 {
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
