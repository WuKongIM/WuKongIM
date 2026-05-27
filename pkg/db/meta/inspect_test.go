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

func TestInspectScanUserLocalFilteredScanContinuesPastFilteredRows(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	fixtures := []struct {
		slot HashSlot
		user User
	}{
		{slot: 1, user: User{UID: "u1a", Token: "skip"}},
		{slot: 1, user: User{UID: "u1b", Token: "skip"}},
		{slot: 2, user: User{UID: "u2", Token: "skip"}},
		{slot: 3, user: User{UID: "u3", Token: "match"}},
	}
	for _, fixture := range fixtures {
		if err := store.db.HashSlot(fixture.slot).CreateUser(ctx, fixture.user); err != nil {
			t.Fatalf("CreateUser(slot %d, uid %s): %v", fixture.slot, fixture.user.UID, err)
		}
	}

	got, err := InspectScan(ctx, store.db, InspectScanRequest{
		Table:         "user",
		HashSlotCount: 4,
		Filters:       map[string]any{"token": "match"},
		Limit:         1,
	})
	if err != nil {
		t.Fatalf("InspectScan(): %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows len = %d, want 1: %+v", len(got.Rows), got.Rows)
	}
	if got.Rows[0]["uid"] != "u3" || got.Rows[0]["token"] != "match" {
		t.Fatalf("row = %+v, want slot 3 match", got.Rows[0])
	}
	if !got.Done {
		t.Fatalf("Done = false, want true")
	}
	if got.Next != nil {
		t.Fatalf("Next = %+v, want nil", got.Next)
	}
	if got.ScannedRows != 4 {
		t.Fatalf("ScannedRows = %d, want 4", got.ScannedRows)
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

func TestInspectScanRejectsEmptyUserCursorPrimary(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	_, err := InspectScan(context.Background(), store.db, InspectScanRequest{
		Table:       "user",
		HashSlot:    7,
		HashSlotSet: true,
		After:       &InspectCursor{HashSlot: 7, Primary: []any{""}},
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("InspectScan() err = %v, want invalid argument", err)
	}
}

func TestInspectScanRejectsExplicitHashSlotCursorMismatch(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)

	_, err := InspectScan(context.Background(), store.db, InspectScanRequest{
		Table:       "user",
		HashSlot:    7,
		HashSlotSet: true,
		After:       &InspectCursor{HashSlot: 8},
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("InspectScan() err = %v, want invalid argument", err)
	}
}

func TestInspectScanRejectsNilDB(t *testing.T) {
	_, err := InspectScan(context.Background(), nil, InspectScanRequest{
		Table:       "user",
		HashSlot:    1,
		HashSlotSet: true,
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("InspectScan() err = %v, want closed", err)
	}
}

func TestInspectScanRejectsClosedDB(t *testing.T) {
	store := openTestMetaStore(t)
	store.close(t)

	_, err := InspectScan(context.Background(), store.db, InspectScanRequest{
		Table:       "user",
		HashSlot:    1,
		HashSlotSet: true,
		Limit:       10,
	})
	if !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("InspectScan() err = %v, want closed", err)
	}
}
