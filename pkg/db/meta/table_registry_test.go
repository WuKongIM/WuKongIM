package meta

import (
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

func TestMetaTableRegistryRejectsDuplicateTableID(t *testing.T) {
	registry := newMetaTableRegistry()
	table := registryTestSchemaTable(65001, "test_duplicate")
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
	if err := registry.register(metaTableDescriptor{Table: registryTestSchemaTable(65002, "z_table")}); err != nil {
		t.Fatalf("register z: %v", err)
	}
	if err := registry.register(metaTableDescriptor{Table: registryTestSchemaTable(65001, "a_table")}); err != nil {
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
	table := registryTestSchemaTable(65003, "preserved")
	if err := registry.register(metaTableDescriptor{Table: table, SnapshotPolicy: SnapshotPolicy{PreserveOnImport: true}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	descriptor, ok := registry.lookup(65003)
	if !ok || !descriptor.SnapshotPolicy.PreserveOnImport {
		t.Fatalf("preserve policy = %#v ok=%v", descriptor.SnapshotPolicy, ok)
	}
}

func TestMetaTableRegistryRowTablesForSnapshotSkipsPreservedTables(t *testing.T) {
	registry := newMetaTableRegistry()
	if err := registry.register(metaTableDescriptor{Table: registryTestSchemaTable(65003, "preserved"), SnapshotPolicy: SnapshotPolicy{PreserveOnImport: true}}); err != nil {
		t.Fatalf("register preserved: %v", err)
	}
	if err := registry.register(metaTableDescriptor{Table: registryTestSchemaTable(65004, "normal")}); err != nil {
		t.Fatalf("register normal: %v", err)
	}

	tables := registry.rowTablesForSnapshot(true)
	if len(tables) != 1 || tables[0].ID != 65004 {
		t.Fatalf("preserving tables = %#v, want only normal table", tables)
	}
	tables = registry.rowTablesForSnapshot(false)
	if len(tables) != 2 || tables[0].ID != 65003 || tables[1].ID != 65004 {
		t.Fatalf("non-preserving tables = %#v, want both sorted tables", tables)
	}
}

func TestMetaTableRegistryRejectsInvalidSchema(t *testing.T) {
	registry := newMetaTableRegistry()
	err := registry.register(metaTableDescriptor{Table: schema.Table{ID: 65004, Name: "invalid"}})
	if err == nil {
		t.Fatal("register invalid schema succeeded")
	}
}

func registryTestSchemaTable(id uint32, name string) schema.Table {
	return schema.Table{
		ID:   id,
		Name: name,
		Columns: []schema.Column{
			{ID: 1, Name: "key", Type: schema.TypeString, Required: true},
			{ID: 2, Name: "value", Type: schema.TypeBytes},
		},
		Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{2}}},
		Primary:  schema.Index{ID: 1, Name: "pk_" + name, Unique: true, Primary: true, Columns: []uint16{1}},
	}
}

type registryTestRow struct {
	ID    string
	Owner string
}

func TestRegisterMetaTableBuildsSchemaDescriptor(t *testing.T) {
	registry := newMetaTableRegistry()
	table, err := registerMetaTableInRegistry(registry, TableSpec[registryTestRow]{
		ID:   65020,
		Name: "registry_test",
		Columns: []schema.Column{
			{ID: 1, Name: "id", Type: schema.TypeString, Required: true},
			{ID: 2, Name: "owner", Type: schema.TypeString},
			{ID: 3, Name: "value", Type: schema.TypeBytes},
		},
		Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{3}}},
		Primary: PrimarySpec[registryTestRow]{
			IndexID:  1,
			FamilyID: 0,
			Name:     "pk_registry_test",
			Columns:  []uint16{1},
			Layout:   KeyLayout{KeyString},
			Key:      func(row registryTestRow) KeyParts { return KeyParts{String(row.ID)} },
		},
		Indexes: []IndexSpec[registryTestRow]{
			{
				ID:      2,
				Name:    "idx_registry_test_owner",
				Columns: []uint16{2, 1},
				Layout:  KeyLayout{KeyString, KeyString},
				Key: func(row registryTestRow) (KeyParts, bool) {
					return KeyParts{String(row.Owner), String(row.ID)}, row.Owner != ""
				},
			},
		},
		EncodeValue: func(row registryTestRow) ([]byte, error) { return []byte(row.Owner), nil },
		DecodeValue: func(primary KeyParts, value []byte) (registryTestRow, error) {
			return registryTestRow{ID: primary[0].S, Owner: string(value)}, nil
		},
	})
	if err != nil {
		t.Fatalf("registerMetaTableInRegistry(): %v", err)
	}
	if table.Schema().Name != "registry_test" {
		t.Fatalf("schema name = %q", table.Schema().Name)
	}
	tables := registry.tables()
	if len(tables) != 1 || tables[0].Primary.Name != "pk_registry_test" || len(tables[0].Indexes) != 1 {
		t.Fatalf("registered tables = %#v", tables)
	}
}

func TestRegisterMetaTableRejectsMismatchedLayout(t *testing.T) {
	registry := newMetaTableRegistry()
	_, err := registerMetaTableInRegistry(registry, TableSpec[registryTestRow]{
		ID:       65021,
		Name:     "bad_layout",
		Columns:  []schema.Column{{ID: 1, Name: "id", Type: schema.TypeString, Required: true}},
		Families: []schema.Family{{ID: 0, Name: "primary"}},
		Primary: PrimarySpec[registryTestRow]{
			IndexID:  1,
			FamilyID: 0,
			Name:     "pk_bad_layout",
			Columns:  []uint16{1},
			Layout:   KeyLayout{},
			Key:      func(row registryTestRow) KeyParts { return KeyParts{String(row.ID)} },
		},
		EncodeValue: func(row registryTestRow) ([]byte, error) { return nil, nil },
		DecodeValue: func(primary KeyParts, value []byte) (registryTestRow, error) { return registryTestRow{}, nil },
	})
	if err == nil {
		t.Fatal("register with mismatched layout succeeded")
	}
}
