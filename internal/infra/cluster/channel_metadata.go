package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
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

// AuthoritativePermissionBatchNode exposes Slot-grouped permission fact reads.
type AuthoritativePermissionBatchNode interface {
	ReadPermissionMetadataBatchAuthoritative(context.Context, []slotproxy.PermissionMetadataRead) []slotproxy.PermissionMetadataReadResult
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
	UpsertUserChannelMemberships(context.Context, string, int64, []string, uint64, uint64, int64) error
	TombstoneUserChannelMemberships(context.Context, string, int64, []string, uint64, int64) error
}

type committedChannelTailNode interface {
	CommittedChannelTail(context.Context, string, int64) (uint64, error)
}

// PersonDirectoryNode exposes cluster mutations needed to establish canonical
// person-channel discovery before the first persistent ordinary append.
type PersonDirectoryNode interface {
	GetChannelMetadataAuthoritative(context.Context, string, int64) (metadb.Channel, error)
	CommittedChannelTail(context.Context, string, int64) (uint64, error)
	PreparePersonChannelDirectoryBatch(context.Context, []metadb.UserChannelMembership, []metadb.ChannelKey) error
	EnsureChannelDirectoriesReady(context.Context, []metadb.ChannelKey) error
}

// ChannelMetadataStore adapts cluster Slot metadata to the entry-agnostic channel usecase.
type ChannelMetadataStore struct {
	node                ChannelMetadataNode
	membershipNode      ChannelMembershipNode
	appendMetadataCache *ChannelAppendMetadataCache
	personDirectories   *personDirectoryBatcher
}

// NewChannelMetadataStore creates a cluster-backed channel metadata store.
func NewChannelMetadataStore(node ChannelMetadataNode, appendMetadataCache *ChannelAppendMetadataCache) *ChannelMetadataStore {
	membershipNode, _ := node.(ChannelMembershipNode)
	store := &ChannelMetadataStore{node: node, membershipNode: membershipNode, appendMetadataCache: appendMetadataCache}
	if personNode, ok := node.(personDirectoryBatchNode); ok {
		store.personDirectories = newPersonDirectoryBatcher(personNode)
	}
	return store
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

// GetChannelForMessagePull reads terminal channel state without the send
// permission cache so disband takes effect on the authoritative path.
func (s *ChannelMetadataStore) GetChannelForMessagePull(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
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

// ReadPermissionsBatch adapts one usecase-owned raw permission fact batch to
// Slot-grouped authoritative metadata reads without moving policy into infra.
func (s *ChannelMetadataStore) ReadPermissionsBatch(ctx context.Context, reads []messageusecase.PermissionRead) []messageusecase.PermissionReadResult {
	results := make([]messageusecase.PermissionReadResult, len(reads))
	if len(reads) == 0 {
		return results
	}
	if s == nil || s.node == nil {
		return permissionBatchErrorResults(results, clusterpkg.ErrRouteNotReady)
	}
	node, ok := s.node.(AuthoritativePermissionBatchNode)
	if !ok {
		return permissionBatchErrorResults(results, clusterpkg.ErrRouteNotReady)
	}
	proxyReads := make([]slotproxy.PermissionMetadataRead, len(reads))
	for i, read := range reads {
		kind, ok := permissionReadKindToProxy(read.Kind)
		if !ok {
			results[i].Err = metadb.ErrInvalidArgument
			continue
		}
		proxyReads[i] = slotproxy.PermissionMetadataRead{
			Kind: kind, ChannelID: read.ChannelID, ChannelType: read.ChannelType, UID: read.UID,
		}
	}
	proxyResults := node.ReadPermissionMetadataBatchAuthoritative(ctx, proxyReads)
	if len(proxyResults) != len(results) {
		return permissionBatchErrorResults(results, fmt.Errorf("permission metadata batch returned %d results for %d reads", len(proxyResults), len(results)))
	}
	for i, result := range proxyResults {
		results[i] = messageusecase.PermissionReadResult{
			Channel: result.Channel,
			Found:   result.Found,
			Value:   result.Value,
			Err:     mapChannelPermissionReadError(result.Err),
		}
	}
	return results
}

func permissionReadKindToProxy(kind messageusecase.PermissionReadKind) (slotproxy.PermissionMetadataReadKind, bool) {
	switch kind {
	case messageusecase.PermissionReadChannel:
		return slotproxy.PermissionMetadataReadChannel, true
	case messageusecase.PermissionReadSubscriberContains:
		return slotproxy.PermissionMetadataReadSubscriberContains, true
	case messageusecase.PermissionReadSubscriberHasAny:
		return slotproxy.PermissionMetadataReadSubscriberHasAny, true
	default:
		return 0, false
	}
}

func permissionBatchErrorResults(results []messageusecase.PermissionReadResult, err error) []messageusecase.PermissionReadResult {
	mapped := mapChannelPermissionReadError(err)
	for i := range results {
		results[i].Err = mapped
	}
	return results
}

// UpsertChannelMemberships projects normal channel subscribers into UID-owned memberships.
func (s *ChannelMetadataStore) UpsertChannelMemberships(ctx context.Context, channelID string, channelType int64, uids []string, committedTail, sourceVersion uint64, updatedAt int64) error {
	if s == nil || s.membershipNode == nil {
		return metadb.ErrNotFound
	}
	return s.membershipNode.UpsertUserChannelMemberships(ctx, channelID, channelType, append([]string(nil), uids...), committedTail, sourceVersion, updatedAt)
}

// TombstoneChannelMemberships records UID-owned removals for normal subscribers.
func (s *ChannelMetadataStore) TombstoneChannelMemberships(ctx context.Context, channelID string, channelType int64, uids []string, sourceVersion uint64, updatedAt int64) error {
	if s == nil || s.membershipNode == nil {
		return metadb.ErrNotFound
	}
	return s.membershipNode.TombstoneUserChannelMemberships(ctx, channelID, channelType, append([]string(nil), uids...), sourceVersion, updatedAt)
}

// CommittedChannelTail captures the channel boundary used to initialize a
// logical membership add.
func (s *ChannelMetadataStore) CommittedChannelTail(ctx context.Context, channelID string, channelType int64) (uint64, error) {
	if s == nil || s.node == nil {
		return 0, metadb.ErrNotFound
	}
	node, ok := s.node.(committedChannelTailNode)
	if !ok {
		return 0, metadb.ErrInvalidArgument
	}
	return node.CommittedChannelTail(ctx, channelID, channelType)
}

// EnsurePersonChannelDirectory establishes both UID-owned memberships before
// the first persistent ordinary person-channel append. The durable readiness
// bit is monotonic; the node-local cache only skips redundant checks.
func (s *ChannelMetadataStore) EnsurePersonChannelDirectory(ctx context.Context, channelID string, channelType int64) error {
	if s == nil || s.node == nil || channelType != 1 {
		return metadb.ErrInvalidArgument
	}
	id := channelappend.ChannelID{ID: channelID, Type: uint8(channelType)}
	if metadata, ok := s.appendMetadataCache.Lookup(id); ok && metadata.DirectoryReady {
		return nil
	}
	node, ok := s.node.(PersonDirectoryNode)
	if !ok {
		return metadb.ErrInvalidArgument
	}
	channel, err := node.GetChannelMetadataAuthoritative(ctx, channelID, channelType)
	if err == nil && channel.DirectoryReady != 0 {
		s.appendMetadataCache.storeChannel(channel)
		return nil
	}
	if err != nil && !errors.Is(err, metadb.ErrNotFound) {
		return mapChannelPermissionReadError(err)
	}
	left, right, err := runtimechannelid.DecodePersonChannel(channelID)
	if err != nil {
		return err
	}
	tail, err := node.CommittedChannelTail(ctx, channelID, channelType)
	if err != nil {
		return err
	}
	if s.personDirectories == nil {
		return metadb.ErrInvalidArgument
	}
	joinSeq := tail + 1
	if joinSeq == 0 {
		joinSeq = tail
	}
	updatedAt := time.Now().UnixNano()
	mutation := personDirectoryMutation{
		key: metadb.ChannelKey{ChannelID: channelID, ChannelType: channelType},
		memberships: []metadb.UserChannelMembership{
			{UID: left, ChannelID: channelID, ChannelType: channelType, JoinSeq: joinSeq, ReadSeq: tail, DeletedToSeq: tail, SourceVersion: 1, UpdatedAt: updatedAt},
			{UID: right, ChannelID: channelID, ChannelType: channelType, JoinSeq: joinSeq, ReadSeq: tail, DeletedToSeq: tail, SourceVersion: 1, UpdatedAt: updatedAt},
		},
	}
	if err := s.personDirectories.ensure(ctx, mutation); err != nil {
		return err
	}
	channel.ChannelID = channelID
	channel.ChannelType = channelType
	channel.DirectoryReady = 1
	s.appendMetadataCache.storeChannel(channel)
	return nil
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
