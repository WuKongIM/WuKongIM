package fsm

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
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

	cmdTypeUpsertUser                    uint8 = 1
	cmdTypeUpsertChannel                 uint8 = 2
	cmdTypeDeleteChannel                 uint8 = 3
	cmdTypeUpsertChannelRuntimeMeta      uint8 = 4
	cmdTypeDeleteChannelRuntimeMeta      uint8 = 5
	cmdTypeCreateUser                    uint8 = 6
	cmdTypeUpsertDevice                  uint8 = 7
	cmdTypeAddSubscribers                uint8 = 8
	cmdTypeRemoveSubscribers             uint8 = 9
	cmdTypeUpsertUserConversationStates  uint8 = 10
	cmdTypeTouchUserConversationActiveAt uint8 = 11
	cmdTypeClearUserConversationActiveAt uint8 = 12
	cmdTypeUpsertChannelUpdateLogs       uint8 = 13
	cmdTypeDeleteChannelUpdateLogs       uint8 = 14

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
	tagChannelID   uint8 = 1
	tagChannelType uint8 = 2
	tagChannelBan  uint8 = 3

	// Channel runtime metadata field tags.
	tagRuntimeMetaChannelID    uint8 = 1
	tagRuntimeMetaChannelType  uint8 = 2
	tagRuntimeMetaChannelEpoch uint8 = 3
	tagRuntimeMetaLeaderEpoch  uint8 = 4
	tagRuntimeMetaReplicas     uint8 = 5
	tagRuntimeMetaISR          uint8 = 6
	tagRuntimeMetaLeader       uint8 = 7
	tagRuntimeMetaMinISR       uint8 = 8
	tagRuntimeMetaStatus       uint8 = 9
	tagRuntimeMetaFeatures     uint8 = 10
	tagRuntimeMetaLeaseUntilMS uint8 = 11

	// Subscriber field tags.
	tagSubscriberChannelID   uint8 = 1
	tagSubscriberChannelType uint8 = 2
	tagSubscriberUIDs        uint8 = 3

	// User conversation state field tags.
	tagUserConversationStateEntryUID          uint8 = 1
	tagUserConversationStateEntryChannelID    uint8 = 2
	tagUserConversationStateEntryChannelType  uint8 = 3
	tagUserConversationStateEntryReadSeq      uint8 = 4
	tagUserConversationStateEntryDeletedToSeq uint8 = 5
	tagUserConversationStateEntryActiveAt     uint8 = 6
	tagUserConversationStateEntryUpdatedAt    uint8 = 7

	// User conversation active patch field tags.
	tagUserConversationActivePatchEntryUID         uint8 = 1
	tagUserConversationActivePatchEntryChannelID   uint8 = 2
	tagUserConversationActivePatchEntryChannelType uint8 = 3
	tagUserConversationActivePatchEntryActiveAt    uint8 = 4

	// Clear user conversation active command field tags.
	tagClearUserConversationActiveUID uint8 = 1
	tagClearUserConversationActiveKey uint8 = 2

	// Conversation key field tags.
	tagConversationKeyEntryChannelID   uint8 = 1
	tagConversationKeyEntryChannelType uint8 = 2

	// Channel update log field tags.
	tagChannelUpdateLogEntryChannelID       uint8 = 1
	tagChannelUpdateLogEntryChannelType     uint8 = 2
	tagChannelUpdateLogEntryUpdatedAt       uint8 = 3
	tagChannelUpdateLogEntryLastMsgSeq      uint8 = 4
	tagChannelUpdateLogEntryLastClientMsgNo uint8 = 5
	tagChannelUpdateLogEntryLastMsgAt       uint8 = 6

	// ApplyResultOK is the result returned by Apply/ApplyBatch on success.
	ApplyResultOK = "ok"

	// headerSize is version (1) + cmdType (1).
	headerSize = 2
	// tlvOverhead is tag (1) + length (4).
	tlvOverhead = 5
)

// command is the decoded representation of a state machine command.
// Each command type implements this interface, carrying its own typed
// payload and knowing how to apply itself to a WriteBatch.
type command interface {
	apply(wb *metadb.WriteBatch, hashSlot uint16) error
}

// commandDecoder parses TLV fields after the header into a typed command.
type commandDecoder func(data []byte) (command, error)

// commandDecoders maps command type bytes to their decoders.
// To add a new command type, create a struct implementing command,
// a corresponding encode function, a decoder, and register it here.
var commandDecoders = map[uint8]commandDecoder{
	cmdTypeUpsertUser:                    decodeUpsertUser,
	cmdTypeUpsertChannel:                 decodeUpsertChannel,
	cmdTypeDeleteChannel:                 decodeDeleteChannel,
	cmdTypeUpsertChannelRuntimeMeta:      decodeUpsertChannelRuntimeMeta,
	cmdTypeDeleteChannelRuntimeMeta:      decodeDeleteChannelRuntimeMeta,
	cmdTypeCreateUser:                    decodeCreateUser,
	cmdTypeUpsertDevice:                  decodeUpsertDevice,
	cmdTypeAddSubscribers:                decodeAddSubscribers,
	cmdTypeRemoveSubscribers:             decodeRemoveSubscribers,
	cmdTypeUpsertUserConversationStates:  decodeUpsertUserConversationStates,
	cmdTypeTouchUserConversationActiveAt: decodeTouchUserConversationActiveAt,
	cmdTypeClearUserConversationActiveAt: decodeClearUserConversationActiveAt,
	cmdTypeUpsertChannelUpdateLogs:       decodeUpsertChannelUpdateLogs,
	cmdTypeDeleteChannelUpdateLogs:       decodeDeleteChannelUpdateLogs,
	cmdTypeApplyDelta:                    decodeApplyDelta,
	cmdTypeEnterFence:                    decodeEnterFence,
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

// --- AddSubscribers ---

type addSubscribersCmd struct {
	channelID   string
	channelType int64
	uids        []string
}

func (c *addSubscribersCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.AddSubscribers(hashSlot, c.channelID, c.channelType, c.uids)
}

// --- RemoveSubscribers ---

type removeSubscribersCmd struct {
	channelID   string
	channelType int64
	uids        []string
}

func (c *removeSubscribersCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.RemoveSubscribers(hashSlot, c.channelID, c.channelType, c.uids)
}

// --- UpsertUserConversationStates ---

type upsertUserConversationStatesCmd struct {
	states []metadb.UserConversationState
}

func (c *upsertUserConversationStatesCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, state := range c.states {
		if err := wb.UpsertUserConversationState(hashSlot, state); err != nil {
			return err
		}
	}
	return nil
}

// --- TouchUserConversationActiveAt ---

type touchUserConversationActiveAtCmd struct {
	patches []metadb.UserConversationActivePatch
}

func (c *touchUserConversationActiveAtCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.TouchUserConversationActiveAt(hashSlot, c.patches)
}

// --- ClearUserConversationActiveAt ---

type clearUserConversationActiveAtCmd struct {
	uid  string
	keys []metadb.ConversationKey
}

func (c *clearUserConversationActiveAtCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.ClearUserConversationActiveAt(hashSlot, c.uid, c.keys)
}

// --- UpsertChannelUpdateLogs ---

type upsertChannelUpdateLogsCmd struct {
	entries []metadb.ChannelUpdateLog
}

func (c *upsertChannelUpdateLogsCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, entry := range c.entries {
		if err := wb.UpsertChannelUpdateLog(hashSlot, entry); err != nil {
			return err
		}
	}
	return nil
}

// --- DeleteChannelUpdateLogs ---

type deleteChannelUpdateLogsCmd struct {
	keys []metadb.ConversationKey
}

func (c *deleteChannelUpdateLogsCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.DeleteChannelUpdateLogs(hashSlot, c.keys)
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
	// header + 1 string field + 2 int64 fields
	size := headerSize +
		tlvOverhead + idLen +
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
	_ = putInt64Field(buf, off, tagChannelBan, ch.Ban)

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

// EncodeAddSubscribersCommand encodes a subscriber add command.
func EncodeAddSubscribersCommand(channelID string, channelType int64, uids []string) []byte {
	return encodeSubscribersCommand(cmdTypeAddSubscribers, channelID, channelType, uids)
}

// EncodeRemoveSubscribersCommand encodes a subscriber removal command.
func EncodeRemoveSubscribersCommand(channelID string, channelType int64, uids []string) []byte {
	return encodeSubscribersCommand(cmdTypeRemoveSubscribers, channelID, channelType, uids)
}

// EncodeUpsertUserConversationStatesCommand encodes a batch of user conversation state upserts.
func EncodeUpsertUserConversationStatesCommand(states []metadb.UserConversationState) []byte {
	buf := make([]byte, 0, headerSize+len(states)*64)
	buf = append(buf, commandVersion, cmdTypeUpsertUserConversationStates)
	for _, state := range states {
		buf = appendBytesTLVField(buf, tagUserConversationStateEntryUID, encodeUserConversationStateEntry(state))
	}
	return buf
}

// EncodeTouchUserConversationActiveAtCommand encodes a batch of user conversation active-at patches.
func EncodeTouchUserConversationActiveAtCommand(patches []metadb.UserConversationActivePatch) []byte {
	buf := make([]byte, 0, headerSize+len(patches)*48)
	buf = append(buf, commandVersion, cmdTypeTouchUserConversationActiveAt)
	for _, patch := range patches {
		buf = appendBytesTLVField(buf, tagUserConversationActivePatchEntryUID, encodeUserConversationActivePatchEntry(patch))
	}
	return buf
}

// EncodeClearUserConversationActiveAtCommand encodes a uid-scoped active-at clear command.
func EncodeClearUserConversationActiveAtCommand(uid string, keys []metadb.ConversationKey) []byte {
	buf := make([]byte, 0, headerSize+len(uid)+len(keys)*32)
	buf = append(buf, commandVersion, cmdTypeClearUserConversationActiveAt)
	buf = appendStringTLVField(buf, tagClearUserConversationActiveUID, uid)
	for _, key := range keys {
		buf = appendBytesTLVField(buf, tagClearUserConversationActiveKey, encodeConversationKeyEntry(key))
	}
	return buf
}

// EncodeUpsertChannelUpdateLogsCommand encodes a batch of channel update log upserts.
func EncodeUpsertChannelUpdateLogsCommand(entries []metadb.ChannelUpdateLog) []byte {
	buf := make([]byte, 0, headerSize+len(entries)*64)
	buf = append(buf, commandVersion, cmdTypeUpsertChannelUpdateLogs)
	for _, entry := range entries {
		buf = appendBytesTLVField(buf, tagChannelUpdateLogEntryChannelID, encodeChannelUpdateLogEntry(entry))
	}
	return buf
}

// EncodeDeleteChannelUpdateLogsCommand encodes a batch of channel update log deletions.
func EncodeDeleteChannelUpdateLogsCommand(keys []metadb.ConversationKey) []byte {
	buf := make([]byte, 0, headerSize+len(keys)*32)
	buf = append(buf, commandVersion, cmdTypeDeleteChannelUpdateLogs)
	for _, key := range keys {
		buf = appendBytesTLVField(buf, tagConversationKeyEntryChannelID, encodeConversationKeyEntry(key))
	}
	return buf
}

func encodeSubscribersCommand(cmdType uint8, channelID string, channelType int64, uids []string) []byte {
	buf := make([]byte, 0, headerSize+len(channelID)+len(uids)*8)
	buf = append(buf, commandVersion, cmdType)
	buf = appendStringTLVField(buf, tagSubscriberChannelID, channelID)
	buf = appendInt64TLVField(buf, tagSubscriberChannelType, channelType)
	buf = appendBytesTLVField(buf, tagSubscriberUIDs, encodeStringSet(uids))
	return buf
}

func encodeUserConversationStateEntry(state metadb.UserConversationState) []byte {
	buf := make([]byte, 0, 64)
	buf = appendStringTLVField(buf, tagUserConversationStateEntryUID, state.UID)
	buf = appendStringTLVField(buf, tagUserConversationStateEntryChannelID, state.ChannelID)
	buf = appendInt64TLVField(buf, tagUserConversationStateEntryChannelType, state.ChannelType)
	buf = appendUint64TLVField(buf, tagUserConversationStateEntryReadSeq, state.ReadSeq)
	buf = appendUint64TLVField(buf, tagUserConversationStateEntryDeletedToSeq, state.DeletedToSeq)
	buf = appendInt64TLVField(buf, tagUserConversationStateEntryActiveAt, state.ActiveAt)
	buf = appendInt64TLVField(buf, tagUserConversationStateEntryUpdatedAt, state.UpdatedAt)
	return buf
}

func encodeUserConversationActivePatchEntry(patch metadb.UserConversationActivePatch) []byte {
	buf := make([]byte, 0, 48)
	buf = appendStringTLVField(buf, tagUserConversationActivePatchEntryUID, patch.UID)
	buf = appendStringTLVField(buf, tagUserConversationActivePatchEntryChannelID, patch.ChannelID)
	buf = appendInt64TLVField(buf, tagUserConversationActivePatchEntryChannelType, patch.ChannelType)
	buf = appendInt64TLVField(buf, tagUserConversationActivePatchEntryActiveAt, patch.ActiveAt)
	return buf
}

func encodeConversationKeyEntry(key metadb.ConversationKey) []byte {
	buf := make([]byte, 0, 32)
	buf = appendStringTLVField(buf, tagConversationKeyEntryChannelID, key.ChannelID)
	buf = appendInt64TLVField(buf, tagConversationKeyEntryChannelType, key.ChannelType)
	return buf
}

func encodeChannelUpdateLogEntry(entry metadb.ChannelUpdateLog) []byte {
	buf := make([]byte, 0, 64)
	buf = appendStringTLVField(buf, tagChannelUpdateLogEntryChannelID, entry.ChannelID)
	buf = appendInt64TLVField(buf, tagChannelUpdateLogEntryChannelType, entry.ChannelType)
	buf = appendInt64TLVField(buf, tagChannelUpdateLogEntryUpdatedAt, entry.UpdatedAt)
	buf = appendUint64TLVField(buf, tagChannelUpdateLogEntryLastMsgSeq, entry.LastMsgSeq)
	buf = appendStringTLVField(buf, tagChannelUpdateLogEntryLastClientMsgNo, entry.LastClientMsgNo)
	buf = appendInt64TLVField(buf, tagChannelUpdateLogEntryLastMsgAt, entry.LastMsgAt)
	return buf
}

func decodeUserConversationStateEntries(data []byte) ([]metadb.UserConversationState, error) {
	var states []metadb.UserConversationState
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagUserConversationStateEntryUID:
			state, err := decodeUserConversationStateEntry(value)
			if err != nil {
				return nil, err
			}
			states = append(states, state)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return states, nil
}

func decodeUserConversationActivePatchEntries(data []byte) ([]metadb.UserConversationActivePatch, error) {
	var patches []metadb.UserConversationActivePatch
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagUserConversationActivePatchEntryUID:
			patch, err := decodeUserConversationActivePatchEntry(value)
			if err != nil {
				return nil, err
			}
			patches = append(patches, patch)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return patches, nil
}

func decodeConversationKeyEntries(data []byte) ([]metadb.ConversationKey, error) {
	var keys []metadb.ConversationKey
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagConversationKeyEntryChannelID:
			key, err := decodeConversationKeyEntry(value)
			if err != nil {
				return nil, err
			}
			keys = append(keys, key)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return keys, nil
}

func decodeChannelUpdateLogEntries(data []byte) ([]metadb.ChannelUpdateLog, error) {
	var entries []metadb.ChannelUpdateLog
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagChannelUpdateLogEntryChannelID:
			entry, err := decodeChannelUpdateLogEntry(value)
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

func decodeUserConversationStateEntry(data []byte) (metadb.UserConversationState, error) {
	var state metadb.UserConversationState
	var haveUID, haveChannelID, haveChannelType, haveReadSeq, haveDeletedToSeq, haveActiveAt, haveUpdatedAt bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.UserConversationState{}, err
		}
		off += n
		switch tag {
		case tagUserConversationStateEntryUID:
			state.UID = string(value)
			haveUID = true
		case tagUserConversationStateEntryChannelID:
			state.ChannelID = string(value)
			haveChannelID = true
		case tagUserConversationStateEntryChannelType:
			if len(value) != 8 {
				return metadb.UserConversationState{}, fmt.Errorf("%w: bad conversation ChannelType length", metadb.ErrCorruptValue)
			}
			state.ChannelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		case tagUserConversationStateEntryReadSeq:
			if len(value) != 8 {
				return metadb.UserConversationState{}, fmt.Errorf("%w: bad conversation ReadSeq length", metadb.ErrCorruptValue)
			}
			state.ReadSeq = binary.BigEndian.Uint64(value)
			haveReadSeq = true
		case tagUserConversationStateEntryDeletedToSeq:
			if len(value) != 8 {
				return metadb.UserConversationState{}, fmt.Errorf("%w: bad conversation DeletedToSeq length", metadb.ErrCorruptValue)
			}
			state.DeletedToSeq = binary.BigEndian.Uint64(value)
			haveDeletedToSeq = true
		case tagUserConversationStateEntryActiveAt:
			if len(value) != 8 {
				return metadb.UserConversationState{}, fmt.Errorf("%w: bad conversation ActiveAt length", metadb.ErrCorruptValue)
			}
			state.ActiveAt = int64(binary.BigEndian.Uint64(value))
			haveActiveAt = true
		case tagUserConversationStateEntryUpdatedAt:
			if len(value) != 8 {
				return metadb.UserConversationState{}, fmt.Errorf("%w: bad conversation UpdatedAt length", metadb.ErrCorruptValue)
			}
			state.UpdatedAt = int64(binary.BigEndian.Uint64(value))
			haveUpdatedAt = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveUID || !haveChannelID || !haveChannelType || !haveReadSeq || !haveDeletedToSeq || !haveActiveAt || !haveUpdatedAt {
		return metadb.UserConversationState{}, fmt.Errorf("%w: incomplete user conversation state record", metadb.ErrCorruptValue)
	}
	return state, nil
}

func decodeUserConversationActivePatchEntry(data []byte) (metadb.UserConversationActivePatch, error) {
	var patch metadb.UserConversationActivePatch
	var haveUID, haveChannelID, haveChannelType, haveActiveAt bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.UserConversationActivePatch{}, err
		}
		off += n
		switch tag {
		case tagUserConversationActivePatchEntryUID:
			patch.UID = string(value)
			haveUID = true
		case tagUserConversationActivePatchEntryChannelID:
			patch.ChannelID = string(value)
			haveChannelID = true
		case tagUserConversationActivePatchEntryChannelType:
			if len(value) != 8 {
				return metadb.UserConversationActivePatch{}, fmt.Errorf("%w: bad conversation ChannelType length", metadb.ErrCorruptValue)
			}
			patch.ChannelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		case tagUserConversationActivePatchEntryActiveAt:
			if len(value) != 8 {
				return metadb.UserConversationActivePatch{}, fmt.Errorf("%w: bad conversation ActiveAt length", metadb.ErrCorruptValue)
			}
			patch.ActiveAt = int64(binary.BigEndian.Uint64(value))
			haveActiveAt = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveUID || !haveChannelID || !haveChannelType || !haveActiveAt {
		return metadb.UserConversationActivePatch{}, fmt.Errorf("%w: incomplete user conversation active patch record", metadb.ErrCorruptValue)
	}
	return patch, nil
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

func decodeChannelUpdateLogEntry(data []byte) (metadb.ChannelUpdateLog, error) {
	var entry metadb.ChannelUpdateLog
	var haveChannelID, haveChannelType, haveUpdatedAt, haveLastMsgSeq, haveLastClientMsgNo, haveLastMsgAt bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.ChannelUpdateLog{}, err
		}
		off += n
		switch tag {
		case tagChannelUpdateLogEntryChannelID:
			entry.ChannelID = string(value)
			haveChannelID = true
		case tagChannelUpdateLogEntryChannelType:
			if len(value) != 8 {
				return metadb.ChannelUpdateLog{}, fmt.Errorf("%w: bad channel update ChannelType length", metadb.ErrCorruptValue)
			}
			entry.ChannelType = int64(binary.BigEndian.Uint64(value))
			haveChannelType = true
		case tagChannelUpdateLogEntryUpdatedAt:
			if len(value) != 8 {
				return metadb.ChannelUpdateLog{}, fmt.Errorf("%w: bad channel update UpdatedAt length", metadb.ErrCorruptValue)
			}
			entry.UpdatedAt = int64(binary.BigEndian.Uint64(value))
			haveUpdatedAt = true
		case tagChannelUpdateLogEntryLastMsgSeq:
			if len(value) != 8 {
				return metadb.ChannelUpdateLog{}, fmt.Errorf("%w: bad channel update LastMsgSeq length", metadb.ErrCorruptValue)
			}
			entry.LastMsgSeq = binary.BigEndian.Uint64(value)
			haveLastMsgSeq = true
		case tagChannelUpdateLogEntryLastClientMsgNo:
			entry.LastClientMsgNo = string(value)
			haveLastClientMsgNo = true
		case tagChannelUpdateLogEntryLastMsgAt:
			if len(value) != 8 {
				return metadb.ChannelUpdateLog{}, fmt.Errorf("%w: bad channel update LastMsgAt length", metadb.ErrCorruptValue)
			}
			entry.LastMsgAt = int64(binary.BigEndian.Uint64(value))
			haveLastMsgAt = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveChannelID || !haveChannelType || !haveUpdatedAt || !haveLastMsgSeq || !haveLastClientMsgNo || !haveLastMsgAt {
		return metadb.ChannelUpdateLog{}, fmt.Errorf("%w: incomplete channel update log record", metadb.ErrCorruptValue)
	}
	return entry, nil
}

func decodeUpsertUserConversationStates(data []byte) (command, error) {
	states, err := decodeUserConversationStateEntries(data)
	if err != nil {
		return nil, err
	}
	if len(states) == 0 {
		return nil, fmt.Errorf("%w: empty user conversation state batch", metadb.ErrInvalidArgument)
	}
	return &upsertUserConversationStatesCmd{states: states}, nil
}

func decodeTouchUserConversationActiveAt(data []byte) (command, error) {
	patches, err := decodeUserConversationActivePatchEntries(data)
	if err != nil {
		return nil, err
	}
	if len(patches) == 0 {
		return nil, fmt.Errorf("%w: empty user conversation active patch batch", metadb.ErrInvalidArgument)
	}
	return &touchUserConversationActiveAtCmd{patches: patches}, nil
}

func decodeClearUserConversationActiveAt(data []byte) (command, error) {
	var uid string
	var keys []metadb.ConversationKey
	var haveUID bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagClearUserConversationActiveUID:
			uid = string(value)
			haveUID = true
		case tagClearUserConversationActiveKey:
			key, err := decodeConversationKeyEntry(value)
			if err != nil {
				return nil, err
			}
			keys = append(keys, key)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if !haveUID {
		return nil, fmt.Errorf("%w: missing uid for clear command", metadb.ErrInvalidArgument)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: empty clear conversation key batch", metadb.ErrInvalidArgument)
	}
	return &clearUserConversationActiveAtCmd{uid: uid, keys: keys}, nil
}

func decodeUpsertChannelUpdateLogs(data []byte) (command, error) {
	entries, err := decodeChannelUpdateLogEntries(data)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: empty channel update log batch", metadb.ErrInvalidArgument)
	}
	return &upsertChannelUpdateLogsCmd{entries: entries}, nil
}

func decodeDeleteChannelUpdateLogs(data []byte) (command, error) {
	var keys []metadb.ConversationKey
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagConversationKeyEntryChannelID:
			key, err := decodeConversationKeyEntry(value)
			if err != nil {
				return nil, err
			}
			keys = append(keys, key)
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: empty delete channel update log batch", metadb.ErrInvalidArgument)
	}
	return &deleteChannelUpdateLogsCmd{keys: keys}, nil
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
		channelID       string
		channelType     int64
		uids            []string
		haveChannelID   bool
		haveChannelType bool
		haveUIDs        bool
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
			uids = decodeStringSet(value)
			haveUIDs = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}

	if !haveChannelID || !haveChannelType || !haveUIDs {
		return nil, fmt.Errorf("%w: incomplete subscriber command", metadb.ErrCorruptValue)
	}
	return build(channelID, channelType, uids), nil
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
	meta.Replicas = canonicalizeUint64Set(meta.Replicas)
	meta.ISR = canonicalizeUint64Set(meta.ISR)
	return meta
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
