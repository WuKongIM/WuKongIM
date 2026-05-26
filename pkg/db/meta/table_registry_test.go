package meta

import (
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

func TestMetaTableRegistryRejectsDuplicateTableID(t *testing.T) {
	registry := newMetaTableRegistry()
	table := simpleMetaTable(65001, "test_duplicate")
	if err := registry.register(metaTableDescriptor{Table: table}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	err := registry.register(metaTableDescriptor{Table: table})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("second register err = %v, want duplicate error", err)
	}
}

func TestMetaTableRegistryTablesAreSortedAndCopied(t *testing.T) {
	registry := newMetaTableRegistry()
	if err := registry.register(metaTableDescriptor{Table: simpleMetaTable(65002, "z_table")}); err != nil {
		t.Fatalf("register z: %v", err)
	}
	if err := registry.register(metaTableDescriptor{Table: simpleMetaTable(65001, "a_table")}); err != nil {
		t.Fatalf("register a: %v", err)
	}

	tables := registry.tables()
	if len(tables) != 2 || tables[0].ID != 65001 || tables[1].ID != 65002 {
		t.Fatalf("tables order = %#v", tables)
	}
	tables[0].Name = "mutated"
	again := registry.tables()
	if again[0].Name != "a_table" {
		t.Fatalf("registry returned mutable table copy: %q", again[0].Name)
	}
}

func TestMetaTableRegistryPreservePolicy(t *testing.T) {
	registry := newMetaTableRegistry()
	table := simpleMetaTable(65003, "preserved")
	if err := registry.register(metaTableDescriptor{Table: table, SnapshotPolicy: SnapshotPolicy{PreserveOnImport: true}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	descriptor, ok := registry.lookup(65003)
	if !ok || !descriptor.SnapshotPolicy.PreserveOnImport {
		t.Fatalf("preserve policy = %#v ok=%v", descriptor.SnapshotPolicy, ok)
	}
}

func TestMetaTableRegistryRejectsInvalidSchema(t *testing.T) {
	registry := newMetaTableRegistry()
	err := registry.register(metaTableDescriptor{Table: schema.Table{ID: 65004, Name: "invalid"}})
	if err == nil {
		t.Fatal("register invalid schema succeeded")
	}
}
