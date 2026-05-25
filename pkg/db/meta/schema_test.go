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
	seen := make(map[uint32]string, len(tables))
	for _, table := range tables {
		if err := schema.ValidateTable(table); err != nil {
			t.Fatalf("ValidateTable(%s): %v", table.Name, err)
		}
		if prev, ok := seen[table.ID]; ok {
			t.Fatalf("table id %d reused by %s and %s", table.ID, prev, table.Name)
		}
		seen[table.ID] = table.Name
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
