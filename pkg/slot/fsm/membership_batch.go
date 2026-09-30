package fsm

import (
	"bytes"
	"encoding/binary"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const cmdTypeUpsertUserChannelMembershipBatch uint8 = 69

// MaxUserChannelMembershipBatchItems bounds one physical-Slot membership
// projection command across its owned logical shards.
const MaxUserChannelMembershipBatchItems = MaxPersonDirectoryBatchItems

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

// applyForHashSlot replays only the migrating shard from a multi-shard proposal.
func (c *upsertUserChannelMembershipBatchCmd) applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, item := range c.items {
		if item.HashSlot == hashSlot {
			if err := wb.UpsertUserChannelMembership(hashSlot, item.Membership); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *upsertUserChannelMembershipBatchCmd) applyHashSlots(uint16) []uint16 {
	return membershipBatchHashSlots(c.items)
}

// EncodeUpsertUserChannelMembershipBatchCommandChecked canonicalizes bounded
// upserts without changing source-version, personal-state or rejoin semantics.
// Every embedded HashSlot must belong to the proposed physical Slot.
func EncodeUpsertUserChannelMembershipBatchCommandChecked(items []UserChannelMembershipBatchItem) ([]byte, error) {
	canonical, err := canonicalMembershipBatch(items)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 0, headerSize+len(canonical)*128)
	buf = append(buf, commandVersion, cmdTypeUpsertUserChannelMembershipBatch)
	for _, item := range canonical {
		if len(item.Membership.UID) > maxPersonDirectoryBatchBytes || len(item.Membership.ChannelID) > maxPersonDirectoryBatchBytes {
			return nil, metadb.ErrInvalidArgument
		}
		entry := make([]byte, 2)
		binary.BigEndian.PutUint16(entry, item.HashSlot)
		entry = append(entry, encodeUserChannelMembershipEntry(item.Membership, true)...)
		if len(buf)+5+len(entry) > maxPersonDirectoryBatchBytes {
			return nil, metadb.ErrInvalidArgument
		}
		buf = appendBytesTLVField(buf, tagPersonDirectoryTaskBatchEntry, entry)
	}
	return buf, nil
}

func decodeUpsertUserChannelMembershipBatch(data []byte) (command, error) {
	items, err := decodeMembershipBatchEntries(data)
	if err != nil {
		return nil, err
	}
	reencoded, err := EncodeUpsertUserChannelMembershipBatchCommandChecked(items)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data, reencoded[headerSize:]) {
		return nil, fmt.Errorf("%w: non-canonical membership upsert batch", metadb.ErrCorruptValue)
	}
	return &upsertUserChannelMembershipBatchCmd{items: items}, nil
}
