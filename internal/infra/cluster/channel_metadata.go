package cluster

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
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

type authoritativeChannelMetadataNode interface {
	GetChannelMetadataAuthoritative(context.Context, string, int64) (metadb.Channel, error)
	ListChannelSubscribersAuthoritative(context.Context, string, int64, string, int) ([]string, string, bool, error)
	ContainsChannelSubscriberAuthoritative(context.Context, string, int64, string) (bool, error)
	HasChannelSubscribersAuthoritative(context.Context, string, int64) (bool, error)
}

type countedChannelSubscriberMutationNode interface {
	AddChannelSubscribersCounted(context.Context, string, int64, []string, uint64) (metadb.SubscriberMutationResult, error)
	RemoveChannelSubscribersCounted(context.Context, string, int64, []string, uint64) (metadb.SubscriberMutationResult, error)
}

type conditionalChannelMetadataNode interface {
	CreateChannelMetadataStrict(context.Context, metadb.Channel) error
	PatchChannelBusinessFlags(context.Context, string, int64, metadb.ChannelBusinessFlags) error
}

type restoreChannelSubscriberNode interface {
	ListRestoreChannelSubscribersPage(context.Context, string, int64, string, int) ([]string, string, bool, error)
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

// GetChannel reads channel metadata from the authoritative Slot leader.
func (s *ChannelMetadataStore) GetChannel(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	if s == nil || s.node == nil {
		return metadb.Channel{}, clusterpkg.ErrRouteNotReady
	}
	node, ok := s.node.(authoritativeChannelMetadataNode)
	if !ok {
		return metadb.Channel{}, clusterpkg.ErrRouteNotReady
	}
	return node.GetChannelMetadataAuthoritative(ctx, channelID, channelType)
}

// GetChannelForPermission reads channel metadata for send authorization.
func (s *ChannelMetadataStore) GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	channel, err := s.GetChannel(ctx, channelID, channelType)
	return channel, mapChannelPermissionReadError(err)
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

// CreateChannelStrict creates channel metadata exactly once at the Slot leader.
func (s *ChannelMetadataStore) CreateChannelStrict(ctx context.Context, ch metadb.Channel) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	node, ok := s.node.(conditionalChannelMetadataNode)
	if !ok {
		return metadb.ErrInvalidArgument
	}
	if err := node.CreateChannelMetadataStrict(ctx, ch); err != nil {
		return err
	}
	s.appendMetadataCache.storeChannel(ch)
	return nil
}

// PatchChannelBusinessFlags atomically patches only Manager-editable flags.
func (s *ChannelMetadataStore) PatchChannelBusinessFlags(ctx context.Context, channelID string, channelType int64, flags metadb.ChannelBusinessFlags) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	node, ok := s.node.(conditionalChannelMetadataNode)
	if !ok {
		return metadb.ErrInvalidArgument
	}
	if err := node.PatchChannelBusinessFlags(ctx, channelID, channelType, flags); err != nil {
		return err
	}
	s.appendMetadataCache.Delete(channelappend.ChannelID{ID: channelID, Type: uint8(channelType)})
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

// AddChannelSubscribersCounted adds subscribers and returns the exact durable set changes.
func (s *ChannelMetadataStore) AddChannelSubscribersCounted(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) (metadb.SubscriberMutationResult, error) {
	if s == nil || s.node == nil {
		return metadb.SubscriberMutationResult{}, metadb.ErrNotFound
	}
	node, ok := s.node.(countedChannelSubscriberMutationNode)
	if !ok {
		return metadb.SubscriberMutationResult{}, metadb.ErrInvalidArgument
	}
	return node.AddChannelSubscribersCounted(ctx, channelID, channelType, append([]string(nil), uids...), firstSubscriberMutationVersion(subscriberMutationVersion))
}

// RemoveChannelSubscribersCounted removes subscribers and returns the exact durable set changes.
func (s *ChannelMetadataStore) RemoveChannelSubscribersCounted(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) (metadb.SubscriberMutationResult, error) {
	if s == nil || s.node == nil {
		return metadb.SubscriberMutationResult{}, metadb.ErrNotFound
	}
	node, ok := s.node.(countedChannelSubscriberMutationNode)
	if !ok {
		return metadb.SubscriberMutationResult{}, metadb.ErrInvalidArgument
	}
	return node.RemoveChannelSubscribersCounted(ctx, channelID, channelType, append([]string(nil), uids...), firstSubscriberMutationVersion(subscriberMutationVersion))
}

// ListChannelSubscribers reads one channel subscriber page from the authoritative Slot leader.
func (s *ChannelMetadataStore) ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	if s == nil || s.node == nil {
		return nil, "", false, clusterpkg.ErrRouteNotReady
	}
	node, ok := s.node.(authoritativeChannelMetadataNode)
	if !ok {
		return nil, "", false, clusterpkg.ErrRouteNotReady
	}
	return node.ListChannelSubscribersAuthoritative(ctx, channelID, channelType, afterUID, limit)
}

// ListChannelSubscribersForRestore reads the restored local Slot metadata
// while Controller maintenance still rejects ordinary metadata requests.
func (s *ChannelMetadataStore) ListChannelSubscribersForRestore(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	if s == nil || s.node == nil {
		return nil, "", true, nil
	}
	node, ok := s.node.(restoreChannelSubscriberNode)
	if !ok {
		return s.node.ListChannelSubscribersPage(
			ctx, channelID, channelType, afterUID, limit,
		)
	}
	return node.ListRestoreChannelSubscribersPage(
		ctx, channelID, channelType, afterUID, limit,
	)
}

// ContainsChannelSubscriber performs a subscriber point lookup for send authorization.
func (s *ChannelMetadataStore) ContainsChannelSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error) {
	if uid == "" {
		return false, nil
	}
	if s == nil || s.node == nil {
		return false, mapChannelPermissionReadError(clusterpkg.ErrRouteNotReady)
	}
	node, ok := s.node.(authoritativeChannelMetadataNode)
	if !ok {
		return false, mapChannelPermissionReadError(clusterpkg.ErrRouteNotReady)
	}
	contains, err := node.ContainsChannelSubscriberAuthoritative(
		ctx, channelID, channelType, uid,
	)
	return contains, mapChannelPermissionReadError(err)
}

// HasChannelSubscribers reports whether the channel has at least one subscriber row.
func (s *ChannelMetadataStore) HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error) {
	if s == nil || s.node == nil {
		return false, mapChannelPermissionReadError(clusterpkg.ErrRouteNotReady)
	}
	node, ok := s.node.(authoritativeChannelMetadataNode)
	if !ok {
		return false, mapChannelPermissionReadError(clusterpkg.ErrRouteNotReady)
	}
	hasSubscribers, err := node.HasChannelSubscribersAuthoritative(
		ctx, channelID, channelType,
	)
	return hasSubscribers, mapChannelPermissionReadError(err)
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

func mapChannelPermissionReadError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, clusterpkg.ErrRouteNotReady),
		errors.Is(err, clusterpkg.ErrNoSlotLeader),
		errors.Is(err, clusterpkg.ErrNotStarted),
		errors.Is(err, clusterpkg.ErrStopping),
		errors.Is(err, transport.ErrDialFailed),
		errors.Is(err, transport.ErrNodeNotFound),
		errors.Is(err, transport.ErrStopped):
		return fmt.Errorf("%w: %w", channelappend.ErrRouteNotReady, err)
	default:
		return err
	}
}
