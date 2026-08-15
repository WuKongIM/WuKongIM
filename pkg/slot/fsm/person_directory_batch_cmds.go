package fsm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// MaxPersonDirectoryBatchItems bounds one multi-hash-slot directory command.
	MaxPersonDirectoryBatchItems = 128
	maxPersonDirectoryBatchBytes = 256 << 10

	tagPersonDirectoryBatchEntry uint8 = 1

	tagPreparePersonDirectoryMembership  uint8 = 1
	tagPreparePersonDirectoryRuntimeMeta uint8 = 2
	maxPreparePersonDirectoryBatchBytes        = 512 << 10
)

// UserChannelMembershipBatchItem binds one membership row to its logical hash slot.
type UserChannelMembershipBatchItem struct {
	HashSlot   uint16
	Membership metadb.UserChannelMembership
}

// ChannelDirectoryReadyBatchItem binds one person channel to its logical hash slot.
type ChannelDirectoryReadyBatchItem struct {
	HashSlot    uint16
	ChannelID   string
	ChannelType int64
}

type upsertUserChannelMembershipBatchCmd struct {
	items []UserChannelMembershipBatchItem
}

func (c *upsertUserChannelMembershipBatchCmd) apply(wb *metadb.WriteBatch, _ uint16) error {
	for _, item := range c.items {
		if err := wb.UpsertUserChannelMembership(item.HashSlot, item.Membership); err != nil {
			return err
		}
	}
	return nil
}

func (c *upsertUserChannelMembershipBatchCmd) applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, item := range c.items {
		if item.HashSlot == hashSlot {
			if err := wb.UpsertUserChannelMembership(item.HashSlot, item.Membership); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *upsertUserChannelMembershipBatchCmd) applyHashSlots(uint16) []uint16 {
	return membershipBatchHashSlots(c.items)
}

type ensureChannelDirectoriesReadyBatchCmd struct {
	items []ChannelDirectoryReadyBatchItem
}

// preparePersonChannelDirectoryBatchCmd commits the discovery membership rows
// and create-only Channel runtime metadata owned by one logical Slot group in
// one WriteBatch. DirectoryReady remains a separate later command so readers
// cannot observe readiness before every prepare group has committed.
type preparePersonChannelDirectoryBatchCmd struct {
	memberships []UserChannelMembershipBatchItem
	runtimeMeta []CreateChannelRuntimeMetaBatchItem
	results     []*metadb.ChannelRuntimeMetaCreateResult
}

func (c *preparePersonChannelDirectoryBatchCmd) apply(wb *metadb.WriteBatch, _ uint16) error {
	c.results = make([]*metadb.ChannelRuntimeMetaCreateResult, len(c.runtimeMeta))
	for i, item := range c.runtimeMeta {
		result, err := wb.CreateChannelRuntimeMeta(item.HashSlot, item.Meta)
		if err != nil {
			return err
		}
		c.results[i] = result
	}
	for _, item := range c.memberships {
		if err := wb.UpsertUserChannelMembership(item.HashSlot, item.Membership); err != nil {
			return err
		}
	}
	return nil
}

func (c *preparePersonChannelDirectoryBatchCmd) applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error {
	for i, item := range c.runtimeMeta {
		if item.HashSlot != hashSlot {
			continue
		}
		result, err := wb.CreateChannelRuntimeMeta(item.HashSlot, item.Meta)
		if err != nil {
			return err
		}
		if len(c.results) != len(c.runtimeMeta) {
			c.results = make([]*metadb.ChannelRuntimeMetaCreateResult, len(c.runtimeMeta))
		}
		c.results[i] = result
	}
	for _, item := range c.memberships {
		if item.HashSlot == hashSlot {
			if err := wb.UpsertUserChannelMembership(item.HashSlot, item.Membership); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *preparePersonChannelDirectoryBatchCmd) applyHashSlots(uint16) []uint16 {
	hashSlots := make([]uint16, 0, len(c.memberships)+len(c.runtimeMeta))
	for _, item := range c.memberships {
		hashSlots = append(hashSlots, item.HashSlot)
	}
	for _, item := range c.runtimeMeta {
		hashSlots = append(hashSlots, item.HashSlot)
	}
	sort.Slice(hashSlots, func(i, j int) bool { return hashSlots[i] < hashSlots[j] })
	unique := hashSlots[:0]
	for _, hashSlot := range hashSlots {
		if len(unique) == 0 || unique[len(unique)-1] != hashSlot {
			unique = append(unique, hashSlot)
		}
	}
	return unique
}

func (c *preparePersonChannelDirectoryBatchCmd) applyResult() []byte {
	results := make([]CreateChannelRuntimeMetaBatchResult, len(c.runtimeMeta))
	for i, item := range c.runtimeMeta {
		results[i] = CreateChannelRuntimeMetaBatchResult{
			HashSlot: item.HashSlot, ChannelID: item.Meta.ChannelID, ChannelType: item.Meta.ChannelType,
		}
		if i < len(c.results) && c.results[i] != nil {
			results[i].Created = c.results[i].Created
		}
	}
	return EncodeCreateChannelRuntimeMetaBatchResult(results)
}

// EncodePreparePersonChannelDirectoryBatchCommandChecked encodes one bounded
// command-62 prepare batch. Runtime metadata is required because membership-
// only batches continue to use command 60.
func EncodePreparePersonChannelDirectoryBatchCommandChecked(memberships []UserChannelMembershipBatchItem, runtimeMeta []CreateChannelRuntimeMetaBatchItem) ([]byte, error) {
	var canonicalMemberships []UserChannelMembershipBatchItem
	var err error
	if len(memberships) > 0 {
		canonicalMemberships, err = canonicalMembershipBatch(memberships)
		if err != nil {
			return nil, err
		}
	}
	canonicalRuntimeMeta, err := canonicalCreateChannelRuntimeMetaBatch(runtimeMeta)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 0, headerSize+len(canonicalMemberships)*128+len(canonicalRuntimeMeta)*160)
	buf = append(buf, commandVersion, cmdTypePreparePersonChannelDirectoryBatch)
	for _, item := range canonicalMemberships {
		entry := make([]byte, 2)
		binary.BigEndian.PutUint16(entry, item.HashSlot)
		entry = append(entry, encodeUserChannelMembershipEntry(item.Membership, true)...)
		buf = appendBytesTLVField(buf, tagPreparePersonDirectoryMembership, entry)
	}
	for _, item := range canonicalRuntimeMeta {
		entry := make([]byte, 2)
		binary.BigEndian.PutUint16(entry, item.HashSlot)
		entry = append(entry, EncodeUpsertChannelRuntimeMetaCommand(item.Meta)...)
		buf = appendBytesTLVField(buf, tagPreparePersonDirectoryRuntimeMeta, entry)
	}
	if len(buf) > maxPreparePersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	return buf, nil
}

func decodePreparePersonChannelDirectoryBatch(data []byte) (command, error) {
	if len(data)+headerSize > maxPreparePersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	memberships := make([]UserChannelMembershipBatchItem, 0, MaxPersonDirectoryBatchItems)
	runtimeMeta := make([]CreateChannelRuntimeMetaBatchItem, 0, MaxCreateChannelRuntimeMetaBatchItems)
	for off := 0; off < len(data); {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		if len(value) < 2 {
			return nil, metadb.ErrInvalidArgument
		}
		hashSlot := binary.BigEndian.Uint16(value[:2])
		switch tag {
		case tagPreparePersonDirectoryMembership:
			if len(memberships) == MaxPersonDirectoryBatchItems {
				return nil, metadb.ErrInvalidArgument
			}
			membership, err := decodeUserChannelMembershipEntry(value[2:], true)
			if err != nil {
				return nil, err
			}
			memberships = append(memberships, UserChannelMembershipBatchItem{HashSlot: hashSlot, Membership: membership})
		case tagPreparePersonDirectoryRuntimeMeta:
			if len(runtimeMeta) == MaxCreateChannelRuntimeMetaBatchItems || len(value) < 4 ||
				value[2] != commandVersion || value[3] != cmdTypeUpsertChannelRuntimeMeta {
				return nil, metadb.ErrInvalidArgument
			}
			decoded, err := decodeUpsertChannelRuntimeMeta(value[4:])
			if err != nil {
				return nil, err
			}
			upsert, ok := decoded.(*upsertChannelRuntimeMetaCmd)
			if !ok {
				return nil, metadb.ErrCorruptValue
			}
			runtimeMeta = append(runtimeMeta, CreateChannelRuntimeMetaBatchItem{HashSlot: hashSlot, Meta: upsert.meta})
		default:
			return nil, metadb.ErrInvalidArgument
		}
	}
	canonical, err := EncodePreparePersonChannelDirectoryBatchCommandChecked(memberships, runtimeMeta)
	if err != nil {
		return nil, err
	}
	original := append([]byte{commandVersion, cmdTypePreparePersonChannelDirectoryBatch}, data...)
	if !bytes.Equal(original, canonical) {
		return nil, fmt.Errorf("%w: non-canonical person directory prepare batch", metadb.ErrCorruptValue)
	}
	decodedMemberships, _ := canonicalMembershipBatchOptional(memberships)
	decodedRuntimeMeta, _ := canonicalCreateChannelRuntimeMetaBatch(runtimeMeta)
	return &preparePersonChannelDirectoryBatchCmd{memberships: decodedMemberships, runtimeMeta: decodedRuntimeMeta}, nil
}

func canonicalMembershipBatchOptional(items []UserChannelMembershipBatchItem) ([]UserChannelMembershipBatchItem, error) {
	if len(items) == 0 {
		return nil, nil
	}
	return canonicalMembershipBatch(items)
}

func (c *ensureChannelDirectoriesReadyBatchCmd) apply(wb *metadb.WriteBatch, _ uint16) error {
	for _, item := range c.items {
		if err := wb.EnsureChannelDirectoryReady(item.HashSlot, item.ChannelID, item.ChannelType); err != nil {
			return err
		}
	}
	return nil
}

func (c *ensureChannelDirectoriesReadyBatchCmd) applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, item := range c.items {
		if item.HashSlot == hashSlot {
			if err := wb.EnsureChannelDirectoryReady(item.HashSlot, item.ChannelID, item.ChannelType); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *ensureChannelDirectoriesReadyBatchCmd) applyHashSlots(uint16) []uint16 {
	hashSlots := make([]uint16, 0, len(c.items))
	for _, item := range c.items {
		if len(hashSlots) == 0 || hashSlots[len(hashSlots)-1] != item.HashSlot {
			hashSlots = append(hashSlots, item.HashSlot)
		}
	}
	return hashSlots
}

// EncodeUpsertUserChannelMembershipBatchCommandChecked canonicalizes and
// encodes membership rows that share one logical Slot Raft group.
func EncodeUpsertUserChannelMembershipBatchCommandChecked(items []UserChannelMembershipBatchItem) ([]byte, error) {
	canonical, err := canonicalMembershipBatch(items)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 0, headerSize+len(canonical)*128)
	buf = append(buf, commandVersion, cmdTypeUpsertUserChannelMembershipBatch)
	for _, item := range canonical {
		entry := make([]byte, 2)
		binary.BigEndian.PutUint16(entry, item.HashSlot)
		entry = append(entry, encodeUserChannelMembershipEntry(item.Membership, true)...)
		buf = appendBytesTLVField(buf, tagPersonDirectoryBatchEntry, entry)
	}
	if len(buf) > maxPersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	return buf, nil
}

func canonicalMembershipBatch(items []UserChannelMembershipBatchItem) ([]UserChannelMembershipBatchItem, error) {
	if len(items) == 0 || len(items) > MaxPersonDirectoryBatchItems {
		return nil, metadb.ErrInvalidArgument
	}
	canonical := append([]UserChannelMembershipBatchItem(nil), items...)
	for _, item := range canonical {
		if item.Membership.UID == "" || item.Membership.ChannelID == "" || item.Membership.ChannelType <= 0 {
			return nil, metadb.ErrInvalidArgument
		}
	}
	if err := ValidateSubscriberCommandLimits(userChannelMembershipBatchUIDs(canonical)); err != nil {
		return nil, err
	}
	sort.Slice(canonical, func(i, j int) bool {
		left, right := canonical[i], canonical[j]
		if left.HashSlot != right.HashSlot {
			return left.HashSlot < right.HashSlot
		}
		if left.Membership.UID != right.Membership.UID {
			return left.Membership.UID < right.Membership.UID
		}
		if left.Membership.ChannelType != right.Membership.ChannelType {
			return left.Membership.ChannelType < right.Membership.ChannelType
		}
		return left.Membership.ChannelID < right.Membership.ChannelID
	})
	for i := 1; i < len(canonical); i++ {
		left, right := canonical[i-1], canonical[i]
		if left.HashSlot == right.HashSlot && left.Membership.UID == right.Membership.UID && left.Membership.ChannelID == right.Membership.ChannelID && left.Membership.ChannelType == right.Membership.ChannelType {
			return nil, metadb.ErrInvalidArgument
		}
	}
	return canonical, nil
}

func userChannelMembershipBatchUIDs(items []UserChannelMembershipBatchItem) []string {
	uids := make([]string, len(items))
	for i, item := range items {
		uids[i] = item.Membership.UID
	}
	return uids
}

// EncodeEnsureChannelDirectoriesReadyBatchCommandChecked canonicalizes and
// encodes readiness rows that share one logical Slot Raft group.
func EncodeEnsureChannelDirectoriesReadyBatchCommandChecked(items []ChannelDirectoryReadyBatchItem) ([]byte, error) {
	canonical, err := canonicalDirectoryReadyBatch(items)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 0, headerSize+len(canonical)*64)
	buf = append(buf, commandVersion, cmdTypeEnsureChannelDirectoriesReadyBatch)
	for _, item := range canonical {
		entry := make([]byte, 2)
		binary.BigEndian.PutUint16(entry, item.HashSlot)
		channel := encodeChannelCommand(cmdTypeEnsureChannelDirectoryReady, metadb.Channel{ChannelID: item.ChannelID, ChannelType: item.ChannelType})
		entry = append(entry, channel[headerSize:]...)
		buf = appendBytesTLVField(buf, tagPersonDirectoryBatchEntry, entry)
	}
	if len(buf) > maxPersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	return buf, nil
}

func canonicalDirectoryReadyBatch(items []ChannelDirectoryReadyBatchItem) ([]ChannelDirectoryReadyBatchItem, error) {
	if len(items) == 0 || len(items) > MaxPersonDirectoryBatchItems {
		return nil, metadb.ErrInvalidArgument
	}
	canonical := append([]ChannelDirectoryReadyBatchItem(nil), items...)
	for _, item := range canonical {
		if item.ChannelID == "" || item.ChannelType <= 0 {
			return nil, metadb.ErrInvalidArgument
		}
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].HashSlot != canonical[j].HashSlot {
			return canonical[i].HashSlot < canonical[j].HashSlot
		}
		if canonical[i].ChannelType != canonical[j].ChannelType {
			return canonical[i].ChannelType < canonical[j].ChannelType
		}
		return canonical[i].ChannelID < canonical[j].ChannelID
	})
	for i := 1; i < len(canonical); i++ {
		left, right := canonical[i-1], canonical[i]
		if left.HashSlot == right.HashSlot && left.ChannelID == right.ChannelID && left.ChannelType == right.ChannelType {
			return nil, metadb.ErrInvalidArgument
		}
	}
	return canonical, nil
}

func decodeUpsertUserChannelMembershipBatch(data []byte) (command, error) {
	if len(data)+headerSize > maxPersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	items := make([]UserChannelMembershipBatchItem, 0, MaxPersonDirectoryBatchItems)
	for off := 0; off < len(data); {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		if tag != tagPersonDirectoryBatchEntry {
			continue
		}
		if len(value) < 2 || len(items) == MaxPersonDirectoryBatchItems {
			return nil, metadb.ErrInvalidArgument
		}
		membership, err := decodeUserChannelMembershipEntry(value[2:], true)
		if err != nil {
			return nil, err
		}
		items = append(items, UserChannelMembershipBatchItem{HashSlot: binary.BigEndian.Uint16(value[:2]), Membership: membership})
	}
	canonical, err := canonicalMembershipBatch(items)
	if err != nil {
		return nil, err
	}
	if !membershipBatchItemsEqual(items, canonical) {
		return nil, fmt.Errorf("%w: non-canonical membership batch", metadb.ErrCorruptValue)
	}
	return &upsertUserChannelMembershipBatchCmd{items: canonical}, nil
}

func decodeEnsureChannelDirectoriesReadyBatch(data []byte) (command, error) {
	if len(data)+headerSize > maxPersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	items := make([]ChannelDirectoryReadyBatchItem, 0, MaxPersonDirectoryBatchItems)
	for off := 0; off < len(data); {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		if tag != tagPersonDirectoryBatchEntry {
			continue
		}
		if len(value) < 2 || len(items) == MaxPersonDirectoryBatchItems {
			return nil, metadb.ErrInvalidArgument
		}
		channel, err := decodeChannel(value[2:])
		if err != nil {
			return nil, err
		}
		items = append(items, ChannelDirectoryReadyBatchItem{HashSlot: binary.BigEndian.Uint16(value[:2]), ChannelID: channel.ChannelID, ChannelType: channel.ChannelType})
	}
	canonical, err := canonicalDirectoryReadyBatch(items)
	if err != nil {
		return nil, err
	}
	if !readyBatchItemsEqual(items, canonical) {
		return nil, fmt.Errorf("%w: non-canonical directory-ready batch", metadb.ErrCorruptValue)
	}
	return &ensureChannelDirectoriesReadyBatchCmd{items: canonical}, nil
}

func membershipBatchHashSlots(items []UserChannelMembershipBatchItem) []uint16 {
	hashSlots := make([]uint16, 0, len(items))
	for _, item := range items {
		if len(hashSlots) == 0 || hashSlots[len(hashSlots)-1] != item.HashSlot {
			hashSlots = append(hashSlots, item.HashSlot)
		}
	}
	return hashSlots
}

func membershipBatchItemsEqual(left, right []UserChannelMembershipBatchItem) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].HashSlot != right[i].HashSlot || left[i].Membership != right[i].Membership {
			return false
		}
	}
	return true
}

func readyBatchItemsEqual(left, right []ChannelDirectoryReadyBatchItem) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
