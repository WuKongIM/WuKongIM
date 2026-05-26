package meta

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// TableSpec describes one runtime-backed metadata table.
type TableSpec[R any] struct {
	ID   uint32
	Name string

	Columns        []schema.Column
	Families       []schema.Family
	Primary        PrimarySpec[R]
	Indexes        []IndexSpec[R]
	SnapshotPolicy SnapshotPolicy

	Validate    func(R) error
	EncodeValue func(R) ([]byte, error)
	DecodeValue func(KeyParts, []byte) (R, error)
}

// PrimarySpec describes the primary key and row family for a table.
type PrimarySpec[R any] struct {
	IndexID  uint16
	FamilyID uint16
	Name     string
	Columns  []uint16
	Layout   KeyLayout
	Key      func(R) KeyParts
}

// IndexSpec describes one secondary index.
type IndexSpec[R any] struct {
	ID      uint16
	Name    string
	Unique  bool
	Columns []uint16
	Layout  KeyLayout
	Key     func(R) (KeyParts, bool)
}

// Table is a typed handle for common metadata table operations.
type Table[R any] struct {
	spec   TableSpec[R]
	schema schema.Table
}

func registerMetaTable[R any](spec TableSpec[R]) Table[R] {
	table, err := registerMetaTableInRegistry(defaultMetaRegistry, spec)
	if err != nil {
		panic(err)
	}
	return table
}

func registerMetaTableInRegistry[R any](registry *metaTableRegistry, spec TableSpec[R]) (Table[R], error) {
	normalized, tableSchema, err := normalizeTableSpec(spec)
	if err != nil {
		return Table[R]{}, err
	}
	table := Table[R]{spec: normalized, schema: tableSchema}
	if err := registry.register(metaTableDescriptor{Table: tableSchema, SnapshotPolicy: spec.SnapshotPolicy}); err != nil {
		return Table[R]{}, err
	}
	return table, nil
}

// Schema returns a copy of the table schema descriptor.
func (t Table[R]) Schema() schema.Table {
	return cloneSchemaTable(t.schema)
}

func normalizeTableSpec[R any](spec TableSpec[R]) (TableSpec[R], schema.Table, error) {
	if spec.ID == 0 || spec.Name == "" || spec.Primary.Key == nil || spec.EncodeValue == nil || spec.DecodeValue == nil {
		return spec, schema.Table{}, fmt.Errorf("%w: incomplete table spec", dberrors.ErrInvalidArgument)
	}
	if spec.Primary.IndexID == 0 || spec.Primary.Name == "" || len(spec.Primary.Layout) != len(spec.Primary.Columns) {
		return spec, schema.Table{}, fmt.Errorf("%w: invalid primary spec", dberrors.ErrInvalidArgument)
	}
	indexes := make([]schema.Index, 0, len(spec.Indexes))
	for _, index := range spec.Indexes {
		if index.ID == 0 || index.Name == "" || index.Key == nil || len(index.Layout) != len(index.Columns) {
			return spec, schema.Table{}, fmt.Errorf("%w: invalid index spec", dberrors.ErrInvalidArgument)
		}
		indexes = append(indexes, schema.Index{
			ID:      index.ID,
			Name:    index.Name,
			Unique:  index.Unique,
			Columns: append([]uint16(nil), index.Columns...),
		})
	}
	tableSchema := schema.Table{
		ID:       spec.ID,
		Name:     spec.Name,
		Columns:  append([]schema.Column(nil), spec.Columns...),
		Families: cloneFamilies(spec.Families),
		Primary: schema.Index{
			ID:      spec.Primary.IndexID,
			Name:    spec.Primary.Name,
			Unique:  true,
			Primary: true,
			Columns: append([]uint16(nil), spec.Primary.Columns...),
		},
		Indexes: indexes,
	}
	if err := schema.ValidateTable(tableSchema); err != nil {
		return spec, schema.Table{}, err
	}
	return spec, tableSchema, nil
}

func cloneFamilies(families []schema.Family) []schema.Family {
	out := append([]schema.Family(nil), families...)
	for i := range out {
		out[i].Columns = append([]uint16(nil), out[i].Columns...)
	}
	return out
}
