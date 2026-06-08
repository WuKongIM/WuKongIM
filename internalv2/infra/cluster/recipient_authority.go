package cluster

import (
	"context"
	"errors"
	"fmt"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

// RecipientAuthorityNode is the clusterv2 surface needed for recipient UID authority routing.
type RecipientAuthorityNode interface {
	NodeID() uint64
	RouteKey(string) (clusterv2.Route, error)
	RouteHashSlot(uint16) (clusterv2.Route, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// RecipientAuthorityClient resolves recipient UID authority and forwards remote work.
type RecipientAuthorityClient struct {
	node   RecipientAuthorityNode
	remote *accessnode.Client
}

var _ recipientusecase.RecipientAuthorityResolver = (*RecipientAuthorityClient)(nil)
var _ recipientusecase.RecipientAuthorityValidator = (*RecipientAuthorityClient)(nil)
var _ recipientusecase.RecipientRemote = (*RecipientAuthorityClient)(nil)

// NewRecipientAuthorityClient creates a clusterv2-backed recipient authority client.
func NewRecipientAuthorityClient(node RecipientAuthorityNode, remote *accessnode.Client) *RecipientAuthorityClient {
	if remote == nil {
		remote = accessnode.NewClient(node)
	}
	return &RecipientAuthorityClient{node: node, remote: remote}
}

// ResolveRecipientAuthority resolves uid to the current clusterv2 recipient authority target.
func (c *RecipientAuthorityClient) ResolveRecipientAuthority(ctx context.Context, uid string) (authority.Target, error) {
	if err := contextError(ctx); err != nil {
		return authority.Target{}, err
	}
	if c == nil || c.node == nil {
		return authority.Target{}, recipientusecase.ErrRouteNotReady
	}
	route, err := c.node.RouteKey(uid)
	if err != nil {
		return authority.Target{}, mapRecipientAuthorityRouteError(err)
	}
	if route.Leader == 0 {
		return authority.Target{}, fmt.Errorf("%w: route leader is unknown", recipientusecase.ErrRouteNotReady)
	}
	return recipientAuthorityTargetFromClusterRoute(route), nil
}

// ValidateRecipientAuthority verifies that target still matches this node's current hash-slot authority.
func (c *RecipientAuthorityClient) ValidateRecipientAuthority(ctx context.Context, target authority.Target) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if c == nil || c.node == nil {
		return recipientusecase.ErrRouteNotReady
	}
	if err := target.Validate(); err != nil {
		return recipientusecase.ErrRouteNotReady
	}
	if c.node.NodeID() != target.LeaderNodeID {
		return recipientusecase.ErrNotLeader
	}
	route, err := c.node.RouteHashSlot(target.HashSlot)
	if err != nil {
		return mapRecipientAuthorityRouteError(err)
	}
	if route.Leader == 0 {
		return fmt.Errorf("%w: route leader is unknown", recipientusecase.ErrRouteNotReady)
	}
	current := recipientAuthorityTargetFromClusterRoute(route)
	if current == target {
		return nil
	}
	if current.LeaderNodeID != target.LeaderNodeID {
		return recipientusecase.ErrNotLeader
	}
	return recipientusecase.ErrStaleRoute
}

// ProcessRemote forwards a recipient-authority request to its target leader.
func (c *RecipientAuthorityClient) ProcessRemote(ctx context.Context, req recipientusecase.ProcessRequest) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if c == nil || c.node == nil || c.remote == nil {
		return recipientusecase.ErrRouteNotReady
	}
	if err := req.Target.Validate(); err != nil {
		return recipientusecase.ErrRouteNotReady
	}
	return mapRecipientAuthorityRouteError(c.remote.ProcessRecipientAuthority(ctx, req.Target.LeaderNodeID, req))
}

func recipientAuthorityTargetFromClusterRoute(route clusterv2.Route) authority.Target {
	return authority.Target{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}
}

func mapRecipientAuthorityRouteError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, recipientusecase.ErrNotLeader), errors.Is(err, recipientusecase.ErrStaleRoute), errors.Is(err, recipientusecase.ErrRouteNotReady):
		return err
	case errors.Is(err, clusterv2.ErrRouteNotReady), errors.Is(err, clusterv2.ErrNoSlotLeader):
		return fmt.Errorf("%w: %w", recipientusecase.ErrRouteNotReady, err)
	case errors.Is(err, clusterv2.ErrNotLeader):
		return fmt.Errorf("%w: %w", recipientusecase.ErrNotLeader, err)
	default:
		return err
	}
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
