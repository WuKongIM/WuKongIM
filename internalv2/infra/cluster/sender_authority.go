package cluster

import (
	"context"
	"errors"
	"fmt"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

// SenderAuthorityNode is the clusterv2 surface needed for sender UID authority routing.
type SenderAuthorityNode interface {
	NodeID() uint64
	RouteKey(string) (clusterv2.Route, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	RegisterRPC(uint8, clusterv2.NodeRPCHandler)
}

// SenderAuthorityClient resolves sender UID authority and forwards remote authority sends.
type SenderAuthorityClient struct {
	node   SenderAuthorityNode
	remote *accessnode.Client
}

var _ message.UIDAuthorityResolver = (*SenderAuthorityClient)(nil)
var _ message.RemoteSenderAuthority = (*SenderAuthorityClient)(nil)

// NewSenderAuthorityClient creates a clusterv2-backed sender authority client.
func NewSenderAuthorityClient(node SenderAuthorityNode, remote *accessnode.Client) *SenderAuthorityClient {
	if remote == nil {
		remote = accessnode.NewClient(node)
	}
	return &SenderAuthorityClient{node: node, remote: remote}
}

// ResolveUIDAuthority resolves uid to the current clusterv2 sender authority target.
func (c *SenderAuthorityClient) ResolveUIDAuthority(_ context.Context, uid string) (authority.Target, error) {
	if c == nil || c.node == nil {
		return authority.Target{}, message.ErrRouteNotReady
	}
	route, err := c.node.RouteKey(uid)
	if err != nil {
		return authority.Target{}, mapSenderAuthorityRouteError(err)
	}
	if route.Leader == 0 {
		return authority.Target{}, fmt.Errorf("%w: route leader is unknown", message.ErrRouteNotReady)
	}
	return authority.Target{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}, nil
}

// SendBatchToAuthority forwards items to the target sender authority node.
func (c *SenderAuthorityClient) SendBatchToAuthority(ctx context.Context, target authority.Target, items []message.SendBatchItem) []message.SendBatchItemResult {
	if c == nil || c.node == nil || c.remote == nil {
		return senderAuthorityClusterErrors(len(items), message.ErrRouteNotReady)
	}
	return c.remote.SendBatchToAuthority(ctx, target, items)
}

func senderAuthorityClusterErrors(n int, err error) []message.SendBatchItemResult {
	results := make([]message.SendBatchItemResult, n)
	for i := range results {
		results[i].Err = err
	}
	return results
}

func mapSenderAuthorityRouteError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, clusterv2.ErrRouteNotReady), errors.Is(err, clusterv2.ErrNoSlotLeader):
		return fmt.Errorf("%w: %w", message.ErrRouteNotReady, err)
	case errors.Is(err, clusterv2.ErrNotLeader):
		return fmt.Errorf("%w: %w", message.ErrNotLeader, err)
	default:
		return err
	}
}
