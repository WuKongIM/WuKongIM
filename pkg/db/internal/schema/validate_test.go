package schema_test

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

func TestValidateTableAcceptsValidDescriptor(t *testing.T) {
	table := validTable()
	if err := schema.ValidateTable(table); err != nil {
		t.Fatalf("ValidateTable(): %v", err)
	}
}

func TestValidateTableRejectsDuplicateColumnID(t *testing.T) {
	table := validTable()
	table.Columns = append(table.Columns, schema.Column{ID: 1, Name: "dupe", Type: schema.TypeString, Required: true})
	if err := schema.ValidateTable(table); !errors.Is(err, db.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
}

func TestValidateTableRejectsFamilyUnknownColumn(t *testing.T) {
	table := validTable()
	table.Families[0].Columns = append(table.Families[0].Columns, 99)
	if err := schema.ValidateTable(table); !errors.Is(err, db.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
}

func TestValidateTableRejectsIndexUnknownColumn(t *testing.T) {
	table := validTable()
	table.Indexes[0].Columns = append(table.Indexes[0].Columns, 99)
	if err := schema.ValidateTable(table); !errors.Is(err, db.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
}

func TestValidateTableRejectsDuplicateIndexID(t *testing.T) {
	table := validTable()
	table.Indexes = append(table.Indexes, table.Indexes[0])
	if err := schema.ValidateTable(table); !errors.Is(err, db.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
}

func validTable() schema.Table {
	return schema.Table{
		ID:   1,
		Name: "user",
		Columns: []schema.Column{
			{ID: 1, Name: "uid", Type: schema.TypeString, Required: true},
			{ID: 2, Name: "token", Type: schema.TypeString},
			{ID: 3, Name: "version", Type: schema.TypeUint64},
		},
		Families: []schema.Family{
			{ID: 0, Name: "primary", Columns: []uint16{2, 3}},
		},
		Primary: schema.Index{ID: 1, Name: "pk_user", Unique: true, Primary: true, Columns: []uint16{1}},
		Indexes: []schema.Index{
			{ID: 2, Name: "idx_token", Columns: []uint16{2}},
		},
	}
}
