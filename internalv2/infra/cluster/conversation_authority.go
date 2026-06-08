package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ConversationAuthorityNode is the clusterv2 surface needed by authority routing.
type ConversationAuthorityNode interface {
	NodeID() uint64
	RouteKey(string) (clusterv2.Route, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	RegisterRPC(uint8, clusterv2.NodeRPCHandler)
	WatchRouteAuthorities() <-chan clusterv2.RouteAuthorityEvent
}

// ConversationAuthorityClient routes conversation authority operations to UID leaders.
type ConversationAuthorityClient struct {
	node   ConversationAuthorityNode
	local  accessnode.ConversationAuthority
	remote *accessnode.Client

	routeRetryBackoff time.Duration
	routeRetrySleep   func(context.Context, time.Duration) error
}

var _ conversationusecase.Store = (*ConversationAuthorityClient)(nil)

const (
	defaultConversationRouteRetryAttempts = 100
	defaultConversationRouteRetryBackoff  = 5 * time.Millisecond
)

// NewConversationAuthorityClient creates a cluster-routed conversation authority client.
func NewConversationAuthorityClient(node ConversationAuthorityNode, local accessnode.ConversationAuthority) *ConversationAuthorityClient {
	return &ConversationAuthorityClient{
		node:              node,
		local:             local,
		remote:            accessnode.NewClient(node),
		routeRetryBackoff: defaultConversationRouteRetryBackoff,
	}
}

// AdmitPatches groups active patches by exact authority target and forwards each group.
func (c *ConversationAuthorityClient) AdmitPatches(ctx context.Context, patches []conversationusecase.ActivePatch) error {
	if len(patches) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	remaining := append([]conversationusecase.ActivePatch(nil), patches...)
	for attempt := 0; attempt < defaultConversationRouteRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		groups, err := c.groupByTarget(remaining)
		if err != nil {
			if attempt+1 < defaultConversationRouteRetryAttempts && shouldRetryConversationRouteLookup(err) {
				if sleepErr := c.sleepBeforeRouteRetry(ctx); sleepErr != nil {
					return sleepErr
				}
				continue
			}
			return err
		}
		var retry []conversationusecase.ActivePatch
		for i, group := range groups {
			authority, err := c.authorityForTarget(group.target)
			if err != nil {
				return err
			}
			if err := authority.AdmitPatches(ctx, group.target, group.patches); err != nil {
				if attempt+1 < defaultConversationRouteRetryAttempts && shouldRetryConversationRoute(err) {
					retry = pendingConversationPatches(groups[i:])
					break
				}
				return err
			}
		}
		if len(retry) == 0 {
			return nil
		}
		remaining = append(remaining[:0], retry...)
		if sleepErr := c.sleepBeforeRouteRetry(ctx); sleepErr != nil {
			return sleepErr
		}
	}
	return conversationusecase.ErrRouteNotReady
}

// ListUserConversationActiveView reads one UID's active view from the current authority leader.
func (c *ConversationAuthorityClient) ListUserConversationActiveView(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	var page conversationusecase.ActiveViewPage
	err := c.withFreshTarget(ctx, uid, func(target conversationusecase.RouteTarget) error {
		authority, err := c.authorityForTarget(target)
		if err != nil {
			return err
		}
		page, err = authority.ListUserConversationActiveViewForTarget(ctx, target, uid, after, limit)
		return err
	})
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	return page, nil
}

// DrainAuthority drains one exact authority target for handoff.
func (c *ConversationAuthorityClient) DrainAuthority(ctx context.Context, target conversationusecase.RouteTarget) (string, error) {
	authority, err := c.authorityForTarget(target)
	if err != nil {
		return "", err
	}
	return authority.DrainAuthority(ctx, target)
}

type conversationAuthorityPatchGroup struct {
	target  conversationusecase.RouteTarget
	patches []conversationusecase.ActivePatch
}

func (c *ConversationAuthorityClient) groupByTarget(patches []conversationusecase.ActivePatch) ([]conversationAuthorityPatchGroup, error) {
	groupIndex := make(map[conversationusecase.RouteTarget]int)
	groups := make([]conversationAuthorityPatchGroup, 0, len(patches))
	for _, patch := range patches {
		target, err := c.resolve(patch.UID)
		if err != nil {
			return nil, err
		}
		if idx, ok := groupIndex[target]; ok {
			groups[idx].patches = append(groups[idx].patches, patch)
			continue
		}
		groupIndex[target] = len(groups)
		groups = append(groups, conversationAuthorityPatchGroup{
			target:  target,
			patches: []conversationusecase.ActivePatch{patch},
		})
	}
	return groups, nil
}

func pendingConversationPatches(groups []conversationAuthorityPatchGroup) []conversationusecase.ActivePatch {
	var out []conversationusecase.ActivePatch
	for _, group := range groups {
		out = append(out, group.patches...)
	}
	return out
}

func (c *ConversationAuthorityClient) withFreshTarget(ctx context.Context, uid string, call func(conversationusecase.RouteTarget) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for attempt := 0; attempt < defaultConversationRouteRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		target, err := c.resolve(uid)
		if err != nil {
			if attempt+1 < defaultConversationRouteRetryAttempts && shouldRetryConversationRouteLookup(err) {
				if sleepErr := c.sleepBeforeRouteRetry(ctx); sleepErr != nil {
					return sleepErr
				}
				continue
			}
			return err
		}
		if err := call(target); err != nil {
			if attempt+1 < defaultConversationRouteRetryAttempts && shouldRetryConversationRoute(err) {
				if sleepErr := c.sleepBeforeRouteRetry(ctx); sleepErr != nil {
					return sleepErr
				}
				continue
			}
			return err
		}
		return nil
	}
	return conversationusecase.ErrRouteNotReady
}

func (c *ConversationAuthorityClient) resolve(uid string) (conversationusecase.RouteTarget, error) {
	if c == nil || c.node == nil {
		return conversationusecase.RouteTarget{}, conversationusecase.ErrRouteNotReady
	}
	route, err := c.node.RouteKey(uid)
	if err != nil {
		return conversationusecase.RouteTarget{}, mapConversationRouteError(err)
	}
	if route.Leader == 0 {
		return conversationusecase.RouteTarget{}, fmt.Errorf("%w: route leader is unknown", conversationusecase.ErrRouteNotReady)
	}
	return conversationRouteTargetFromClusterRoute(route), nil
}

func (c *ConversationAuthorityClient) authorityForTarget(target conversationusecase.RouteTarget) (accessnode.ConversationAuthority, error) {
	if c == nil {
		return nil, conversationusecase.ErrRouteNotReady
	}
	if c.node == nil {
		return nil, conversationusecase.ErrRouteNotReady
	}
	if target.LeaderNodeID == c.node.NodeID() {
		if c.local == nil {
			return nil, conversationusecase.ErrRouteNotReady
		}
		return c.local, nil
	}
	if c.remote == nil {
		return nil, conversationusecase.ErrRouteNotReady
	}
	return remoteConversationAuthority{client: c.remote, nodeID: target.LeaderNodeID}, nil
}

func (c *ConversationAuthorityClient) sleepBeforeRouteRetry(ctx context.Context) error {
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

func conversationRouteTargetFromClusterRoute(route clusterv2.Route) conversationusecase.RouteTarget {
	return conversationusecase.RouteTarget{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}
}

func shouldRetryConversationRoute(err error) bool {
	return errors.Is(err, conversationusecase.ErrStaleRoute) ||
		errors.Is(err, conversationusecase.ErrNotLeader) ||
		errors.Is(err, conversationusecase.ErrRouteNotReady)
}

func shouldRetryConversationRouteLookup(err error) bool {
	return shouldRetryConversationRoute(err)
}

func mapConversationRouteError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, clusterv2.ErrRouteNotReady), errors.Is(err, clusterv2.ErrNoSlotLeader):
		return fmt.Errorf("%w: %w", conversationusecase.ErrRouteNotReady, err)
	case errors.Is(err, clusterv2.ErrNotLeader):
		return fmt.Errorf("%w: %w", conversationusecase.ErrNotLeader, err)
	default:
		return err
	}
}

type remoteConversationAuthority struct {
	client *accessnode.Client
	nodeID uint64
}

func (a remoteConversationAuthority) AdmitPatches(ctx context.Context, target conversationusecase.RouteTarget, patches []conversationusecase.ActivePatch) error {
	if a.client == nil || a.nodeID == 0 {
		return conversationusecase.ErrRouteNotReady
	}
	return a.client.AdmitConversationPatches(ctx, a.nodeID, target, patches)
}

func (a remoteConversationAuthority) ListUserConversationActiveViewForTarget(ctx context.Context, target conversationusecase.RouteTarget, uid string, after metadb.UserConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	if a.client == nil || a.nodeID == 0 {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrRouteNotReady
	}
	return a.client.ListConversations(ctx, a.nodeID, target, uid, after, limit)
}

func (a remoteConversationAuthority) DrainAuthority(ctx context.Context, target conversationusecase.RouteTarget) (string, error) {
	if a.client == nil || a.nodeID == 0 {
		return "", conversationusecase.ErrRouteNotReady
	}
	return a.client.DrainConversationAuthority(ctx, a.nodeID, target)
}
