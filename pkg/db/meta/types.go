package meta

// HashSlot identifies one metadata hash-slot partition.
type HashSlot = uint16

// ChannelID identifies a channel in metadata rows and indexes.
type ChannelID struct {
	// ID is the channel identifier.
	ID string
	// Type is the channel type.
	Type int64
}

const (
	// TableIDUser stores user token and device defaults.
	TableIDUser uint32 = 1
	// TableIDChannel stores channel flags and subscriber version.
	TableIDChannel uint32 = 2
	// TableIDChannelRuntimeMeta stores channel runtime routing metadata.
	TableIDChannelRuntimeMeta uint32 = 3
	// TableIDDevice stores per-device token state.
	TableIDDevice uint32 = 4
	// TableIDSubscriber stores channel subscribers.
	TableIDSubscriber uint32 = 5
	// TableIDConversation stores user conversation state.
	TableIDConversation uint32 = 6
	// TableIDCMDConversation stores command conversation state.
	TableIDCMDConversation uint32 = 7
	// TableIDPluginBinding stores plugin user bindings.
	TableIDPluginBinding uint32 = 8
	// TableIDChannelMigration stores channel migration tasks.
	TableIDChannelMigration uint32 = 9
	// TableIDHashSlotMigration stores hash-slot migration tasks.
	TableIDHashSlotMigration uint32 = 10
)

const (
	userPrimaryFamilyID uint16 = 0
	userPrimaryIndexID  uint16 = 1

	devicePrimaryFamilyID uint16 = 0
	devicePrimaryIndexID  uint16 = 1

	channelPrimaryFamilyID    uint16 = 0
	channelPrimaryIndexID     uint16 = 1
	channelIDIndexID          uint16 = 2
	channelActiveIndexID      uint16 = 3
	subscriberPrimaryFamilyID uint16 = 0
	subscriberPrimaryIndexID  uint16 = 1

	conversationPrimaryIndexID uint16 = 1
	conversationActiveIndexID  uint16 = 2

	systemIDSnapshot uint16 = 1
)
