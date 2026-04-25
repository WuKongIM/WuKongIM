package slotmigration

import (
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

func ExportHashSlot(db *metadb.DB, hashSlot uint16) (*metadb.SlotSnapshot, error) {
	if db == nil {
		return nil, metadb.ErrInvalidArgument
	}
	snap, err := db.ExportHashSlotSnapshot(context.Background(), []uint16{hashSlot})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

func ImportHashSlot(db *metadb.DB, snap *metadb.SlotSnapshot) error {
	if db == nil || snap == nil {
		return metadb.ErrInvalidArgument
	}
	return db.ImportHashSlotSnapshot(context.Background(), *snap)
}
