package fsm

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/hashslot"
)

// Wire format (version 1):
//
//	[version:1][cmdType:1][TLV fields...]
//
// Each TLV field:
//
//	[tag:1][length:4 big-endian][value:length bytes]
//
// Unknown tags are skipped by the decoder, so new fields can be added
// without breaking older readers (forward-compatible).

const (
	commandVersion uint8 = 1

	cmdTypeUpsertUser                uint8 = 1
	cmdTypeUpsertChannel             uint8 = 2
	cmdTypeDeleteChannel             uint8 = 3
	cmdTypeUpsertChannelRuntimeMeta  uint8 = 4
	cmdTypeDeleteChannelRuntimeMeta  uint8 = 5
	cmdTypeCreateUser                uint8 = 6
	cmdTypeUpsertDevice              uint8 = 7
	cmdTypeAddSubscribers            uint8 = 8
	cmdTypeRemoveSubscribers         uint8 = 9
	cmdTypeUpsertConversationStates  uint8 = 10
	cmdTypeTouchConversationActiveAt uint8 = 11
	cmdTypeClearConversationActiveAt uint8 = 12
	// Deprecated: reserved for the removed durable conversation projection. Do not reuse.
	cmdTypeReservedConversationProjectionUpsert uint8 = 13
	// Deprecated: reserved for the removed durable conversation projection. Do not reuse.
	cmdTypeReservedConversationProjectionDelete uint8 = 14
	cmdTypeAdvanceChannelRetention              uint8 = 15
	cmdTypeHideConversations                    uint8 = 16
	cmdTypeNoop                                 uint8 = 19
	cmdTypeUpsertUserChannelMemberships         uint8 = 44
	cmdTypeDeleteUserChannelMemberships         uint8 = 45
	cmdTypeUpsertChannelLatest                  uint8 = 46
	cmdTypeUpsertChannelLatestBatch             uint8 = 47
	cmdTypeAppendMessageEvent                   uint8 = 48
	cmdTypeAppendMessageEventsBatch             uint8 = 49
	cmdTypeBindPluginUser                       uint8 = 42
	cmdTypeUnbindPluginUser                     uint8 = 43

	// User field tags.
	tagUserUID         uint8 = 1
	tagUserToken       uint8 = 2
	tagUserDeviceFlag  uint8 = 3
	tagUserDeviceLevel uint8 = 4

	// Device field tags.
	tagDeviceUID   uint8 = 1
	tagDeviceFlag  uint8 = 2
	tagDeviceToken uint8 = 3
	tagDeviceLevel uint8 = 4

	// Channel field tags.
	tagChannelID            uint8 = 1
	tagChannelType          uint8 = 2
	tagChannelBan           uint8 = 3
	tagChannelDisband       uint8 = 4
	tagChannelSendBan       uint8 = 5
	tagChannelAllowStranger uint8 = 6
	tagChannelLarge         uint8 = 7

	// Channel runtime metadata field tags.
	tagRuntimeMetaChannelID            uint8 = 1
	tagRuntimeMetaChannelType          uint8 = 2
	tagRuntimeMetaChannelEpoch         uint8 = 3
	tagRuntimeMetaLeaderEpoch          uint8 = 4
	tagRuntimeMetaReplicas             uint8 = 5
	tagRuntimeMetaISR                  uint8 = 6
	tagRuntimeMetaLeader               uint8 = 7
	tagRuntimeMetaMinISR               uint8 = 8
	tagRuntimeMetaStatus               uint8 = 9
	tagRuntimeMetaFeatures             uint8 = 10
	tagRuntimeMetaLeaseUntilMS         uint8 = 11
	tagRuntimeMetaRetentionThroughSeq  uint8 = 12
	tagRuntimeMetaRetentionUpdatedAtMS uint8 = 13
	tagRuntimeMetaWriteFenceToken      uint8 = 14
	tagRuntimeMetaWriteFenceVersion    uint8 = 15
	tagRuntimeMetaWriteFenceReason     uint8 = 16
	tagRuntimeMetaWriteFenceUntilMS    uint8 = 17
	tagRuntimeMetaRouteGeneration      uint8 = 18

	// Channel retention advance field tags.
	tagRetentionAdvanceChannelID            uint8 = 1
	tagRetentionAdvanceChannelType          uint8 = 2
	tagRetentionAdvanceExpectedChannelEpoch uint8 = 3
	tagRetentionAdvanceExpectedLeaderEpoch  uint8 = 4
	tagRetentionAdvanceExpectedLeader       uint8 = 5
	tagRetentionAdvanceExpectedLeaseUntilMS uint8 = 6
	tagRetentionAdvanceThroughSeq           uint8 = 7
	tagRetentionAdvanceUpdatedAtMS          uint8 = 8

	// Subscriber field tags.
	tagSubscriberChannelID       uint8 = 1
	tagSubscriberChannelType     uint8 = 2
	tagSubscriberUIDs            uint8 = 3
	tagSubscriberMutationVersion uint8 = 4

	// User channel membership field tags.
	tagUserChannelMembershipCommandEntry uint8 = 1
	tagUserChannelMembershipEntryUID     uint8 = 1
	tagUserChannelMembershipChannelID    uint8 = 2
	tagUserChannelMembershipChannelType  uint8 = 3
	tagUserChannelMembershipJoinSeq      uint8 = 4
	tagUserChannelMembershipUpdatedAt    uint8 = 5

	// Channel latest field tags.
	tagChannelLatestChannelID      uint8 = 1
	tagChannelLatestChannelType    uint8 = 2
	tagChannelLatestLastMessageID  uint8 = 3
	tagChannelLatestLastMessageSeq uint8 = 4
	tagChannelLatestLastAt         uint8 = 5
	tagChannelLatestFromUID        uint8 = 6
	tagChannelLatestClientMsgNo    uint8 = 7
	tagChannelLatestPayload        uint8 = 8
	tagChannelLatestUpdatedAt      uint8 = 9
	tagChannelLatestBatchEntry     uint8 = 10

	// Channel latest batch entry field tags.
	tagChannelLatestBatchEntryHashSlot uint8 = 1
	tagChannelLatestBatchEntryRecord   uint8 = 2

	// Conversation state field tags.
	tagConversationStateCommandEntry      uint8 = 1
	tagConversationStateEntryUID          uint8 = 1
	tagConversationStateEntryChannelID    uint8 = 2
	tagConversationStateEntryChannelType  uint8 = 3
	tagConversationStateEntryReadSeq      uint8 = 4
	tagConversationStateEntryDeletedToSeq uint8 = 5
	tagConversationStateEntryActiveAt     uint8 = 6
	tagConversationStateEntryUpdatedAt    uint8 = 7
	tagConversationStateEntrySparseActive uint8 = 8
	tagConversationStateEntryHashSlot     uint8 = 9

	// Conversation active patch field tags.
	tagConversationActivePatchCommandEntry     uint8 = 1
	tagConversationActivePatchEntryUID         uint8 = 1
	tagConversationActivePatchEntryChannelID   uint8 = 2
	tagConversationActivePatchEntryChannelType uint8 = 3
	tagConversationActivePatchEntryActiveAt    uint8 = 4
	tagConversationActivePatchEntryMessageSeq  uint8 = 5
	tagConversationActivePatchSparseActive     uint8 = 6
	tagConversationActivePatchSparseActiveSet  uint8 = 7
	tagConversationActivePatchHashSlot         uint8 = 8
	tagConversationActivePatchReadSeq          uint8 = 9
	tagConversationActivePatchDeletedToSeq     uint8 = 10
	tagConversationActivePatchUpdatedAt        uint8 = 11

	// Conversation delete field tags.
	tagConversationDeleteCommandEntry      uint8 = 1
	tagConversationDeleteEntryUID          uint8 = 1
	tagConversationDeleteEntryChannelID    uint8 = 2
	tagConversationDeleteEntryChannelType  uint8 = 3
	tagConversationDeleteEntryDeletedToSeq uint8 = 4
	tagConversationDeleteEntryUpdatedAt    uint8 = 5
	tagConversationDeleteEntryHashSlot     uint8 = 6

	// Conversation entry field tags shared by state, patch, and delete records.
	tagConversationEntryKind uint8 = 12

	// Clear conversation active command field tags.
	tagClearConversationActiveKind uint8 = 1
	tagClearConversationActiveUID  uint8 = 2
	tagClearConversationActiveKey  uint8 = 3

	// Conversation key field tags.
	tagConversationKeyEntryChannelID   uint8 = 1
	tagConversationKeyEntryChannelType uint8 = 2

	// Plugin user binding field tags.
	tagPluginUserBindingUID         uint8 = 1
	tagPluginUserBindingPluginNo    uint8 = 2
	tagPluginUserBindingCreatedAtMS uint8 = 3
	tagPluginUserBindingUpdatedAtMS uint8 = 4

	// ApplyResultOK is the result returned by Apply/ApplyBatch on success.
	ApplyResultOK = "ok"
	// ApplyResultHashSlotFenced reports a committed source write rejected by a migration fence.
	ApplyResultHashSlotFenced = "hash_slot_fenced"
	// ApplyResultStaleMeta reports a deterministic stale metadata no-op.
	ApplyResultStaleMeta = "stale_meta"

	// headerSize is version (1) + cmdType (1).
	headerSize = 2
	// tlvOverhead is tag (1) + length (4).
	tlvOverhead = 5

	// MaxSubscriberCommandUIDs bounds one subscriber Raft command by UID count.
	MaxSubscriberCommandUIDs = 1000
	// MaxSubscriberCommandUIDBytes bounds one subscriber Raft command by encoded UID bytes.
	MaxSubscriberCommandUIDBytes = 64 * 1024
)

// command is the decoded representation of a state machine command.
// Each command type implements this interface, carrying its own typed
// payload and knowing how to apply itself to a WriteBatch.
type command interface {
	apply(wb *metadb.WriteBatch, hashSlot uint16) error
}

type resultCommand interface {
	applyResult() []byte
}

type scopedHashSlotCommand interface {
	applyHashSlots(envelopeHashSlot uint16) []uint16
}

type hashSlotFilteredCommand interface {
	applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error
}

// commandDecoder parses TLV fields after the header into a typed command.
type commandDecoder func(data []byte) (command, error)

// commandDecoders maps command type bytes to their decoders.
// To add a new command type, create a struct implementing command,
// a corresponding encode function, a decoder, and register it here.
var commandDecoders = map[uint8]commandDecoder{
	cmdTypeUpsertUser:                           decodeUpsertUser,
	cmdTypeUpsertChannel:                        decodeUpsertChannel,
	cmdTypeDeleteChannel:                        decodeDeleteChannel,
	cmdTypeUpsertChannelRuntimeMeta:             decodeUpsertChannelRuntimeMeta,
	cmdTypeDeleteChannelRuntimeMeta:             decodeDeleteChannelRuntimeMeta,
	cmdTypeCreateUser:                           decodeCreateUser,
	cmdTypeUpsertDevice:                         decodeUpsertDevice,
	cmdTypeAddSubscribers:                       decodeAddSubscribers,
	cmdTypeRemoveSubscribers:                    decodeRemoveSubscribers,
	cmdTypeUpsertConversationStates:             decodeUpsertConversationStates,
	cmdTypeTouchConversationActiveAt:            decodeTouchConversationActiveAt,
	cmdTypeClearConversationActiveAt:            decodeClearConversationActiveAt,
	cmdTypeReservedConversationProjectionUpsert: decodeReservedConversationProjection,
	cmdTypeReservedConversationProjectionDelete: decodeReservedConversationProjection,
	cmdTypeAdvanceChannelRetention:              decodeAdvanceChannelRetentionThroughSeq,
	cmdTypeHideConversations:                    decodeHideConversations,
	cmdTypeNoop:                                 decodeNoop,
	cmdTypeUpsertUserChannelMemberships:         decodeUpsertUserChannelMemberships,
	cmdTypeDeleteUserChannelMemberships:         decodeDeleteUserChannelMemberships,
	cmdTypeUpsertChannelLatest:                  decodeUpsertChannelLatest,
	cmdTypeUpsertChannelLatestBatch:             decodeUpsertChannelLatestBatch,
	cmdTypeAppendMessageEvent:                   decodeAppendMessageEvent,
	cmdTypeAppendMessageEventsBatch:             decodeAppendMessageEventsBatch,
	cmdTypeBindPluginUser:                       decodeBindPluginUser,
	cmdTypeUnbindPluginUser:                     decodeUnbindPluginUser,
	cmdTypeApplyDelta:                           decodeApplyDelta,
	cmdTypeEnterFence:                           decodeEnterFence,
	cmdTypeAckMigrationOutbox:                   decodeAckMigrationOutbox,
	cmdTypeCleanupMigrationOutbox:               decodeCleanupMigrationOutbox,
	cmdTypeCreateChannelMigrationTask:           decodeCreateChannelMigrationTask,
	cmdTypeClaimChannelMigrationTask:            decodeClaimChannelMigrationTask,
	cmdTypeAdvanceChannelMigrationTask:          decodeAdvanceChannelMigrationTask,
	cmdTypeSetChannelWriteFence:                 decodeSetChannelWriteFence,
	cmdTypeResetChannelWriteFence:               decodeResetChannelWriteFence,
	cmdTypeCommitChannelLeaderTransfer:          decodeCommitChannelLeaderTransfer,
	cmdTypeAddChannelLearner:                    decodeAddChannelLearner,
	cmdTypePromoteLearnerAndRemoveReplica:       decodePromoteLearnerAndRemoveReplica,
	cmdTypeClearChannelWriteFence:               decodeClearChannelWriteFence,
	cmdTypeAbortChannelMigration:                decodeAbortChannelMigration,
	cmdTypeGarbageCollectMigrationTasks:         decodeGarbageCollectMigrationTasks,
	cmdTypeCreateChannelMigrationGuarded:        decodeCreateChannelMigrationTaskWithRuntimeGuard,
}

// --- Noop ---

type noopCmd struct{}

func (*noopCmd) apply(*metadb.WriteBatch, uint16) error { return nil }

// EncodeNoopCommand encodes a side-effect-free Slot write probe command.
func EncodeNoopCommand() []byte {
	return []byte{commandVersion, cmdTypeNoop}
}

// --- UpsertUser ---

type upsertUserCmd struct {
	user metadb.User
}

func (c *upsertUserCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.UpsertUser(hashSlot, c.user)
}

// --- CreateUser ---

type createUserCmd struct {
	user metadb.User
}

func (c *createUserCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.CreateUser(hashSlot, c.user)
}

// --- UpsertDevice ---

type upsertDeviceCmd struct {
	device metadb.Device
}

func (c *upsertDeviceCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.UpsertDevice(hashSlot, c.device)
}

// --- UpsertChannel ---

type upsertChannelCmd struct {
	channel metadb.Channel
}

func (c *upsertChannelCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.UpsertChannel(hashSlot, c.channel)
}

// --- DeleteChannel ---

type deleteChannelCmd struct {
	channelID   string
	channelType int64
}

func (c *deleteChannelCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.DeleteChannel(hashSlot, c.channelID, c.channelType)
}

// --- UpsertChannelRuntimeMeta ---

type upsertChannelRuntimeMetaCmd struct {
	meta metadb.ChannelRuntimeMeta
}

func (c *upsertChannelRuntimeMetaCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.UpsertChannelRuntimeMeta(hashSlot, c.meta)
}

// --- DeleteChannelRuntimeMeta ---

type deleteChannelRuntimeMetaCmd struct {
	channelID   string
	channelType int64
}

func (c *deleteChannelRuntimeMetaCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.DeleteChannelRuntimeMeta(hashSlot, c.channelID, c.channelType)
}

// --- AdvanceChannelRetentionThroughSeq ---

type advanceChannelRetentionThroughSeqCmd struct {
	req metadb.ChannelRetentionAdvance
}

func (c *advanceChannelRetentionThroughSeqCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.AdvanceChannelRetentionThroughSeq(hashSlot, c.req)
}

// --- AddSubscribers ---

type addSubscribersCmd struct {
	channelID                 string
	channelType               int64
	uids                      []string
	subscriberMutationVersion uint64
}

func (c *addSubscribersCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.AddSubscribers(hashSlot, c.channelID, c.channelType, c.uids, c.subscriberMutationVersion)
}

// --- RemoveSubscribers ---

type removeSubscribersCmd struct {
	channelID                 string
	channelType               int64
	uids                      []string
	subscriberMutationVersion uint64
}

func (c *removeSubscribersCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.RemoveSubscribers(hashSlot, c.channelID, c.channelType, c.uids, c.subscriberMutationVersion)
}

// --- UserChannelMemberships ---

type upsertUserChannelMembershipsCmd struct {
	memberships []metadb.UserChannelMembership
}

func (c *upsertUserChannelMembershipsCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, membership := range c.memberships {
		if err := wb.UpsertUserChannelMembership(hashSlot, membership); err != nil {
			return err
		}
	}
	return nil
}

type deleteUserChannelMembershipsCmd struct {
	memberships []metadb.UserChannelMembership
}

func (c *deleteUserChannelMembershipsCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, membership := range c.memberships {
		if err := wb.DeleteUserChannelMembership(hashSlot, membership.UID, metadb.ConversationKey{ChannelID: membership.ChannelID, ChannelType: membership.ChannelType}); err != nil {
			return err
		}
	}
	return nil
}

// --- ChannelLatest ---

type upsertChannelLatestCmd struct {
	latest metadb.ChannelLatest
}

// ChannelLatestBatchItem carries one channel latest row and its logical hash slot.
type ChannelLatestBatchItem struct {
	// HashSlot is the logical hash slot that owns Latest.ChannelID.
	HashSlot uint16
	// Latest is the channel-owned latest message projection row.
	Latest metadb.ChannelLatest
}

// ConversationStateBatchItem carries one UID-owned conversation state row and its logical hash slot.
type ConversationStateBatchItem struct {
	// HashSlot is the logical hash slot that owns State.UID.
	HashSlot uint16
	// State is the durable conversation state row.
	State metadb.ConversationState
}

// ConversationActivePatchBatchItem carries one UID-owned active patch and its logical hash slot.
type ConversationActivePatchBatchItem struct {
	// HashSlot is the logical hash slot that owns Patch.UID.
	HashSlot uint16
	// Patch is the active-at mutation for one conversation row.
	Patch metadb.ConversationActivePatch
}

// ConversationDeleteBatchItem carries one UID-owned hide request and its logical hash slot.
type ConversationDeleteBatchItem struct {
	// HashSlot is the logical hash slot that owns Delete.UID.
	HashSlot uint16
	// Delete hides one conversation row.
	Delete metadb.ConversationDelete
}

type upsertChannelLatestBatchCmd struct {
	items []ChannelLatestBatchItem
}

func (c *upsertChannelLatestCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.UpsertChannelLatest(hashSlot, c.latest)
}

func (c *upsertChannelLatestBatchCmd) apply(wb *metadb.WriteBatch, _ uint16) error {
	for _, item := range c.items {
		if err := wb.UpsertChannelLatest(item.HashSlot, item.Latest); err != nil {
			return err
		}
	}
	return nil
}

func (c *upsertChannelLatestBatchCmd) applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, item := range c.items {
		if item.HashSlot != hashSlot {
			continue
		}
		if err := wb.UpsertChannelLatest(item.HashSlot, item.Latest); err != nil {
			return err
		}
	}
	return nil
}

func (c *upsertChannelLatestBatchCmd) applyHashSlots(uint16) []uint16 {
	if c == nil {
		return nil
	}
	hashSlots := make([]uint16, 0, len(c.items))
	seen := make(map[uint16]struct{}, len(c.items))
	for _, item := range c.items {
		if _, ok := seen[item.HashSlot]; ok {
			continue
		}
		seen[item.HashSlot] = struct{}{}
		hashSlots = append(hashSlots, item.HashSlot)
	}
	sort.Slice(hashSlots, func(i, j int) bool {
		return hashSlots[i] < hashSlots[j]
	})
	return hashSlots
}

// --- UpsertConversationStates ---

type conversationStateEntry struct {
	hashSlot    uint16
	hasHashSlot bool
	state       metadb.ConversationState
}

type upsertConversationStatesCmd struct {
	entries []conversationStateEntry
}

func (c *upsertConversationStatesCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, entry := range c.entries {
		if err := wb.UpsertConversationState(entryHashSlot(entry.hashSlot, entry.hasHashSlot, hashSlot), entry.state); err != nil {
			return err
		}
	}
	return nil
}

func (c *upsertConversationStatesCmd) applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, entry := range c.entries {
		if entry.hasHashSlot && entry.hashSlot != hashSlot {
			continue
		}
		if err := wb.UpsertConversationState(hashSlot, entry.state); err != nil {
			return err
		}
	}
	return nil
}

func (c *upsertConversationStatesCmd) applyHashSlots(envelopeHashSlot uint16) []uint16 {
	return conversationStateEntryHashSlots(c.entries, envelopeHashSlot)
}

func (c *upsertConversationStatesCmd) states() []metadb.ConversationState {
	out := make([]metadb.ConversationState, 0, len(c.entries))
	for _, entry := range c.entries {
		out = append(out, entry.state)
	}
	return out
}

// --- TouchConversationActiveAt ---

type conversationActivePatchEntry struct {
	hashSlot    uint16
	hasHashSlot bool
	patch       metadb.ConversationActivePatch
}

type touchConversationActiveAtCmd struct {
	entries []conversationActivePatchEntry
}

func (c *touchConversationActiveAtCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	groups := make(map[uint16][]metadb.ConversationActivePatch)
	for _, entry := range c.entries {
		applyHashSlot := entryHashSlot(entry.hashSlot, entry.hasHashSlot, hashSlot)
		groups[applyHashSlot] = append(groups[applyHashSlot], entry.patch)
	}
	for _, applyHashSlot := range sortedUint16Keys(groups) {
		if err := wb.TouchConversationActiveAt(applyHashSlot, groups[applyHashSlot]); err != nil {
			return err
		}
	}
	return nil
}

func (c *touchConversationActiveAtCmd) applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error {
	var patches []metadb.ConversationActivePatch
	for _, entry := range c.entries {
		if entry.hasHashSlot && entry.hashSlot != hashSlot {
			continue
		}
		patches = append(patches, entry.patch)
	}
	if len(patches) == 0 {
		return nil
	}
	return wb.TouchConversationActiveAt(hashSlot, patches)
}

func (c *touchConversationActiveAtCmd) applyHashSlots(envelopeHashSlot uint16) []uint16 {
	return conversationActivePatchEntryHashSlots(c.entries, envelopeHashSlot)
}

func (c *touchConversationActiveAtCmd) patches() []metadb.ConversationActivePatch {
	out := make([]metadb.ConversationActivePatch, 0, len(c.entries))
	for _, entry := range c.entries {
		out = append(out, entry.patch)
	}
	return out
}

// --- ClearConversationActiveAt ---

type clearConversationActiveAtCmd struct {
	kind metadb.ConversationKind
	uid  string
	keys []metadb.ConversationKey
}

func (c *clearConversationActiveAtCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.ClearConversationActiveAt(hashSlot, c.kind, c.uid, c.keys)
}

// --- Reserved conversation projection commands ---

type reservedConversationProjectionCmd struct{}

func (c *reservedConversationProjectionCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return nil
}

// --- HideConversations ---

type conversationDeleteEntry struct {
	hashSlot    uint16
	hasHashSlot bool
	delete      metadb.ConversationDelete
}

type hideConversationsCmd struct {
	entries []conversationDeleteEntry
}

func (c *hideConversationsCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, entry := range c.entries {
		if err := wb.HideConversation(entryHashSlot(entry.hashSlot, entry.hasHashSlot, hashSlot), entry.delete); err != nil {
			return err
		}
	}
	return nil
}

func (c *hideConversationsCmd) applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, entry := range c.entries {
		if entry.hasHashSlot && entry.hashSlot != hashSlot {
			continue
		}
		if err := wb.HideConversation(hashSlot, entry.delete); err != nil {
			return err
		}
	}
	return nil
}

func (c *hideConversationsCmd) applyHashSlots(envelopeHashSlot uint16) []uint16 {
	return conversationDeleteEntryHashSlots(c.entries, envelopeHashSlot)
}

func (c *hideConversationsCmd) deletes() []metadb.ConversationDelete {
	out := make([]metadb.ConversationDelete, 0, len(c.entries))
	for _, entry := range c.entries {
		out = append(out, entry.delete)
	}
	return out
}

func entryHashSlot(entryHashSlot uint16, hasEntryHashSlot bool, envelopeHashSlot uint16) uint16 {
	if hasEntryHashSlot {
		return entryHashSlot
	}
	return envelopeHashSlot
}

func conversationStateEntryHashSlots(entries []conversationStateEntry, envelopeHashSlot uint16) []uint16 {
	hashSlots := make([]uint16, 0, len(entries))
	seen := make(map[uint16]struct{}, len(entries))
	for _, entry := range entries {
		hashSlot := entryHashSlot(entry.hashSlot, entry.hasHashSlot, envelopeHashSlot)
		if _, ok := seen[hashSlot]; ok {
			continue
		}
		seen[hashSlot] = struct{}{}
		hashSlots = append(hashSlots, hashSlot)
	}
	sort.Slice(hashSlots, func(i, j int) bool { return hashSlots[i] < hashSlots[j] })
	return hashSlots
}

func conversationActivePatchEntryHashSlots(entries []conversationActivePatchEntry, envelopeHashSlot uint16) []uint16 {
	hashSlots := make([]uint16, 0, len(entries))
	seen := make(map[uint16]struct{}, len(entries))
	for _, entry := range entries {
		hashSlot := entryHashSlot(entry.hashSlot, entry.hasHashSlot, envelopeHashSlot)
		if _, ok := seen[hashSlot]; ok {
			continue
		}
		seen[hashSlot] = struct{}{}
		hashSlots = append(hashSlots, hashSlot)
	}
	sort.Slice(hashSlots, func(i, j int) bool { return hashSlots[i] < hashSlots[j] })
	return hashSlots
}

func conversationDeleteEntryHashSlots(entries []conversationDeleteEntry, envelopeHashSlot uint16) []uint16 {
	hashSlots := make([]uint16, 0, len(entries))
	seen := make(map[uint16]struct{}, len(entries))
	for _, entry := range entries {
		hashSlot := entryHashSlot(entry.hashSlot, entry.hasHashSlot, envelopeHashSlot)
		if _, ok := seen[hashSlot]; ok {
			continue
		}
		seen[hashSlot] = struct{}{}
		hashSlots = append(hashSlots, hashSlot)
	}
	sort.Slice(hashSlots, func(i, j int) bool { return hashSlots[i] < hashSlots[j] })
	return hashSlots
}

func sortedUint16Keys[T any](m map[uint16]T) []uint16 {
	keys := make([]uint16, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// EncodeUpsertUserCommand encodes a User into a binary command.
func EncodeUpsertUserCommand(u metadb.User) []byte {
	return encodeUserCommand(cmdTypeUpsertUser, u)
}

// EncodeCreateUserCommand encodes a create-only User command.
func EncodeCreateUserCommand(u metadb.User) []byte {
	return encodeUserCommand(cmdTypeCreateUser, u)
}

func encodeUserCommand(cmdType uint8, u metadb.User) []byte {
	uidLen := len(u.UID)
	tokenLen := len(u.Token)
	// header + 2 string fields + 2 int64 fields
	size := headerSize +
		tlvOverhead + uidLen +
		tlvOverhead + tokenLen +
		tlvOverhead + 8 +
		tlvOverhead + 8

	buf := make([]byte, size)
	off := 0

	buf[off] = commandVersion
	off++
	buf[off] = cmdType
	off++

	off = putStringField(buf, off, tagUserUID, u.UID)
	off = putStringField(buf, off, tagUserToken, u.Token)
	off = putInt64Field(buf, off, tagUserDeviceFlag, u.DeviceFlag)
	_ = putInt64Field(buf, off, tagUserDeviceLevel, u.DeviceLevel)

	return buf
}

// EncodeUpsertDeviceCommand encodes a Device into a binary command.
func EncodeUpsertDeviceCommand(d metadb.Device) []byte {
	uidLen := len(d.UID)
	tokenLen := len(d.Token)
	size := headerSize +
		tlvOverhead + uidLen +
		tlvOverhead + 8 +
		tlvOverhead + tokenLen +
		tlvOverhead + 8

	buf := make([]byte, size)
	off := 0

	buf[off] = commandVersion
	off++
	buf[off] = cmdTypeUpsertDevice
	off++

	off = putStringField(buf, off, tagDeviceUID, d.UID)
	off = putInt64Field(buf, off, tagDeviceFlag, d.DeviceFlag)
	off = putStringField(buf, off, tagDeviceToken, d.Token)
	_ = putInt64Field(buf, off, tagDeviceLevel, d.DeviceLevel)

	return buf
}

// EncodeUpsertChannelCommand encodes a Channel into a binary command.
func EncodeUpsertChannelCommand(ch metadb.Channel) []byte {
	idLen := len(ch.ChannelID)
	// header + 1 string field + 6 int64 fields
	size := headerSize +
		tlvOverhead + idLen +
		tlvOverhead + 8 +
		tlvOverhead + 8 +
		tlvOverhead + 8 +
		tlvOverhead + 8 +
		tlvOverhead + 8 +
		tlvOverhead + 8

	buf := make([]byte, size)
	off := 0

	buf[off] = commandVersion
	off++
	buf[off] = cmdTypeUpsertChannel
	off++

	off = putStringField(buf, off, tagChannelID, ch.ChannelID)
	off = putInt64Field(buf, off, tagChannelType, ch.ChannelType)
	off = putInt64Field(buf, off, tagChannelBan, ch.Ban)
	off = putInt64Field(buf, off, tagChannelDisband, ch.Disband)
	off = putInt64Field(buf, off, tagChannelSendBan, ch.SendBan)
	off = putInt64Field(buf, off, tagChannelAllowStranger, ch.AllowStranger)
	_ = putInt64Field(buf, off, tagChannelLarge, ch.Large)

	return buf
}

// EncodeDeleteChannelCommand encodes a channel deletion into a binary command.
func EncodeDeleteChannelCommand(channelID string, channelType int64) []byte {
	size := headerSize +
		tlvOverhead + len(channelID) +
		tlvOverhead + 8
	buf := make([]byte, size)
	buf[0] = commandVersion
	buf[1] = cmdTypeDeleteChannel
	off := headerSize
	off = putStringField(buf, off, tagChannelID, channelID)
	putInt64Field(buf, off, tagChannelType, channelType)
	return buf
}

// EncodeUpsertChannelRuntimeMetaCommand encodes channel runtime metadata into a binary command.
func EncodeUpsertChannelRuntimeMetaCommand(meta metadb.ChannelRuntimeMeta) []byte {
	meta = canonicalizeChannelRuntimeMeta(meta)

	buf := make([]byte, 0, headerSize+128)
	buf = append(buf, commandVersion, cmdTypeUpsertChannelRuntimeMeta)
	buf = appendStringTLVField(buf, tagRuntimeMetaChannelID, meta.ChannelID)
	buf = appendInt64TLVField(buf, tagRuntimeMetaChannelType, meta.ChannelType)
	buf = appendUint64TLVField(buf, tagRuntimeMetaChannelEpoch, meta.ChannelEpoch)
	buf = appendUint64TLVField(buf, tagRuntimeMetaLeaderEpoch, meta.LeaderEpoch)
	buf = appendBytesTLVField(buf, tagRuntimeMetaReplicas, encodeUint64Slice(meta.Replicas))
	buf = appendBytesTLVField(buf, tagRuntimeMetaISR, encodeUint64Slice(meta.ISR))
	buf = appendUint64TLVField(buf, tagRuntimeMetaLeader, meta.Leader)
	buf = appendInt64TLVField(buf, tagRuntimeMetaMinISR, meta.MinISR)
	buf = appendUint64TLVField(buf, tagRuntimeMetaStatus, uint64(meta.Status))
	buf = appendUint64TLVField(buf, tagRuntimeMetaFeatures, meta.Features)
	buf = appendInt64TLVField(buf, tagRuntimeMetaLeaseUntilMS, meta.LeaseUntilMS)
	buf = appendUint64TLVField(buf, tagRuntimeMetaRetentionThroughSeq, meta.RetentionThroughSeq)
	buf = appendInt64TLVField(buf, tagRuntimeMetaRetentionUpdatedAtMS, meta.RetentionUpdatedAtMS)
	if meta.WriteFenceToken != "" {
		buf = appendStringTLVField(buf, tagRuntimeMetaWriteFenceToken, meta.WriteFenceToken)
	}
	buf = appendUint64TLVField(buf, tagRuntimeMetaWriteFenceVersion, meta.WriteFenceVersion)
	buf = appendUint64TLVField(buf, tagRuntimeMetaWriteFenceReason, uint64(meta.WriteFenceReason))
	buf = appendInt64TLVField(buf, tagRuntimeMetaWriteFenceUntilMS, meta.WriteFenceUntilMS)
	buf = appendUint64TLVField(buf, tagRuntimeMetaRouteGeneration, meta.RouteGeneration)
	return buf
}

// EncodeDeleteChannelRuntimeMetaCommand encodes runtime metadata deletion into a binary command.
func EncodeDeleteChannelRuntimeMetaCommand(channelID string, channelType int64) []byte {
	buf := make([]byte, 0, headerSize+len(channelID)+18)
	buf = append(buf, commandVersion, cmdTypeDeleteChannelRuntimeMeta)
	buf = appendStringTLVField(buf, tagRuntimeMetaChannelID, channelID)
	buf = appendInt64TLVField(buf, tagRuntimeMetaChannelType, channelType)
	return buf
}

// EncodeAdvanceChannelRetentionThroughSeqCommand encodes a fenced retention-only metadata advance.
func EncodeAdvanceChannelRetentionThroughSeqCommand(req metadb.ChannelRetentionAdvance) []byte {
	buf := make([]byte, 0, headerSize+len(req.ChannelID)+96)
	buf = append(buf, commandVersion, cmdTypeAdvanceChannelRetention)
	buf = appendStringTLVField(buf, tagRetentionAdvanceChannelID, req.ChannelID)
	buf = appendInt64TLVField(buf, tagRetentionAdvanceChannelType, req.ChannelType)
	buf = appendUint64TLVField(buf, tagRetentionAdvanceExpectedChannelEpoch, req.ExpectedChannelEpoch)
	buf = appendUint64TLVField(buf, tagRetentionAdvanceExpectedLeaderEpoch, req.ExpectedLeaderEpoch)
	buf = appendUint64TLVField(buf, tagRetentionAdvanceExpectedLeader, req.ExpectedLeader)
	buf = appendInt64TLVField(buf, tagRetentionAdvanceExpectedLeaseUntilMS, req.ExpectedLeaseUntilMS)
	buf = appendUint64TLVField(buf, tagRetentionAdvanceThroughSeq, req.RetentionThroughSeq)
	buf = appendInt64TLVField(buf, tagRetentionAdvanceUpdatedAtMS, req.RetentionUpdatedAtMS)
	return buf
}

// EncodeAddSubscribersCommand encodes a subscriber add command.
func EncodeAddSubscribersCommand(channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) []byte {
	return encodeSubscribersCommand(cmdTypeAddSubscribers, channelID, channelType, uids, subscriberMutationVersion...)
}

// EncodeAddSubscribersCommandChecked validates and encodes a bounded subscriber add command.
func EncodeAddSubscribersCommandChecked(channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) ([]byte, error) {
	if err := ValidateSubscriberCommandLimits(uids); err != nil {
		return nil, err
	}
	return EncodeAddSubscribersCommand(channelID, channelType, uids, subscriberMutationVersion...), nil
}

// EncodeRemoveSubscribersCommand encodes a subscriber removal command.
func EncodeRemoveSubscribersCommand(channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) []byte {
	return encodeSubscribersCommand(cmdTypeRemoveSubscribers, channelID, channelType, uids, subscriberMutationVersion...)
}

// EncodeRemoveSubscribersCommandChecked validates and encodes a bounded subscriber removal command.
func EncodeRemoveSubscribersCommandChecked(channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) ([]byte, error) {
	if err := ValidateSubscriberCommandLimits(uids); err != nil {
		return nil, err
	}
	return EncodeRemoveSubscribersCommand(channelID, channelType, uids, subscriberMutationVersion...), nil
}

// EncodeUpsertUserChannelMembershipsCommand encodes UID-owned membership upserts.
func EncodeUpsertUserChannelMembershipsCommand(memberships []metadb.UserChannelMembership) []byte {
	buf := make([]byte, 0, headerSize+len(memberships)*64)
	buf = append(buf, commandVersion, cmdTypeUpsertUserChannelMemberships)
	for _, membership := range memberships {
		buf = appendBytesTLVField(buf, tagUserChannelMembershipCommandEntry, encodeUserChannelMembershipEntry(membership, true))
	}
	return buf
}

// EncodeUpsertUserChannelMembershipsCommandChecked validates and encodes membership upserts.
func EncodeUpsertUserChannelMembershipsCommandChecked(memberships []metadb.UserChannelMembership) ([]byte, error) {
	if err := ValidateSubscriberCommandLimits(userChannelMembershipUIDs(memberships)); err != nil {
		return nil, err
	}
	return EncodeUpsertUserChannelMembershipsCommand(memberships), nil
}

// EncodeDeleteUserChannelMembershipsCommand encodes UID-owned membership deletes.
func EncodeDeleteUserChannelMembershipsCommand(memberships []metadb.UserChannelMembership) []byte {
	buf := make([]byte, 0, headerSize+len(memberships)*48)
	buf = append(buf, commandVersion, cmdTypeDeleteUserChannelMemberships)
	for _, membership := range memberships {
		buf = appendBytesTLVField(buf, tagUserChannelMembershipCommandEntry, encodeUserChannelMembershipEntry(membership, false))
	}
	return buf
}

// EncodeDeleteUserChannelMembershipsCommandChecked validates and encodes membership deletes.
func EncodeDeleteUserChannelMembershipsCommandChecked(memberships []metadb.UserChannelMembership) ([]byte, error) {
	if err := ValidateSubscriberCommandLimits(userChannelMembershipUIDs(memberships)); err != nil {
		return nil, err
	}
	return EncodeDeleteUserChannelMembershipsCommand(memberships), nil
}

// EncodeUpsertChannelLatestCommand encodes one channel latest projection upsert.
func EncodeUpsertChannelLatestCommand(latest metadb.ChannelLatest) []byte {
	buf := make([]byte, 0, headerSize+128+len(latest.Payload))
	buf = append(buf, commandVersion, cmdTypeUpsertChannelLatest)
	return append(buf, encodeChannelLatestRecord(latest)...)
}

func encodeChannelLatestRecord(latest metadb.ChannelLatest) []byte {
	buf := make([]byte, 0, 128+len(latest.Payload))
	buf = appendStringTLVField(buf, tagChannelLatestChannelID, latest.ChannelID)
	buf = appendInt64TLVField(buf, tagChannelLatestChannelType, latest.ChannelType)
	buf = appendUint64TLVField(buf, tagChannelLatestLastMessageID, latest.LastMessageID)
	buf = appendUint64TLVField(buf, tagChannelLatestLastMessageSeq, latest.LastMessageSeq)
	buf = appendInt64TLVField(buf, tagChannelLatestLastAt, latest.LastAt)
	buf = appendStringTLVField(buf, tagChannelLatestFromUID, latest.FromUID)
	buf = appendStringTLVField(buf, tagChannelLatestClientMsgNo, latest.ClientMsgNo)
	buf = appendBytesTLVField(buf, tagChannelLatestPayload, latest.Payload)
	buf = appendInt64TLVField(buf, tagChannelLatestUpdatedAt, latest.UpdatedAt)
	return buf
}

// EncodeUpsertChannelLatestCommandChecked validates and encodes a channel latest upsert.
func EncodeUpsertChannelLatestCommandChecked(latest metadb.ChannelLatest) ([]byte, error) {
	if err := validateChannelLatest(latest); err != nil {
		return nil, err
	}
	return EncodeUpsertChannelLatestCommand(latest), nil
}

// EncodeUpsertChannelLatestBatchCommand encodes multiple channel latest upserts with per-row hash slots.
func EncodeUpsertChannelLatestBatchCommand(items []ChannelLatestBatchItem) []byte {
	buf := make([]byte, 0, headerSize+len(items)*128)
	buf = append(buf, commandVersion, cmdTypeUpsertChannelLatestBatch)
	for _, item := range items {
		buf = appendBytesTLVField(buf, tagChannelLatestBatchEntry, encodeChannelLatestBatchItem(item))
	}
	return buf
}

// EncodeUpsertChannelLatestBatchCommandChecked validates and encodes a channel latest upsert batch.
func EncodeUpsertChannelLatestBatchCommandChecked(items []ChannelLatestBatchItem) ([]byte, error) {
	if len(items) == 0 {
		return nil, metadb.ErrInvalidArgument
	}
	for _, item := range items {
		if err := validateChannelLatest(item.Latest); err != nil {
			return nil, err
		}
	}
	return EncodeUpsertChannelLatestBatchCommand(items), nil
}

func encodeChannelLatestBatchItem(item ChannelLatestBatchItem) []byte {
	buf := make([]byte, 0, 144+len(item.Latest.Payload))
	buf = appendUint64TLVField(buf, tagChannelLatestBatchEntryHashSlot, uint64(item.HashSlot))
	buf = appendBytesTLVField(buf, tagChannelLatestBatchEntryRecord, encodeChannelLatestRecord(item.Latest))
	return buf
}

func validateChannelLatest(latest metadb.ChannelLatest) error {
	if latest.ChannelID == "" || latest.ChannelType == 0 {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func validateConversationState(state metadb.ConversationState) error {
	if err := validateConversationKind(state.Kind); err != nil {
		return err
	}
	if state.UID == "" || state.ChannelID == "" || state.ChannelType == 0 {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func validateConversationActivePatch(patch metadb.ConversationActivePatch) error {
	if err := validateConversationKind(patch.Kind); err != nil {
		return err
	}
	if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func validateConversationDelete(req metadb.ConversationDelete) error {
	if err := validateConversationKind(req.Kind); err != nil {
		return err
	}
	if req.UID == "" || req.ChannelID == "" || req.ChannelType == 0 {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func validateConversationKind(kind metadb.ConversationKind) error {
	switch kind {
	case metadb.ConversationKindNormal, metadb.ConversationKindCMD:
		return nil
	default:
		return metadb.ErrInvalidArgument
	}
}

func decodeConversationKindTLVValue(value []byte, label string) (metadb.ConversationKind, error) {
	if len(value) != 8 {
		return 0, fmt.Errorf("%w: bad %s length", metadb.ErrCorruptValue, label)
	}
	raw := binary.BigEndian.Uint64(value)
	if raw > uint64(^uint8(0)) {
		return 0, fmt.Errorf("%w: bad %s value %d", metadb.ErrCorruptValue, label, raw)
	}
	kind := metadb.ConversationKind(raw)
	if err := validateConversationKind(kind); err != nil {
		return 0, fmt.Errorf("%w: bad %s value %d", metadb.ErrCorruptValue, label, raw)
	}
	return kind, nil
}

func validateConversationBatchHashSlot(hashSlot uint16, uid string, hashSlotCount uint16) error {
	if hashSlotCount == 0 {
		return fmt.Errorf("%w: hash slot count must not be zero", metadb.ErrInvalidArgument)
	}
	if hashSlot >= hashSlotCount {
		return fmt.Errorf("%w: conversation hash slot %d out of range %d", metadb.ErrInvalidArgument, hashSlot, hashSlotCount)
	}
	want := hashslot.HashSlotForKey(uid, hashSlotCount)
	if hashSlot != want {
		return fmt.Errorf("%w: conversation hash slot %d does not match uid %q hash slot %d", metadb.ErrInvalidArgument, hashSlot, uid, want)
	}
	return nil
}

// ValidateSubscriberCommandLimits rejects subscriber mutations that would create oversized Raft entries.
func ValidateSubscriberCommandLimits(uids []string) error {
	if err := validateSubscriberCommandUIDCount(len(uids)); err != nil {
		return err
	}
	return validateSubscriberCommandUIDBytes(len(encodeStringSet(uids)))
}

// EncodeUpsertConversationStatesCommand encodes a batch of conversation state upserts.
func EncodeUpsertConversationStatesCommand(states []metadb.ConversationState) []byte {
	buf := make([]byte, 0, headerSize+len(states)*64)
	buf = append(buf, commandVersion, cmdTypeUpsertConversationStates)
	for _, state := range states {
		buf = appendBytesTLVField(buf, tagConversationStateCommandEntry, encodeConversationStateEntry(state))
	}
	return buf
}

// EncodeUpsertConversationStatesCommandChecked validates and encodes state upserts.
func EncodeUpsertConversationStatesCommandChecked(states []metadb.ConversationState) ([]byte, error) {
	if len(states) == 0 {
		return nil, metadb.ErrInvalidArgument
	}
	for _, state := range states {
		if err := validateConversationState(state); err != nil {
			return nil, err
		}
	}
	return EncodeUpsertConversationStatesCommand(states), nil
}

// EncodeUpsertConversationStateBatchCommand encodes state upserts with per-row hash slots.
func EncodeUpsertConversationStateBatchCommand(items []ConversationStateBatchItem) []byte {
	buf := make([]byte, 0, headerSize+len(items)*72)
	buf = append(buf, commandVersion, cmdTypeUpsertConversationStates)
	for _, item := range items {
		buf = appendBytesTLVField(buf, tagConversationStateCommandEntry, encodeConversationStateBatchItem(item))
	}
	return buf
}

// EncodeUpsertConversationStateBatchCommandChecked validates and encodes per-row hash-slot state upserts.
func EncodeUpsertConversationStateBatchCommandChecked(hashSlotCount uint16, items []ConversationStateBatchItem) ([]byte, error) {
	if len(items) == 0 {
		return nil, metadb.ErrInvalidArgument
	}
	for _, item := range items {
		if err := validateConversationState(item.State); err != nil {
			return nil, err
		}
		if err := validateConversationBatchHashSlot(item.HashSlot, item.State.UID, hashSlotCount); err != nil {
			return nil, err
		}
	}
	return EncodeUpsertConversationStateBatchCommand(items), nil
}

// EncodeTouchConversationActiveAtCommand encodes a batch of conversation active-at patches.
func EncodeTouchConversationActiveAtCommand(patches []metadb.ConversationActivePatch) []byte {
	buf := make([]byte, 0, headerSize+len(patches)*48)
	buf = append(buf, commandVersion, cmdTypeTouchConversationActiveAt)
	for _, patch := range patches {
		buf = appendBytesTLVField(buf, tagConversationActivePatchCommandEntry, encodeConversationActivePatchEntry(patch))
	}
	return buf
}

// EncodeTouchConversationActiveAtCommandChecked validates and encodes active-at patches.
func EncodeTouchConversationActiveAtCommandChecked(patches []metadb.ConversationActivePatch) ([]byte, error) {
	if len(patches) == 0 {
		return nil, metadb.ErrInvalidArgument
	}
	for _, patch := range patches {
		if err := validateConversationActivePatch(patch); err != nil {
			return nil, err
		}
	}
	return EncodeTouchConversationActiveAtCommand(patches), nil
}

// EncodeTouchConversationActiveAtBatchCommand encodes active-at patches with per-row hash slots.
func EncodeTouchConversationActiveAtBatchCommand(items []ConversationActivePatchBatchItem) []byte {
	buf := make([]byte, 0, headerSize+len(items)*64)
	buf = append(buf, commandVersion, cmdTypeTouchConversationActiveAt)
	for _, item := range items {
		buf = appendBytesTLVField(buf, tagConversationActivePatchCommandEntry, encodeConversationActivePatchBatchItem(item))
	}
	return buf
}

// EncodeTouchConversationActiveAtBatchCommandChecked validates and encodes per-row hash-slot active patches.
func EncodeTouchConversationActiveAtBatchCommandChecked(hashSlotCount uint16, items []ConversationActivePatchBatchItem) ([]byte, error) {
	if len(items) == 0 {
		return nil, metadb.ErrInvalidArgument
	}
	for _, item := range items {
		if err := validateConversationActivePatch(item.Patch); err != nil {
			return nil, err
		}
		if err := validateConversationBatchHashSlot(item.HashSlot, item.Patch.UID, hashSlotCount); err != nil {
			return nil, err
		}
	}
	return EncodeTouchConversationActiveAtBatchCommand(items), nil
}

// EncodeClearConversationActiveAtCommand encodes a uid-scoped active-at clear command.
func EncodeClearConversationActiveAtCommand(kind metadb.ConversationKind, uid string, keys []metadb.ConversationKey) []byte {
	buf := make([]byte, 0, headerSize+len(uid)+len(keys)*32)
	buf = append(buf, commandVersion, cmdTypeClearConversationActiveAt)
	buf = appendUint64TLVField(buf, tagClearConversationActiveKind, uint64(kind))
	buf = appendStringTLVField(buf, tagClearConversationActiveUID, uid)
	for _, key := range keys {
		buf = appendBytesTLVField(buf, tagClearConversationActiveKey, encodeConversationKeyEntry(key))
	}
	return buf
}

// EncodeHideConversationsCommand encodes durable conversation hides.
func EncodeHideConversationsCommand(deletes []metadb.ConversationDelete) []byte {
	buf := make([]byte, 0, headerSize+len(deletes)*64)
	buf = append(buf, commandVersion, cmdTypeHideConversations)
	for _, req := range deletes {
		buf = appendBytesTLVField(buf, tagConversationDeleteCommandEntry, encodeConversationDeleteEntry(req))
	}
	return buf
}

// EncodeHideConversationsCommandChecked validates and encodes durable conversation hides.
func EncodeHideConversationsCommandChecked(deletes []metadb.ConversationDelete) ([]byte, error) {
	if len(deletes) == 0 {
		return nil, metadb.ErrInvalidArgument
	}
	for _, req := range deletes {
		if err := validateConversationDelete(req); err != nil {
			return nil, err
		}
	}
	return EncodeHideConversationsCommand(deletes), nil
}

// EncodeHideConversationBatchCommand encodes durable hides with per-row hash slots.
func EncodeHideConversationBatchCommand(items []ConversationDeleteBatchItem) []byte {
	buf := make([]byte, 0, headerSize+len(items)*72)
	buf = append(buf, commandVersion, cmdTypeHideConversations)
	for _, item := range items {
		buf = appendBytesTLVField(buf, tagConversationDeleteCommandEntry, encodeConversationDeleteBatchItem(item))
	}
	return buf
}

// EncodeHideConversationBatchCommandChecked validates and encodes per-row hash-slot hides.
func EncodeHideConversationBatchCommandChecked(hashSlotCount uint16, items []ConversationDeleteBatchItem) ([]byte, error) {
	if len(items) == 0 {
		return nil, metadb.ErrInvalidArgument
	}
	for _, item := range items {
		if err := validateConversationDelete(item.Delete); err != nil {
			return nil, err
		}
		if err := validateConversationBatchHashSlot(item.HashSlot, item.Delete.UID, hashSlotCount); err != nil {
			return nil, err
		}
	}
	return EncodeHideConversationBatchCommand(items), nil
}

// EncodeUpsertUserConversationStatesCommand maps legacy ordinary rows to the unified conversation command.
func EncodeUpsertUserConversationStatesCommand(states []metadb.UserConversationState) []byte {
	return EncodeUpsertConversationStatesCommand(userConversationStatesToConversation(states))
}

// EncodeTouchUserConversationActiveAtCommand maps legacy ordinary patches to the unified conversation command.
func EncodeTouchUserConversationActiveAtCommand(patches []metadb.UserConversationActivePatch) []byte {
	return EncodeTouchConversationActiveAtCommand(userConversationActivePatchesToConversation(patches))
}

// EncodeClearUserConversationActiveAtCommand maps a legacy ordinary clear to the unified conversation command.
func EncodeClearUserConversationActiveAtCommand(uid string, keys []metadb.ConversationKey) []byte {
	return EncodeClearConversationActiveAtCommand(metadb.ConversationKindNormal, uid, keys)
}

// EncodeHideUserConversationsCommand maps legacy ordinary deletes to the unified conversation command.
func EncodeHideUserConversationsCommand(deletes []metadb.UserConversationDelete) []byte {
	return EncodeHideConversationsCommand(userConversationDeletesToConversation(deletes))
}

// EncodeUpsertCMDConversationStatesCommand maps legacy CMD rows to the unified conversation command.
func EncodeUpsertCMDConversationStatesCommand(states []metadb.CMDConversationState) []byte {
	return EncodeUpsertConversationStatesCommand(cmdConversationStatesToConversation(states))
}

// EncodeAdvanceCMDConversationReadSeqCommand maps legacy CMD read patches to the unified conversation command.
func EncodeAdvanceCMDConversationReadSeqCommand(patches []metadb.CMDConversationReadPatch) []byte {
	return EncodeTouchConversationActiveAtCommand(cmdConversationReadPatchesToConversation(patches))
}

func userConversationStatesToConversation(states []metadb.UserConversationState) []metadb.ConversationState {
	out := make([]metadb.ConversationState, 0, len(states))
	for _, state := range states {
		out = append(out, metadb.ConversationState{
			UID:          state.UID,
			Kind:         metadb.ConversationKindNormal,
			ChannelID:    state.ChannelID,
			ChannelType:  state.ChannelType,
			ReadSeq:      state.ReadSeq,
			DeletedToSeq: state.DeletedToSeq,
			ActiveAt:     state.ActiveAt,
			UpdatedAt:    state.UpdatedAt,
			SparseActive: state.SparseActive,
		})
	}
	return out
}

func userConversationActivePatchesToConversation(patches []metadb.UserConversationActivePatch) []metadb.ConversationActivePatch {
	out := make([]metadb.ConversationActivePatch, 0, len(patches))
	for _, patch := range patches {
		out = append(out, metadb.ConversationActivePatch{
			UID:             patch.UID,
			Kind:            metadb.ConversationKindNormal,
			ChannelID:       patch.ChannelID,
			ChannelType:     patch.ChannelType,
			ReadSeq:         patch.ReadSeq,
			DeletedToSeq:    patch.DeletedToSeq,
			ActiveAt:        patch.ActiveAt,
			UpdatedAt:       patch.UpdatedAt,
			MessageSeq:      patch.MessageSeq,
			SparseActive:    patch.SparseActive,
			SparseActiveSet: patch.SparseActiveSet,
		})
	}
	return out
}

func userConversationDeletesToConversation(deletes []metadb.UserConversationDelete) []metadb.ConversationDelete {
	out := make([]metadb.ConversationDelete, 0, len(deletes))
	for _, req := range deletes {
		out = append(out, metadb.ConversationDelete{
			UID:          req.UID,
			Kind:         metadb.ConversationKindNormal,
			ChannelID:    req.ChannelID,
			ChannelType:  req.ChannelType,
			DeletedToSeq: req.DeletedToSeq,
			UpdatedAt:    req.UpdatedAt,
		})
	}
	return out
}

func cmdConversationStatesToConversation(states []metadb.CMDConversationState) []metadb.ConversationState {
	out := make([]metadb.ConversationState, 0, len(states))
	for _, state := range states {
		out = append(out, metadb.ConversationState{
			UID:          state.UID,
			Kind:         metadb.ConversationKindCMD,
			ChannelID:    state.ChannelID,
			ChannelType:  state.ChannelType,
			ReadSeq:      state.ReadSeq,
			DeletedToSeq: state.DeletedToSeq,
			ActiveAt:     state.ActiveAt,
			UpdatedAt:    state.UpdatedAt,
		})
	}
	return out
}

func cmdConversationReadPatchesToConversation(patches []metadb.CMDConversationReadPatch) []metadb.ConversationActivePatch {
	out := make([]metadb.ConversationActivePatch, 0, len(patches))
	for _, patch := range patches {
		out = append(out, metadb.ConversationActivePatch{
			UID:         patch.UID,
			Kind:        metadb.ConversationKindCMD,
			ChannelID:   patch.ChannelID,
			ChannelType: patch.ChannelType,
			ReadSeq:     patch.ReadSeq,
			UpdatedAt:   patch.UpdatedAt,
		})
	}
	return out
}

func encodeSubscribersCommand(cmdType uint8, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) []byte {
	buf := make([]byte, 0, headerSize+len(channelID)+len(uids)*8+16)
	buf = append(buf, commandVersion, cmdType)
	buf = appendStringTLVField(buf, tagSubscriberChannelID, channelID)
	buf = appendInt64TLVField(buf, tagSubscriberChannelType, channelType)
	if len(subscriberMutationVersion) > 0 && subscriberMutationVersion[0] > 0 {
		buf = appendUint64TLVField(buf, tagSubscriberMutationVersion, subscriberMutationVersion[0])
	}
	buf = appendBytesTLVField(buf, tagSubscriberUIDs, encodeStringSet(uids))
	return buf
}

func encodeUserChannelMembershipEntry(membership metadb.UserChannelMembership, includeState bool) []byte {
	buf := make([]byte, 0, 64)
	buf = appendStringTLVField(buf, tagUserChannelMembershipEntryUID, membership.UID)
	buf = appendStringTLVField(buf, tagUserChannelMembershipChannelID, membership.ChannelID)
	buf = appendInt64TLVField(buf, tagUserChannelMembershipChannelType, membership.ChannelType)
	if includeState {
		buf = appendUint64TLVField(buf, tagUserChannelMembershipJoinSeq, membership.JoinSeq)
		buf = appendInt64TLVField(buf, tagUserChannelMembershipUpdatedAt, membership.UpdatedAt)
	}
	return buf
}

func userChannelMembershipUIDs(memberships []metadb.UserChannelMembership) []string {
	uids := make([]string, 0, len(memberships))
	for _, membership := range memberships {
		uids = append(uids, membership.UID)
	}
	return uids
}

func encodeConversationStateEntry(state metadb.ConversationState) []byte {
	buf := make([]byte, 0, 72)
	buf = appendStringTLVField(buf, tagConversationStateEntryUID, state.UID)
	buf = appendStringTLVField(buf, tagConversationStateEntryChannelID, state.ChannelID)
	buf = appendInt64TLVField(buf, tagConversationStateEntryChannelType, state.ChannelType)
	buf = appendUint64TLVField(buf, tagConversationStateEntryReadSeq, state.ReadSeq)
	buf = appendUint64TLVField(buf, tagConversationStateEntryDeletedToSeq, state.DeletedToSeq)
	buf = appendInt64TLVField(buf, tagConversationStateEntryActiveAt, state.ActiveAt)
	buf = appendInt64TLVField(buf, tagConversationStateEntryUpdatedAt, state.UpdatedAt)
	buf = appendBoolTLVField(buf, tagConversationStateEntrySparseActive, state.SparseActive)
	buf = appendUint64TLVField(buf, tagConversationEntryKind, uint64(state.Kind))
	return buf
}

func encodeConversationStateBatchItem(item ConversationStateBatchItem) []byte {
	buf := encodeConversationStateEntry(item.State)
	return appendUint64TLVField(buf, tagConversationStateEntryHashSlot, uint64(item.HashSlot))
}

func encodeConversationActivePatchEntry(patch metadb.ConversationActivePatch) []byte {
	buf := make([]byte, 0, 72)
	buf = appendStringTLVField(buf, tagConversationActivePatchEntryUID, patch.UID)
	buf = appendStringTLVField(buf, tagConversationActivePatchEntryChannelID, patch.ChannelID)
	buf = appendInt64TLVField(buf, tagConversationActivePatchEntryChannelType, patch.ChannelType)
	buf = appendUint64TLVField(buf, tagConversationActivePatchReadSeq, patch.ReadSeq)
	buf = appendUint64TLVField(buf, tagConversationActivePatchDeletedToSeq, patch.DeletedToSeq)
	buf = appendInt64TLVField(buf, tagConversationActivePatchEntryActiveAt, patch.ActiveAt)
	buf = appendInt64TLVField(buf, tagConversationActivePatchUpdatedAt, patch.UpdatedAt)
	buf = appendUint64TLVField(buf, tagConversationActivePatchEntryMessageSeq, patch.MessageSeq)
	buf = appendBoolTLVField(buf, tagConversationActivePatchSparseActive, patch.SparseActive)
	buf = appendBoolTLVField(buf, tagConversationActivePatchSparseActiveSet, patch.SparseActiveSet)
	buf = appendUint64TLVField(buf, tagConversationEntryKind, uint64(patch.Kind))
	return buf
}

func encodeConversationActivePatchBatchItem(item ConversationActivePatchBatchItem) []byte {
	buf := encodeConversationActivePatchEntry(item.Patch)
	return appendUint64TLVField(buf, tagConversationActivePatchHashSlot, uint64(item.HashSlot))
}

func encodeConversationKeyEntry(key metadb.ConversationKey) []byte {
	buf := make([]byte, 0, 32)
	buf = appendStringTLVField(buf, tagConversationKeyEntryChannelID, key.ChannelID)
	buf = appendInt64TLVField(buf, tagConversationKeyEntryChannelType, key.ChannelType)
	return buf
}

func encodeConversationDeleteEntry(req metadb.ConversationDelete) []byte {
	buf := make([]byte, 0, 64)
	buf = appendStringTLVField(buf, tagConversationDeleteEntryUID, req.UID)
	buf = appendStringTLVField(buf, tagConversationDeleteEntryChannelID, req.ChannelID)
	buf = appendInt64TLVField(buf, tagConversationDeleteEntryChannelType, req.ChannelType)
	buf = appendUint64TLVField(buf, tagConversationDeleteEntryDeletedToSeq, req.DeletedToSeq)
	buf = appendInt64TLVField(buf, tagConversationDeleteEntryUpdatedAt, req.UpdatedAt)
	buf = appendUint64TLVField(buf, tagConversationEntryKind, uint64(req.Kind))
	return buf
}

func encodeConversationDeleteBatchItem(item ConversationDeleteBatchItem) []byte {
	buf := encodeConversationDeleteEntry(item.Delete)
	return appendUint64TLVField(buf, tagConversationDeleteEntryHashSlot, uint64(item.HashSlot))
}

func decodeConversationStateEntries(data []byte) ([]conversationStateEntry, error) {
	var entries []conversationStateEntry
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagConversationStateCommandEntry:
			entry, err := decodeConversationStateEntry(value)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return entries, nil
}

func decodeConversationActivePatchEntries(data []byte) ([]conversationActivePatchEntry, error) {
	var entries []conversationActivePatchEntry
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagConversationActivePatchCommandEntry:
			entry, err := decodeConversationActivePatchEntry(value)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return entries, nil
}

func decodeConversationDeleteEntries(data []byte) ([]conversationDeleteEntry, error) {
	var entries []conversationDeleteEntry
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagConversationDeleteCommandEntry:
			entry, err := decodeConversationDeleteEntry(value)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return entries, nil
}

func decodeUserChannelMembershipEntries(data []byte, requireState bool) ([]metadb.UserChannelMembership, error) {
	var memberships []metadb.UserChannelMembership
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagUserChannelMembershipCommandEntry:
			membership, err := decodeUserChannelMembershipEntry(value, requireState)
			if err != nil {
				return nil, err
			}
			memberships = append(memberships, membership)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return memberships, nil
}

func decodeUserChannelMembershipEntry(data []byte, requireState bool) (metadb.UserChannelMembership, error) {
	var membership metadb.UserChannelMembership
	var haveUID, haveChannelID, haveChannelType, haveJoinSeq, haveUpdatedAt bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.UserChannelMembership{}, err
		}
		off += n
		switch tag {
		case tagUserChannelMembershipEntryUID:
			membership.UID = string(value)
			haveUID = true
		case tagUserChannelMembershipChannelID:
			membership.ChannelID = string(value)
			haveChannelID = true
		case tagUserChannelMembershipChannelType:
			if len(value) != 8 {
				return metadb.UserChannelMembership{}, fmt.Errorf("%w: bad user channel membership ChannelType length", metadb.ErrCorruptValue)
			}
			membership.ChannelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		case tagUserChannelMembershipJoinSeq:
			if len(value) != 8 {
				return metadb.UserChannelMembership{}, fmt.Errorf("%w: bad user channel membership JoinSeq length", metadb.ErrCorruptValue)
			}
			membership.JoinSeq = binary.BigEndian.Uint64(value)
			haveJoinSeq = true
		case tagUserChannelMembershipUpdatedAt:
			if len(value) != 8 {
				return metadb.UserChannelMembership{}, fmt.Errorf("%w: bad user channel membership UpdatedAt length", metadb.ErrCorruptValue)
			}
			membership.UpdatedAt = int64(binary.BigEndian.Uint64(value))
			haveUpdatedAt = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveUID || !haveChannelID || !haveChannelType || requireState && (!haveJoinSeq || !haveUpdatedAt) {
		return metadb.UserChannelMembership{}, fmt.Errorf("%w: incomplete user channel membership record", metadb.ErrCorruptValue)
	}
	return membership, nil
}

func decodeConversationStateEntry(data []byte) (conversationStateEntry, error) {
	var entry conversationStateEntry
	state := &entry.state
	var haveUID, haveKind, haveChannelID, haveChannelType, haveReadSeq, haveDeletedToSeq, haveActiveAt, haveUpdatedAt bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return conversationStateEntry{}, err
		}
		off += n
		switch tag {
		case tagConversationStateEntryUID:
			state.UID = string(value)
			haveUID = true
		case tagConversationEntryKind:
			kind, err := decodeConversationKindTLVValue(value, "conversation state Kind")
			if err != nil {
				return conversationStateEntry{}, err
			}
			state.Kind = kind
			haveKind = true
		case tagConversationStateEntryChannelID:
			state.ChannelID = string(value)
			haveChannelID = true
		case tagConversationStateEntryChannelType:
			if len(value) != 8 {
				return conversationStateEntry{}, fmt.Errorf("%w: bad conversation ChannelType length", metadb.ErrCorruptValue)
			}
			state.ChannelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		case tagConversationStateEntryReadSeq:
			if len(value) != 8 {
				return conversationStateEntry{}, fmt.Errorf("%w: bad conversation ReadSeq length", metadb.ErrCorruptValue)
			}
			state.ReadSeq = binary.BigEndian.Uint64(value)
			haveReadSeq = true
		case tagConversationStateEntryDeletedToSeq:
			if len(value) != 8 {
				return conversationStateEntry{}, fmt.Errorf("%w: bad conversation DeletedToSeq length", metadb.ErrCorruptValue)
			}
			state.DeletedToSeq = binary.BigEndian.Uint64(value)
			haveDeletedToSeq = true
		case tagConversationStateEntryActiveAt:
			if len(value) != 8 {
				return conversationStateEntry{}, fmt.Errorf("%w: bad conversation ActiveAt length", metadb.ErrCorruptValue)
			}
			state.ActiveAt = int64(binary.BigEndian.Uint64(value))
			haveActiveAt = true
		case tagConversationStateEntryUpdatedAt:
			if len(value) != 8 {
				return conversationStateEntry{}, fmt.Errorf("%w: bad conversation UpdatedAt length", metadb.ErrCorruptValue)
			}
			state.UpdatedAt = int64(binary.BigEndian.Uint64(value))
			haveUpdatedAt = true
		case tagConversationStateEntrySparseActive:
			sparse, err := decodeBoolTLVValue(value, "conversation SparseActive")
			if err != nil {
				return conversationStateEntry{}, err
			}
			state.SparseActive = sparse
		case tagConversationStateEntryHashSlot:
			hashSlot, err := decodeHashSlotTLVValue(value, "conversation HashSlot")
			if err != nil {
				return conversationStateEntry{}, err
			}
			entry.hashSlot = hashSlot
			entry.hasHashSlot = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveUID || !haveKind || !haveChannelID || !haveChannelType || !haveReadSeq || !haveDeletedToSeq || !haveActiveAt || !haveUpdatedAt {
		return conversationStateEntry{}, fmt.Errorf("%w: incomplete conversation state record", metadb.ErrCorruptValue)
	}
	return entry, nil
}

func decodeConversationActivePatchEntry(data []byte) (conversationActivePatchEntry, error) {
	var entry conversationActivePatchEntry
	patch := &entry.patch
	var haveUID, haveKind, haveChannelID, haveChannelType bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return conversationActivePatchEntry{}, err
		}
		off += n
		switch tag {
		case tagConversationActivePatchEntryUID:
			patch.UID = string(value)
			haveUID = true
		case tagConversationEntryKind:
			kind, err := decodeConversationKindTLVValue(value, "conversation active patch Kind")
			if err != nil {
				return conversationActivePatchEntry{}, err
			}
			patch.Kind = kind
			haveKind = true
		case tagConversationActivePatchEntryChannelID:
			patch.ChannelID = string(value)
			haveChannelID = true
		case tagConversationActivePatchEntryChannelType:
			if len(value) != 8 {
				return conversationActivePatchEntry{}, fmt.Errorf("%w: bad conversation ChannelType length", metadb.ErrCorruptValue)
			}
			patch.ChannelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		case tagConversationActivePatchReadSeq:
			if len(value) != 8 {
				return conversationActivePatchEntry{}, fmt.Errorf("%w: bad conversation ReadSeq length", metadb.ErrCorruptValue)
			}
			patch.ReadSeq = binary.BigEndian.Uint64(value)
		case tagConversationActivePatchDeletedToSeq:
			if len(value) != 8 {
				return conversationActivePatchEntry{}, fmt.Errorf("%w: bad conversation DeletedToSeq length", metadb.ErrCorruptValue)
			}
			patch.DeletedToSeq = binary.BigEndian.Uint64(value)
		case tagConversationActivePatchEntryActiveAt:
			if len(value) != 8 {
				return conversationActivePatchEntry{}, fmt.Errorf("%w: bad conversation ActiveAt length", metadb.ErrCorruptValue)
			}
			patch.ActiveAt = int64(binary.BigEndian.Uint64(value))
		case tagConversationActivePatchUpdatedAt:
			if len(value) != 8 {
				return conversationActivePatchEntry{}, fmt.Errorf("%w: bad conversation UpdatedAt length", metadb.ErrCorruptValue)
			}
			patch.UpdatedAt = int64(binary.BigEndian.Uint64(value))
		case tagConversationActivePatchEntryMessageSeq:
			if len(value) != 8 {
				return conversationActivePatchEntry{}, fmt.Errorf("%w: bad conversation MessageSeq length", metadb.ErrCorruptValue)
			}
			patch.MessageSeq = binary.BigEndian.Uint64(value)
		case tagConversationActivePatchSparseActive:
			sparse, err := decodeBoolTLVValue(value, "conversation SparseActive")
			if err != nil {
				return conversationActivePatchEntry{}, err
			}
			patch.SparseActive = sparse
		case tagConversationActivePatchSparseActiveSet:
			sparseSet, err := decodeBoolTLVValue(value, "conversation SparseActiveSet")
			if err != nil {
				return conversationActivePatchEntry{}, err
			}
			patch.SparseActiveSet = sparseSet
		case tagConversationActivePatchHashSlot:
			hashSlot, err := decodeHashSlotTLVValue(value, "conversation HashSlot")
			if err != nil {
				return conversationActivePatchEntry{}, err
			}
			entry.hashSlot = hashSlot
			entry.hasHashSlot = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveUID || !haveKind || !haveChannelID || !haveChannelType {
		return conversationActivePatchEntry{}, fmt.Errorf("%w: incomplete conversation active patch record", metadb.ErrCorruptValue)
	}
	return entry, nil
}

func decodeConversationKeyEntry(data []byte) (metadb.ConversationKey, error) {
	var key metadb.ConversationKey
	var haveChannelID, haveChannelType bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.ConversationKey{}, err
		}
		off += n
		switch tag {
		case tagConversationKeyEntryChannelID:
			key.ChannelID = string(value)
			haveChannelID = true
		case tagConversationKeyEntryChannelType:
			if len(value) != 8 {
				return metadb.ConversationKey{}, fmt.Errorf("%w: bad conversation ChannelType length", metadb.ErrCorruptValue)
			}
			key.ChannelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveChannelID || !haveChannelType {
		return metadb.ConversationKey{}, fmt.Errorf("%w: incomplete conversation key record", metadb.ErrCorruptValue)
	}
	return key, nil
}

func decodeConversationDeleteEntry(data []byte) (conversationDeleteEntry, error) {
	var entry conversationDeleteEntry
	req := &entry.delete
	var haveUID, haveKind, haveChannelID, haveChannelType, haveDeletedToSeq, haveUpdatedAt bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return conversationDeleteEntry{}, err
		}
		off += n
		switch tag {
		case tagConversationDeleteEntryUID:
			req.UID = string(value)
			haveUID = true
		case tagConversationEntryKind:
			kind, err := decodeConversationKindTLVValue(value, "conversation delete Kind")
			if err != nil {
				return conversationDeleteEntry{}, err
			}
			req.Kind = kind
			haveKind = true
		case tagConversationDeleteEntryChannelID:
			req.ChannelID = string(value)
			haveChannelID = true
		case tagConversationDeleteEntryChannelType:
			if len(value) != 8 {
				return conversationDeleteEntry{}, fmt.Errorf("%w: bad conversation delete ChannelType length", metadb.ErrCorruptValue)
			}
			req.ChannelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		case tagConversationDeleteEntryDeletedToSeq:
			if len(value) != 8 {
				return conversationDeleteEntry{}, fmt.Errorf("%w: bad conversation delete DeletedToSeq length", metadb.ErrCorruptValue)
			}
			req.DeletedToSeq = binary.BigEndian.Uint64(value)
			haveDeletedToSeq = true
		case tagConversationDeleteEntryUpdatedAt:
			if len(value) != 8 {
				return conversationDeleteEntry{}, fmt.Errorf("%w: bad conversation delete UpdatedAt length", metadb.ErrCorruptValue)
			}
			req.UpdatedAt = int64(binary.BigEndian.Uint64(value))
			haveUpdatedAt = true
		case tagConversationDeleteEntryHashSlot:
			hashSlot, err := decodeHashSlotTLVValue(value, "conversation delete HashSlot")
			if err != nil {
				return conversationDeleteEntry{}, err
			}
			entry.hashSlot = hashSlot
			entry.hasHashSlot = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveUID || !haveKind || !haveChannelID || !haveChannelType || !haveDeletedToSeq || !haveUpdatedAt {
		return conversationDeleteEntry{}, fmt.Errorf("%w: incomplete conversation delete record", metadb.ErrCorruptValue)
	}
	return entry, nil
}

func decodeUpsertConversationStates(data []byte) (command, error) {
	entries, err := decodeConversationStateEntries(data)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: empty conversation state batch", metadb.ErrInvalidArgument)
	}
	return &upsertConversationStatesCmd{entries: entries}, nil
}

func decodeUpsertUserChannelMemberships(data []byte) (command, error) {
	memberships, err := decodeUserChannelMembershipEntries(data, true)
	if err != nil {
		return nil, err
	}
	if len(memberships) == 0 {
		return nil, fmt.Errorf("%w: empty user channel membership upsert batch", metadb.ErrInvalidArgument)
	}
	return &upsertUserChannelMembershipsCmd{memberships: memberships}, nil
}

func decodeDeleteUserChannelMemberships(data []byte) (command, error) {
	memberships, err := decodeUserChannelMembershipEntries(data, false)
	if err != nil {
		return nil, err
	}
	if len(memberships) == 0 {
		return nil, fmt.Errorf("%w: empty user channel membership delete batch", metadb.ErrInvalidArgument)
	}
	return &deleteUserChannelMembershipsCmd{memberships: memberships}, nil
}

func decodeUpsertChannelLatest(data []byte) (command, error) {
	latest, err := decodeChannelLatestRecord(data)
	if err != nil {
		return nil, err
	}
	return &upsertChannelLatestCmd{latest: latest}, nil
}

func decodeUpsertChannelLatestBatch(data []byte) (command, error) {
	var items []ChannelLatestBatchItem
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagChannelLatestBatchEntry:
			item, err := decodeChannelLatestBatchItem(value)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: empty channel latest batch", metadb.ErrInvalidArgument)
	}
	return &upsertChannelLatestBatchCmd{items: items}, nil
}

func decodeChannelLatestBatchItem(data []byte) (ChannelLatestBatchItem, error) {
	var item ChannelLatestBatchItem
	var haveHashSlot, haveRecord bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return ChannelLatestBatchItem{}, err
		}
		off += n
		switch tag {
		case tagChannelLatestBatchEntryHashSlot:
			if len(value) != 8 {
				return ChannelLatestBatchItem{}, fmt.Errorf("%w: bad channel latest batch HashSlot length", metadb.ErrCorruptValue)
			}
			raw := binary.BigEndian.Uint64(value)
			if raw > uint64(^uint16(0)) {
				return ChannelLatestBatchItem{}, fmt.Errorf("%w: bad channel latest batch HashSlot value %d", metadb.ErrCorruptValue, raw)
			}
			item.HashSlot = uint16(raw)
			haveHashSlot = true
		case tagChannelLatestBatchEntryRecord:
			latest, err := decodeChannelLatestRecord(value)
			if err != nil {
				return ChannelLatestBatchItem{}, err
			}
			item.Latest = latest
			haveRecord = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveHashSlot || !haveRecord {
		return ChannelLatestBatchItem{}, fmt.Errorf("%w: incomplete channel latest batch entry", metadb.ErrCorruptValue)
	}
	return item, nil
}

func decodeChannelLatestRecord(data []byte) (metadb.ChannelLatest, error) {
	var latest metadb.ChannelLatest
	var haveChannelID, haveChannelType, haveMessageID, haveMessageSeq, haveLastAt, haveUpdatedAt bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.ChannelLatest{}, err
		}
		off += n
		switch tag {
		case tagChannelLatestChannelID:
			latest.ChannelID = string(value)
			haveChannelID = true
		case tagChannelLatestChannelType:
			if len(value) != 8 {
				return metadb.ChannelLatest{}, fmt.Errorf("%w: bad channel latest ChannelType length", metadb.ErrCorruptValue)
			}
			latest.ChannelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		case tagChannelLatestLastMessageID:
			if len(value) != 8 {
				return metadb.ChannelLatest{}, fmt.Errorf("%w: bad channel latest LastMessageID length", metadb.ErrCorruptValue)
			}
			latest.LastMessageID = binary.BigEndian.Uint64(value)
			haveMessageID = true
		case tagChannelLatestLastMessageSeq:
			if len(value) != 8 {
				return metadb.ChannelLatest{}, fmt.Errorf("%w: bad channel latest LastMessageSeq length", metadb.ErrCorruptValue)
			}
			latest.LastMessageSeq = binary.BigEndian.Uint64(value)
			haveMessageSeq = true
		case tagChannelLatestLastAt:
			if len(value) != 8 {
				return metadb.ChannelLatest{}, fmt.Errorf("%w: bad channel latest LastAt length", metadb.ErrCorruptValue)
			}
			latest.LastAt = int64(binary.BigEndian.Uint64(value))
			haveLastAt = true
		case tagChannelLatestFromUID:
			latest.FromUID = string(value)
		case tagChannelLatestClientMsgNo:
			latest.ClientMsgNo = string(value)
		case tagChannelLatestPayload:
			latest.Payload = append([]byte(nil), value...)
		case tagChannelLatestUpdatedAt:
			if len(value) != 8 {
				return metadb.ChannelLatest{}, fmt.Errorf("%w: bad channel latest UpdatedAt length", metadb.ErrCorruptValue)
			}
			latest.UpdatedAt = int64(binary.BigEndian.Uint64(value))
			haveUpdatedAt = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveChannelID || !haveChannelType || !haveMessageID || !haveMessageSeq || !haveLastAt || !haveUpdatedAt {
		return metadb.ChannelLatest{}, fmt.Errorf("%w: incomplete channel latest record", metadb.ErrCorruptValue)
	}
	return latest, nil
}

func decodeTouchConversationActiveAt(data []byte) (command, error) {
	entries, err := decodeConversationActivePatchEntries(data)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: empty conversation active patch batch", metadb.ErrInvalidArgument)
	}
	return &touchConversationActiveAtCmd{entries: entries}, nil
}

func decodeClearConversationActiveAt(data []byte) (command, error) {
	var kind metadb.ConversationKind
	var uid string
	var keys []metadb.ConversationKey
	var haveKind, haveUID bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagClearConversationActiveKind:
			decoded, err := decodeConversationKindTLVValue(value, "clear conversation active Kind")
			if err != nil {
				return nil, err
			}
			kind = decoded
			haveKind = true
		case tagClearConversationActiveUID:
			uid = string(value)
			haveUID = true
		case tagClearConversationActiveKey:
			key, err := decodeConversationKeyEntry(value)
			if err != nil {
				return nil, err
			}
			keys = append(keys, key)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveKind || !haveUID {
		return nil, fmt.Errorf("%w: missing kind or uid for clear command", metadb.ErrInvalidArgument)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: empty clear conversation key batch", metadb.ErrInvalidArgument)
	}
	return &clearConversationActiveAtCmd{kind: kind, uid: uid, keys: keys}, nil
}

func decodeReservedConversationProjection([]byte) (command, error) {
	return &reservedConversationProjectionCmd{}, nil
}

func decodeHideConversations(data []byte) (command, error) {
	entries, err := decodeConversationDeleteEntries(data)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: empty conversation delete batch", metadb.ErrInvalidArgument)
	}
	return &hideConversationsCmd{entries: entries}, nil
}

// decodeCommand parses a binary-encoded command using the decoder registry.
func decodeCommand(data []byte) (command, error) {
	if len(data) < headerSize {
		return nil, fmt.Errorf("%w: command too short", metadb.ErrCorruptValue)
	}

	version := data[0]
	if version != commandVersion {
		return nil, fmt.Errorf("%w: unsupported command version %d", metadb.ErrCorruptValue, version)
	}

	cmdType := data[1]
	decoder, ok := commandDecoders[cmdType]
	if !ok {
		return nil, fmt.Errorf("%w: unknown command type %d", metadb.ErrInvalidArgument, cmdType)
	}
	return decoder(data[headerSize:])
}

func decodeNoop(data []byte) (command, error) {
	off := 0
	for off < len(data) {
		_, _, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
	}
	return &noopCmd{}, nil
}

func decodeUpsertUser(data []byte) (command, error) {
	u, err := decodeUser(data)
	if err != nil {
		return nil, err
	}
	return &upsertUserCmd{user: u}, nil
}

func decodeCreateUser(data []byte) (command, error) {
	u, err := decodeUser(data)
	if err != nil {
		return nil, err
	}
	return &createUserCmd{user: u}, nil
}

func decodeUser(data []byte) (metadb.User, error) {
	var u metadb.User
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.User{}, err
		}
		off += n
		switch tag {
		case tagUserUID:
			u.UID = string(value)
		case tagUserToken:
			u.Token = string(value)
		case tagUserDeviceFlag:
			if len(value) != 8 {
				return metadb.User{}, fmt.Errorf("%w: bad DeviceFlag length", metadb.ErrCorruptValue)
			}
			u.DeviceFlag = int64(binary.BigEndian.Uint64(value))
		case tagUserDeviceLevel:
			if len(value) != 8 {
				return metadb.User{}, fmt.Errorf("%w: bad DeviceLevel length", metadb.ErrCorruptValue)
			}
			u.DeviceLevel = int64(binary.BigEndian.Uint64(value))
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return u, nil
}

func decodeUpsertDevice(data []byte) (command, error) {
	d, err := decodeDevice(data)
	if err != nil {
		return nil, err
	}
	return &upsertDeviceCmd{device: d}, nil
}

func decodeDevice(data []byte) (metadb.Device, error) {
	var d metadb.Device
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.Device{}, err
		}
		off += n
		switch tag {
		case tagDeviceUID:
			d.UID = string(value)
		case tagDeviceFlag:
			if len(value) != 8 {
				return metadb.Device{}, fmt.Errorf("%w: bad DeviceFlag length", metadb.ErrCorruptValue)
			}
			d.DeviceFlag = int64(binary.BigEndian.Uint64(value))
		case tagDeviceToken:
			d.Token = string(value)
		case tagDeviceLevel:
			if len(value) != 8 {
				return metadb.Device{}, fmt.Errorf("%w: bad DeviceLevel length", metadb.ErrCorruptValue)
			}
			d.DeviceLevel = int64(binary.BigEndian.Uint64(value))
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return d, nil
}

func decodeUpsertChannel(data []byte) (command, error) {
	var ch metadb.Channel
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagChannelID:
			ch.ChannelID = string(value)
		case tagChannelType:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad ChannelType length", metadb.ErrCorruptValue)
			}
			ch.ChannelType = int64(binary.BigEndian.Uint64(value))
		case tagChannelBan:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad Ban length", metadb.ErrCorruptValue)
			}
			ch.Ban = int64(binary.BigEndian.Uint64(value))
		case tagChannelDisband:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad Disband length", metadb.ErrCorruptValue)
			}
			ch.Disband = int64(binary.BigEndian.Uint64(value))
		case tagChannelSendBan:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad SendBan length", metadb.ErrCorruptValue)
			}
			ch.SendBan = int64(binary.BigEndian.Uint64(value))
		case tagChannelAllowStranger:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad AllowStranger length", metadb.ErrCorruptValue)
			}
			ch.AllowStranger = int64(binary.BigEndian.Uint64(value))
		case tagChannelLarge:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad Large length", metadb.ErrCorruptValue)
			}
			ch.Large = int64(binary.BigEndian.Uint64(value))
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return &upsertChannelCmd{channel: ch}, nil
}

func decodeDeleteChannel(data []byte) (command, error) {
	var cmd deleteChannelCmd
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagChannelID:
			cmd.channelID = string(value)
		case tagChannelType:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad ChannelType length", metadb.ErrCorruptValue)
			}
			cmd.channelType = int64(binary.BigEndian.Uint64(value))
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return &cmd, nil
}

func decodeUpsertChannelRuntimeMeta(data []byte) (command, error) {
	var meta metadb.ChannelRuntimeMeta
	var (
		haveChannelID    bool
		haveChannelType  bool
		haveChannelEpoch bool
		haveLeaderEpoch  bool
		haveReplicas     bool
		haveISR          bool
		haveLeader       bool
		haveMinISR       bool
		haveStatus       bool
		haveFeatures     bool
		haveLeaseUntilMS bool
	)
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n

		switch tag {
		case tagRuntimeMetaChannelID:
			meta.ChannelID = string(value)
			haveChannelID = true
		case tagRuntimeMetaChannelType:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime ChannelType length", metadb.ErrCorruptValue)
			}
			meta.ChannelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		case tagRuntimeMetaChannelEpoch:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime ChannelEpoch length", metadb.ErrCorruptValue)
			}
			meta.ChannelEpoch = binary.BigEndian.Uint64(value)
			haveChannelEpoch = true
		case tagRuntimeMetaLeaderEpoch:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime LeaderEpoch length", metadb.ErrCorruptValue)
			}
			meta.LeaderEpoch = binary.BigEndian.Uint64(value)
			haveLeaderEpoch = true
		case tagRuntimeMetaReplicas:
			meta.Replicas, err = decodeUint64Slice(value)
			if err != nil {
				return nil, err
			}
			haveReplicas = true
		case tagRuntimeMetaISR:
			meta.ISR, err = decodeUint64Slice(value)
			if err != nil {
				return nil, err
			}
			haveISR = true
		case tagRuntimeMetaLeader:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime Leader length", metadb.ErrCorruptValue)
			}
			meta.Leader = binary.BigEndian.Uint64(value)
			haveLeader = true
		case tagRuntimeMetaMinISR:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime MinISR length", metadb.ErrCorruptValue)
			}
			meta.MinISR = int64(binary.BigEndian.Uint64(value))
			haveMinISR = true
		case tagRuntimeMetaStatus:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime Status length", metadb.ErrCorruptValue)
			}
			raw := binary.BigEndian.Uint64(value)
			if raw > uint64(^uint8(0)) {
				return nil, fmt.Errorf("%w: bad runtime Status value %d", metadb.ErrCorruptValue, raw)
			}
			meta.Status = uint8(raw)
			haveStatus = true
		case tagRuntimeMetaFeatures:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime Features length", metadb.ErrCorruptValue)
			}
			meta.Features = binary.BigEndian.Uint64(value)
			haveFeatures = true
		case tagRuntimeMetaLeaseUntilMS:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime LeaseUntilMS length", metadb.ErrCorruptValue)
			}
			meta.LeaseUntilMS = int64(binary.BigEndian.Uint64(value))
			haveLeaseUntilMS = true
		case tagRuntimeMetaRetentionThroughSeq:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime RetentionThroughSeq length", metadb.ErrCorruptValue)
			}
			meta.RetentionThroughSeq = binary.BigEndian.Uint64(value)
		case tagRuntimeMetaRetentionUpdatedAtMS:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime RetentionUpdatedAtMS length", metadb.ErrCorruptValue)
			}
			meta.RetentionUpdatedAtMS = int64(binary.BigEndian.Uint64(value))
		case tagRuntimeMetaWriteFenceToken:
			meta.WriteFenceToken = string(value)
		case tagRuntimeMetaWriteFenceVersion:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime WriteFenceVersion length", metadb.ErrCorruptValue)
			}
			meta.WriteFenceVersion = binary.BigEndian.Uint64(value)
		case tagRuntimeMetaWriteFenceReason:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime WriteFenceReason length", metadb.ErrCorruptValue)
			}
			raw := binary.BigEndian.Uint64(value)
			if raw > uint64(^uint8(0)) {
				return nil, fmt.Errorf("%w: bad runtime WriteFenceReason value %d", metadb.ErrCorruptValue, raw)
			}
			meta.WriteFenceReason = uint8(raw)
		case tagRuntimeMetaWriteFenceUntilMS:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime WriteFenceUntilMS length", metadb.ErrCorruptValue)
			}
			meta.WriteFenceUntilMS = int64(binary.BigEndian.Uint64(value))
		case tagRuntimeMetaRouteGeneration:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime RouteGeneration length", metadb.ErrCorruptValue)
			}
			meta.RouteGeneration = binary.BigEndian.Uint64(value)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveChannelID || !haveChannelType || !haveChannelEpoch || !haveLeaderEpoch ||
		!haveReplicas || !haveISR || !haveLeader || !haveMinISR || !haveStatus ||
		!haveFeatures || !haveLeaseUntilMS {
		return nil, fmt.Errorf("%w: incomplete runtime metadata command", metadb.ErrCorruptValue)
	}
	return &upsertChannelRuntimeMetaCmd{meta: canonicalizeChannelRuntimeMeta(meta)}, nil
}

func decodeDeleteChannelRuntimeMeta(data []byte) (command, error) {
	var cmd deleteChannelRuntimeMetaCmd
	var haveChannelID, haveChannelType bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagRuntimeMetaChannelID:
			cmd.channelID = string(value)
			haveChannelID = true
		case tagRuntimeMetaChannelType:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad runtime ChannelType length", metadb.ErrCorruptValue)
			}
			cmd.channelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveChannelID || !haveChannelType {
		return nil, fmt.Errorf("%w: incomplete runtime metadata delete command", metadb.ErrCorruptValue)
	}
	return &cmd, nil
}

func decodeAdvanceChannelRetentionThroughSeq(data []byte) (command, error) {
	var req metadb.ChannelRetentionAdvance
	var (
		haveChannelID            bool
		haveChannelType          bool
		haveExpectedChannelEpoch bool
		haveExpectedLeaderEpoch  bool
		haveExpectedLeader       bool
		haveExpectedLeaseUntilMS bool
		haveRetentionThroughSeq  bool
		haveRetentionUpdatedAtMS bool
	)
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n

		switch tag {
		case tagRetentionAdvanceChannelID:
			req.ChannelID = string(value)
			haveChannelID = true
		case tagRetentionAdvanceChannelType:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad retention advance ChannelType length", metadb.ErrCorruptValue)
			}
			req.ChannelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		case tagRetentionAdvanceExpectedChannelEpoch:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad retention advance ExpectedChannelEpoch length", metadb.ErrCorruptValue)
			}
			req.ExpectedChannelEpoch = binary.BigEndian.Uint64(value)
			haveExpectedChannelEpoch = true
		case tagRetentionAdvanceExpectedLeaderEpoch:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad retention advance ExpectedLeaderEpoch length", metadb.ErrCorruptValue)
			}
			req.ExpectedLeaderEpoch = binary.BigEndian.Uint64(value)
			haveExpectedLeaderEpoch = true
		case tagRetentionAdvanceExpectedLeader:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad retention advance ExpectedLeader length", metadb.ErrCorruptValue)
			}
			req.ExpectedLeader = binary.BigEndian.Uint64(value)
			haveExpectedLeader = true
		case tagRetentionAdvanceExpectedLeaseUntilMS:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad retention advance ExpectedLeaseUntilMS length", metadb.ErrCorruptValue)
			}
			req.ExpectedLeaseUntilMS = int64(binary.BigEndian.Uint64(value))
			haveExpectedLeaseUntilMS = true
		case tagRetentionAdvanceThroughSeq:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad retention advance RetentionThroughSeq length", metadb.ErrCorruptValue)
			}
			req.RetentionThroughSeq = binary.BigEndian.Uint64(value)
			haveRetentionThroughSeq = true
		case tagRetentionAdvanceUpdatedAtMS:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad retention advance RetentionUpdatedAtMS length", metadb.ErrCorruptValue)
			}
			req.RetentionUpdatedAtMS = int64(binary.BigEndian.Uint64(value))
			haveRetentionUpdatedAtMS = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveChannelID || !haveChannelType || !haveExpectedChannelEpoch ||
		!haveExpectedLeaderEpoch || !haveExpectedLeader || !haveExpectedLeaseUntilMS ||
		!haveRetentionThroughSeq || !haveRetentionUpdatedAtMS {
		return nil, fmt.Errorf("%w: incomplete retention advance command", metadb.ErrCorruptValue)
	}
	return &advanceChannelRetentionThroughSeqCmd{req: req}, nil
}

func decodeAddSubscribers(data []byte) (command, error) {
	return decodeSubscribersCommand(data, func(channelID string, channelType int64, uids []string) command {
		return &addSubscribersCmd{
			channelID:   channelID,
			channelType: channelType,
			uids:        uids,
		}
	})
}

func decodeRemoveSubscribers(data []byte) (command, error) {
	return decodeSubscribersCommand(data, func(channelID string, channelType int64, uids []string) command {
		return &removeSubscribersCmd{
			channelID:   channelID,
			channelType: channelType,
			uids:        uids,
		}
	})
}

func decodeSubscribersCommand(data []byte, build func(channelID string, channelType int64, uids []string) command) (command, error) {
	var (
		channelID                 string
		channelType               int64
		uids                      []string
		subscriberMutationVersion uint64
		haveChannelID             bool
		haveChannelType           bool
		haveUIDs                  bool
	)

	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n

		switch tag {
		case tagSubscriberChannelID:
			channelID = string(value)
			haveChannelID = true
		case tagSubscriberChannelType:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad subscriber ChannelType length", metadb.ErrCorruptValue)
			}
			channelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		case tagSubscriberUIDs:
			if err := validateSubscriberCommandUIDBytes(len(value)); err != nil {
				return nil, err
			}
			uids = decodeStringSet(value)
			if err := validateSubscriberCommandUIDCount(len(uids)); err != nil {
				return nil, err
			}
			haveUIDs = true
		case tagSubscriberMutationVersion:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad subscriber mutation version length", metadb.ErrCorruptValue)
			}
			subscriberMutationVersion = binary.BigEndian.Uint64(value)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}

	if !haveChannelID || !haveChannelType || !haveUIDs {
		return nil, fmt.Errorf("%w: incomplete subscriber command", metadb.ErrCorruptValue)
	}
	built := build(channelID, channelType, uids)
	switch cmd := built.(type) {
	case *addSubscribersCmd:
		cmd.subscriberMutationVersion = subscriberMutationVersion
		return cmd, nil
	case *removeSubscribersCmd:
		cmd.subscriberMutationVersion = subscriberMutationVersion
		return cmd, nil
	default:
		return built, nil
	}
}

func validateSubscriberCommandUIDCount(count int) error {
	if count > MaxSubscriberCommandUIDs {
		return fmt.Errorf("%w: subscriber command uid count %d exceeds limit %d", metadb.ErrInvalidArgument, count, MaxSubscriberCommandUIDs)
	}
	return nil
}

func validateSubscriberCommandUIDBytes(size int) error {
	if size > MaxSubscriberCommandUIDBytes {
		return fmt.Errorf("%w: subscriber command uid bytes %d exceeds limit %d", metadb.ErrInvalidArgument, size, MaxSubscriberCommandUIDBytes)
	}
	return nil
}

// ---------- TLV helpers ----------

// putStringField writes [tag][len:4][string bytes] and returns the new offset.
func putStringField(buf []byte, off int, tag uint8, s string) int {
	buf[off] = tag
	binary.BigEndian.PutUint32(buf[off+1:], uint32(len(s)))
	off += tlvOverhead
	copy(buf[off:], s)
	return off + len(s)
}

// putInt64Field writes [tag][len:4=00000008][8-byte big-endian] and returns the new offset.
func putInt64Field(buf []byte, off int, tag uint8, v int64) int {
	buf[off] = tag
	binary.BigEndian.PutUint32(buf[off+1:], 8)
	off += tlvOverhead
	binary.BigEndian.PutUint64(buf[off:], uint64(v))
	return off + 8
}

func appendStringTLVField(dst []byte, tag uint8, value string) []byte {
	dst = append(dst, tag, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(dst[len(dst)-4:], uint32(len(value)))
	return append(dst, value...)
}

func appendBytesTLVField(dst []byte, tag uint8, value []byte) []byte {
	dst = append(dst, tag, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(dst[len(dst)-4:], uint32(len(value)))
	return append(dst, value...)
}

func appendInt64TLVField(dst []byte, tag uint8, value int64) []byte {
	dst = append(dst, tag, 0, 0, 0, 8)
	return binary.BigEndian.AppendUint64(dst, uint64(value))
}

func appendUint64TLVField(dst []byte, tag uint8, value uint64) []byte {
	dst = append(dst, tag, 0, 0, 0, 8)
	return binary.BigEndian.AppendUint64(dst, value)
}

func appendBoolTLVField(dst []byte, tag uint8, value bool) []byte {
	var raw byte
	if value {
		raw = 1
	}
	dst = append(dst, tag, 0, 0, 0, 1)
	return append(dst, raw)
}

func decodeBoolTLVValue(value []byte, label string) (bool, error) {
	if len(value) != 1 {
		return false, fmt.Errorf("%w: bad %s length", metadb.ErrCorruptValue, label)
	}
	switch value[0] {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("%w: bad %s value", metadb.ErrCorruptValue, label)
	}
}

func decodeHashSlotTLVValue(value []byte, label string) (uint16, error) {
	if len(value) != 8 {
		return 0, fmt.Errorf("%w: bad %s length", metadb.ErrCorruptValue, label)
	}
	raw := binary.BigEndian.Uint64(value)
	if raw > uint64(^uint16(0)) {
		return 0, fmt.Errorf("%w: bad %s value %d", metadb.ErrCorruptValue, label, raw)
	}
	return uint16(raw), nil
}

// readTLV reads one TLV entry from data and returns (tag, value, bytesConsumed, error).
func readTLV(data []byte) (uint8, []byte, int, error) {
	if len(data) < tlvOverhead {
		return 0, nil, 0, fmt.Errorf("%w: truncated TLV header", metadb.ErrCorruptValue)
	}
	tag := data[0]
	length := int(binary.BigEndian.Uint32(data[1:]))
	end := tlvOverhead + length
	if end > len(data) {
		return 0, nil, 0, fmt.Errorf("%w: truncated TLV value (tag=%d, need=%d, have=%d)",
			metadb.ErrCorruptValue, tag, length, len(data)-tlvOverhead)
	}
	return tag, data[tlvOverhead:end], end, nil
}

func canonicalizeChannelRuntimeMeta(meta metadb.ChannelRuntimeMeta) metadb.ChannelRuntimeMeta {
	return metadb.NormalizeChannelRuntimeMeta(meta)
}

func canonicalizeUint64Set(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]uint64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	n := 1
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[n-1] {
			continue
		}
		sorted[n] = sorted[i]
		n++
	}
	return sorted[:n]
}

func encodeUint64Slice(values []uint64) []byte {
	if len(values) == 0 {
		return nil
	}
	buf := make([]byte, 8*len(values))
	for i, value := range values {
		binary.BigEndian.PutUint64(buf[i*8:], value)
	}
	return buf
}

func decodeUint64Slice(data []byte) ([]uint64, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if len(data)%8 != 0 {
		return nil, fmt.Errorf("%w: malformed runtime uint64 slice", metadb.ErrCorruptValue)
	}
	values := make([]uint64, len(data)/8)
	for i := range values {
		values[i] = binary.BigEndian.Uint64(data[i*8:])
	}
	return values, nil
}

func encodeStringSet(values []string) []byte {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	n := 1
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[n-1] {
			continue
		}
		sorted[n] = sorted[i]
		n++
	}
	sorted = sorted[:n]
	return []byte(strings.Join(sorted, "\x00"))
}

func decodeStringSet(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	return strings.Split(string(data), "\x00")
}
