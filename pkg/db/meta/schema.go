package meta

import "github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"

const (
	columnIDStringKey uint16 = 1
	columnIDIntKey    uint16 = 2
	columnIDValue     uint16 = 3
	columnIDUpdatedAt uint16 = 4
)

// Tables returns every metadata table descriptor.
func Tables() []schema.Table {
	return []schema.Table{
		UserTable,
		DeviceTable,
		ChannelTable,
		SubscriberTable,
		ChannelRuntimeMetaTable,
		ConversationTable,
		CMDConversationTable,
		PluginBindingTable,
		ChannelMigrationTable,
		HashSlotMigrationTable,
	}
}

// Core metadata table descriptors. Later table tasks expand column coverage
// without changing IDs or primary/index IDs.
var (
	UserTable               = simpleMetaTable(TableIDUser, "user")
	DeviceTable             = simpleMetaTable(TableIDDevice, "device")
	SubscriberTable         = simpleMetaTable(TableIDSubscriber, "subscriber")
	ChannelRuntimeMetaTable = simpleMetaTable(TableIDChannelRuntimeMeta, "channel_runtime_meta")
	CMDConversationTable    = activeMetaTable(TableIDCMDConversation, "cmd_conversation")
	PluginBindingTable      = simpleMetaTable(TableIDPluginBinding, "plugin_binding")
	ChannelMigrationTable   = activeMetaTable(TableIDChannelMigration, "channel_migration")
	HashSlotMigrationTable  = activeMetaTable(TableIDHashSlotMigration, "hashslot_migration")

	ChannelTable = schema.Table{
		ID:   TableIDChannel,
		Name: "channel",
		Columns: []schema.Column{
			{ID: columnIDStringKey, Name: "channel_id", Type: schema.TypeString, Required: true},
			{ID: columnIDIntKey, Name: "channel_type", Type: schema.TypeInt64, Required: true},
			{ID: columnIDValue, Name: "value", Type: schema.TypeBytes},
			{ID: columnIDUpdatedAt, Name: "updated_at", Type: schema.TypeInt64},
		},
		Families: []schema.Family{{ID: channelPrimaryFamilyID, Name: "primary", Columns: []uint16{columnIDValue, columnIDUpdatedAt}}},
		Primary:  schema.Index{ID: channelPrimaryIndexID, Name: "pk_channel", Unique: true, Primary: true, Columns: []uint16{columnIDStringKey, columnIDIntKey}},
		Indexes: []schema.Index{
			{ID: channelIDIndexID, Name: "idx_channel_id", Columns: []uint16{columnIDStringKey, columnIDIntKey}},
			{ID: channelActiveIndexID, Name: "idx_channel_active", Columns: []uint16{columnIDUpdatedAt, columnIDStringKey}},
		},
	}

	ConversationTable = activeMetaTable(TableIDConversation, "conversation")
)

func simpleMetaTable(id uint32, name string) schema.Table {
	return schema.Table{
		ID:   id,
		Name: name,
		Columns: []schema.Column{
			{ID: columnIDStringKey, Name: "key", Type: schema.TypeString, Required: true},
			{ID: columnIDValue, Name: "value", Type: schema.TypeBytes},
		},
		Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{columnIDValue}}},
		Primary:  schema.Index{ID: 1, Name: "pk_" + name, Unique: true, Primary: true, Columns: []uint16{columnIDStringKey}},
	}
}

func activeMetaTable(id uint32, name string) schema.Table {
	table := simpleMetaTable(id, name)
	table.Columns = append(table.Columns, schema.Column{ID: columnIDUpdatedAt, Name: "active_at", Type: schema.TypeInt64})
	table.Indexes = []schema.Index{{ID: conversationActiveIndexID, Name: "idx_" + name + "_active", Columns: []uint16{columnIDUpdatedAt, columnIDStringKey}}}
	return table
}
