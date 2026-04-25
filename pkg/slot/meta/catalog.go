package meta

const (
	TableIDUser                  uint32 = 1
	TableIDChannel               uint32 = 2
	TableIDChannelRuntimeMeta    uint32 = 3
	TableIDDevice                uint32 = 4
	TableIDSubscriber            uint32 = 5
	TableIDUserConversationState uint32 = 6
	TableIDChannelUpdateLog      uint32 = 7

	maxKeyStringLen = 1<<16 - 1
)

const (
	userPrimaryFamilyID uint16 = 0
	userPrimaryIndexID  uint16 = 1

	userColumnIDUID         uint16 = 1
	userColumnIDToken       uint16 = 2
	userColumnIDDeviceFlag  uint16 = 3
	userColumnIDDeviceLevel uint16 = 4
)

const (
	channelPrimaryFamilyID   uint16 = 0
	channelPrimaryIndexID    uint16 = 1
	channelIndexIDChannelID  uint16 = 2
	channelColumnIDChannelID uint16 = 1
	channelColumnIDType      uint16 = 2
	channelColumnIDBan       uint16 = 3
)

const (
	channelRuntimeMetaPrimaryFamilyID      uint16 = 0
	channelRuntimeMetaPrimaryIndexID       uint16 = 1
	channelRuntimeMetaColumnIDChannelID    uint16 = 1
	channelRuntimeMetaColumnIDType         uint16 = 2
	channelRuntimeMetaColumnIDChannelEpoch uint16 = 3
	channelRuntimeMetaColumnIDLeaderEpoch  uint16 = 4
	channelRuntimeMetaColumnIDReplicas     uint16 = 5
	channelRuntimeMetaColumnIDISR          uint16 = 6
	channelRuntimeMetaColumnIDLeader       uint16 = 7
	channelRuntimeMetaColumnIDMinISR       uint16 = 8
	channelRuntimeMetaColumnIDStatus       uint16 = 9
	channelRuntimeMetaColumnIDFeatures     uint16 = 10
	channelRuntimeMetaColumnIDLeaseUntilMS uint16 = 11
)

const (
	devicePrimaryFamilyID uint16 = 0
	devicePrimaryIndexID  uint16 = 1

	deviceColumnIDUID         uint16 = 1
	deviceColumnIDDeviceFlag  uint16 = 2
	deviceColumnIDToken       uint16 = 3
	deviceColumnIDDeviceLevel uint16 = 4
)

const (
	subscriberPrimaryFamilyID uint16 = 0
	subscriberPrimaryIndexID  uint16 = 1

	subscriberColumnIDChannelID uint16 = 1
	subscriberColumnIDType      uint16 = 2
	subscriberColumnIDUID       uint16 = 3
)

const (
	userConversationStatePrimaryFamilyID uint16 = 0
	userConversationStatePrimaryIndexID  uint16 = 1
	userConversationStateActiveIndexID   uint16 = 2

	userConversationStateColumnIDUID          uint16 = 1
	userConversationStateColumnIDChannelID    uint16 = 2
	userConversationStateColumnIDChannelType  uint16 = 3
	userConversationStateColumnIDReadSeq      uint16 = 4
	userConversationStateColumnIDDeletedToSeq uint16 = 5
	userConversationStateColumnIDActiveAt     uint16 = 6
	userConversationStateColumnIDUpdatedAt    uint16 = 7
)

const (
	channelUpdateLogPrimaryFamilyID uint16 = 0
	channelUpdateLogPrimaryIndexID  uint16 = 1

	channelUpdateLogColumnIDChannelID       uint16 = 1
	channelUpdateLogColumnIDChannelType     uint16 = 2
	channelUpdateLogColumnIDUpdatedAt       uint16 = 3
	channelUpdateLogColumnIDLastMsgSeq      uint16 = 4
	channelUpdateLogColumnIDLastClientMsgNo uint16 = 5
	channelUpdateLogColumnIDLastMsgAt       uint16 = 6
)

type ColumnType int

const (
	ColumnString ColumnType = iota + 1
	ColumnInt64
	ColumnUint64
	ColumnBytes
)

type ColumnDesc struct {
	ID       uint16
	Name     string
	Type     ColumnType
	Nullable bool
}

type ColumnFamilyDesc struct {
	ID              uint16
	Name            string
	ColumnIDs       []uint16
	DefaultColumnID uint16
}

type IndexDesc struct {
	ID        uint16
	Name      string
	Unique    bool
	ColumnIDs []uint16
	Primary   bool
}

type TableDesc struct {
	ID               uint32
	Name             string
	Columns          []ColumnDesc
	Families         []ColumnFamilyDesc
	PrimaryIndex     IndexDesc
	SecondaryIndexes []IndexDesc
}

var UserTable = &TableDesc{
	ID:   TableIDUser,
	Name: "user",
	Columns: []ColumnDesc{
		{ID: userColumnIDUID, Name: "uid", Type: ColumnString},
		{ID: userColumnIDToken, Name: "token", Type: ColumnString},
		{ID: userColumnIDDeviceFlag, Name: "device_flag", Type: ColumnInt64},
		{ID: userColumnIDDeviceLevel, Name: "device_level", Type: ColumnInt64},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:              userPrimaryFamilyID,
			Name:            "primary",
			ColumnIDs:       []uint16{userColumnIDToken, userColumnIDDeviceFlag, userColumnIDDeviceLevel},
			DefaultColumnID: userColumnIDToken,
		},
	},
	PrimaryIndex: IndexDesc{
		ID:        userPrimaryIndexID,
		Name:      "pk_user",
		Unique:    true,
		Primary:   true,
		ColumnIDs: []uint16{userColumnIDUID},
	},
}

var ChannelTable = &TableDesc{
	ID:   TableIDChannel,
	Name: "channel",
	Columns: []ColumnDesc{
		{ID: channelColumnIDChannelID, Name: "channel_id", Type: ColumnString},
		{ID: channelColumnIDType, Name: "channel_type", Type: ColumnInt64},
		{ID: channelColumnIDBan, Name: "ban", Type: ColumnInt64},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:              channelPrimaryFamilyID,
			Name:            "primary",
			ColumnIDs:       []uint16{channelColumnIDBan},
			DefaultColumnID: channelColumnIDBan,
		},
	},
	PrimaryIndex: IndexDesc{
		ID:        channelPrimaryIndexID,
		Name:      "pk_channel",
		Unique:    true,
		Primary:   true,
		ColumnIDs: []uint16{channelColumnIDChannelID, channelColumnIDType},
	},
	SecondaryIndexes: []IndexDesc{
		{
			ID:        channelIndexIDChannelID,
			Name:      "idx_channel_id",
			Unique:    false,
			ColumnIDs: []uint16{channelColumnIDChannelID},
		},
	},
}

var ChannelRuntimeMetaTable = &TableDesc{
	ID:   TableIDChannelRuntimeMeta,
	Name: "channel_runtime_meta",
	Columns: []ColumnDesc{
		{ID: channelRuntimeMetaColumnIDChannelID, Name: "channel_id", Type: ColumnString},
		{ID: channelRuntimeMetaColumnIDType, Name: "channel_type", Type: ColumnInt64},
		{ID: channelRuntimeMetaColumnIDChannelEpoch, Name: "channel_epoch", Type: ColumnUint64},
		{ID: channelRuntimeMetaColumnIDLeaderEpoch, Name: "leader_epoch", Type: ColumnUint64},
		{ID: channelRuntimeMetaColumnIDReplicas, Name: "replicas", Type: ColumnBytes},
		{ID: channelRuntimeMetaColumnIDISR, Name: "isr", Type: ColumnBytes},
		{ID: channelRuntimeMetaColumnIDLeader, Name: "leader", Type: ColumnUint64},
		{ID: channelRuntimeMetaColumnIDMinISR, Name: "min_isr", Type: ColumnInt64},
		{ID: channelRuntimeMetaColumnIDStatus, Name: "status", Type: ColumnUint64},
		{ID: channelRuntimeMetaColumnIDFeatures, Name: "features", Type: ColumnUint64},
		{ID: channelRuntimeMetaColumnIDLeaseUntilMS, Name: "lease_until_ms", Type: ColumnInt64},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:   channelRuntimeMetaPrimaryFamilyID,
			Name: "primary",
			ColumnIDs: []uint16{
				channelRuntimeMetaColumnIDChannelEpoch,
				channelRuntimeMetaColumnIDLeaderEpoch,
				channelRuntimeMetaColumnIDReplicas,
				channelRuntimeMetaColumnIDISR,
				channelRuntimeMetaColumnIDLeader,
				channelRuntimeMetaColumnIDMinISR,
				channelRuntimeMetaColumnIDStatus,
				channelRuntimeMetaColumnIDFeatures,
				channelRuntimeMetaColumnIDLeaseUntilMS,
			},
			DefaultColumnID: channelRuntimeMetaColumnIDChannelEpoch,
		},
	},
	PrimaryIndex: IndexDesc{
		ID:        channelRuntimeMetaPrimaryIndexID,
		Name:      "pk_channel_runtime_meta",
		Unique:    true,
		Primary:   true,
		ColumnIDs: []uint16{channelRuntimeMetaColumnIDChannelID, channelRuntimeMetaColumnIDType},
	},
}

var DeviceTable = &TableDesc{
	ID:   TableIDDevice,
	Name: "device",
	Columns: []ColumnDesc{
		{ID: deviceColumnIDUID, Name: "uid", Type: ColumnString},
		{ID: deviceColumnIDDeviceFlag, Name: "device_flag", Type: ColumnInt64},
		{ID: deviceColumnIDToken, Name: "token", Type: ColumnString},
		{ID: deviceColumnIDDeviceLevel, Name: "device_level", Type: ColumnInt64},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:              devicePrimaryFamilyID,
			Name:            "primary",
			ColumnIDs:       []uint16{deviceColumnIDToken, deviceColumnIDDeviceLevel},
			DefaultColumnID: deviceColumnIDToken,
		},
	},
	PrimaryIndex: IndexDesc{
		ID:        devicePrimaryIndexID,
		Name:      "pk_device",
		Unique:    true,
		Primary:   true,
		ColumnIDs: []uint16{deviceColumnIDUID, deviceColumnIDDeviceFlag},
	},
}

var SubscriberTable = &TableDesc{
	ID:   TableIDSubscriber,
	Name: "subscriber",
	Columns: []ColumnDesc{
		{ID: subscriberColumnIDChannelID, Name: "channel_id", Type: ColumnString},
		{ID: subscriberColumnIDType, Name: "channel_type", Type: ColumnInt64},
		{ID: subscriberColumnIDUID, Name: "uid", Type: ColumnString},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:              subscriberPrimaryFamilyID,
			Name:            "primary",
			DefaultColumnID: subscriberColumnIDUID,
		},
	},
	PrimaryIndex: IndexDesc{
		ID:        subscriberPrimaryIndexID,
		Name:      "pk_subscriber",
		Unique:    true,
		Primary:   true,
		ColumnIDs: []uint16{subscriberColumnIDChannelID, subscriberColumnIDType, subscriberColumnIDUID},
	},
}

var UserConversationStateTable = &TableDesc{
	ID:   TableIDUserConversationState,
	Name: "user_conversation_state",
	Columns: []ColumnDesc{
		{ID: userConversationStateColumnIDUID, Name: "uid", Type: ColumnString},
		{ID: userConversationStateColumnIDChannelType, Name: "channel_type", Type: ColumnInt64},
		{ID: userConversationStateColumnIDChannelID, Name: "channel_id", Type: ColumnString},
		{ID: userConversationStateColumnIDReadSeq, Name: "read_seq", Type: ColumnUint64},
		{ID: userConversationStateColumnIDDeletedToSeq, Name: "deleted_to_seq", Type: ColumnUint64},
		{ID: userConversationStateColumnIDActiveAt, Name: "active_at", Type: ColumnInt64},
		{ID: userConversationStateColumnIDUpdatedAt, Name: "updated_at", Type: ColumnInt64},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:              userConversationStatePrimaryFamilyID,
			Name:            "primary",
			ColumnIDs:       []uint16{userConversationStateColumnIDReadSeq, userConversationStateColumnIDDeletedToSeq, userConversationStateColumnIDActiveAt, userConversationStateColumnIDUpdatedAt},
			DefaultColumnID: userConversationStateColumnIDReadSeq,
		},
	},
	PrimaryIndex: IndexDesc{
		ID:        userConversationStatePrimaryIndexID,
		Name:      "pk_user_conversation_state",
		Unique:    true,
		Primary:   true,
		ColumnIDs: []uint16{userConversationStateColumnIDUID, userConversationStateColumnIDChannelType, userConversationStateColumnIDChannelID},
	},
	SecondaryIndexes: []IndexDesc{
		{
			ID:        userConversationStateActiveIndexID,
			Name:      "idx_user_conversation_active",
			Unique:    false,
			ColumnIDs: []uint16{userConversationStateColumnIDUID, userConversationStateColumnIDActiveAt, userConversationStateColumnIDChannelType, userConversationStateColumnIDChannelID},
		},
	},
}

var ChannelUpdateLogTable = &TableDesc{
	ID:   TableIDChannelUpdateLog,
	Name: "channel_update_log",
	Columns: []ColumnDesc{
		{ID: channelUpdateLogColumnIDChannelType, Name: "channel_type", Type: ColumnInt64},
		{ID: channelUpdateLogColumnIDChannelID, Name: "channel_id", Type: ColumnString},
		{ID: channelUpdateLogColumnIDUpdatedAt, Name: "updated_at", Type: ColumnInt64},
		{ID: channelUpdateLogColumnIDLastMsgSeq, Name: "last_msg_seq", Type: ColumnUint64},
		{ID: channelUpdateLogColumnIDLastClientMsgNo, Name: "last_client_msg_no", Type: ColumnString},
		{ID: channelUpdateLogColumnIDLastMsgAt, Name: "last_msg_at", Type: ColumnInt64},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:              channelUpdateLogPrimaryFamilyID,
			Name:            "primary",
			ColumnIDs:       []uint16{channelUpdateLogColumnIDUpdatedAt, channelUpdateLogColumnIDLastMsgSeq, channelUpdateLogColumnIDLastClientMsgNo, channelUpdateLogColumnIDLastMsgAt},
			DefaultColumnID: channelUpdateLogColumnIDUpdatedAt,
		},
	},
	PrimaryIndex: IndexDesc{
		ID:        channelUpdateLogPrimaryIndexID,
		Name:      "pk_channel_update_log",
		Unique:    true,
		Primary:   true,
		ColumnIDs: []uint16{channelUpdateLogColumnIDChannelType, channelUpdateLogColumnIDChannelID},
	},
}
