package meta

import (
	"context"
	"fmt"
	"reflect"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// InspectRow contains one decoded metadata row.
type InspectRow map[string]any

// InspectCursor identifies the next row position for an inspection scan.
type InspectCursor struct {
	// HashSlot is the hash slot where the next scan should resume.
	HashSlot HashSlot
	// Primary stores the table primary-key cursor values.
	Primary []any
}

// InspectScanRequest describes a bounded read-only metadata table scan.
type InspectScanRequest struct {
	// Table is the metadata table name to inspect.
	Table string
	// HashSlot selects one hash slot when HashSlotSet is true.
	HashSlot HashSlot
	// HashSlotSet reports whether HashSlot was explicitly selected.
	HashSlotSet bool
	// HashSlotCount bounds local scans when no explicit hash slot is selected.
	HashSlotCount uint16
	// Filters applies simple equality checks to decoded row fields.
	Filters map[string]any
	// After resumes the scan after a previous cursor.
	After *InspectCursor
	// Limit bounds the total returned rows.
	Limit int
}

// InspectScanResult contains rows and resume metadata from an inspection scan.
type InspectScanResult struct {
	// Rows contains decoded rows that matched the request filters.
	Rows []InspectRow
	// Next is the cursor to continue scanning when Done is false.
	Next *InspectCursor
	// Done reports whether the requested scan range was exhausted.
	Done bool
	// ScannedRows counts decoded rows read before filters were applied.
	ScannedRows int
	// ScannedHashSlots lists hash slots touched by the scan.
	ScannedHashSlots []HashSlot
}

// InspectScan scans metadata rows for read-only inspection.
func InspectScan(ctx context.Context, db *MetaDB, req InspectScanRequest) (InspectScanResult, error) {
	if err := inspectCheckDB(db); err != nil {
		return InspectScanResult{}, err
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.Table != "user" {
		return InspectScanResult{}, fmt.Errorf("%w: unknown inspect table %q", dberrors.ErrInvalidArgument, req.Table)
	}
	slots, err := inspectScanSlots(req)
	if err != nil {
		return InspectScanResult{}, err
	}
	afterUID, err := inspectUserAfterUID(req.After)
	if err != nil {
		return InspectScanResult{}, err
	}

	result := InspectScanResult{
		Rows: make([]InspectRow, 0, req.Limit),
		Done: true,
	}
	for _, slot := range slots {
		if len(result.Rows) >= req.Limit {
			result.Done = false
			result.Next = &InspectCursor{HashSlot: slot}
			break
		}
		cursorUID := ""
		if req.After != nil && req.After.HashSlot == slot {
			cursorUID = afterUID
		}
		scannedSlot, done, nextUID, err := inspectScanUserSlot(ctx, db.HashSlot(slot), cursorUID, req.Limit, req.Filters, &result)
		if err != nil {
			return InspectScanResult{}, err
		}
		if scannedSlot {
			result.ScannedHashSlots = append(result.ScannedHashSlots, slot)
		}
		if !done {
			result.Done = false
			result.Next = &InspectCursor{HashSlot: slot, Primary: []any{nextUID}}
			break
		}
	}
	return result, nil
}

func inspectScanSlots(req InspectScanRequest) ([]HashSlot, error) {
	if req.HashSlotSet {
		return []HashSlot{req.HashSlot}, nil
	}
	if req.HashSlotCount == 0 {
		return nil, fmt.Errorf("%w: hash slot count is required", dberrors.ErrInvalidArgument)
	}
	start := HashSlot(0)
	if req.After != nil {
		start = req.After.HashSlot
	}
	if start >= HashSlot(req.HashSlotCount) {
		return nil, nil
	}
	slots := make([]HashSlot, 0, int(req.HashSlotCount)-int(start))
	for slot := start; slot < HashSlot(req.HashSlotCount); slot++ {
		slots = append(slots, slot)
	}
	return slots, nil
}

func inspectCheckDB(db *MetaDB) error {
	if db == nil || db.engine == nil {
		return dberrors.ErrClosed
	}
	iter, err := db.engine.NewIter(engine.Span{}, engine.IterOptions{})
	if err != nil {
		return err
	}
	return iter.Close()
}

func inspectUserAfterUID(cursor *InspectCursor) (string, error) {
	if cursor == nil || len(cursor.Primary) == 0 {
		return "", nil
	}
	if len(cursor.Primary) != 1 {
		return "", fmt.Errorf("%w: invalid user inspect cursor", dberrors.ErrInvalidArgument)
	}
	uid, ok := cursor.Primary[0].(string)
	if !ok {
		return "", fmt.Errorf("%w: invalid user inspect cursor", dberrors.ErrInvalidArgument)
	}
	return uid, nil
}

func inspectScanUserSlot(ctx context.Context, shard *Shard, afterUID string, targetRows int, filters map[string]any, result *InspectScanResult) (bool, bool, string, error) {
	cursorUID := afterUID
	for len(result.Rows) < targetRows {
		pageLimit := targetRows - len(result.Rows)
		users, cursor, done, err := shard.ListUsersPage(ctx, cursorUID, pageLimit)
		if err != nil {
			return false, false, "", err
		}
		result.ScannedRows += len(users)
		for _, user := range users {
			row := inspectUserRow(user)
			if inspectRowMatches(row, filters) {
				result.Rows = append(result.Rows, row)
			}
		}
		if done {
			return true, true, "", nil
		}
		if cursor == "" || cursor == cursorUID {
			return true, false, "", fmt.Errorf("%w: non-advancing user inspect cursor", dberrors.ErrCorruptState)
		}
		cursorUID = cursor
	}
	return true, false, cursorUID, nil
}

func inspectUserRow(user User) InspectRow {
	return InspectRow{
		"uid":          user.UID,
		"token":        user.Token,
		"device_flag":  user.DeviceFlag,
		"device_level": user.DeviceLevel,
	}
}

func inspectRowMatches(row InspectRow, filters map[string]any) bool {
	for key, want := range filters {
		got, ok := row[key]
		if !ok || !reflect.DeepEqual(got, want) {
			return false
		}
	}
	return true
}
