package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// PresenceNode is the cluster surface required by the presence authority adapter.
type PresenceNode interface {
	NodeID() uint64
	RouteKey(string) (cluster.Route, error)
	RouteKeysPartial([]string) ([]cluster.RouteKeyResult, error)
	RouteHashSlot(uint16) (cluster.Route, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	RegisterRPC(uint8, cluster.NodeRPCHandler)
	WatchRouteAuthorities() <-chan cluster.RouteAuthorityEvent
}

// PresenceAuthorityClient routes UID authority operations to the current slot leader.
type PresenceAuthorityClient struct {
	// node resolves UID routes and carries remote node RPC calls.
	node PresenceNode
	// local handles authority calls when this node is the current leader.
	local accessnode.PresenceAuthority
	// localOwner handles conflict actions owned by this node.
	localOwner accessnode.PresenceOwner
	// remote adapts authority calls to access/node RPC for non-local leaders.
	remote *accessnode.Client

	mu      sync.Mutex
	pending map[presence.PendingRouteToken]pendingRouteRef

	routeRetryBackoff time.Duration
	routeRetrySleep   func(context.Context, time.Duration) error
	endpointObserver  PresenceEndpointLookupObserver
}

const (
	defaultPresenceUnregisterTimeout         = 3 * time.Second
	defaultPresenceRouteRetryAttempts        = 100
	defaultPresenceRouteRetryBackoff         = 5 * time.Millisecond
	defaultPresenceEndpointRetryAttempts     = 50
	defaultPresenceEndpointRetryMaxBackoff   = 100 * time.Millisecond
	defaultPresenceEndpointLeaderConcurrency = 4
)

var errPresenceEndpointLookupPanic = errors.New("internal/infra/cluster: presence endpoint lookup panicked")

const (
	// PresenceEndpointLookupPathLocalBulk identifies one local target-group batch call.
	PresenceEndpointLookupPathLocalBulk = "local_bulk"
	// PresenceEndpointLookupPathRemoteBulk identifies one remote leader envelope call.
	PresenceEndpointLookupPathRemoteBulk = "remote_bulk"
	// PresenceEndpointLookupPathLegacyFallback identifies local authorities without the target-group batch contract.
	PresenceEndpointLookupPathLegacyFallback = "legacy_fallback"

	// PresenceEndpointLookupOutcomeOK reports that every target group succeeded.
	PresenceEndpointLookupOutcomeOK = "ok"
	// PresenceEndpointLookupOutcomePartial reports mixed successful and failed target groups.
	PresenceEndpointLookupOutcomePartial = "partial"
	// PresenceEndpointLookupOutcomeRouteNotReady reports only route-not-ready failures.
	PresenceEndpointLookupOutcomeRouteNotReady = "route_not_ready"
	// PresenceEndpointLookupOutcomeStaleRoute reports only stale-route failures.
	PresenceEndpointLookupOutcomeStaleRoute = "stale_route"
	// PresenceEndpointLookupOutcomeNotLeader reports only not-leader failures.
	PresenceEndpointLookupOutcomeNotLeader = "not_leader"
	// PresenceEndpointLookupOutcomeCanceled reports only canceled failures.
	PresenceEndpointLookupOutcomeCanceled = "canceled"
	// PresenceEndpointLookupOutcomeDeadline reports only deadline failures.
	PresenceEndpointLookupOutcomeDeadline = "deadline"
	// PresenceEndpointLookupOutcomePanic reports a recovered leader execution panic.
	PresenceEndpointLookupOutcomePanic = "panic"
	// PresenceEndpointLookupOutcomeError reports an unclassified or mixed all-error result.
	PresenceEndpointLookupOutcomeError = "error"
)

// PresenceEndpointLookupObservation describes one exact-target leader execution stage.
type PresenceEndpointLookupObservation struct {
	// Path is local_bulk, remote_bulk, or legacy_fallback; it is empty only when
	// selecting the path itself panicked and the stage was contained.
	Path string
	// Outcome is a bounded aggregate result for the leader batch.
	Outcome string
	// StaleRetry reports whether this stage followed a stale target refresh.
	StaleRetry bool
	// Items is the number of non-empty UIDs handled by the stage.
	Items int
	// Groups is the number of exact target groups handled by the stage.
	Groups int
	// Duration is the complete leader execution latency.
	Duration time.Duration
}

// PresenceEndpointLookupObserver receives one aggregate event per leader execution stage.
type PresenceEndpointLookupObserver interface {
	ObservePresenceEndpointLookup(PresenceEndpointLookupObservation)
}

type pendingRouteRef struct {
	uid      string
	rawToken string
}

// NewPresenceAuthorityClient creates an internal presence authority client.
func NewPresenceAuthorityClient(node PresenceNode, local accessnode.PresenceAuthority) *PresenceAuthorityClient {
	return &PresenceAuthorityClient{
		node:    node,
		local:   local,
		remote:  accessnode.NewClient(node),
		pending: make(map[presence.PendingRouteToken]pendingRouteRef),

		routeRetryBackoff: defaultPresenceRouteRetryBackoff,
	}
}

// SetLocalOwner installs the owner-local action handler used for route conflicts.
func (c *PresenceAuthorityClient) SetLocalOwner(owner accessnode.PresenceOwner) {
	if c == nil {
		return
	}
	c.localOwner = owner
}

// SetEndpointLookupObserver installs the aggregate exact-target lookup observer.
// It must be called before concurrent endpoint lookups begin.
func (c *PresenceAuthorityClient) SetEndpointLookupObserver(observer PresenceEndpointLookupObserver) {
	if c == nil {
		return
	}
	c.endpointObserver = observer
}

// ResolveRouteTarget returns the current authority target for uid.
func (c *PresenceAuthorityClient) ResolveRouteTarget(uid string) (presence.RouteTarget, error) {
	if c == nil || c.node == nil {
		return presence.RouteTarget{}, authoritypresence.ErrRouteNotReady
	}
	route, err := c.node.RouteKey(uid)
	if err != nil {
		return presence.RouteTarget{}, mapPresenceRouteError(err)
	}
	if route.Leader == 0 {
		return presence.RouteTarget{}, fmt.Errorf("%w: route leader is unknown", authoritypresence.ErrRouteNotReady)
	}
	return routeTargetFromClusterRoute(route), nil
}

// ResolveRouteTargets returns one aligned authority target result per input UID.
func (c *PresenceAuthorityClient) ResolveRouteTargets(ctx context.Context, uids []string) []presence.RouteTargetResult {
	results := make([]presence.RouteTargetResult, len(uids))
	if len(uids) == 0 {
		return results
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return fillPresenceRouteTargetErrors(results, err)
	}
	if c == nil || c.node == nil {
		return fillPresenceRouteTargetErrors(results, authoritypresence.ErrRouteNotReady)
	}

	routes, err := c.node.RouteKeysPartial(uids)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fillPresenceRouteTargetErrors(results, ctxErr)
	}
	if err != nil {
		return fillPresenceRouteTargetErrors(results, mapPresenceRouteError(err))
	}
	if len(routes) != len(uids) {
		return fillPresenceRouteTargetErrors(results, fmt.Errorf("%w: aligned route result count %d does not match uid count %d", authoritypresence.ErrRouteNotReady, len(routes), len(uids)))
	}

	for i, routeResult := range routes {
		if routeResult.Err != nil {
			results[i].Err = mapPresenceRouteError(routeResult.Err)
			continue
		}
		if routeResult.Route.Leader == 0 {
			results[i].Err = fmt.Errorf("%w: route leader is unknown", authoritypresence.ErrRouteNotReady)
			continue
		}
		results[i].Target = routeTargetFromClusterRoute(routeResult.Route)
	}
	return results
}

// RegisterRoute registers one owner route on the UID authority leader.
func (c *PresenceAuthorityClient) RegisterRoute(ctx context.Context, route presence.Route) (presence.RegisterResult, error) {
	var result presence.RegisterResult
	err := c.withFreshTarget(ctx, route.UID, func(target presence.RouteTarget) error {
		authority, err := c.authorityForTarget(target)
		if err != nil {
			return err
		}
		result, err = authority.RegisterRoute(ctx, target, route)
		return err
	})
	if err != nil {
		return presence.RegisterResult{}, err
	}
	if result.PendingToken != "" {
		rawToken := string(result.PendingToken)
		result.PendingToken = wrapPendingToken(result.PendingToken, route.UID, c)
		c.rememberPending(result.PendingToken, pendingRouteRef{uid: route.UID, rawToken: rawToken})
	}
	return result, nil
}

// CommitRoute commits one pending route token against a freshly resolved target.
func (c *PresenceAuthorityClient) CommitRoute(ctx context.Context, token presence.PendingRouteToken) error {
	ref, ok := c.pendingRef(token)
	if !ok {
		return authoritypresence.ErrRouteNotReady
	}
	err := c.withFreshTarget(ctx, ref.uid, func(target presence.RouteTarget) error {
		authority, err := c.authorityForTarget(target)
		if err != nil {
			return err
		}
		return authority.CommitRoute(ctx, target, ref.rawToken)
	})
	if err == nil {
		c.forgetPending(token)
	}
	return err
}

// AbortRoute aborts one pending route token against a freshly resolved target.
func (c *PresenceAuthorityClient) AbortRoute(ctx context.Context, token presence.PendingRouteToken) error {
	ref, ok := c.pendingRef(token)
	if !ok {
		return authoritypresence.ErrRouteNotReady
	}
	fromAuthority, err := c.withFreshTargetSource(ctx, ref.uid, func(target presence.RouteTarget) error {
		authority, err := c.authorityForTarget(target)
		if err != nil {
			return err
		}
		return authority.AbortRoute(ctx, target, ref.rawToken)
	})
	if err == nil || (fromAuthority && errors.Is(err, authoritypresence.ErrRouteNotReady)) {
		c.forgetPending(token)
	}
	return err
}

// UnregisterRoute removes or tombstones one exact owner route on its UID authority.
func (c *PresenceAuthorityClient) UnregisterRoute(ctx context.Context, identity presence.RouteIdentity, ownerSeq uint64) error {
	return c.withFreshTarget(ctx, identity.UID, func(target presence.RouteTarget) error {
		authority, err := c.authorityForTarget(target)
		if err != nil {
			return err
		}
		return authority.UnregisterRoute(ctx, target, identity, ownerSeq)
	})
}

// EnqueueUnregister performs a bounded best-effort unregister for the presence usecase port.
func (c *PresenceAuthorityClient) EnqueueUnregister(ctx context.Context, identity presence.RouteIdentity, ownerSeq uint64) {
	if ctx == nil {
		ctx = context.Background()
	}
	unregisterCtx, cancel := context.WithTimeout(ctx, defaultPresenceUnregisterTimeout)
	defer cancel()
	_ = c.UnregisterRoute(unregisterCtx, identity, ownerSeq)
}

// EndpointsByUID reads active authority routes for one UID.
func (c *PresenceAuthorityClient) EndpointsByUID(ctx context.Context, uid string) ([]presence.Route, error) {
	var routes []presence.Route
	err := c.withFreshTarget(ctx, uid, func(target presence.RouteTarget) error {
		authority, err := c.authorityForTarget(target)
		if err != nil {
			return err
		}
		routes, err = authority.EndpointsByUID(ctx, target, uid)
		return err
	})
	if err != nil {
		return nil, err
	}
	return routes, nil
}

// EndpointsByUIDs reads active authority routes for multiple UIDs.
func (c *PresenceAuthorityClient) EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]presence.Route, error) {
	out := make(map[string][]presence.Route, len(uids))
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		routes, err := c.EndpointsByUID(ctx, uid)
		if err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], routes...)
	}
	return out, nil
}

// EndpointsByTargets reads aligned endpoint groups through their exact observed targets.
func (c *PresenceAuthorityClient) EndpointsByTargets(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
	if ctx == nil {
		ctx = context.Background()
	}
	results := c.endpointsByTargetsOnce(ctx, groups, false)
	for attempt := 1; attempt < defaultPresenceEndpointRetryAttempts && hasRetryablePresenceEndpointLookup(results); attempt++ {
		if err := c.sleepBeforeEndpointRetry(ctx, attempt); err != nil {
			return replaceRetryablePresenceEndpointErrors(results, err)
		}
		results = c.retryStaleEndpointLookupGroups(ctx, groups, results)
	}
	return results
}

func (c *PresenceAuthorityClient) endpointsByTargetsOnce(ctx context.Context, groups []presence.EndpointLookupGroup, staleRetry bool) []presence.EndpointLookupResult {
	results := make([]presence.EndpointLookupResult, len(groups))
	if len(groups) == 0 {
		return results
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil || c.node == nil {
		return fillPresenceEndpointLookupErrors(results, nil, authoritypresence.ErrRouteNotReady)
	}

	type leaderBatch struct {
		indexes []int
		groups  []presence.EndpointLookupGroup
		items   int
	}

	processLeader := func(leaderNodeID uint64, batch *leaderBatch) {
		resultIndexes := batch.indexes
		resultIndex := func(batchIndex int) int {
			if resultIndexes == nil {
				return batchIndex
			}
			return resultIndexes[batchIndex]
		}
		path := ""
		observer := c.endpointObserver
		var started time.Time
		if observer != nil {
			started = time.Now()
		}
		defer func() {
			if recover() != nil {
				fillPresenceEndpointLookupErrors(results, resultIndexes, errPresenceEndpointLookupPanic)
			}
			if observer != nil {
				observePresenceEndpointLookupSafely(observer, PresenceEndpointLookupObservation{
					Path:       path,
					Outcome:    presenceEndpointLookupOutcome(results, resultIndexes),
					StaleRetry: staleRetry,
					Items:      batch.items,
					Groups:     len(batch.groups),
					Duration:   time.Since(started),
				})
			}
		}()
		localNodeID := c.node.NodeID()
		path = PresenceEndpointLookupPathRemoteBulk
		if leaderNodeID == localNodeID {
			path = PresenceEndpointLookupPathLegacyFallback
			if _, ok := c.local.(accessnode.PresenceTargetBatchAuthority); ok {
				path = PresenceEndpointLookupPathLocalBulk
			}
		}
		if err := ctx.Err(); err != nil {
			fillPresenceEndpointLookupErrors(results, resultIndexes, err)
			return
		}
		if leaderNodeID == localNodeID {
			if targetBatch, ok := c.local.(accessnode.PresenceTargetBatchAuthority); ok {
				localResults := targetBatch.EndpointsByTargets(ctx, batch.groups)
				if len(localResults) != len(batch.groups) {
					err := fmt.Errorf("%w: aligned local endpoint result count %d does not match group count %d", authoritypresence.ErrRouteNotReady, len(localResults), len(batch.groups))
					fillPresenceEndpointLookupErrors(results, resultIndexes, err)
					return
				}
				for i, result := range localResults {
					results[resultIndex(i)] = result
				}
				return
			}
			for i, group := range batch.groups {
				results[resultIndex(i)] = c.endpointsByExactTarget(ctx, group)
			}
			return
		}
		if c.remote == nil {
			fillPresenceEndpointLookupErrors(results, resultIndexes, authoritypresence.ErrRouteNotReady)
			return
		}
		remoteResults, err := c.remote.EndpointsByTargets(ctx, leaderNodeID, batch.groups)
		if err != nil {
			fillPresenceEndpointLookupErrors(results, resultIndexes, err)
			return
		}
		if len(remoteResults) != len(batch.groups) {
			err := fmt.Errorf("%w: aligned endpoint result count %d does not match group count %d", authoritypresence.ErrRouteNotReady, len(remoteResults), len(batch.groups))
			fillPresenceEndpointLookupErrors(results, resultIndexes, err)
			return
		}
		for i, result := range remoteResults {
			results[resultIndex(i)] = result
		}
	}
	if leaderNodeID, items, ok := singlePresenceEndpointLeader(groups); ok {
		processLeader(leaderNodeID, &leaderBatch{
			groups: groups,
			items:  items,
		})
		return results
	}

	byLeader := make(map[uint64]*leaderBatch)
	leaderOrder := make([]uint64, 0)
	for i, group := range groups {
		if len(group.UIDs) == 0 {
			continue
		}
		if group.Target.LeaderNodeID == 0 {
			results[i].Err = fmt.Errorf("%w: endpoint lookup target leader is unknown", authoritypresence.ErrRouteNotReady)
			continue
		}
		batch := byLeader[group.Target.LeaderNodeID]
		if batch == nil {
			batch = &leaderBatch{}
			byLeader[group.Target.LeaderNodeID] = batch
			leaderOrder = append(leaderOrder, group.Target.LeaderNodeID)
		}
		batch.indexes = append(batch.indexes, i)
		batch.groups = append(batch.groups, group)
		batch.items += countNonEmptyPresenceUIDs(group.UIDs)
	}
	runBoundedPresenceLeaderBatches(
		leaderOrder,
		defaultPresenceEndpointLeaderConcurrency,
		func(leaderNodeID uint64) {
			processLeader(leaderNodeID, byLeader[leaderNodeID])
		},
	)
	return results
}

func singlePresenceEndpointLeader(groups []presence.EndpointLookupGroup) (uint64, int, bool) {
	if len(groups) == 0 {
		return 0, 0, false
	}
	var leaderNodeID uint64
	items := 0
	for _, group := range groups {
		if len(group.UIDs) == 0 {
			return 0, 0, false
		}
		if group.Target.LeaderNodeID == 0 {
			return 0, 0, false
		}
		if leaderNodeID == 0 {
			leaderNodeID = group.Target.LeaderNodeID
		} else if group.Target.LeaderNodeID != leaderNodeID {
			return 0, 0, false
		}
		items += countNonEmptyPresenceUIDs(group.UIDs)
	}
	return leaderNodeID, items, leaderNodeID != 0
}

func observePresenceEndpointLookupSafely(observer PresenceEndpointLookupObserver, event PresenceEndpointLookupObservation) {
	if observer == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	observer.ObservePresenceEndpointLookup(event)
}

func runBoundedPresenceLeaderBatches(leaders []uint64, concurrency int, run func(uint64)) {
	if len(leaders) == 0 {
		return
	}
	if concurrency <= 1 || len(leaders) == 1 {
		for _, leaderNodeID := range leaders {
			run(leaderNodeID)
		}
		return
	}
	if concurrency > len(leaders) {
		concurrency = len(leaders)
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	worker := func() {
		for {
			index := int(next.Add(1) - 1)
			if index >= len(leaders) {
				return
			}
			run(leaders[index])
		}
	}
	wg.Add(concurrency - 1)
	for i := 1; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			worker()
		}()
	}
	worker()
	wg.Wait()
}

func (c *PresenceAuthorityClient) retryStaleEndpointLookupGroups(
	ctx context.Context,
	groups []presence.EndpointLookupGroup,
	results []presence.EndpointLookupResult,
) []presence.EndpointLookupResult {
	retryIndexes := make([]int, 0)
	retryUIDs := make([]string, 0)
	retryUIDCounts := make([]int, 0)
	for i, result := range results {
		if !shouldRetryPresenceRouteLookup(result.Err) || i >= len(groups) {
			continue
		}
		groupUIDs := nonEmptyPresenceUIDs(groups[i].UIDs)
		if len(groupUIDs) == 0 {
			results[i] = presence.EndpointLookupResult{}
			continue
		}
		retryIndexes = append(retryIndexes, i)
		retryUIDCounts = append(retryUIDCounts, len(groupUIDs))
		retryUIDs = append(retryUIDs, groupUIDs...)
	}
	if len(retryIndexes) == 0 {
		return results
	}

	refreshedTargets := c.ResolveRouteTargets(ctx, retryUIDs)
	refreshedGroups := make([]presence.EndpointLookupGroup, 0, len(retryIndexes))
	refreshedIndexes := make([]int, 0, len(retryIndexes))
	offset := 0
	for retryIndex, resultIndex := range retryIndexes {
		uidCount := retryUIDCounts[retryIndex]
		next := offset + uidCount
		if next > len(refreshedTargets) {
			results[resultIndex] = presence.EndpointLookupResult{Err: fmt.Errorf("%w: refreshed target result count is not aligned", authoritypresence.ErrRouteNotReady)}
			offset = next
			continue
		}
		target, err := onePresenceRouteTarget(refreshedTargets[offset:next])
		offset = next
		if err != nil {
			results[resultIndex] = presence.EndpointLookupResult{Err: err}
			continue
		}
		refreshedGroups = append(refreshedGroups, presence.EndpointLookupGroup{
			Target: target,
			UIDs:   nonEmptyPresenceUIDs(groups[resultIndex].UIDs),
		})
		refreshedIndexes = append(refreshedIndexes, resultIndex)
	}
	if len(refreshedGroups) == 0 {
		return results
	}

	retried := c.endpointsByTargetsOnce(ctx, refreshedGroups, true)
	for i, resultIndex := range refreshedIndexes {
		if i >= len(retried) {
			results[resultIndex] = presence.EndpointLookupResult{Err: fmt.Errorf("%w: retried endpoint result count is not aligned", authoritypresence.ErrRouteNotReady)}
			continue
		}
		results[resultIndex] = retried[i]
	}
	return results
}

func hasRetryablePresenceEndpointLookup(results []presence.EndpointLookupResult) bool {
	for _, result := range results {
		if shouldRetryPresenceRouteLookup(result.Err) {
			return true
		}
	}
	return false
}

func replaceRetryablePresenceEndpointErrors(results []presence.EndpointLookupResult, err error) []presence.EndpointLookupResult {
	for i, result := range results {
		if shouldRetryPresenceRouteLookup(result.Err) {
			results[i] = presence.EndpointLookupResult{Err: err}
		}
	}
	return results
}

func (c *PresenceAuthorityClient) sleepBeforeEndpointRetry(ctx context.Context, attempt int) error {
	backoff := boundedAuthorityHandoffBackoff(
		c.routeRetryBackoff,
		defaultPresenceEndpointRetryMaxBackoff,
		attempt,
	)
	return c.sleepBeforeRouteRetryFor(ctx, backoff)
}

func nonEmptyPresenceUIDs(uids []string) []string {
	out := make([]string, 0, len(uids))
	for _, uid := range uids {
		if uid != "" {
			out = append(out, uid)
		}
	}
	return out
}

func countNonEmptyPresenceUIDs(uids []string) int {
	count := 0
	for _, uid := range uids {
		if uid != "" {
			count++
		}
	}
	return count
}

func presenceEndpointLookupOutcome(results []presence.EndpointLookupResult, indexes []int) string {
	succeeded := 0
	failed := 0
	commonFailure := ""
	record := func(index int) {
		if index < 0 || index >= len(results) {
			failed++
			commonFailure = PresenceEndpointLookupOutcomeError
			return
		}
		if results[index].Err == nil {
			succeeded++
			return
		}
		failed++
		outcome := presenceEndpointLookupErrorOutcome(results[index].Err)
		if commonFailure == "" {
			commonFailure = outcome
		} else if commonFailure != outcome {
			commonFailure = PresenceEndpointLookupOutcomeError
		}
	}
	if indexes == nil {
		for index := range results {
			record(index)
		}
	} else {
		for _, index := range indexes {
			record(index)
		}
	}
	if failed == 0 {
		return PresenceEndpointLookupOutcomeOK
	}
	if succeeded > 0 {
		return PresenceEndpointLookupOutcomePartial
	}
	if commonFailure == "" {
		return PresenceEndpointLookupOutcomeError
	}
	return commonFailure
}

func presenceEndpointLookupErrorOutcome(err error) string {
	switch {
	case errors.Is(err, errPresenceEndpointLookupPanic):
		return PresenceEndpointLookupOutcomePanic
	case errors.Is(err, context.Canceled):
		return PresenceEndpointLookupOutcomeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return PresenceEndpointLookupOutcomeDeadline
	case errors.Is(err, authoritypresence.ErrRouteNotReady):
		return PresenceEndpointLookupOutcomeRouteNotReady
	case errors.Is(err, authoritypresence.ErrStaleRoute):
		return PresenceEndpointLookupOutcomeStaleRoute
	case errors.Is(err, authoritypresence.ErrNotLeader):
		return PresenceEndpointLookupOutcomeNotLeader
	default:
		return PresenceEndpointLookupOutcomeError
	}
}

func onePresenceRouteTarget(results []presence.RouteTargetResult) (presence.RouteTarget, error) {
	if len(results) == 0 {
		return presence.RouteTarget{}, authoritypresence.ErrRouteNotReady
	}
	target := results[0].Target
	for _, result := range results {
		if result.Err != nil {
			return presence.RouteTarget{}, result.Err
		}
		if result.Target != target {
			return presence.RouteTarget{}, fmt.Errorf("%w: refreshed UIDs no longer share one exact target", authoritypresence.ErrRouteNotReady)
		}
	}
	return target, nil
}

func (c *PresenceAuthorityClient) endpointsByExactTarget(ctx context.Context, group presence.EndpointLookupGroup) presence.EndpointLookupResult {
	if c.local == nil {
		return presence.EndpointLookupResult{Err: authoritypresence.ErrRouteNotReady}
	}
	if batch, ok := c.local.(accessnode.PresenceBatchAuthority); ok {
		routes, err := batch.EndpointsByUIDs(ctx, group.Target, group.UIDs)
		return presence.EndpointLookupResult{Routes: routes, Err: err}
	}
	var routes []presence.Route
	for _, uid := range group.UIDs {
		if uid == "" {
			continue
		}
		uidRoutes, err := c.local.EndpointsByUID(ctx, group.Target, uid)
		if err != nil {
			return presence.EndpointLookupResult{Routes: routes, Err: err}
		}
		routes = append(routes, uidRoutes...)
	}
	return presence.EndpointLookupResult{Routes: routes}
}

// TouchRoutesTo refreshes owner routes in a specific observed authority epoch.
func (c *PresenceAuthorityClient) TouchRoutesTo(ctx context.Context, target presence.RouteTarget, routes []presence.Route) error {
	authority, err := c.authorityForTarget(target)
	if err != nil {
		return err
	}
	return authority.TouchRoutes(ctx, target, routes)
}

// ApplyRouteAction applies a conflict action on the node that owns the real session.
func (c *PresenceAuthorityClient) ApplyRouteAction(ctx context.Context, action presence.RouteAction) error {
	if c == nil || c.node == nil || action.OwnerNodeID == 0 {
		return authoritypresence.ErrRouteNotReady
	}
	if action.OwnerNodeID == c.node.NodeID() {
		if c.localOwner == nil {
			return authoritypresence.ErrRouteNotReady
		}
		return c.localOwner.ApplyRouteAction(ctx, action)
	}
	if c.remote == nil {
		return authoritypresence.ErrRouteNotReady
	}
	return c.remote.ApplyRouteAction(ctx, action.OwnerNodeID, action)
}

func (c *PresenceAuthorityClient) withFreshTarget(ctx context.Context, uid string, call func(presence.RouteTarget) error) error {
	_, err := c.withFreshTargetSource(ctx, uid, call)
	return err
}

func (c *PresenceAuthorityClient) withFreshTargetSource(ctx context.Context, uid string, call func(presence.RouteTarget) error) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fromAuthority := false
	for attempt := 0; attempt < defaultPresenceRouteRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fromAuthority, err
		}
		target, err := c.ResolveRouteTarget(uid)
		if err != nil {
			if attempt+1 < defaultPresenceRouteRetryAttempts && shouldRetryPresenceRouteLookup(err) {
				if sleepErr := c.sleepBeforeRouteRetry(ctx); sleepErr != nil {
					return fromAuthority, sleepErr
				}
				continue
			}
			return fromAuthority, err
		}
		err = call(target)
		fromAuthority = true
		if err == nil {
			return true, nil
		}
		if attempt+1 < defaultPresenceRouteRetryAttempts && shouldRetryPresenceRoute(err) {
			if sleepErr := c.sleepBeforeRouteRetry(ctx); sleepErr != nil {
				return true, sleepErr
			}
			continue
		}
		return true, err
	}
	return fromAuthority, authoritypresence.ErrRouteNotReady
}

func (c *PresenceAuthorityClient) sleepBeforeRouteRetry(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return c.sleepBeforeRouteRetryFor(ctx, c.routeRetryBackoff)
}

func (c *PresenceAuthorityClient) sleepBeforeRouteRetryFor(ctx context.Context, backoff time.Duration) error {
	if c == nil || backoff <= 0 {
		return nil
	}
	if c.routeRetrySleep != nil {
		return c.routeRetrySleep(ctx, backoff)
	}
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *PresenceAuthorityClient) authorityForTarget(target presence.RouteTarget) (accessnode.PresenceAuthority, error) {
	if c == nil {
		return nil, authoritypresence.ErrRouteNotReady
	}
	if c.node != nil && target.LeaderNodeID == c.node.NodeID() {
		if c.local == nil {
			return nil, authoritypresence.ErrRouteNotReady
		}
		return c.local, nil
	}
	if c.remote == nil {
		return nil, authoritypresence.ErrRouteNotReady
	}
	return c.remote, nil
}

func (c *PresenceAuthorityClient) rememberPending(token presence.PendingRouteToken, ref pendingRouteRef) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = make(map[presence.PendingRouteToken]pendingRouteRef)
	}
	c.pending[token] = ref
}

func (c *PresenceAuthorityClient) pendingRef(token presence.PendingRouteToken) (pendingRouteRef, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ref, ok := c.pending[token]
	return ref, ok
}

func (c *PresenceAuthorityClient) forgetPending(token presence.PendingRouteToken) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, token)
}

func routeTargetFromClusterRoute(route cluster.Route) presence.RouteTarget {
	return presence.RouteTarget{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		LeaderTerm:     route.LeaderTerm,
		ConfigEpoch:    route.ConfigEpoch,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}
}

func fillPresenceRouteTargetErrors(results []presence.RouteTargetResult, err error) []presence.RouteTargetResult {
	for i := range results {
		results[i].Err = err
	}
	return results
}

func fillPresenceEndpointLookupErrors(results []presence.EndpointLookupResult, indexes []int, err error) []presence.EndpointLookupResult {
	if indexes == nil {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	for _, index := range indexes {
		results[index].Err = err
	}
	return results
}

func wrapPendingToken(token presence.PendingRouteToken, uid string, c *PresenceAuthorityClient) presence.PendingRouteToken {
	return presence.PendingRouteToken(fmt.Sprintf("%p/%s/%s", c, uid, string(token)))
}

func shouldRetryPresenceRoute(err error) bool {
	return errors.Is(err, authoritypresence.ErrStaleRoute) ||
		errors.Is(err, authoritypresence.ErrNotLeader)
}

func shouldRetryPresenceRouteLookup(err error) bool {
	return shouldRetryPresenceRoute(err) || errors.Is(err, authoritypresence.ErrRouteNotReady)
}

func mapPresenceRouteError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, cluster.ErrRouteNotReady), errors.Is(err, cluster.ErrNoSlotLeader):
		return fmt.Errorf("%w: %w", authoritypresence.ErrRouteNotReady, err)
	case errors.Is(err, cluster.ErrNotLeader):
		return fmt.Errorf("%w: %w", authoritypresence.ErrNotLeader, err)
	default:
		return err
	}
}
