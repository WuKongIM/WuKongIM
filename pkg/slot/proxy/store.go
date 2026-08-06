package proxy

import (
	"context"
	"errors"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// Store provides business-level distributed storage APIs
// built on top of the cluster metadata proposal port.
type Store struct {
	cluster Cluster
	db      *metadb.DB
}

// New creates a Store.
func New(cluster Cluster, db *metadb.DB) *Store {
	store := &Store{cluster: cluster, db: db}
	registerStoreRPCHandlers(cluster, store)
	return store
}

// NewChannelMetadataStore creates the channel/member subset and registers only
// its non-conflicting authoritative RPC services.
func NewChannelMetadataStore(cluster Cluster, db *metadb.DB) *Store {
	store := &Store{cluster: cluster, db: db}
	registerSelectedStoreRPCHandlers(cluster, []storeRPCRegistration{
		{serviceID: runtimeMetaRPCServiceID, handler: store.handleRuntimeMetaRPC},
		{serviceID: subscriberRPCServiceID, handler: store.handleSubscriberRPC},
		{serviceID: channelRPCServiceID, handler: store.handleChannelRPC},
		{serviceID: membershipRPCServiceID, handler: store.handleMembershipRPC},
	})
	return store
}

func (s *Store) HashSlotTableVersion() uint64 {
	if s == nil || s.cluster == nil {
		return 0
	}
	return s.cluster.HashSlotTableVersion()
}

func (s *Store) CreateChannel(ctx context.Context, channelID string, channelType int64) error {
	return s.UpsertChannel(ctx, metadb.Channel{
		ChannelID:   channelID,
		ChannelType: channelType,
	})
}

func (s *Store) UpdateChannel(ctx context.Context, channelID string, channelType int64, ban int64) error {
	return s.UpsertChannel(ctx, metadb.Channel{
		ChannelID:   channelID,
		ChannelType: channelType,
		Ban:         ban,
	})
}

// UpsertChannel persists all supported channel metadata flags through the authoritative slot.
func (s *Store) UpsertChannel(ctx context.Context, ch metadb.Channel) error {
	slotID := s.cluster.SlotForKey(ch.ChannelID)
	hashSlot := hashSlotForKey(s.cluster, ch.ChannelID)
	cmd := metafsm.EncodeUpsertChannelCommand(ch)
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

// CreateChannelMetadata applies a create-only channel metadata mutation.
func (s *Store) CreateChannelMetadata(ctx context.Context, ch metadb.Channel) error {
	slotID := s.cluster.SlotForKey(ch.ChannelID)
	hashSlot := hashSlotForKey(s.cluster, ch.ChannelID)
	result, err := proposeWithHashSlotResult(
		ctx,
		s.cluster,
		slotID,
		hashSlot,
		metafsm.EncodeCreateChannelCommand(ch),
	)
	if err != nil {
		return err
	}
	applied, err := metafsm.DecodeChannelConditionalMutationResult(result)
	if err != nil {
		return err
	}
	if !applied {
		return metadb.ErrAlreadyExists
	}
	return nil
}

// PatchChannelBusinessFlags applies an existing-only partial channel flag mutation.
func (s *Store) PatchChannelBusinessFlags(ctx context.Context, channelID string, channelType int64, flags metadb.ChannelBusinessFlags) error {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	result, err := proposeWithHashSlotResult(
		ctx,
		s.cluster,
		slotID,
		hashSlot,
		metafsm.EncodePatchChannelBusinessFlagsCommand(channelID, channelType, flags),
	)
	if err != nil {
		return err
	}
	applied, err := metafsm.DecodeChannelConditionalMutationResult(result)
	if err != nil {
		return err
	}
	if !applied {
		return metadb.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteChannel(ctx context.Context, channelID string, channelType int64) error {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	cmd := metafsm.EncodeDeleteChannelCommand(channelID, channelType)
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

func (s *Store) GetChannel(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	hashSlot := hashSlotForKey(s.cluster, channelID)
	return s.db.ForHashSlot(hashSlot).GetChannel(ctx, channelID, channelType)
}

// GetChannelForPermission reads channel metadata from the authoritative slot owner.
func (s *Store) GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).GetChannel(ctx, channelID, channelType)
	}
	return s.getChannelForPermissionAuthoritative(ctx, slotID, hashSlot, channelID, channelType)
}

// ScanChannelsSlotPage reads one authoritative channel page for a physical Slot.
func (s *Store) ScanChannelsSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error) {
	return s.scanChannelsSlotPageAuthoritative(ctx, slotID, after, limit)
}

func (s *Store) AddChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	cmd, err := metafsm.EncodeAddSubscribersCommandChecked(channelID, channelType, uids, subscriberMutationVersion...)
	if err != nil {
		return err
	}
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

// AddChannelSubscribersCounted adds a UID set and returns the exact committed row count.
func (s *Store) AddChannelSubscribersCounted(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) (metadb.SubscriberMutationResult, error) {
	return s.mutateChannelSubscribersCounted(ctx, channelID, channelType, uids, true, subscriberMutationVersion...)
}

func (s *Store) RemoveChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	cmd, err := metafsm.EncodeRemoveSubscribersCommandChecked(channelID, channelType, uids, subscriberMutationVersion...)
	if err != nil {
		return err
	}
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

// RemoveChannelSubscribersCounted removes a UID set and returns the exact committed row count.
func (s *Store) RemoveChannelSubscribersCounted(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) (metadb.SubscriberMutationResult, error) {
	return s.mutateChannelSubscribersCounted(ctx, channelID, channelType, uids, false, subscriberMutationVersion...)
}

func (s *Store) mutateChannelSubscribersCounted(ctx context.Context, channelID string, channelType int64, uids []string, add bool, subscriberMutationVersion ...uint64) (metadb.SubscriberMutationResult, error) {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	var (
		cmd []byte
		err error
	)
	if add {
		cmd, err = metafsm.EncodeAddSubscribersCommandChecked(channelID, channelType, uids, subscriberMutationVersion...)
	} else {
		cmd, err = metafsm.EncodeRemoveSubscribersCommandChecked(channelID, channelType, uids, subscriberMutationVersion...)
	}
	if err != nil {
		return metadb.SubscriberMutationResult{}, err
	}
	resultBytes, err := proposeWithHashSlotResult(ctx, s.cluster, slotID, hashSlot, cmd)
	if err != nil {
		return metadb.SubscriberMutationResult{}, err
	}
	return metafsm.DecodeSubscriberMutationResult(resultBytes)
}

func (s *Store) ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	slotID := s.cluster.SlotForKey(channelID)
	return s.listChannelSubscribersAuthoritative(ctx, slotID, channelID, channelType, afterUID, limit)
}

func (s *Store) UpsertChannelRuntimeMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) error {
	slotID := s.cluster.SlotForKey(meta.ChannelID)
	hashSlot := hashSlotForKey(s.cluster, meta.ChannelID)
	cmd := metafsm.EncodeUpsertChannelRuntimeMetaCommand(meta)
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

// AdvanceChannelRetentionThroughSeq proposes a fenced retention-only metadata update.
func (s *Store) AdvanceChannelRetentionThroughSeq(ctx context.Context, req metadb.ChannelRetentionAdvance) error {
	slotID := s.cluster.SlotForKey(req.ChannelID)
	hashSlot := hashSlotForKey(s.cluster, req.ChannelID)
	cmd := metafsm.EncodeAdvanceChannelRetentionThroughSeqCommand(req)
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

// UpsertChannelRuntimeMetaIfLocalLeader persists runtime metadata only when the
// current slot leader is local to this process.
func (s *Store) UpsertChannelRuntimeMetaIfLocalLeader(ctx context.Context, meta metadb.ChannelRuntimeMeta) error {
	slotID := s.cluster.SlotForKey(meta.ChannelID)
	hashSlot := hashSlotForKey(s.cluster, meta.ChannelID)
	cmd := metafsm.EncodeUpsertChannelRuntimeMetaCommand(meta)
	return proposeLocalWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

func (s *Store) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	return s.getChannelRuntimeMetaAuthoritative(ctx, slotID, hashSlot, channelID, channelType)
}

func (s *Store) ListChannelRuntimeMeta(ctx context.Context) ([]metadb.ChannelRuntimeMeta, error) {
	if s.cluster == nil {
		return s.db.ListChannelRuntimeMeta(ctx)
	}

	metas := make([]metadb.ChannelRuntimeMeta, 0, 16)
	for _, slotID := range s.cluster.SlotIDs() {
		groupMetas, err := s.listChannelRuntimeMetaAuthoritative(ctx, slotID)
		if err != nil {
			return nil, err
		}
		metas = append(metas, groupMetas...)
	}
	return metas, nil
}

// ScanChannelRuntimeMetaSlotPage returns one authoritative page for a physical slot.
func (s *Store) ScanChannelRuntimeMetaSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	return s.scanChannelRuntimeMetaSlotPageAuthoritative(ctx, slotID, after, limit)
}

func (s *Store) UpsertUser(ctx context.Context, u metadb.User) error {
	slotID := s.cluster.SlotForKey(u.UID)
	hashSlot := hashSlotForKey(s.cluster, u.UID)
	cmd := metafsm.EncodeUpsertUserCommand(u)
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

// CreateUser returns ErrAlreadyExists when the authoritative slot already has
// the uid. Under concurrent duplicate creates, the replicated apply path
// treats the later create as a benign no-op to avoid failing the raft slot.
func (s *Store) CreateUser(ctx context.Context, u metadb.User) error {
	slotID := s.cluster.SlotForKey(u.UID)
	hashSlot := hashSlotForKey(s.cluster, u.UID)
	if _, err := s.getUserAuthoritative(ctx, slotID, hashSlot, u.UID); err == nil {
		return metadb.ErrAlreadyExists
	} else if err != nil && !errors.Is(err, metadb.ErrNotFound) {
		return err
	}
	cmd := metafsm.EncodeCreateUserCommand(u)
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

func (s *Store) GetUser(ctx context.Context, uid string) (metadb.User, error) {
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	return s.getUserAuthoritative(ctx, slotID, hashSlot, uid)
}

func (s *Store) UpsertDevice(ctx context.Context, d metadb.Device) error {
	slotID := s.cluster.SlotForKey(d.UID)
	hashSlot := hashSlotForKey(s.cluster, d.UID)
	cmd := metafsm.EncodeUpsertDeviceCommand(d)
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

func (s *Store) GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error) {
	slotID := s.cluster.SlotForKey(uid)
	hashSlot := hashSlotForKey(s.cluster, uid)
	return s.getDeviceAuthoritative(ctx, slotID, hashSlot, uid, deviceFlag)
}
