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

func encodeChannelRowKey(hashSlot HashSlot, channelID string, channelType int64, familyID uint16) []byte {
	key := encodeRowPrefix(hashSlot, TableIDChannel)
	key = keycodec.AppendString(key, channelID)
	key = keycodec.AppendInt64Ordered(key, int64(channelType))
	return keycodec.AppendUint16(key, familyID)
}

func encodeChannelRuntimeMetaRowPrefix(hashSlot HashSlot) []byte {
	return encodeRowPrefix(hashSlot, TableIDChannelRuntimeMeta)
}

func encodeChannelRuntimeMetaRowKey(hashSlot HashSlot, channelID string, channelType int64, familyID uint16) []byte {
	key := encodeChannelRuntimeMetaRowPrefix(hashSlot)
	key = keycodec.AppendString(key, channelID)
	key = keycodec.AppendInt64Ordered(key, int64(channelType))
	return keycodec.AppendUint16(key, familyID)
}

func encodeSubscriberRowPrefix(hashSlot HashSlot, channelID string, channelType int64) []byte {
	key := encodeRowPrefix(hashSlot, TableIDSubscriber)
	key = keycodec.AppendString(key, channelID)
	return keycodec.AppendInt64Ordered(key, int64(channelType))
}

func encodeSubscriberRowKey(hashSlot HashSlot, channelID string, channelType int64, uid string, familyID uint16) []byte {
	key := encodeSubscriberRowPrefix(hashSlot, channelID, channelType)
	key = keycodec.AppendString(key, uid)
	return keycodec.AppendUint16(key, familyID)
}

func encodeConversationRowPrefix(hashSlot HashSlot, uid string) []byte {
	key := encodeRowPrefix(hashSlot, TableIDConversation)
	return keycodec.AppendString(key, uid)
}

func encodeConversationRowKey(hashSlot HashSlot, uid string, channelID string, channelType int64, familyID uint16) []byte {
	key := encodeConversationRowPrefix(hashSlot, uid)
	key = keycodec.AppendString(key, channelID)
	key = keycodec.AppendInt64Ordered(key, int64(channelType))
	return keycodec.AppendUint16(key, familyID)
}

func encodeCMDConversationRowPrefix(hashSlot HashSlot, uid string) []byte {
	key := encodeRowPrefix(hashSlot, TableIDCMDConversation)
	return keycodec.AppendString(key, uid)
}

func encodeCMDConversationRowKey(hashSlot HashSlot, uid string, channelID string, channelType int64, familyID uint16) []byte {
	key := encodeCMDConversationRowPrefix(hashSlot, uid)
	key = keycodec.AppendString(key, channelID)
	key = keycodec.AppendInt64Ordered(key, int64(channelType))
	return keycodec.AppendUint16(key, familyID)
}

func encodeConversationActiveIndexPrefix(hashSlot HashSlot, tableID uint32, uid string) []byte {
	key := encodeIndexPrefix(hashSlot, tableID, conversationActiveIndexID)
	return keycodec.AppendString(key, uid)
}

func encodeConversationActiveIndexKey(hashSlot HashSlot, tableID uint32, uid string, activeAt int64, channelID string, channelType int64) []byte {
	key := encodeConversationActiveIndexPrefix(hashSlot, tableID, uid)
	key = keycodec.AppendInt64Desc(key, activeAt)
	key = keycodec.AppendString(key, channelID)
	return keycodec.AppendInt64Ordered(key, int64(channelType))
}

func encodePluginBindingRowPrefix(hashSlot HashSlot, uid string) []byte {
	key := encodeRowPrefix(hashSlot, TableIDPluginBinding)
	return keycodec.AppendString(key, uid)
}

func encodePluginBindingRowKey(hashSlot HashSlot, uid, pluginNo string, familyID uint16) []byte {
	key := encodePluginBindingRowPrefix(hashSlot, uid)
	key = keycodec.AppendString(key, pluginNo)
	return keycodec.AppendUint16(key, familyID)
}

func encodePluginBindingPluginIndexPrefix(hashSlot HashSlot, pluginNo string) []byte {
	key := encodeIndexPrefix(hashSlot, TableIDPluginBinding, pluginBindingPluginIndexID)
	return keycodec.AppendString(key, pluginNo)
}

func encodePluginBindingPluginIndexKey(hashSlot HashSlot, pluginNo string, uid string) []byte {
	key := encodePluginBindingPluginIndexPrefix(hashSlot, pluginNo)
	return keycodec.AppendString(key, uid)
}

func encodeHashSlotMigrationRowPrefix(hashSlot HashSlot) []byte {
	return encodeRowPrefix(hashSlot, TableIDHashSlotMigration)
}

func encodeHashSlotMigrationStateKey(hashSlot HashSlot) []byte {
	key := encodeHashSlotMigrationRowPrefix(hashSlot)
	return append(key, hashSlotMigrationRecordState)
}

func encodeAppliedHashSlotDeltaPrefix(hashSlot HashSlot) []byte {
	key := encodeHashSlotMigrationRowPrefix(hashSlot)
	return append(key, hashSlotMigrationRecordAppliedDelta)
}

func encodeAppliedHashSlotDeltaKey(delta AppliedHashSlotDelta) []byte {
	key := encodeAppliedHashSlotDeltaPrefix(delta.HashSlot)
	key = keycodec.AppendUint64(key, delta.SourceSlot)
	return keycodec.AppendUint64(key, delta.SourceIndex)
}

func encodeHashSlotMigrationOutboxHashSlotPrefix(hashSlot HashSlot) []byte {
	key := encodeHashSlotMigrationRowPrefix(hashSlot)
	return append(key, hashSlotMigrationRecordOutbox)
}

func encodeHashSlotMigrationOutboxPrefix(hashSlot HashSlot, sourceSlot uint64, targetSlot uint64) []byte {
	key := encodeHashSlotMigrationOutboxHashSlotPrefix(hashSlot)
	key = keycodec.AppendUint64(key, sourceSlot)
	return keycodec.AppendUint64(key, targetSlot)
}

func encodeHashSlotMigrationOutboxKey(hashSlot HashSlot, sourceSlot uint64, targetSlot uint64, sourceIndex uint64) []byte {
	key := encodeHashSlotMigrationOutboxPrefix(hashSlot, sourceSlot, targetSlot)
	return keycodec.AppendUint64(key, sourceIndex)
}

func encodeChannelMigrationRowPrefix(hashSlot HashSlot) []byte {
	return encodeRowPrefix(hashSlot, TableIDChannelMigration)
}

func encodeChannelMigrationTaskRowKey(hashSlot HashSlot, channelID string, channelType int64, taskID string, familyID uint16) []byte {
	key := encodeChannelMigrationRowPrefix(hashSlot)
	key = keycodec.AppendString(key, channelID)
	key = keycodec.AppendInt64Ordered(key, int64(channelType))
	key = keycodec.AppendString(key, taskID)
	return keycodec.AppendUint16(key, familyID)
}

func encodeChannelMigrationActiveIndexKey(hashSlot HashSlot, channelID string, channelType int64) []byte {
	key := encodeIndexPrefix(hashSlot, TableIDChannelMigration, channelMigrationActiveIndexID)
	key = keycodec.AppendString(key, channelID)
	return keycodec.AppendInt64Ordered(key, int64(channelType))
}

func encodeChannelMigrationTerminalIndexPrefix(hashSlot HashSlot) []byte {
	return encodeIndexPrefix(hashSlot, TableIDChannelMigration, channelMigrationTerminalIndexID)
}

func encodeChannelMigrationTerminalIndexKey(hashSlot HashSlot, completedAtMS int64, channelID string, channelType int64, taskID string) []byte {
	key := encodeChannelMigrationTerminalIndexPrefix(hashSlot)
	key = keycodec.AppendInt64Ordered(key, completedAtMS)
	key = keycodec.AppendString(key, channelID)
	key = keycodec.AppendInt64Ordered(key, int64(channelType))
	return keycodec.AppendString(key, taskID)
}

func encodeChannelIDIndexPrefix(hashSlot HashSlot, channelID string) []byte {
	key := encodeIndexPrefix(hashSlot, TableIDChannel, channelIDIndexID)
	return keycodec.AppendString(key, channelID)
}

func encodeChannelIDIndexKey(hashSlot HashSlot, channelID string, channelType int64) []byte {
	key := encodeChannelIDIndexPrefix(hashSlot, channelID)
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
