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
	return defaultMetaRegistry.tables()
}

func init() {
	for _, descriptor := range []metaTableDescriptor{
		{Table: SubscriberTable},
		{Table: ChannelRuntimeMetaTable},
		{Table: ChannelMigrationTable},
		{Table: HashSlotMigrationTable, SnapshotPolicy: SnapshotPolicy{PreserveOnImport: true}},
	} {
		defaultMetaRegistry.mustRegister(descriptor)
	}
}

// Core metadata table descriptors. Later table tasks expand column coverage
// without changing IDs or primary/index IDs.
var (
	SubscriberTable         = simpleMetaTable(TableIDSubscriber, "subscriber")
	ChannelRuntimeMetaTable = simpleMetaTable(TableIDChannelRuntimeMeta, "channel_runtime_meta")
	ChannelMigrationTable   = activeMetaTable(TableIDChannelMigration, "channel_migration")
	HashSlotMigrationTable  = activeMetaTable(TableIDHashSlotMigration, "hashslot_migration")
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
