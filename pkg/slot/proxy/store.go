package proxy

import (
	"context"
	"errors"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// Store provides business-level distributed storage APIs
// built on top of raftcluster's generic Propose mechanism.
type Store struct {
	cluster              raftcluster.API
	db                   *metadb.DB
	channelUpdateOverlay ChannelUpdateOverlay
}

type ChannelUpdateOverlay interface {
	BatchGetHotChannelUpdates(ctx context.Context, keys []metadb.ConversationKey) (map[metadb.ConversationKey]metadb.ChannelUpdateLog, error)
}

// New creates a Store.
func New(cluster raftcluster.API, db *metadb.DB) *Store {
	store := &Store{cluster: cluster, db: db}
	if cluster != nil && cluster.RPCMux() != nil {
		cluster.RPCMux().Handle(runtimeMetaRPCServiceID, store.handleRuntimeMetaRPC)
		cluster.RPCMux().Handle(identityRPCServiceID, store.handleIdentityRPC)
		cluster.RPCMux().Handle(subscriberRPCServiceID, store.handleSubscriberRPC)
		cluster.RPCMux().Handle(userConversationStateRPCServiceID, store.handleUserConversationStateRPC)
		cluster.RPCMux().Handle(channelUpdateLogRPCServiceID, store.handleChannelUpdateLogRPC)
	}
	return store
}

func (s *Store) RegisterChannelUpdateOverlay(overlay ChannelUpdateOverlay) {
	if s == nil {
		return
	}
	s.channelUpdateOverlay = overlay
}

func (s *Store) HashSlotTableVersion() uint64 {
	if s == nil || s.cluster == nil {
		return 0
	}
	return s.cluster.HashSlotTableVersion()
}

func (s *Store) CreateChannel(ctx context.Context, channelID string, channelType int64) error {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	cmd := metafsm.EncodeUpsertChannelCommand(metadb.Channel{
		ChannelID:   channelID,
		ChannelType: channelType,
	})
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

func (s *Store) UpdateChannel(ctx context.Context, channelID string, channelType int64, ban int64) error {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	cmd := metafsm.EncodeUpsertChannelCommand(metadb.Channel{
		ChannelID:   channelID,
		ChannelType: channelType,
		Ban:         ban,
	})
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
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

func (s *Store) AddChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string) error {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	cmd := metafsm.EncodeAddSubscribersCommand(channelID, channelType, uids)
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}

func (s *Store) RemoveChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string) error {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	cmd := metafsm.EncodeRemoveSubscribersCommand(channelID, channelType, uids)
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
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
