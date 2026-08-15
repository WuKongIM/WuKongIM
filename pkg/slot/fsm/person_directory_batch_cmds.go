package fsm

import (
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
