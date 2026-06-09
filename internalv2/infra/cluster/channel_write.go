package cluster

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelwrite"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

// ChannelWriteNode is the narrow clusterv2 surface needed for channel authority writes.
type ChannelWriteNode interface {
	// NodeID returns the local clusterv2 node id.
	NodeID() uint64
	// ResolveChannelAppendAuthority resolves the ChannelV2 append authority.
	ResolveChannelAppendAuthority(context.Context, channelv2.ChannelID) (channelv2.Meta, error)
}

// ChannelWriteRemoteForwarder forwards sends to a remote channel authority.
type ChannelWriteRemoteForwarder interface {
	// ForwardSendBatch forwards items to the resolved channel authority target.
	ForwardSendBatch(context.Context, channelwrite.AuthorityTarget, []channelwrite.SendBatchItem) []channelwrite.SendBatchItemResult
}

// ChannelWriteClient adapts clusterv2 channel authority resolution to channelwrite ports.
type ChannelWriteClient struct {
	node   ChannelWriteNode
	remote ChannelWriteRemoteForwarder
}

var _ channelwrite.AuthorityResolver = (*ChannelWriteClient)(nil)
var _ channelwrite.RemoteForwarder = (*ChannelWriteClient)(nil)

// NewChannelWriteClient creates a clusterv2-backed channel write client.
func NewChannelWriteClient(node ChannelWriteNode, remote ChannelWriteRemoteForwarder) *ChannelWriteClient {
	return &ChannelWriteClient{node: node, remote: remote}
}

// ResolveAppendAuthority maps ChannelV2 metadata to a channelwrite authority target.
func (c *ChannelWriteClient) ResolveAppendAuthority(ctx context.Context, id channelwrite.ChannelID) (channelwrite.AuthorityTarget, error) {
	if err := contextError(ctx); err != nil {
		return channelwrite.AuthorityTarget{}, err
	}
	if c == nil || c.node == nil {
		return channelwrite.AuthorityTarget{}, channelwrite.ErrRouteNotReady
	}
	meta, err := c.node.ResolveChannelAppendAuthority(ctx, channelv2.ChannelID{ID: id.ID, Type: id.Type})
	if err != nil {
		return channelwrite.AuthorityTarget{}, mapChannelWriteRouteError(err)
	}
	target := channelWriteTargetFromMeta(meta)
	if target.LeaderNodeID == 0 {
		return channelwrite.AuthorityTarget{}, fmt.Errorf("%w: channel leader is unknown", channelwrite.ErrRouteNotReady)
	}
	if target.ChannelID != id {
		return channelwrite.AuthorityTarget{}, channelwrite.ErrStaleRoute
	}
	return target, nil
}

// ForwardSendBatch forwards items to a remote channel authority without interpreting results.
func (c *ChannelWriteClient) ForwardSendBatch(ctx context.Context, target channelwrite.AuthorityTarget, items []channelwrite.SendBatchItem) []channelwrite.SendBatchItemResult {
	if err := contextError(ctx); err != nil {
		return channelWriteClusterErrors(len(items), err)
	}
	if c == nil || c.remote == nil {
		return channelWriteClusterErrors(len(items), channelwrite.ErrRouteNotReady)
	}
	return c.remote.ForwardSendBatch(ctx, target, items)
}

func channelWriteTargetFromMeta(meta channelv2.Meta) channelwrite.AuthorityTarget {
	return channelwrite.AuthorityTarget{
		ChannelID:     channelwrite.ChannelID{ID: meta.ID.ID, Type: meta.ID.Type},
		ChannelKey:    string(meta.Key),
		LeaderNodeID:  uint64(meta.Leader),
		Epoch:         meta.Epoch,
		LeaderEpoch:   meta.LeaderEpoch,
		RouteRevision: 0,
	}
}

func channelWriteClusterErrors(n int, err error) []channelwrite.SendBatchItemResult {
	results := make([]channelwrite.SendBatchItemResult, n)
	for i := range results {
		results[i].Err = err
	}
	return results
}

func mapChannelWriteRouteError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case appendErrorMatches(err, channelv2.ErrNotLeader), errors.Is(err, clusterv2.ErrNotLeader):
		return fmt.Errorf("%w: %w", channelwrite.ErrNotChannelAuthority, err)
	case appendErrorMatches(err, channelv2.ErrStaleMeta):
		return fmt.Errorf("%w: %w", channelwrite.ErrStaleRoute, err)
	case appendErrorMatches(err, channelv2.ErrNotReady),
		errors.Is(err, clusterv2.ErrRouteNotReady),
		errors.Is(err, clusterv2.ErrNoSlotLeader),
		errors.Is(err, clusterv2.ErrNotStarted),
		errors.Is(err, clusterv2.ErrStopping):
		return fmt.Errorf("%w: %w", channelwrite.ErrRouteNotReady, err)
	default:
		return err
	}
}
