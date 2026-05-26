package meta

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

type runtimeTestRow struct {
	ID    string
	Owner string
	Value string
}

func newRuntimeTestTable(t *testing.T) Table[runtimeTestRow] {
	t.Helper()
	table, err := registerMetaTableInRegistry(newMetaTableRegistry(), TableSpec[runtimeTestRow]{
		ID:   65030,
		Name: "runtime_test",
		Columns: []schema.Column{
			{ID: 1, Name: "id", Type: schema.TypeString, Required: true},
			{ID: 2, Name: "owner", Type: schema.TypeString},
			{ID: 3, Name: "value", Type: schema.TypeString},
		},
		Families: []schema.Family{{ID: 0, Name: "primary", Columns: []uint16{2, 3}}},
		Primary: PrimarySpec[runtimeTestRow]{
			IndexID:  1,
			FamilyID: 0,
			Name:     "pk_runtime_test",
			Columns:  []uint16{1},
			Layout:   KeyLayout{KeyString},
			Key:      func(row runtimeTestRow) KeyParts { return KeyParts{String(row.ID)} },
		},
		Validate: func(row runtimeTestRow) error {
			return validateKeyString(row.ID)
		},
		EncodeValue: func(row runtimeTestRow) ([]byte, error) {
			value := appendValueString(nil, row.Owner)
			value = appendValueString(value, row.Value)
			return value, nil
		},
		DecodeValue: func(primary KeyParts, value []byte) (runtimeTestRow, error) {
			owner, rest, err := readValueString(value)
			if err != nil {
				return runtimeTestRow{}, err
			}
			val, rest, err := readValueString(rest)
			if err != nil {
				return runtimeTestRow{}, err
			}
			if len(rest) != 0 {
				return runtimeTestRow{}, dberrors.ErrCorruptValue
			}
			return runtimeTestRow{ID: primary[0].S, Owner: owner, Value: val}, nil
		},
	})
	if err != nil {
		t.Fatalf("register test table: %v", err)
	}
	return table
}

func TestTableRuntimePrimaryCRUD(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(3)
	ctx := context.Background()

	row := runtimeTestRow{ID: "a", Owner: "o1", Value: "v1"}
	if err := table.Create(ctx, shard, row); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if err := table.Create(ctx, shard, row); err != dberrors.ErrAlreadyExists {
		t.Fatalf("duplicate Create() = %v, want ErrAlreadyExists", err)
	}

	got, ok, err := table.Get(ctx, shard, KeyParts{String("a")})
	if err != nil || !ok || got != row {
		t.Fatalf("Get() = %#v ok=%v err=%v", got, ok, err)
	}

	updated := runtimeTestRow{ID: "a", Owner: "o1", Value: "v2"}
	if err := table.Update(ctx, shard, updated); err != nil {
		t.Fatalf("Update(): %v", err)
	}
	got, ok, err = table.Get(ctx, shard, KeyParts{String("a")})
	if err != nil || !ok || got.Value != "v2" {
		t.Fatalf("Get after update = %#v ok=%v err=%v", got, ok, err)
	}

	if err := table.Update(ctx, shard, runtimeTestRow{ID: "missing"}); err != dberrors.ErrNotFound {
		t.Fatalf("missing Update() = %v, want ErrNotFound", err)
	}

	if err := table.Delete(ctx, shard, KeyParts{String("a")}); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	_, ok, err = table.Get(ctx, shard, KeyParts{String("a")})
	if err != nil || ok {
		t.Fatalf("Get deleted ok=%v err=%v", ok, err)
	}
}

func TestTableRuntimePrimaryScan(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	table := newRuntimeTestTable(t)
	shard := store.db.HashSlot(4)
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c"} {
		if err := table.Upsert(ctx, shard, runtimeTestRow{ID: id, Owner: "o", Value: id}); err != nil {
			t.Fatalf("Upsert(%s): %v", id, err)
		}
	}
	rows, cursor, done, err := table.ScanPrimary(ctx, shard, nil, 2)
	if err != nil || done || len(rows) != 2 || rows[0].ID != "a" || rows[1].ID != "b" || !cursor.Equal(KeyParts{String("b")}) {
		t.Fatalf("first page rows=%#v cursor=%#v done=%v err=%v", rows, cursor, done, err)
	}
	rows, cursor, done, err = table.ScanPrimary(ctx, shard, cursor, 2)
	if err != nil || !done || len(rows) != 1 || rows[0].ID != "c" || len(cursor) != 0 {
		t.Fatalf("second page rows=%#v cursor=%#v done=%v err=%v", rows, cursor, done, err)
	}
}
