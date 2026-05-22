package meta

const (
	TableIDUser                  uint32 = 1
	TableIDChannel               uint32 = 2
	TableIDChannelRuntimeMeta    uint32 = 3
	TableIDDevice                uint32 = 4
	TableIDSubscriber            uint32 = 5
	TableIDUserConversationState uint32 = 6
	// TableIDReservedConversationProjection is reserved for the removed conversation projection table.
	TableIDReservedConversationProjection uint32 = 7
	TableIDChannelMigrationTask           uint32 = 8
	TableIDCMDConversationState           uint32 = 9
	TableIDPluginUserBinding              uint32 = 10

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
	channelPrimaryFamilyID                   uint16 = 0
	channelPrimaryIndexID                    uint16 = 1
	channelIndexIDChannelID                  uint16 = 2
	channelColumnIDChannelID                 uint16 = 1
	channelColumnIDType                      uint16 = 2
	channelColumnIDBan                       uint16 = 3
	channelColumnIDSubscriberMutationVersion uint16 = 4
	channelColumnIDDisband                   uint16 = 5
	channelColumnIDSendBan                   uint16 = 6
	channelColumnIDAllowStranger             uint16 = 7
)

const (
	channelRuntimeMetaPrimaryFamilyID              uint16 = 0
	channelRuntimeMetaPrimaryIndexID               uint16 = 1
	channelRuntimeMetaColumnIDChannelID            uint16 = 1
	channelRuntimeMetaColumnIDType                 uint16 = 2
	channelRuntimeMetaColumnIDChannelEpoch         uint16 = 3
	channelRuntimeMetaColumnIDLeaderEpoch          uint16 = 4
	channelRuntimeMetaColumnIDReplicas             uint16 = 5
	channelRuntimeMetaColumnIDISR                  uint16 = 6
	channelRuntimeMetaColumnIDLeader               uint16 = 7
	channelRuntimeMetaColumnIDMinISR               uint16 = 8
	channelRuntimeMetaColumnIDStatus               uint16 = 9
	channelRuntimeMetaColumnIDFeatures             uint16 = 10
	channelRuntimeMetaColumnIDLeaseUntilMS         uint16 = 11
	channelRuntimeMetaColumnIDRetentionThroughSeq  uint16 = 12
	channelRuntimeMetaColumnIDRetentionUpdatedAtMS uint16 = 13
	channelRuntimeMetaColumnIDWriteFenceToken      uint16 = 14
	channelRuntimeMetaColumnIDWriteFenceVersion    uint16 = 15
	channelRuntimeMetaColumnIDWriteFenceReason     uint16 = 16
	channelRuntimeMetaColumnIDWriteFenceUntilMS    uint16 = 17
	channelRuntimeMetaColumnIDRouteGeneration      uint16 = 18
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
	channelMigrationTaskPrimaryFamilyID    uint16 = 0
	channelMigrationTaskPrimaryIndexID     uint16 = 1
	channelMigrationTaskActiveIndexID      uint16 = 2
	channelMigrationTaskTerminalIndexID    uint16 = 3
	channelMigrationTaskColumnIDChannelID  uint16 = 1
	channelMigrationTaskColumnIDType       uint16 = 2
	channelMigrationTaskColumnIDTaskID     uint16 = 3
	channelMigrationTaskColumnIDKind       uint16 = 4
	channelMigrationTaskColumnIDStatus     uint16 = 5
	channelMigrationTaskColumnIDPhase      uint16 = 6
	channelMigrationTaskColumnIDSourceNode uint16 = 7
	channelMigrationTaskColumnIDTargetNode uint16 = 8

	channelMigrationTaskColumnIDDesiredLeader            uint16 = 9
	channelMigrationTaskColumnIDBaseChannelEpoch         uint16 = 10
	channelMigrationTaskColumnIDBaseLeaderEpoch          uint16 = 11
	channelMigrationTaskColumnIDFenceToken               uint16 = 12
	channelMigrationTaskColumnIDFenceVersion             uint16 = 13
	channelMigrationTaskColumnIDFenceUntilMS             uint16 = 14
	channelMigrationTaskColumnIDEmbeddedLeaderTransfer   uint16 = 15
	channelMigrationTaskColumnIDEmbeddedDesiredLeader    uint16 = 16
	channelMigrationTaskColumnIDOwnerNodeID              uint16 = 17
	channelMigrationTaskColumnIDOwnerLeaseUntilMS        uint16 = 18
	channelMigrationTaskColumnIDCutoverLEO               uint16 = 19
	channelMigrationTaskColumnIDCutoverHW                uint16 = 20
	channelMigrationTaskColumnIDDrainedLeaderNode        uint16 = 21
	channelMigrationTaskColumnIDDrainedRuntimeGeneration uint16 = 22
	channelMigrationTaskColumnIDDrainedChannelEpoch      uint16 = 23
	channelMigrationTaskColumnIDDrainedLeaderEpoch       uint16 = 24
	channelMigrationTaskColumnIDDrainedFenceVersion      uint16 = 25
	channelMigrationTaskColumnIDAttempt                  uint16 = 26
	channelMigrationTaskColumnIDNextRunAtMS              uint16 = 27
	channelMigrationTaskColumnIDBlockerCode              uint16 = 28
	channelMigrationTaskColumnIDBlockerMessage           uint16 = 29
	channelMigrationTaskColumnIDLastError                uint16 = 30
	channelMigrationTaskColumnIDCreatedAtMS              uint16 = 31
	channelMigrationTaskColumnIDUpdatedAtMS              uint16 = 32
	channelMigrationTaskColumnIDCompletedAtMS            uint16 = 33
	channelMigrationTaskColumnIDProgress                 uint16 = 34
)

const (
	cmdConversationStatePrimaryFamilyID uint16 = 0
	cmdConversationStatePrimaryIndexID  uint16 = 1
	cmdConversationStateActiveIndexID   uint16 = 2

	cmdConversationStateColumnIDUID          uint16 = 1
	cmdConversationStateColumnIDChannelID    uint16 = 2
	cmdConversationStateColumnIDChannelType  uint16 = 3
	cmdConversationStateColumnIDReadSeq      uint16 = 4
	cmdConversationStateColumnIDDeletedToSeq uint16 = 5
	cmdConversationStateColumnIDActiveAt     uint16 = 6
	cmdConversationStateColumnIDUpdatedAt    uint16 = 7
)

const (
	pluginUserBindingPrimaryFamilyID uint16 = 0
	pluginUserBindingPrimaryIndexID  uint16 = 1
	pluginUserBindingPluginIndexID   uint16 = 2

	pluginUserBindingColumnIDUID         uint16 = 1
	pluginUserBindingColumnIDPluginNo    uint16 = 2
	pluginUserBindingColumnIDCreatedAtMS uint16 = 3
	pluginUserBindingColumnIDUpdatedAtMS uint16 = 4
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
		{ID: channelColumnIDSubscriberMutationVersion, Name: "subscriber_mutation_version", Type: ColumnUint64},
		{ID: channelColumnIDDisband, Name: "disband", Type: ColumnInt64},
		{ID: channelColumnIDSendBan, Name: "send_ban", Type: ColumnInt64},
		{ID: channelColumnIDAllowStranger, Name: "allow_stranger", Type: ColumnInt64},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:              channelPrimaryFamilyID,
			Name:            "primary",
			ColumnIDs:       []uint16{channelColumnIDBan, channelColumnIDSubscriberMutationVersion, channelColumnIDDisband, channelColumnIDSendBan, channelColumnIDAllowStranger},
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
		{ID: channelRuntimeMetaColumnIDRetentionThroughSeq, Name: "retention_through_seq", Type: ColumnUint64},
		{ID: channelRuntimeMetaColumnIDRetentionUpdatedAtMS, Name: "retention_updated_at_ms", Type: ColumnInt64},
		{ID: channelRuntimeMetaColumnIDWriteFenceToken, Name: "write_fence_token", Type: ColumnString},
		{ID: channelRuntimeMetaColumnIDWriteFenceVersion, Name: "write_fence_version", Type: ColumnUint64},
		{ID: channelRuntimeMetaColumnIDWriteFenceReason, Name: "write_fence_reason", Type: ColumnUint64},
		{ID: channelRuntimeMetaColumnIDWriteFenceUntilMS, Name: "write_fence_until_ms", Type: ColumnInt64},
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
				channelRuntimeMetaColumnIDRetentionThroughSeq,
				channelRuntimeMetaColumnIDRetentionUpdatedAtMS,
				channelRuntimeMetaColumnIDWriteFenceToken,
				channelRuntimeMetaColumnIDWriteFenceVersion,
				channelRuntimeMetaColumnIDWriteFenceReason,
				channelRuntimeMetaColumnIDWriteFenceUntilMS,
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

var ChannelMigrationTaskTable = &TableDesc{
	ID:   TableIDChannelMigrationTask,
	Name: "channel_migration_task",
	Columns: []ColumnDesc{
		{ID: channelMigrationTaskColumnIDChannelID, Name: "channel_id", Type: ColumnString},
		{ID: channelMigrationTaskColumnIDType, Name: "channel_type", Type: ColumnInt64},
		{ID: channelMigrationTaskColumnIDTaskID, Name: "task_id", Type: ColumnString},
		{ID: channelMigrationTaskColumnIDKind, Name: "kind", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDStatus, Name: "status", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDPhase, Name: "phase", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDSourceNode, Name: "source_node", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDTargetNode, Name: "target_node", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDDesiredLeader, Name: "desired_leader", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDBaseChannelEpoch, Name: "base_channel_epoch", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDBaseLeaderEpoch, Name: "base_leader_epoch", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDFenceToken, Name: "fence_token", Type: ColumnString, Nullable: true},
		{ID: channelMigrationTaskColumnIDFenceVersion, Name: "fence_version", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDFenceUntilMS, Name: "fence_until_ms", Type: ColumnInt64},
		{ID: channelMigrationTaskColumnIDEmbeddedLeaderTransfer, Name: "embedded_leader_transfer", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDEmbeddedDesiredLeader, Name: "embedded_desired_leader", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDOwnerNodeID, Name: "owner_node_id", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDOwnerLeaseUntilMS, Name: "owner_lease_until_ms", Type: ColumnInt64},
		{ID: channelMigrationTaskColumnIDCutoverLEO, Name: "cutover_leo", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDCutoverHW, Name: "cutover_hw", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDDrainedLeaderNode, Name: "drained_leader_node", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDDrainedRuntimeGeneration, Name: "drained_runtime_generation", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDDrainedChannelEpoch, Name: "drained_channel_epoch", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDDrainedLeaderEpoch, Name: "drained_leader_epoch", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDDrainedFenceVersion, Name: "drained_fence_version", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDAttempt, Name: "attempt", Type: ColumnUint64},
		{ID: channelMigrationTaskColumnIDNextRunAtMS, Name: "next_run_at_ms", Type: ColumnInt64},
		{ID: channelMigrationTaskColumnIDBlockerCode, Name: "blocker_code", Type: ColumnString, Nullable: true},
		{ID: channelMigrationTaskColumnIDBlockerMessage, Name: "blocker_message", Type: ColumnString, Nullable: true},
		{ID: channelMigrationTaskColumnIDLastError, Name: "last_error", Type: ColumnString, Nullable: true},
		{ID: channelMigrationTaskColumnIDCreatedAtMS, Name: "created_at_ms", Type: ColumnInt64},
		{ID: channelMigrationTaskColumnIDUpdatedAtMS, Name: "updated_at_ms", Type: ColumnInt64},
		{ID: channelMigrationTaskColumnIDCompletedAtMS, Name: "completed_at_ms", Type: ColumnInt64},
		{ID: channelMigrationTaskColumnIDProgress, Name: "progress", Type: ColumnBytes, Nullable: true},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:   channelMigrationTaskPrimaryFamilyID,
			Name: "primary",
			ColumnIDs: []uint16{
				channelMigrationTaskColumnIDKind,
				channelMigrationTaskColumnIDStatus,
				channelMigrationTaskColumnIDPhase,
				channelMigrationTaskColumnIDSourceNode,
				channelMigrationTaskColumnIDTargetNode,
				channelMigrationTaskColumnIDDesiredLeader,
				channelMigrationTaskColumnIDBaseChannelEpoch,
				channelMigrationTaskColumnIDBaseLeaderEpoch,
				channelMigrationTaskColumnIDFenceToken,
				channelMigrationTaskColumnIDFenceVersion,
				channelMigrationTaskColumnIDFenceUntilMS,
				channelMigrationTaskColumnIDEmbeddedLeaderTransfer,
				channelMigrationTaskColumnIDEmbeddedDesiredLeader,
				channelMigrationTaskColumnIDOwnerNodeID,
				channelMigrationTaskColumnIDOwnerLeaseUntilMS,
				channelMigrationTaskColumnIDCutoverLEO,
				channelMigrationTaskColumnIDCutoverHW,
				channelMigrationTaskColumnIDDrainedLeaderNode,
				channelMigrationTaskColumnIDDrainedRuntimeGeneration,
				channelMigrationTaskColumnIDDrainedChannelEpoch,
				channelMigrationTaskColumnIDDrainedLeaderEpoch,
				channelMigrationTaskColumnIDDrainedFenceVersion,
				channelMigrationTaskColumnIDAttempt,
				channelMigrationTaskColumnIDNextRunAtMS,
				channelMigrationTaskColumnIDBlockerCode,
				channelMigrationTaskColumnIDBlockerMessage,
				channelMigrationTaskColumnIDLastError,
				channelMigrationTaskColumnIDCreatedAtMS,
				channelMigrationTaskColumnIDUpdatedAtMS,
				channelMigrationTaskColumnIDCompletedAtMS,
				channelMigrationTaskColumnIDProgress,
			},
			DefaultColumnID: channelMigrationTaskColumnIDKind,
		},
	},
	PrimaryIndex: IndexDesc{
		ID:        channelMigrationTaskPrimaryIndexID,
		Name:      "pk_channel_migration_task",
		Unique:    true,
		Primary:   true,
		ColumnIDs: []uint16{channelMigrationTaskColumnIDChannelID, channelMigrationTaskColumnIDType, channelMigrationTaskColumnIDTaskID},
	},
	SecondaryIndexes: []IndexDesc{
		{
			ID:        channelMigrationTaskActiveIndexID,
			Name:      "idx_channel_migration_active",
			Unique:    true,
			ColumnIDs: []uint16{channelMigrationTaskColumnIDChannelID, channelMigrationTaskColumnIDType},
		},
		{
			ID:        channelMigrationTaskTerminalIndexID,
			Name:      "idx_channel_migration_terminal",
			Unique:    true,
			ColumnIDs: []uint16{channelMigrationTaskColumnIDCompletedAtMS, channelMigrationTaskColumnIDChannelID, channelMigrationTaskColumnIDType, channelMigrationTaskColumnIDTaskID},
		},
	},
}

var CMDConversationStateTable = &TableDesc{
	ID:   TableIDCMDConversationState,
	Name: "cmd_conversation_state",
	Columns: []ColumnDesc{
		{ID: cmdConversationStateColumnIDUID, Name: "uid", Type: ColumnString},
		{ID: cmdConversationStateColumnIDChannelType, Name: "channel_type", Type: ColumnInt64},
		{ID: cmdConversationStateColumnIDChannelID, Name: "channel_id", Type: ColumnString},
		{ID: cmdConversationStateColumnIDReadSeq, Name: "read_seq", Type: ColumnUint64},
		{ID: cmdConversationStateColumnIDDeletedToSeq, Name: "deleted_to_seq", Type: ColumnUint64},
		{ID: cmdConversationStateColumnIDActiveAt, Name: "active_at", Type: ColumnInt64},
		{ID: cmdConversationStateColumnIDUpdatedAt, Name: "updated_at", Type: ColumnInt64},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:              cmdConversationStatePrimaryFamilyID,
			Name:            "primary",
			ColumnIDs:       []uint16{cmdConversationStateColumnIDReadSeq, cmdConversationStateColumnIDDeletedToSeq, cmdConversationStateColumnIDActiveAt, cmdConversationStateColumnIDUpdatedAt},
			DefaultColumnID: cmdConversationStateColumnIDReadSeq,
		},
	},
	PrimaryIndex: IndexDesc{
		ID:        cmdConversationStatePrimaryIndexID,
		Name:      "pk_cmd_conversation_state",
		Unique:    true,
		Primary:   true,
		ColumnIDs: []uint16{cmdConversationStateColumnIDUID, cmdConversationStateColumnIDChannelType, cmdConversationStateColumnIDChannelID},
	},
	SecondaryIndexes: []IndexDesc{
		{
			ID:        cmdConversationStateActiveIndexID,
			Name:      "idx_cmd_conversation_active",
			Unique:    false,
			ColumnIDs: []uint16{cmdConversationStateColumnIDUID, cmdConversationStateColumnIDActiveAt, cmdConversationStateColumnIDChannelType, cmdConversationStateColumnIDChannelID},
		},
	},
}
var PluginUserBindingTable = &TableDesc{
	ID:   TableIDPluginUserBinding,
	Name: "plugin_user_binding",
	Columns: []ColumnDesc{
		{ID: pluginUserBindingColumnIDUID, Name: "uid", Type: ColumnString},
		{ID: pluginUserBindingColumnIDPluginNo, Name: "plugin_no", Type: ColumnString},
		{ID: pluginUserBindingColumnIDCreatedAtMS, Name: "created_at_ms", Type: ColumnInt64},
		{ID: pluginUserBindingColumnIDUpdatedAtMS, Name: "updated_at_ms", Type: ColumnInt64},
	},
	Families: []ColumnFamilyDesc{
		{
			ID:              pluginUserBindingPrimaryFamilyID,
			Name:            "primary",
			ColumnIDs:       []uint16{pluginUserBindingColumnIDCreatedAtMS, pluginUserBindingColumnIDUpdatedAtMS},
			DefaultColumnID: pluginUserBindingColumnIDCreatedAtMS,
		},
	},
	PrimaryIndex: IndexDesc{
		ID:        pluginUserBindingPrimaryIndexID,
		Name:      "pk_plugin_user_binding",
		Unique:    true,
		Primary:   true,
		ColumnIDs: []uint16{pluginUserBindingColumnIDUID, pluginUserBindingColumnIDPluginNo},
	},
	SecondaryIndexes: []IndexDesc{
		{
			ID:        pluginUserBindingPluginIndexID,
			Name:      "idx_plugin_no_uid",
			Unique:    true,
			ColumnIDs: []uint16{pluginUserBindingColumnIDPluginNo, pluginUserBindingColumnIDUID},
		},
	},
}
