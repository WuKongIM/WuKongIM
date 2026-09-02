package meta

import (
	"context"
	"testing"
)

func TestDeviceUpsertAndGetAreHashSlotScoped(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	left := store.db.HashSlot(1)
	right := store.db.HashSlot(2)

	device := Device{UID: "u1", DeviceFlag: 1, Token: "left", DeviceLevel: 3}
	if err := left.UpsertDevice(context.Background(), device); err != nil {
		t.Fatalf("UpsertDevice(): %v", err)
	}
	got, ok, err := left.GetDevice(context.Background(), "u1", 1)
	if err != nil || !ok || got != device {
		t.Fatalf("left GetDevice() = (%+v, %v, %v), want %+v", got, ok, err, device)
	}
	if _, ok, err := right.GetDevice(context.Background(), "u1", 1); err != nil || ok {
		t.Fatalf("right GetDevice() = ok %v err %v, want missing", ok, err)
	}

	updated := Device{UID: "u1", DeviceFlag: 1, Token: "new", DeviceLevel: 4}
	if err := left.UpsertDevice(context.Background(), updated); err != nil {
		t.Fatalf("UpsertDevice(update): %v", err)
	}
	got, ok, err = left.GetDevice(context.Background(), "u1", 1)
	if err != nil || !ok || got != updated {
		t.Fatalf("updated GetDevice() = (%+v, %v, %v), want %+v", got, ok, err, updated)
	}
}

func TestDeviceTableRuntimeDescriptor(t *testing.T) {
	if deviceTable.Schema().ID != TableIDDevice {
		t.Fatalf("device table id = %d, want %d", deviceTable.Schema().ID, TableIDDevice)
	}
	if got := deviceTable.Schema().Primary.Columns; len(got) != 2 {
		t.Fatalf("device primary columns = %#v, want uid and device flag", got)
	}
}
