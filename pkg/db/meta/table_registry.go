package meta

import (
	"fmt"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// SnapshotPolicy controls table behavior during hash-slot snapshot import.
type SnapshotPolicy struct {
	// PreserveOnImport keeps existing local rows when importing preserving snapshots.
	PreserveOnImport bool
}

type metaTableDescriptor struct {
	Table          schema.Table
	SnapshotPolicy SnapshotPolicy
}

type metaTableRegistry struct {
	byID map[uint32]metaTableDescriptor
}

var defaultMetaRegistry = newMetaTableRegistry()

func newMetaTableRegistry() *metaTableRegistry {
	return &metaTableRegistry{byID: make(map[uint32]metaTableDescriptor)}
}

func (r *metaTableRegistry) register(descriptor metaTableDescriptor) error {
	if r == nil {
		return fmt.Errorf("%w: nil meta table registry", dberrors.ErrInvalidArgument)
	}
	if err := schema.ValidateTable(descriptor.Table); err != nil {
		return err
	}
	if _, ok := r.byID[descriptor.Table.ID]; ok {
		return fmt.Errorf("%w: duplicate meta table id %d", dberrors.ErrInvalidArgument, descriptor.Table.ID)
	}
	r.byID[descriptor.Table.ID] = cloneMetaTableDescriptor(descriptor)
	return nil
}

func (r *metaTableRegistry) mustRegister(descriptor metaTableDescriptor) {
	if err := r.register(descriptor); err != nil {
		panic(err)
	}
}

func (r *metaTableRegistry) lookup(tableID uint32) (metaTableDescriptor, bool) {
	if r == nil {
		return metaTableDescriptor{}, false
	}
	descriptor, ok := r.byID[tableID]
	if !ok {
		return metaTableDescriptor{}, false
	}
	return cloneMetaTableDescriptor(descriptor), true
}

func (r *metaTableRegistry) tables() []schema.Table {
	if r == nil || len(r.byID) == 0 {
		return nil
	}
	ids := make([]uint32, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	tables := make([]schema.Table, 0, len(ids))
	for _, id := range ids {
		tables = append(tables, cloneSchemaTable(r.byID[id].Table))
	}
	return tables
}

func (r *metaTableRegistry) rowTablesForSnapshot(preserve bool) []schema.Table {
	if r == nil || len(r.byID) == 0 {
		return nil
	}
	ids := make([]uint32, 0, len(r.byID))
	for id, descriptor := range r.byID {
		if preserve && descriptor.SnapshotPolicy.PreserveOnImport {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	tables := make([]schema.Table, 0, len(ids))
	for _, id := range ids {
		tables = append(tables, cloneSchemaTable(r.byID[id].Table))
	}
	return tables
}

func cloneMetaTableDescriptor(descriptor metaTableDescriptor) metaTableDescriptor {
	descriptor.Table = cloneSchemaTable(descriptor.Table)
	return descriptor
}

func cloneSchemaTable(table schema.Table) schema.Table {
	table.Columns = append([]schema.Column(nil), table.Columns...)
	table.Families = append([]schema.Family(nil), table.Families...)
	for i := range table.Families {
		table.Families[i].Columns = append([]uint16(nil), table.Families[i].Columns...)
	}
	table.Primary.Columns = append([]uint16(nil), table.Primary.Columns...)
	table.Primary.Covering = append([]uint16(nil), table.Primary.Covering...)
	table.Indexes = append([]schema.Index(nil), table.Indexes...)
	for i := range table.Indexes {
		table.Indexes[i].Columns = append([]uint16(nil), table.Indexes[i].Columns...)
		table.Indexes[i].Covering = append([]uint16(nil), table.Indexes[i].Covering...)
	}
	return table
}
