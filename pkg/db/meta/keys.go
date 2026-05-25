package meta

import (
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// Span identifies a half-open key range.
type Span struct {
	// Start is the inclusive lower bound.
	Start []byte
	// End is the exclusive upper bound.
	End []byte
}

func hashSlotPartitionID(hashSlot HashSlot) []byte {
	return keycodec.AppendUint16(nil, uint16(hashSlot))
}

func encodeHashSlotSpacePrefix(hashSlot HashSlot, space keycodec.Space) []byte {
	var builder keycodec.Builder
	key := builder.Reset().
		Domain(keycodec.DomainMeta).
		Partition(keycodec.PartitionHashSlot, hashSlotPartitionID(hashSlot)).
		Key()
	return append(key, byte(space))
}

func encodeRowPrefix(hashSlot HashSlot, tableID uint32) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMeta).
		Partition(keycodec.PartitionHashSlot, hashSlotPartitionID(hashSlot)).
		Row(tableID).
		Key()
}

func encodeIndexPrefix(hashSlot HashSlot, tableID uint32, indexID uint16) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMeta).
		Partition(keycodec.PartitionHashSlot, hashSlotPartitionID(hashSlot)).
		Index(tableID, indexID).
		Key()
}

func encodeSystemPrefix(hashSlot HashSlot, systemID uint16) []byte {
	var builder keycodec.Builder
	return builder.Reset().
		Domain(keycodec.DomainMeta).
		Partition(keycodec.PartitionHashSlot, hashSlotPartitionID(hashSlot)).
		System(0, systemID).
		Key()
}

func encodeUserRowKey(hashSlot HashSlot, uid string, familyID uint16) []byte {
	key := encodeRowPrefix(hashSlot, TableIDUser)
	key = keycodec.AppendString(key, uid)
	return keycodec.AppendUint16(key, familyID)
}

func encodeChannelIDIndexKey(hashSlot HashSlot, channelID string, channelType int64) []byte {
	key := encodeIndexPrefix(hashSlot, TableIDChannel, channelIDIndexID)
	key = keycodec.AppendString(key, channelID)
	return keycodec.AppendInt64Ordered(key, channelType)
}

func encodeActiveIndexKey(hashSlot HashSlot, tableID uint32, indexID uint16, activeAt int64, parts ...string) []byte {
	key := encodeIndexPrefix(hashSlot, tableID, indexID)
	key = keycodec.AppendInt64Desc(key, activeAt)
	for _, part := range parts {
		key = keycodec.AppendString(key, part)
	}
	return key
}

func encodeHashSlotSystemKey(hashSlot HashSlot, systemID uint16) []byte {
	return encodeSystemPrefix(hashSlot, systemID)
}

func hashSlotRowSpan(hashSlot HashSlot) Span {
	return prefixSpan(encodeHashSlotSpacePrefix(hashSlot, keycodec.SpaceRow))
}

func hashSlotIndexSpan(hashSlot HashSlot) Span {
	return prefixSpan(encodeHashSlotSpacePrefix(hashSlot, keycodec.SpaceIndex))
}

func hashSlotSystemSpan(hashSlot HashSlot) Span {
	return prefixSpan(encodeHashSlotSpacePrefix(hashSlot, keycodec.SpaceSystem))
}

func prefixSpan(prefix []byte) Span {
	span := keycodec.NewPrefixSpan(prefix)
	return Span{Start: span.Start, End: span.End}
}
