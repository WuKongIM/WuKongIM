package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestInspectScanUserByHashSlot(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	user := User{UID: "u7", Token: "tk7", DeviceFlag: 1, DeviceLevel: 2}
	if err := store.db.HashSlot(7).CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:       "user",
		HashSlot:    7,
		HashSlotSet: true,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1: %+v", len(got.Rows), got.Rows)
	}
	if got.Rows[0]["uid"] != "u7" || got.Rows[0]["token"] != "tk7" {
		t.Fatalf("row = %+v, want uid/token", got.Rows[0])
	}
	if got.ScannedRows != 1 {
		t.Fatalf("ScannedRows = %d, want 1", got.ScannedRows)
	}
	if !got.Done {
		t.Fatalf("Done = false, want true")
	}
}

func TestInspectScanUserLocalBoundedLimitAcrossHashSlots(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	for _, slot := range []HashSlot{1, 2, 3} {
		user := User{UID: "u" + string(rune('0'+slot)), Token: "tk"}
		if err := store.db.HashSlot(slot).CreateUser(ctx, user); err != nil {
			t.Fatalf("CreateUser(slot %d): %v", slot, err)
		}
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:         "user",
		HashSlotCount: 4,
		Limit:         2,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("rows len = %d, want 2: %+v", len(got.Rows), got.Rows)
	}
	if got.Done {
		t.Fatalf("Done = true, want false")
	}
	if got.Next == nil {
		t.Fatalf("Next = nil, want cursor")
	}
}

func TestInspectScanRejectsUnknownTable(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	_, err := InspectScan(context.Background(), store.db, InspectScanRequest{
		Table:       "unknown",
		HashSlot:    1,
		HashSlotSet: true,
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("InspectScan() err = %v, want invalid argument", err)
	}
}
