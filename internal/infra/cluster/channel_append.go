package cluster

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	channelv2 "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelAppendAuthorityNode is the narrow cluster surface needed to resolve channel append authorities.
type ChannelAppendAuthorityNode interface {
	// NodeID returns the local cluster node id.
	NodeID() uint64
	// ResolveChannelAppendAuthority resolves the ChannelV2 append authority.
	ResolveChannelAppendAuthority(context.Context, channelv2.ChannelID) (channelv2.Meta, error)
	// GetChannelMetadata reads durable channel metadata used by channelappend recipient fanout.
	GetChannelMetadata(context.Context, string, int64) (metadb.Channel, error)
}

// ChannelAppendRemoteForwarder forwards sends to a remote channel authority.
type ChannelAppendRemoteForwarder interface {
	// ForwardSendBatch forwards items to the resolved channel authority target.
	ForwardSendBatch(context.Context, channelappend.AuthorityTarget, []channelappend.SendBatchItem) []channelappend.SendBatchItemResult
}

// ChannelAppendClient adapts cluster channel authority resolution to channelappend ports.
type ChannelAppendClient struct {
	node     ChannelAppendAuthorityNode
	remote   ChannelAppendRemoteForwarder
	metadata *ChannelAppendMetadataCache
}

var _ channelappend.AuthorityResolver = (*ChannelAppendClient)(nil)
var _ channelappend.RemoteForwarder = (*ChannelAppendClient)(nil)

// NewChannelAppendClient creates a cluster-backed channel append client.
func NewChannelAppendClient(node ChannelAppendAuthorityNode, remote ChannelAppendRemoteForwarder, metadata *ChannelAppendMetadataCache) *ChannelAppendClient {
	return &ChannelAppendClient{node: node, remote: remote, metadata: metadata}
}

// ResolveAppendAuthority maps ChannelV2 metadata to a channelappend authority target.
func (c *ChannelAppendClient) ResolveAppendAuthority(ctx context.Context, id channelappend.ChannelID) (channelappend.AuthorityTarget, error) {
	if err := contextError(ctx); err != nil {
		return channelappend.AuthorityTarget{}, err
	}
	if c == nil || c.node == nil {
		return channelappend.AuthorityTarget{}, channelappend.ErrRouteNotReady
	}
	meta, err := c.node.ResolveChannelAppendAuthority(ctx, channelv2.ChannelID{ID: id.ID, Type: id.Type})
	if err != nil {
		return channelappend.AuthorityTarget{}, mapChannelAppendRouteError(err)
	}
	target := channelAppendTargetFromMeta(meta)
	if target.LeaderNodeID == 0 {
		return channelappend.AuthorityTarget{}, fmt.Errorf("%w: channel leader is unknown", channelappend.ErrRouteNotReady)
	}
	if target.ChannelID != id {
		return channelappend.AuthorityTarget{}, channelappend.ErrStaleRoute
	}
	if metadata, ok := c.metadata.Lookup(id); ok {
		applyChannelAppendMetadata(&target, metadata)
		return target, nil
	}
	channel, err := c.node.GetChannelMetadata(ctx, id.ID, int64(id.Type))
	if err != nil && !errors.Is(err, metadb.ErrNotFound) {
		return channelappend.AuthorityTarget{}, mapChannelAppendRouteError(err)
	}
	if err == nil {
		metadata := ChannelAppendMetadata{
			Large:                     channel.Large != 0,
			SubscriberMutationVersion: channel.SubscriberMutationVersion,
		}
		applyChannelAppendMetadata(&target, metadata)
		c.metadata.Store(id, metadata)
	}
	return target, nil
}

func applyChannelAppendMetadata(target *channelappend.AuthorityTarget, metadata ChannelAppendMetadata) {
	target.Large = metadata.Large
	target.SubscriberMutationVersion = metadata.SubscriberMutationVersion
}

// ForwardSendBatch forwards items to a remote channel authority without interpreting results.
func (c *ChannelAppendClient) ForwardSendBatch(ctx context.Context, target channelappend.AuthorityTarget, items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	if err := contextError(ctx); err != nil {
		return channelAppendClusterErrors(len(items), err)
	}
	if c == nil || c.remote == nil {
		return channelAppendClusterErrors(len(items), channelappend.ErrRouteNotReady)
	}
	return c.remote.ForwardSendBatch(ctx, target, items)
}

func channelAppendTargetFromMeta(meta channelv2.Meta) channelappend.AuthorityTarget {
	return channelappend.AuthorityTarget{
		ChannelID:    channelappend.ChannelID{ID: meta.ID.ID, Type: meta.ID.Type},
		ChannelKey:   string(meta.Key),
		LeaderNodeID: uint64(meta.Leader),
		Epoch:        meta.Epoch,
		LeaderEpoch:  meta.LeaderEpoch,
	}
}

func channelAppendClusterErrors(n int, err error) []channelappend.SendBatchItemResult {
	results := make([]channelappend.SendBatchItemResult, n)
	for i := range results {
		results[i].Err = err
	}
	return results
}

func mapChannelAppendRouteError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case appendErrorMatches(err, channelv2.ErrNotLeader), appendErrorMatches(err, propose.ErrNotLeader), errors.Is(err, cluster.ErrNotLeader):
		return fmt.Errorf("%w: %w", channelappend.ErrNotChannelAuthority, err)
	case appendErrorMatches(err, channelv2.ErrStaleMeta):
		return fmt.Errorf("%w: %w", channelappend.ErrStaleRoute, err)
	case appendErrorMatches(err, channelv2.ErrNotReady),
		appendErrorIsChannelPlacementUnavailable(err),
		errors.Is(err, cluster.ErrRouteNotReady),
		errors.Is(err, cluster.ErrNoSlotLeader),
		errors.Is(err, cluster.ErrNotStarted),
		errors.Is(err, cluster.ErrStopping):
		return fmt.Errorf("%w: %w", channelappend.ErrRouteNotReady, err)
	default:
		return err
	}
}
