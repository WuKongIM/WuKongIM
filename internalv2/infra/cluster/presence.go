package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	authoritypresence "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

// PresenceNode is the clusterv2 surface required by the presence authority adapter.
type PresenceNode interface {
	NodeID() uint64
	RouteKey(string) (clusterv2.Route, error)
	RouteHashSlot(uint16) (clusterv2.Route, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	RegisterRPC(uint8, clusterv2.NodeRPCHandler)
	WatchRouteAuthorities() <-chan clusterv2.RouteAuthorityEvent
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
}

const defaultPresenceUnregisterTimeout = 3 * time.Second

type pendingRouteRef struct {
	uid      string
	rawToken string
}

// NewPresenceAuthorityClient creates an internalv2 presence authority client.
func NewPresenceAuthorityClient(node PresenceNode, local accessnode.PresenceAuthority) *PresenceAuthorityClient {
	return &PresenceAuthorityClient{
		node:    node,
		local:   local,
		remote:  accessnode.NewClient(node),
		pending: make(map[presence.PendingRouteToken]pendingRouteRef),
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

// RehydrateRoutesTo replays owner routes into a specific observed authority epoch.
func (c *PresenceAuthorityClient) RehydrateRoutesTo(ctx context.Context, target presence.RouteTarget, routes []presence.Route) ([]presence.RehydrateResult, error) {
	if len(routes) == 0 {
		return nil, nil
	}
	authority, err := c.authorityForTarget(target)
	if err != nil {
		return nil, err
	}
	return authority.RehydrateRoutes(ctx, target, routes)
}

// CommitRehydratedRoute commits one raw pending token returned by RehydrateRoutesTo.
func (c *PresenceAuthorityClient) CommitRehydratedRoute(ctx context.Context, target presence.RouteTarget, token string) error {
	authority, err := c.authorityForTarget(target)
	if err != nil {
		return err
	}
	return authority.CommitRoute(ctx, target, token)
}

// AbortRehydratedRoute aborts one raw pending token returned by RehydrateRoutesTo.
func (c *PresenceAuthorityClient) AbortRehydratedRoute(ctx context.Context, target presence.RouteTarget, token string) error {
	authority, err := c.authorityForTarget(target)
	if err != nil {
		return err
	}
	return authority.AbortRoute(ctx, target, token)
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
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		target, err := c.ResolveRouteTarget(uid)
		if err != nil {
			return false, err
		}
		err = call(target)
		if err == nil {
			return true, nil
		}
		if attempt == 0 && shouldRetryPresenceRoute(err) {
			continue
		}
		return true, err
	}
	return false, authoritypresence.ErrRouteNotReady
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

func routeTargetFromClusterRoute(route clusterv2.Route) presence.RouteTarget {
	return presence.RouteTarget{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}
}

func wrapPendingToken(token presence.PendingRouteToken, uid string, c *PresenceAuthorityClient) presence.PendingRouteToken {
	return presence.PendingRouteToken(fmt.Sprintf("%p/%s/%s", c, uid, string(token)))
}

func shouldRetryPresenceRoute(err error) bool {
	return errors.Is(err, authoritypresence.ErrStaleRoute) || errors.Is(err, authoritypresence.ErrNotLeader)
}

func mapPresenceRouteError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, clusterv2.ErrRouteNotReady), errors.Is(err, clusterv2.ErrNoSlotLeader):
		return fmt.Errorf("%w: %w", authoritypresence.ErrRouteNotReady, err)
	case errors.Is(err, clusterv2.ErrNotLeader):
		return fmt.Errorf("%w: %w", authoritypresence.ErrNotLeader, err)
	default:
		return err
	}
}
