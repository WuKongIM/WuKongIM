package cluster

import (
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelMetadataNode exposes clusterv2 Slot metadata operations used by the channel usecase.
type ChannelMetadataNode interface {
	GetChannelMetadata(context.Context, string, int64) (metadb.Channel, error)
	UpsertChannelMetadata(context.Context, metadb.Channel) error
	DeleteChannelMetadata(context.Context, string, int64) error
	AddChannelSubscribers(context.Context, string, int64, []string, uint64) error
	RemoveChannelSubscribers(context.Context, string, int64, []string, uint64) error
	ListChannelSubscribersPage(context.Context, string, int64, string, int) ([]string, string, bool, error)
}

// ChannelMembershipNode exposes UID-owned reverse membership projection operations.
type ChannelMembershipNode interface {
	UpsertUserChannelMemberships(context.Context, string, int64, []string, uint64, int64) error
	DeleteUserChannelMemberships(context.Context, string, int64, []string, int64) error
}

// ChannelMetadataStore adapts clusterv2 Slot metadata to the entry-agnostic channel usecase.
type ChannelMetadataStore struct {
	node           ChannelMetadataNode
	membershipNode ChannelMembershipNode
}

// NewChannelMetadataStore creates a clusterv2-backed channel metadata store.
func NewChannelMetadataStore(node ChannelMetadataNode) *ChannelMetadataStore {
	membershipNode, _ := node.(ChannelMembershipNode)
	return &ChannelMetadataStore{node: node, membershipNode: membershipNode}
}

// GetChannel reads channel metadata from the current Slot route.
func (s *ChannelMetadataStore) GetChannel(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	if s == nil || s.node == nil {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return s.node.GetChannelMetadata(ctx, channelID, channelType)
}

// UpsertChannel persists channel metadata through Slot ownership.
func (s *ChannelMetadataStore) UpsertChannel(ctx context.Context, ch metadb.Channel) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.UpsertChannelMetadata(ctx, ch)
}

// DeleteChannel removes channel metadata through Slot ownership.
func (s *ChannelMetadataStore) DeleteChannel(ctx context.Context, channelID string, channelType int64) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.DeleteChannelMetadata(ctx, channelID, channelType)
}

// AddChannelSubscribers appends channel subscribers through Slot ownership.
func (s *ChannelMetadataStore) AddChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.AddChannelSubscribers(ctx, channelID, channelType, append([]string(nil), uids...), firstSubscriberMutationVersion(subscriberMutationVersion))
}

// RemoveChannelSubscribers removes channel subscribers through Slot ownership.
func (s *ChannelMetadataStore) RemoveChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.RemoveChannelSubscribers(ctx, channelID, channelType, append([]string(nil), uids...), firstSubscriberMutationVersion(subscriberMutationVersion))
}

// ListChannelSubscribers reads one channel subscriber page from Slot metadata.
func (s *ChannelMetadataStore) ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	if s == nil || s.node == nil {
		return nil, "", true, nil
	}
	return s.node.ListChannelSubscribersPage(ctx, channelID, channelType, afterUID, limit)
}

// UpsertChannelMemberships projects normal channel subscribers into UID-owned memberships.
func (s *ChannelMetadataStore) UpsertChannelMemberships(ctx context.Context, channelID string, channelType int64, uids []string, joinSeq uint64, updatedAt int64) error {
	if s == nil || s.membershipNode == nil {
		return metadb.ErrNotFound
	}
	return s.membershipNode.UpsertUserChannelMemberships(ctx, channelID, channelType, append([]string(nil), uids...), joinSeq, updatedAt)
}

// DeleteChannelMemberships removes UID-owned memberships for normal channel subscribers.
func (s *ChannelMetadataStore) DeleteChannelMemberships(ctx context.Context, channelID string, channelType int64, uids []string, updatedAt int64) error {
	if s == nil || s.membershipNode == nil {
		return metadb.ErrNotFound
	}
	return s.membershipNode.DeleteUserChannelMemberships(ctx, channelID, channelType, append([]string(nil), uids...), updatedAt)
}

func firstSubscriberMutationVersion(values []uint64) uint64 {
	if len(values) == 0 {
		return 1
	}
	return values[0]
}
