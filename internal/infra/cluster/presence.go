package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
}

const (
	defaultPresenceUnregisterTimeout  = 3 * time.Second
	defaultPresenceRouteRetryAttempts = 100
	defaultPresenceRouteRetryBackoff  = 5 * time.Millisecond
)

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
	if c == nil || c.routeRetryBackoff <= 0 {
		return nil
	}
	if c.routeRetrySleep != nil {
		return c.routeRetrySleep(ctx, c.routeRetryBackoff)
	}
	timer := time.NewTimer(c.routeRetryBackoff)
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
