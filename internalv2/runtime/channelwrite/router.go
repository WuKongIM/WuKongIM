package channelwrite

import (
	"context"
	"errors"
	"sync"
	"time"
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
	for i, item := range items {
		prepared, result, done := prepareRouterItem(item, time.Now())
		if done {
			results[i] = result
			continue
		}
		items[i] = prepared
		pending = append(pending, i)
	}

	for len(pending) > 0 {
		groups, nextPending := r.resolvePending(items, results, pending, attempts)
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

func (r *Router) resolvePending(items []SendBatchItem, results []SendBatchItemResult, pending []int, attempts []int) ([]routerBatchGroup, []int) {
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
		channelID := ChannelID{ID: item.Command.ChannelID, Type: item.Command.ChannelType}
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
	localNodeID := uint64(0)
	if r != nil {
		localNodeID = r.localNodeID
	}
	if group.target.LeaderNodeID == localNodeID {
		if r == nil || r.local == nil {
			return routerErrorResults(len(group.items), ErrRouteNotReady)
		}
		ctx, cancel := routerGroupContext(group.items)
		defer cancel()
		future, err := r.local.SubmitLocal(ctx, group.target, group.items)
		if err != nil {
			return routerErrorResults(len(group.items), err)
		}
		if future == nil {
			return routerErrorResults(len(group.items), ErrAppendResultMissing)
		}
		results, err := future.Wait(ctx)
		if err != nil {
			return routerErrorResults(len(group.items), err)
		}
		return results
	}
	if r == nil || r.remote == nil {
		return routerErrorResults(len(group.items), ErrRouteNotReady)
	}
	if !r.acquireOutbound(group.target.LeaderNodeID) {
		return routerErrorResults(len(group.items), ErrBackpressured)
	}
	defer r.releaseOutbound(group.target.LeaderNodeID)
	ctx, cancel := routerGroupContext(group.items)
	defer cancel()
	return r.remote.ForwardSendBatch(ctx, group.target, group.items)
}

func prepareRouterItem(item SendBatchItem, now time.Time) (SendBatchItem, SendBatchItemResult, bool) {
	if item.Context == nil {
		item.Context = context.Background()
	}
	if err := routerItemError(item, now); err != nil {
		return item, SendBatchItemResult{Err: err}, true
	}
	if item.Command.ChannelID == "" || item.Command.ChannelType == 0 {
		return item, SendBatchItemResult{Result: SendResult{Reason: ReasonInvalidRequest}}, true
	}
	return item, SendBatchItemResult{}, false
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
	<-timer.C
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

func routerGroupContext(items []SendBatchItem) (context.Context, context.CancelFunc) {
	base := context.Background()
	baseSet := false
	var earliest time.Time
	hasDeadline := false
	for _, item := range items {
		if !baseSet && item.Context != nil {
			base = item.Context
			baseSet = true
		}
		if deadline, ok := routerItemDeadline(item); ok {
			if !hasDeadline || deadline.Before(earliest) {
				earliest = deadline
				hasDeadline = true
			}
		}
	}
	if hasDeadline {
		return context.WithDeadline(base, earliest)
	}
	return context.WithCancel(base)
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
