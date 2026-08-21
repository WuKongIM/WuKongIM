package fsm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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

	cmdTypeUpsertUser               uint8 = 1
	cmdTypeUpsertChannel            uint8 = 2
	cmdTypeDeleteChannel            uint8 = 3
	cmdTypeUpsertChannelRuntimeMeta uint8 = 4
	cmdTypeDeleteChannelRuntimeMeta uint8 = 5
	cmdTypeCreateUser               uint8 = 6
	cmdTypeUpsertDevice             uint8 = 7
	cmdTypeAddSubscribers           uint8 = 8
	cmdTypeRemoveSubscribers        uint8 = 9
	// Command type IDs 10 through 14 and 16 are reserved by removed development-era conversation commands.
	cmdTypeAdvanceChannelRetention             uint8 = 15
	cmdTypeNoop                                uint8 = 19
	cmdTypeUpsertUserChannelMemberships        uint8 = 44
	cmdTypeDeleteUserChannelMemberships        uint8 = 45
	cmdTypeUpsertChannelLatest                 uint8 = 46
	cmdTypeUpsertChannelLatestBatch            uint8 = 47
	cmdTypeAppendMessageEvent                  uint8 = 48
	cmdTypeAppendMessageEventsBatch            uint8 = 49
	cmdTypeCreateChannel                       uint8 = 50
	cmdTypePatchChannelBusinessFlags           uint8 = 51
	cmdTypeAdvanceUserChannelMembershipReadSeq uint8 = 52
	cmdTypeHideUserChannelMembership           uint8 = 53
	cmdTypeActivateUserChannelMembership       uint8 = 54
	cmdTypeUpsertUserCMDChannelMemberships     uint8 = 55
	cmdTypeAdvanceUserCMDChannelMembershipAcks uint8 = 56
	cmdTypeTombstoneUserCMDChannelMemberships  uint8 = 57
	cmdTypeCreateChannelRuntimeMeta            uint8 = 59
	cmdTypeAdmitPersonDirectoryTaskBatch       uint8 = 63
	cmdTypeEnsureUserChannelMembershipBatch    uint8 = 64
	cmdTypeCompletePersonDirectoryTaskBatch    uint8 = 65
	cmdTypeBindPluginUser                      uint8 = 42
	cmdTypeUnbindPluginUser                    uint8 = 43

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
	tagUserChannelMembershipReadSeq      uint8 = 5
	tagUserChannelMembershipDeletedSeq   uint8 = 6
	tagUserChannelMembershipActivatedAt  uint8 = 7
	tagUserChannelMembershipTombstone    uint8 = 8
	tagUserChannelMembershipTombstoneAt  uint8 = 9
	tagUserChannelMembershipSourceVer    uint8 = 10
	tagUserChannelMembershipUpdatedAt    uint8 = 11

	// User CMD channel membership field tags.
	tagUserCMDChannelMembershipCommandEntry uint8 = 1
	tagUserCMDChannelMembershipUID          uint8 = 1
	tagUserCMDChannelMembershipChannelID    uint8 = 2
	tagUserCMDChannelMembershipChannelType  uint8 = 3
	tagUserCMDChannelMembershipStartSeq     uint8 = 4
	tagUserCMDChannelMembershipAckSeq       uint8 = 5
	tagUserCMDChannelMembershipTombstone    uint8 = 6
	tagUserCMDChannelMembershipTombstoneAt  uint8 = 7
	tagUserCMDChannelMembershipUpdatedAt    uint8 = 8

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
	cmdTypeUpsertUser:                          decodeUpsertUser,
	cmdTypeUpsertChannel:                       decodeUpsertChannel,
	cmdTypeDeleteChannel:                       decodeDeleteChannel,
	cmdTypeUpsertChannelRuntimeMeta:            decodeUpsertChannelRuntimeMeta,
	cmdTypeDeleteChannelRuntimeMeta:            decodeDeleteChannelRuntimeMeta,
	cmdTypeCreateUser:                          decodeCreateUser,
	cmdTypeUpsertDevice:                        decodeUpsertDevice,
	cmdTypeAddSubscribers:                      decodeAddSubscribers,
	cmdTypeRemoveSubscribers:                   decodeRemoveSubscribers,
	cmdTypeAdvanceChannelRetention:             decodeAdvanceChannelRetentionThroughSeq,
	cmdTypeNoop:                                decodeNoop,
	cmdTypeUpsertUserChannelMemberships:        decodeUpsertUserChannelMemberships,
	cmdTypeDeleteUserChannelMemberships:        decodeDeleteUserChannelMemberships,
	cmdTypeUpsertChannelLatest:                 decodeUpsertChannelLatest,
	cmdTypeUpsertChannelLatestBatch:            decodeUpsertChannelLatestBatch,
	cmdTypeAppendMessageEvent:                  decodeAppendMessageEvent,
	cmdTypeAppendMessageEventsBatch:            decodeAppendMessageEventsBatch,
	cmdTypeCreateChannel:                       decodeCreateChannel,
	cmdTypePatchChannelBusinessFlags:           decodePatchChannelBusinessFlags,
	cmdTypeAdvanceUserChannelMembershipReadSeq: decodeAdvanceUserChannelMembershipReadSeq,
	cmdTypeHideUserChannelMembership:           decodeHideUserChannelMembership,
	cmdTypeActivateUserChannelMembership:       decodeActivateUserChannelMembership,
	cmdTypeUpsertUserCMDChannelMemberships:     decodeUpsertUserCMDChannelMemberships,
	cmdTypeAdvanceUserCMDChannelMembershipAcks: decodeAdvanceUserCMDChannelMembershipAcks,
	cmdTypeTombstoneUserCMDChannelMemberships:  decodeTombstoneUserCMDChannelMemberships,
	cmdTypeCreateChannelRuntimeMeta:            decodeCreateChannelRuntimeMeta,
	cmdTypeAdmitPersonDirectoryTaskBatch:       decodeAdmitPersonDirectoryTaskBatch,
	cmdTypeEnsureUserChannelMembershipBatch:    decodeEnsureUserChannelMembershipBatch,
	cmdTypeCompletePersonDirectoryTaskBatch:    decodeCompletePersonDirectoryTaskBatch,
	cmdTypeBindPluginUser:                      decodeBindPluginUser,
	cmdTypeUnbindPluginUser:                    decodeUnbindPluginUser,
	cmdTypeApplyDelta:                          decodeApplyDelta,
	cmdTypeEnterFence:                          decodeEnterFence,
	cmdTypeAckMigrationOutbox:                  decodeAckMigrationOutbox,
	cmdTypeCleanupMigrationOutbox:              decodeCleanupMigrationOutbox,
	cmdTypeCreateChannelMigrationTask:          decodeCreateChannelMigrationTask,
	cmdTypeClaimChannelMigrationTask:           decodeClaimChannelMigrationTask,
	cmdTypeAdvanceChannelMigrationTask:         decodeAdvanceChannelMigrationTask,
	cmdTypeSetChannelWriteFence:                decodeSetChannelWriteFence,
	cmdTypeResetChannelWriteFence:              decodeResetChannelWriteFence,
	cmdTypeCommitChannelLeaderTransfer:         decodeCommitChannelLeaderTransfer,
	cmdTypeAddChannelLearner:                   decodeAddChannelLearner,
	cmdTypePromoteLearnerAndRemoveReplica:      decodePromoteLearnerAndRemoveReplica,
	cmdTypeClearChannelWriteFence:              decodeClearChannelWriteFence,
	cmdTypeAbortChannelMigration:               decodeAbortChannelMigration,
	cmdTypeGarbageCollectMigrationTasks:        decodeGarbageCollectMigrationTasks,
	cmdTypeCreateChannelMigrationGuarded:       decodeCreateChannelMigrationTaskWithRuntimeGuard,
}

// DecodeCommandHashSlots returns the logical Hash Slots mutated by one exact
// Slot FSM command under its Raft-envelope Hash Slot.
func DecodeCommandHashSlots(data []byte, envelopeHashSlot uint16) ([]uint16, error) {
	decoded, err := decodeCommand(data)
	if err != nil {
		return nil, err
	}
	return append([]uint16(nil), commandApplyHashSlots(decoded, envelopeHashSlot)...), nil
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

type createChannelCmd struct {
	channel metadb.Channel
	result  *metadb.ChannelConditionalMutationResult
}

func (c *createChannelCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	result, err := wb.CreateChannelConditionally(hashSlot, c.channel)
	c.result = result
	return err
}

func (c *createChannelCmd) applyResult() []byte {
	return EncodeChannelConditionalMutationResult(c.result)
}

type patchChannelBusinessFlagsCmd struct {
	channelID   string
	channelType int64
	flags       metadb.ChannelBusinessFlags
	result      *metadb.ChannelConditionalMutationResult
}

func (c *patchChannelBusinessFlagsCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	result, err := wb.PatchChannelBusinessFlags(hashSlot, c.channelID, c.channelType, c.flags)
	c.result = result
	return err
}

func (c *patchChannelBusinessFlagsCmd) applyResult() []byte {
	return EncodeChannelConditionalMutationResult(c.result)
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
	result                    *metadb.SubscriberMutationResult
}

func (c *addSubscribersCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	result, err := wb.AddSubscribersCounted(hashSlot, c.channelID, c.channelType, c.uids, c.subscriberMutationVersion)
	c.result = result
	return err
}

func (c *addSubscribersCmd) applyResult() []byte {
	return EncodeSubscriberMutationResult(c.result)
}

// --- RemoveSubscribers ---

type removeSubscribersCmd struct {
	channelID                 string
	channelType               int64
	uids                      []string
	subscriberMutationVersion uint64
	result                    *metadb.SubscriberMutationResult
}

func (c *removeSubscribersCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	result, err := wb.RemoveSubscribersCounted(hashSlot, c.channelID, c.channelType, c.uids, c.subscriberMutationVersion)
	c.result = result
	return err
}

func (c *removeSubscribersCmd) applyResult() []byte {
	return EncodeSubscriberMutationResult(c.result)
}

var (
	subscriberMutationResultMagic         = [...]byte{'W', 'K', 'S', 'M', 1}
	channelConditionalMutationResultMagic = [...]byte{'W', 'K', 'C', 'M', 1}
)

// EncodeChannelConditionalMutationResult encodes whether a conditional channel mutation applied.
func EncodeChannelConditionalMutationResult(result *metadb.ChannelConditionalMutationResult) []byte {
	applied := byte(0)
	if result != nil && result.Applied {
		applied = 1
	}
	return append(append([]byte(nil), channelConditionalMutationResultMagic[:]...), applied)
}

// DecodeChannelConditionalMutationResult decodes whether a conditional channel mutation applied.
func DecodeChannelConditionalMutationResult(data []byte) (bool, error) {
	if len(data) != len(channelConditionalMutationResultMagic)+1 ||
		!bytes.HasPrefix(data, channelConditionalMutationResultMagic[:]) {
		return false, fmt.Errorf("%w: conditional channel mutation result", metadb.ErrCorruptValue)
	}
	switch data[len(channelConditionalMutationResultMagic)] {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("%w: conditional channel mutation applied value", metadb.ErrCorruptValue)
	}
}

// EncodeSubscriberMutationResult encodes a durable subscriber-set apply result.
func EncodeSubscriberMutationResult(result *metadb.SubscriberMutationResult) []byte {
	buf := append([]byte(nil), subscriberMutationResultMagic[:]...)
	if result == nil {
		return append(buf, 0, 0)
	}
	buf = binary.AppendUvarint(buf, uint64(result.RequestedCount))
	return binary.AppendUvarint(buf, uint64(result.ChangedCount))
}

// DecodeSubscriberMutationResult decodes a subscriber-set apply result.
func DecodeSubscriberMutationResult(data []byte) (metadb.SubscriberMutationResult, error) {
	if !bytes.HasPrefix(data, subscriberMutationResultMagic[:]) {
		return metadb.SubscriberMutationResult{}, fmt.Errorf("%w: subscriber mutation result", metadb.ErrCorruptValue)
	}
	remaining := data[len(subscriberMutationResultMagic):]
	requested, n := binary.Uvarint(remaining)
	if n <= 0 {
		return metadb.SubscriberMutationResult{}, fmt.Errorf("%w: subscriber mutation requested count", metadb.ErrCorruptValue)
	}
	remaining = remaining[n:]
	changed, n := binary.Uvarint(remaining)
	if n <= 0 || n != len(remaining) {
		return metadb.SubscriberMutationResult{}, fmt.Errorf("%w: subscriber mutation changed count", metadb.ErrCorruptValue)
	}
	return metadb.SubscriberMutationResult{RequestedCount: int(requested), ChangedCount: int(changed)}, nil
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

type advanceUserChannelMembershipReadSeqCmd struct {
	memberships []metadb.UserChannelMembership
}

func (c *advanceUserChannelMembershipReadSeqCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, membership := range c.memberships {
		if err := wb.AdvanceUserChannelMembershipReadSeq(hashSlot, membership.UID, metadb.ChannelKey{ChannelID: membership.ChannelID, ChannelType: membership.ChannelType}, membership.ReadSeq, membership.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

type hideUserChannelMembershipCmd struct {
	memberships []metadb.UserChannelMembership
}

func (c *hideUserChannelMembershipCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, membership := range c.memberships {
		if err := wb.HideUserChannelMembership(hashSlot, membership.UID, metadb.ChannelKey{ChannelID: membership.ChannelID, ChannelType: membership.ChannelType}, membership.DeletedToSeq, membership.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

type activateUserChannelMembershipCmd struct {
	memberships []metadb.UserChannelMembership
}

func (c *activateUserChannelMembershipCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, membership := range c.memberships {
		if err := wb.ActivateUserChannelMembership(hashSlot, membership.UID, metadb.ChannelKey{ChannelID: membership.ChannelID, ChannelType: membership.ChannelType}, membership.ActivatedAt, membership.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (c *deleteUserChannelMembershipsCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, membership := range c.memberships {
		if err := wb.UpsertUserChannelMembership(hashSlot, membership); err != nil {
			return err
		}
	}
	return nil
}

// --- UserCMDChannelMemberships ---

type upsertUserCMDChannelMembershipsCmd struct {
	memberships []metadb.UserCMDChannelMembership
}

func (c *upsertUserCMDChannelMembershipsCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, membership := range c.memberships {
		if err := wb.UpsertUserCMDChannelMembership(hashSlot, membership); err != nil {
			return err
		}
	}
	return nil
}

type advanceUserCMDChannelMembershipAcksCmd struct {
	memberships []metadb.UserCMDChannelMembership
}

func (c *advanceUserCMDChannelMembershipAcksCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, membership := range c.memberships {
		if err := wb.AdvanceUserCMDChannelMembershipAckSeq(hashSlot, membership); err != nil {
			return err
		}
	}
	return nil
}

type tombstoneUserCMDChannelMembershipsCmd struct {
	memberships []metadb.UserCMDChannelMembership
}

func (c *tombstoneUserCMDChannelMembershipsCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, membership := range c.memberships {
		if err := wb.TombstoneUserCMDChannelMembership(hashSlot, membership); err != nil {
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
	return encodeChannelCommand(cmdTypeUpsertChannel, ch)
}

// EncodeCreateChannelCommand encodes a create-only Channel mutation.
func EncodeCreateChannelCommand(ch metadb.Channel) []byte {
	return encodeChannelCommand(cmdTypeCreateChannel, ch)
}

// EncodePatchChannelBusinessFlagsCommand encodes an existing-only partial flag mutation.
func EncodePatchChannelBusinessFlagsCommand(channelID string, channelType int64, flags metadb.ChannelBusinessFlags) []byte {
	return encodeChannelCommand(cmdTypePatchChannelBusinessFlags, metadb.Channel{
		ChannelID:   channelID,
		ChannelType: channelType,
		Ban:         flags.Ban,
		Disband:     flags.Disband,
		SendBan:     flags.SendBan,
	})
}

func encodeChannelCommand(commandType uint8, ch metadb.Channel) []byte {
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
	buf[off] = commandType
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
	buf := make([]byte, 0, headerSize+len(memberships)*96)
	buf = append(buf, commandVersion, cmdTypeDeleteUserChannelMemberships)
	for _, membership := range memberships {
		buf = appendBytesTLVField(buf, tagUserChannelMembershipCommandEntry, encodeUserChannelMembershipEntry(membership, true))
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

// EncodeAdvanceUserChannelMembershipReadSeqCommand encodes monotonic badge-floor mutations.
func EncodeAdvanceUserChannelMembershipReadSeqCommand(memberships []metadb.UserChannelMembership) []byte {
	return encodeUserChannelMembershipMutationCommand(cmdTypeAdvanceUserChannelMembershipReadSeq, memberships)
}

// EncodeHideUserChannelMembershipCommand encodes visibility-floor and activation-clear mutations.
func EncodeHideUserChannelMembershipCommand(memberships []metadb.UserChannelMembership) []byte {
	return encodeUserChannelMembershipMutationCommand(cmdTypeHideUserChannelMembership, memberships)
}

// EncodeActivateUserChannelMembershipCommand encodes explicit activation mutations.
func EncodeActivateUserChannelMembershipCommand(memberships []metadb.UserChannelMembership) []byte {
	return encodeUserChannelMembershipMutationCommand(cmdTypeActivateUserChannelMembership, memberships)
}

func encodeUserChannelMembershipMutationCommand(commandType uint8, memberships []metadb.UserChannelMembership) []byte {
	buf := make([]byte, 0, headerSize+len(memberships)*96)
	buf = append(buf, commandVersion, commandType)
	for _, membership := range memberships {
		buf = appendBytesTLVField(buf, tagUserChannelMembershipCommandEntry, encodeUserChannelMembershipEntry(membership, true))
	}
	return buf
}

// EncodeUpsertUserCMDChannelMembershipsCommand encodes CMD directory bindings.
func EncodeUpsertUserCMDChannelMembershipsCommand(memberships []metadb.UserCMDChannelMembership) []byte {
	return encodeUserCMDChannelMembershipCommand(cmdTypeUpsertUserCMDChannelMemberships, memberships)
}

// EncodeAdvanceUserCMDChannelMembershipAcksCommand encodes monotonic CMD acknowledgements.
func EncodeAdvanceUserCMDChannelMembershipAcksCommand(memberships []metadb.UserCMDChannelMembership) []byte {
	return encodeUserCMDChannelMembershipCommand(cmdTypeAdvanceUserCMDChannelMembershipAcks, memberships)
}

// EncodeTombstoneUserCMDChannelMembershipsCommand encodes CMD directory unbinds.
func EncodeTombstoneUserCMDChannelMembershipsCommand(memberships []metadb.UserCMDChannelMembership) []byte {
	return encodeUserCMDChannelMembershipCommand(cmdTypeTombstoneUserCMDChannelMemberships, memberships)
}

func encodeUserCMDChannelMembershipCommand(commandType uint8, memberships []metadb.UserCMDChannelMembership) []byte {
	buf := make([]byte, 0, headerSize+len(memberships)*96)
	buf = append(buf, commandVersion, commandType)
	for _, membership := range memberships {
		buf = appendBytesTLVField(buf, tagUserCMDChannelMembershipCommandEntry, encodeUserCMDChannelMembershipEntry(membership))
	}
	return buf
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

// ValidateSubscriberCommandLimits rejects subscriber mutations that would create oversized Raft entries.
func ValidateSubscriberCommandLimits(uids []string) error {
	if err := validateSubscriberCommandUIDCount(len(uids)); err != nil {
		return err
	}
	return validateSubscriberCommandUIDBytes(len(encodeStringSet(uids)))
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
	buf := make([]byte, 0, 112)
	buf = appendStringTLVField(buf, tagUserChannelMembershipEntryUID, membership.UID)
	buf = appendStringTLVField(buf, tagUserChannelMembershipChannelID, membership.ChannelID)
	buf = appendInt64TLVField(buf, tagUserChannelMembershipChannelType, membership.ChannelType)
	if includeState {
		buf = appendUint64TLVField(buf, tagUserChannelMembershipJoinSeq, membership.JoinSeq)
		buf = appendUint64TLVField(buf, tagUserChannelMembershipReadSeq, membership.ReadSeq)
		buf = appendUint64TLVField(buf, tagUserChannelMembershipDeletedSeq, membership.DeletedToSeq)
		buf = appendInt64TLVField(buf, tagUserChannelMembershipActivatedAt, membership.ActivatedAt)
		if membership.Tombstone {
			buf = appendUint64TLVField(buf, tagUserChannelMembershipTombstone, 1)
		} else {
			buf = appendUint64TLVField(buf, tagUserChannelMembershipTombstone, 0)
		}
		buf = appendInt64TLVField(buf, tagUserChannelMembershipTombstoneAt, membership.TombstoneAt)
		buf = appendUint64TLVField(buf, tagUserChannelMembershipSourceVer, membership.SourceVersion)
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

func encodeUserCMDChannelMembershipEntry(membership metadb.UserCMDChannelMembership) []byte {
	buf := make([]byte, 0, 96)
	buf = appendStringTLVField(buf, tagUserCMDChannelMembershipUID, membership.UID)
	buf = appendStringTLVField(buf, tagUserCMDChannelMembershipChannelID, membership.CommandChannelID)
	buf = appendInt64TLVField(buf, tagUserCMDChannelMembershipChannelType, membership.ChannelType)
	buf = appendUint64TLVField(buf, tagUserCMDChannelMembershipStartSeq, membership.StartSeq)
	buf = appendUint64TLVField(buf, tagUserCMDChannelMembershipAckSeq, membership.AckSeq)
	if membership.Tombstone {
		buf = appendUint64TLVField(buf, tagUserCMDChannelMembershipTombstone, 1)
	} else {
		buf = appendUint64TLVField(buf, tagUserCMDChannelMembershipTombstone, 0)
	}
	buf = appendInt64TLVField(buf, tagUserCMDChannelMembershipTombstoneAt, membership.TombstoneAt)
	return appendInt64TLVField(buf, tagUserCMDChannelMembershipUpdatedAt, membership.UpdatedAt)
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
	var haveUID, haveChannelID, haveChannelType, haveJoinSeq, haveReadSeq, haveDeletedSeq, haveActivatedAt, haveTombstone, haveTombstoneAt, haveSourceVersion, haveUpdatedAt bool
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
		case tagUserChannelMembershipReadSeq:
			if len(value) != 8 {
				return metadb.UserChannelMembership{}, fmt.Errorf("%w: bad user channel membership ReadSeq length", metadb.ErrCorruptValue)
			}
			membership.ReadSeq = binary.BigEndian.Uint64(value)
			haveReadSeq = true
		case tagUserChannelMembershipDeletedSeq:
			if len(value) != 8 {
				return metadb.UserChannelMembership{}, fmt.Errorf("%w: bad user channel membership DeletedToSeq length", metadb.ErrCorruptValue)
			}
			membership.DeletedToSeq = binary.BigEndian.Uint64(value)
			haveDeletedSeq = true
		case tagUserChannelMembershipActivatedAt:
			if len(value) != 8 {
				return metadb.UserChannelMembership{}, fmt.Errorf("%w: bad user channel membership ActivatedAt length", metadb.ErrCorruptValue)
			}
			membership.ActivatedAt = int64(binary.BigEndian.Uint64(value))
			haveActivatedAt = true
		case tagUserChannelMembershipTombstone:
			if len(value) != 8 || binary.BigEndian.Uint64(value) > 1 {
				return metadb.UserChannelMembership{}, fmt.Errorf("%w: bad user channel membership Tombstone", metadb.ErrCorruptValue)
			}
			membership.Tombstone = binary.BigEndian.Uint64(value) == 1
			haveTombstone = true
		case tagUserChannelMembershipTombstoneAt:
			if len(value) != 8 {
				return metadb.UserChannelMembership{}, fmt.Errorf("%w: bad user channel membership TombstoneAt length", metadb.ErrCorruptValue)
			}
			membership.TombstoneAt = int64(binary.BigEndian.Uint64(value))
			haveTombstoneAt = true
		case tagUserChannelMembershipSourceVer:
			if len(value) != 8 {
				return metadb.UserChannelMembership{}, fmt.Errorf("%w: bad user channel membership SourceVersion length", metadb.ErrCorruptValue)
			}
			membership.SourceVersion = binary.BigEndian.Uint64(value)
			haveSourceVersion = true
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
	if !haveUID || !haveChannelID || !haveChannelType || requireState && (!haveJoinSeq || !haveReadSeq || !haveDeletedSeq || !haveActivatedAt || !haveTombstone || !haveTombstoneAt || !haveSourceVersion || !haveUpdatedAt) {
		return metadb.UserChannelMembership{}, fmt.Errorf("%w: incomplete user channel membership record", metadb.ErrCorruptValue)
	}
	return membership, nil
}

func decodeUserCMDChannelMembershipEntries(data []byte) ([]metadb.UserCMDChannelMembership, error) {
	var memberships []metadb.UserCMDChannelMembership
	for off := 0; off < len(data); {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		if tag != tagUserCMDChannelMembershipCommandEntry {
			continue
		}
		membership, err := decodeUserCMDChannelMembershipEntry(value)
		if err != nil {
			return nil, err
		}
		memberships = append(memberships, membership)
	}
	return memberships, nil
}

func decodeUserCMDChannelMembershipEntry(data []byte) (metadb.UserCMDChannelMembership, error) {
	var membership metadb.UserCMDChannelMembership
	var haveUID, haveChannelID, haveChannelType, haveStartSeq, haveAckSeq, haveTombstone, haveTombstoneAt, haveUpdatedAt bool
	for off := 0; off < len(data); {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.UserCMDChannelMembership{}, err
		}
		off += n
		switch tag {
		case tagUserCMDChannelMembershipUID:
			membership.UID, haveUID = string(value), true
		case tagUserCMDChannelMembershipChannelID:
			membership.CommandChannelID, haveChannelID = string(value), true
		case tagUserCMDChannelMembershipChannelType:
			if len(value) != 8 {
				return metadb.UserCMDChannelMembership{}, fmt.Errorf("%w: bad user CMD channel membership ChannelType length", metadb.ErrCorruptValue)
			}
			membership.ChannelType, haveChannelType = int64(binary.BigEndian.Uint64(value)), true
		case tagUserCMDChannelMembershipStartSeq:
			if len(value) != 8 {
				return metadb.UserCMDChannelMembership{}, fmt.Errorf("%w: bad user CMD channel membership StartSeq length", metadb.ErrCorruptValue)
			}
			membership.StartSeq, haveStartSeq = binary.BigEndian.Uint64(value), true
		case tagUserCMDChannelMembershipAckSeq:
			if len(value) != 8 {
				return metadb.UserCMDChannelMembership{}, fmt.Errorf("%w: bad user CMD channel membership AckSeq length", metadb.ErrCorruptValue)
			}
			membership.AckSeq, haveAckSeq = binary.BigEndian.Uint64(value), true
		case tagUserCMDChannelMembershipTombstone:
			if len(value) != 8 || binary.BigEndian.Uint64(value) > 1 {
				return metadb.UserCMDChannelMembership{}, fmt.Errorf("%w: bad user CMD channel membership Tombstone", metadb.ErrCorruptValue)
			}
			membership.Tombstone, haveTombstone = binary.BigEndian.Uint64(value) == 1, true
		case tagUserCMDChannelMembershipTombstoneAt:
			if len(value) != 8 {
				return metadb.UserCMDChannelMembership{}, fmt.Errorf("%w: bad user CMD channel membership TombstoneAt length", metadb.ErrCorruptValue)
			}
			membership.TombstoneAt, haveTombstoneAt = int64(binary.BigEndian.Uint64(value)), true
		case tagUserCMDChannelMembershipUpdatedAt:
			if len(value) != 8 {
				return metadb.UserCMDChannelMembership{}, fmt.Errorf("%w: bad user CMD channel membership UpdatedAt length", metadb.ErrCorruptValue)
			}
			membership.UpdatedAt, haveUpdatedAt = int64(binary.BigEndian.Uint64(value)), true
		}
	}
	if !haveUID || !haveChannelID || !haveChannelType || !haveStartSeq || !haveAckSeq || !haveTombstone || !haveTombstoneAt || !haveUpdatedAt {
		return metadb.UserCMDChannelMembership{}, fmt.Errorf("%w: incomplete user CMD channel membership record", metadb.ErrCorruptValue)
	}
	return membership, nil
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

func decodeAdvanceUserChannelMembershipReadSeq(data []byte) (command, error) {
	memberships, err := decodeUserChannelMembershipEntries(data, true)
	if err != nil {
		return nil, err
	}
	if len(memberships) == 0 {
		return nil, fmt.Errorf("%w: empty membership read-seq batch", metadb.ErrInvalidArgument)
	}
	return &advanceUserChannelMembershipReadSeqCmd{memberships: memberships}, nil
}

func decodeHideUserChannelMembership(data []byte) (command, error) {
	memberships, err := decodeUserChannelMembershipEntries(data, true)
	if err != nil {
		return nil, err
	}
	if len(memberships) == 0 {
		return nil, fmt.Errorf("%w: empty membership hide batch", metadb.ErrInvalidArgument)
	}
	return &hideUserChannelMembershipCmd{memberships: memberships}, nil
}

func decodeActivateUserChannelMembership(data []byte) (command, error) {
	memberships, err := decodeUserChannelMembershipEntries(data, true)
	if err != nil {
		return nil, err
	}
	if len(memberships) == 0 {
		return nil, fmt.Errorf("%w: empty membership activation batch", metadb.ErrInvalidArgument)
	}
	return &activateUserChannelMembershipCmd{memberships: memberships}, nil
}

func decodeUpsertUserCMDChannelMemberships(data []byte) (command, error) {
	memberships, err := decodeUserCMDChannelMembershipEntries(data)
	if err != nil {
		return nil, err
	}
	if len(memberships) == 0 {
		return nil, fmt.Errorf("%w: empty user CMD channel membership upsert batch", metadb.ErrInvalidArgument)
	}
	return &upsertUserCMDChannelMembershipsCmd{memberships: memberships}, nil
}

func decodeAdvanceUserCMDChannelMembershipAcks(data []byte) (command, error) {
	memberships, err := decodeUserCMDChannelMembershipEntries(data)
	if err != nil {
		return nil, err
	}
	if len(memberships) == 0 {
		return nil, fmt.Errorf("%w: empty user CMD channel membership ack batch", metadb.ErrInvalidArgument)
	}
	return &advanceUserCMDChannelMembershipAcksCmd{memberships: memberships}, nil
}

func decodeTombstoneUserCMDChannelMemberships(data []byte) (command, error) {
	memberships, err := decodeUserCMDChannelMembershipEntries(data)
	if err != nil {
		return nil, err
	}
	if len(memberships) == 0 {
		return nil, fmt.Errorf("%w: empty user CMD channel membership tombstone batch", metadb.ErrInvalidArgument)
	}
	return &tombstoneUserCMDChannelMembershipsCmd{memberships: memberships}, nil
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
	ch, err := decodeChannel(data)
	if err != nil {
		return nil, err
	}
	return &upsertChannelCmd{channel: ch}, nil
}

func decodeCreateChannel(data []byte) (command, error) {
	ch, err := decodeChannel(data)
	if err != nil {
		return nil, err
	}
	return &createChannelCmd{channel: ch}, nil
}

func decodePatchChannelBusinessFlags(data []byte) (command, error) {
	ch, err := decodeChannel(data)
	if err != nil {
		return nil, err
	}
	return &patchChannelBusinessFlagsCmd{
		channelID:   ch.ChannelID,
		channelType: ch.ChannelType,
		flags: metadb.ChannelBusinessFlags{
			Ban:     ch.Ban,
			Disband: ch.Disband,
			SendBan: ch.SendBan,
		},
	}, nil
}

func decodeChannel(data []byte) (metadb.Channel, error) {
	var ch metadb.Channel
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.Channel{}, err
		}
		off += n
		switch tag {
		case tagChannelID:
			ch.ChannelID = string(value)
		case tagChannelType:
			if len(value) != 8 {
				return metadb.Channel{}, fmt.Errorf("%w: bad ChannelType length", metadb.ErrCorruptValue)
			}
			ch.ChannelType = int64(binary.BigEndian.Uint64(value))
		case tagChannelBan:
			if len(value) != 8 {
				return metadb.Channel{}, fmt.Errorf("%w: bad Ban length", metadb.ErrCorruptValue)
			}
			ch.Ban = int64(binary.BigEndian.Uint64(value))
		case tagChannelDisband:
			if len(value) != 8 {
				return metadb.Channel{}, fmt.Errorf("%w: bad Disband length", metadb.ErrCorruptValue)
			}
			ch.Disband = int64(binary.BigEndian.Uint64(value))
		case tagChannelSendBan:
			if len(value) != 8 {
				return metadb.Channel{}, fmt.Errorf("%w: bad SendBan length", metadb.ErrCorruptValue)
			}
			ch.SendBan = int64(binary.BigEndian.Uint64(value))
		case tagChannelAllowStranger:
			if len(value) != 8 {
				return metadb.Channel{}, fmt.Errorf("%w: bad AllowStranger length", metadb.ErrCorruptValue)
			}
			ch.AllowStranger = int64(binary.BigEndian.Uint64(value))
		case tagChannelLarge:
			if len(value) != 8 {
				return metadb.Channel{}, fmt.Errorf("%w: bad Large length", metadb.ErrCorruptValue)
			}
			ch.Large = int64(binary.BigEndian.Uint64(value))
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return ch, nil
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
