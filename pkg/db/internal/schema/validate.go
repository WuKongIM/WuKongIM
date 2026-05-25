package schema

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// ValidateTable checks a table descriptor for stable ID and reference errors.
func ValidateTable(table Table) error {
	if table.ID == 0 || table.Name == "" {
		return fmt.Errorf("%w: table id and name are required", dberrors.ErrInvalidArgument)
	}
	columns := make(map[uint16]Column, len(table.Columns))
	for _, column := range table.Columns {
		if column.ID == 0 || column.Name == "" || column.Type == 0 {
			return fmt.Errorf("%w: invalid column in table %s", dberrors.ErrInvalidArgument, table.Name)
		}
		if _, ok := columns[column.ID]; ok {
			return fmt.Errorf("%w: duplicate column id %d", dberrors.ErrInvalidArgument, column.ID)
		}
		columns[column.ID] = column
	}
	if len(columns) == 0 {
		return fmt.Errorf("%w: table %s has no columns", dberrors.ErrInvalidArgument, table.Name)
	}

	familyIDs := make(map[uint16]struct{}, len(table.Families))
	for _, family := range table.Families {
		if family.Name == "" {
			return fmt.Errorf("%w: family name is required", dberrors.ErrInvalidArgument)
		}
		if _, ok := familyIDs[family.ID]; ok {
			return fmt.Errorf("%w: duplicate family id %d", dberrors.ErrInvalidArgument, family.ID)
		}
		familyIDs[family.ID] = struct{}{}
		for _, columnID := range family.Columns {
			if _, ok := columns[columnID]; !ok {
				return fmt.Errorf("%w: family %s references unknown column %d", dberrors.ErrInvalidArgument, family.Name, columnID)
			}
		}
	}

	if err := validateIndex(table.Primary, columns, "primary index"); err != nil {
		return err
	}
	if !table.Primary.Primary || !table.Primary.Unique {
		return fmt.Errorf("%w: primary index must be primary and unique", dberrors.ErrInvalidArgument)
	}

	indexIDs := map[uint16]struct{}{table.Primary.ID: {}}
	for _, index := range table.Indexes {
		if _, ok := indexIDs[index.ID]; ok {
			return fmt.Errorf("%w: duplicate index id %d", dberrors.ErrInvalidArgument, index.ID)
		}
		indexIDs[index.ID] = struct{}{}
		if err := validateIndex(index, columns, "index"); err != nil {
			return err
		}
	}
	return nil
}

func validateIndex(index Index, columns map[uint16]Column, label string) error {
	if index.ID == 0 || index.Name == "" || len(index.Columns) == 0 {
		return fmt.Errorf("%w: invalid %s", dberrors.ErrInvalidArgument, label)
	}
	for _, columnID := range index.Columns {
		if _, ok := columns[columnID]; !ok {
			return fmt.Errorf("%w: %s %s references unknown column %d", dberrors.ErrInvalidArgument, label, index.Name, columnID)
		}
	}
	for _, columnID := range index.Covering {
		if _, ok := columns[columnID]; !ok {
			return fmt.Errorf("%w: %s %s references unknown covering column %d", dberrors.ErrInvalidArgument, label, index.Name, columnID)
		}
	}
	return nil
}
