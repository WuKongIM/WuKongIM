package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelMetadataNode exposes cluster Slot metadata operations used by the channel usecase.
type ChannelMetadataNode interface {
	GetChannelMetadata(context.Context, string, int64) (metadb.Channel, error)
	UpsertChannelMetadata(context.Context, metadb.Channel) error
	DeleteChannelMetadata(context.Context, string, int64) error
	AddChannelSubscribers(context.Context, string, int64, []string, uint64) error
	RemoveChannelSubscribers(context.Context, string, int64, []string, uint64) error
	ListChannelSubscribersPage(context.Context, string, int64, string, int) ([]string, string, bool, error)
}

type channelSubscriberLookupNode interface {
	ContainsChannelSubscriber(context.Context, string, int64, string) (bool, error)
	HasChannelSubscribers(context.Context, string, int64) (bool, error)
}

// ChannelMembershipNode exposes UID-owned reverse membership projection operations.
type ChannelMembershipNode interface {
	UpsertUserChannelMemberships(context.Context, string, int64, []string, uint64, int64) error
	DeleteUserChannelMemberships(context.Context, string, int64, []string, int64) error
}

// ChannelMetadataStore adapts cluster Slot metadata to the entry-agnostic channel usecase.
type ChannelMetadataStore struct {
	node                ChannelMetadataNode
	membershipNode      ChannelMembershipNode
	appendMetadataCache *ChannelAppendMetadataCache
}

// NewChannelMetadataStore creates a cluster-backed channel metadata store.
func NewChannelMetadataStore(node ChannelMetadataNode, appendMetadataCache *ChannelAppendMetadataCache) *ChannelMetadataStore {
	membershipNode, _ := node.(ChannelMembershipNode)
	return &ChannelMetadataStore{node: node, membershipNode: membershipNode, appendMetadataCache: appendMetadataCache}
}

// GetChannel reads channel metadata from the current Slot route.
func (s *ChannelMetadataStore) GetChannel(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	if s == nil || s.node == nil {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return s.node.GetChannelMetadata(ctx, channelID, channelType)
}

// GetChannelForPermission reads channel metadata for send authorization.
func (s *ChannelMetadataStore) GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	return s.GetChannel(ctx, channelID, channelType)
}

// UpsertChannel persists channel metadata through Slot ownership.
func (s *ChannelMetadataStore) UpsertChannel(ctx context.Context, ch metadb.Channel) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	if err := s.node.UpsertChannelMetadata(ctx, ch); err != nil {
		return err
	}
	s.appendMetadataCache.storeChannel(ch)
	return nil
}

// DeleteChannel removes channel metadata through Slot ownership.
func (s *ChannelMetadataStore) DeleteChannel(ctx context.Context, channelID string, channelType int64) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	if err := s.node.DeleteChannelMetadata(ctx, channelID, channelType); err != nil {
		return err
	}
	s.appendMetadataCache.Delete(channelappend.ChannelID{ID: channelID, Type: uint8(channelType)})
	return nil
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

// ContainsChannelSubscriber performs a subscriber point lookup for send authorization.
func (s *ChannelMetadataStore) ContainsChannelSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error) {
	if s == nil || s.node == nil || uid == "" {
		return false, nil
	}
	if lookup, ok := s.node.(channelSubscriberLookupNode); ok {
		return lookup.ContainsChannelSubscriber(ctx, channelID, channelType, uid)
	}
	afterUID := ""
	for {
		uids, cursor, done, err := s.node.ListChannelSubscribersPage(ctx, channelID, channelType, afterUID, 512)
		if err != nil {
			return false, err
		}
		for _, next := range uids {
			if next == uid {
				return true, nil
			}
		}
		if done || cursor == "" || cursor == afterUID {
			return false, nil
		}
		afterUID = cursor
	}
}

// HasChannelSubscribers reports whether the channel has at least one subscriber row.
func (s *ChannelMetadataStore) HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error) {
	if s == nil || s.node == nil {
		return false, nil
	}
	if lookup, ok := s.node.(channelSubscriberLookupNode); ok {
		return lookup.HasChannelSubscribers(ctx, channelID, channelType)
	}
	uids, _, _, err := s.node.ListChannelSubscribersPage(ctx, channelID, channelType, "", 1)
	if err != nil {
		return false, err
	}
	return len(uids) > 0, nil
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
