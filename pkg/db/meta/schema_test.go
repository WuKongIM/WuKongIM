package meta

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

func TestMetaSchemaValidateAllTables(t *testing.T) {
	tables := Tables()
	if len(tables) < 10 {
		t.Fatalf("len(Tables()) = %d, want at least 10", len(tables))
	}
	if len(tables) != len(defaultMetaRegistry.tables()) {
		t.Fatalf("len(Tables()) = %d, registry length = %d", len(tables), len(defaultMetaRegistry.tables()))
	}
	seen := make(map[uint32]string, len(tables))
	channelIDIndexRegistered := false
	channelActiveIndexRegistered := false
	channelRuntimeMetaPrimaryRegistered := false
	channelMigrationPrimaryRegistered := false
	channelMigrationTerminalIndexRegistered := false
	hashSlotMigrationPrimaryRegistered := false
	subscriberPrimaryRegistered := false
	conversationActiveIndexRegistered := false
	cmdConversationActiveIndexRegistered := false
	for _, table := range tables {
		if err := schema.ValidateTable(table); err != nil {
			t.Fatalf("ValidateTable(%s): %v", table.Name, err)
		}
		if prev, ok := seen[table.ID]; ok {
			t.Fatalf("table id %d reused by %s and %s", table.ID, prev, table.Name)
		}
		seen[table.ID] = table.Name
		if table.ID == TableIDChannel {
			for _, index := range table.Indexes {
				if index.ID == channelIDIndexID && index.Name == "idx_channel_id" {
					channelIDIndexRegistered = true
				}
				if index.ID == channelActiveIndexID && index.Name == "idx_channel_active" {
					channelActiveIndexRegistered = true
				}
			}
		}
		if table.ID == TableIDChannelRuntimeMeta &&
			table.Primary.ID == channelRuntimeMetaPrimaryIndexID &&
			table.Primary.Name == "pk_channel_runtime_meta" &&
			len(table.Primary.Columns) == 2 {
			channelRuntimeMetaPrimaryRegistered = true
		}
		if table.ID == TableIDChannelMigration &&
			table.Primary.ID == channelMigrationPrimaryIndexID &&
			table.Primary.Name == "pk_channel_migration" &&
			len(table.Primary.Columns) == 3 {
			channelMigrationPrimaryRegistered = true
			for _, index := range table.Indexes {
				if index.ID == channelMigrationTerminalIndexID &&
					index.Name == "idx_channel_migration_terminal" &&
					len(index.Columns) == 4 {
					channelMigrationTerminalIndexRegistered = true
				}
			}
		}
		if table.ID == TableIDHashSlotMigration &&
			table.Primary.ID == hashSlotMigrationPrimaryIndexID &&
			table.Primary.Name == "pk_hashslot_migration" &&
			len(table.Primary.Columns) == 1 {
			hashSlotMigrationPrimaryRegistered = true
		}
		if table.ID == TableIDSubscriber &&
			table.Primary.ID == subscriberPrimaryIndexID &&
			table.Primary.Name == "pk_subscriber" &&
			len(table.Primary.Columns) == 3 {
			subscriberPrimaryRegistered = true
		}
		if table.ID == TableIDConversation {
			for _, index := range table.Indexes {
				if index.ID == conversationActiveIndexID && index.Name == "idx_conversation_active" {
					conversationActiveIndexRegistered = true
				}
			}
		}
		if table.ID == TableIDCMDConversation {
			for _, index := range table.Indexes {
				if index.ID == conversationActiveIndexID && index.Name == "idx_cmd_conversation_active" {
					cmdConversationActiveIndexRegistered = true
				}
			}
		}
	}
	if !channelIDIndexRegistered {
		t.Fatalf("channel table missing idx_channel_id index %d", channelIDIndexID)
	}
	if !channelActiveIndexRegistered {
		t.Fatalf("channel table missing idx_channel_active index %d", channelActiveIndexID)
	}
	if !channelRuntimeMetaPrimaryRegistered {
		t.Fatalf("channel runtime meta table missing typed primary index")
	}
	if !channelMigrationPrimaryRegistered {
		t.Fatalf("channel migration table missing typed primary index")
	}
	if !channelMigrationTerminalIndexRegistered {
		t.Fatalf("channel migration table missing terminal index %d", channelMigrationTerminalIndexID)
	}
	if !hashSlotMigrationPrimaryRegistered {
		t.Fatalf("hash-slot migration table missing typed primary index")
	}
	if !subscriberPrimaryRegistered {
		t.Fatalf("subscriber table missing typed primary index %d", subscriberPrimaryIndexID)
	}
	if !conversationActiveIndexRegistered {
		t.Fatalf("conversation table missing idx_conversation_active index %d", conversationActiveIndexID)
	}
	if !cmdConversationActiveIndexRegistered {
		t.Fatalf("cmd conversation table missing idx_cmd_conversation_active index %d", conversationActiveIndexID)
	}
	for _, tableID := range []uint32{
		TableIDUser,
		TableIDDevice,
		TableIDChannel,
		TableIDSubscriber,
		TableIDChannelRuntimeMeta,
		TableIDConversation,
		TableIDCMDConversation,
		TableIDPluginBinding,
		TableIDChannelMigration,
		TableIDHashSlotMigration,
	} {
		if _, ok := seen[tableID]; !ok {
			t.Fatalf("table id %d missing from Tables()", tableID)
		}
	}
}
