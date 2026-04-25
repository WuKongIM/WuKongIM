package meta

import (
	"context"
	"errors"
	"testing"
)

func TestDeviceUpsertAndGetAreSlotScoped(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	left := db.ForSlot(1)
	right := db.ForSlot(2)

	device := Device{
		UID:         "u1",
		DeviceFlag:  1,
		Token:       "left-token",
		DeviceLevel: 1,
	}
	if err := left.UpsertDevice(ctx, device); err != nil {
		t.Fatalf("upsert device: %v", err)
	}

	got, err := left.GetDevice(ctx, device.UID, device.DeviceFlag)
	if err != nil {
		t.Fatalf("left get device: %v", err)
	}
	if got != device {
		t.Fatalf("left device = %#v, want %#v", got, device)
	}

	_, err = right.GetDevice(ctx, device.UID, device.DeviceFlag)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound in other slot, got %v", err)
	}
}

func TestDeviceCoexistsForSameUIDAcrossDifferentDeviceFlags(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(1)

	if err := shard.UpsertDevice(ctx, Device{UID: "u1", DeviceFlag: 1, Token: "app", DeviceLevel: 1}); err != nil {
		t.Fatalf("upsert app device: %v", err)
	}
	if err := shard.UpsertDevice(ctx, Device{UID: "u1", DeviceFlag: 2, Token: "web", DeviceLevel: 0}); err != nil {
		t.Fatalf("upsert web device: %v", err)
	}

	appDevice, err := shard.GetDevice(ctx, "u1", 1)
	if err != nil {
		t.Fatalf("get app device: %v", err)
	}
	webDevice, err := shard.GetDevice(ctx, "u1", 2)
	if err != nil {
		t.Fatalf("get web device: %v", err)
	}

	if appDevice.Token != "app" {
		t.Fatalf("app token = %q", appDevice.Token)
	}
	if webDevice.Token != "web" {
		t.Fatalf("web token = %q", webDevice.Token)
	}
}

func TestDeviceUpsertUpdatesOnlyTargetedDeviceFlag(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(1)

	if err := shard.UpsertDevice(ctx, Device{UID: "u1", DeviceFlag: 1, Token: "app-v1", DeviceLevel: 1}); err != nil {
		t.Fatalf("upsert app-v1: %v", err)
	}
	if err := shard.UpsertDevice(ctx, Device{UID: "u1", DeviceFlag: 2, Token: "web-v1", DeviceLevel: 0}); err != nil {
		t.Fatalf("upsert web-v1: %v", err)
	}
	if err := shard.UpsertDevice(ctx, Device{UID: "u1", DeviceFlag: 1, Token: "app-v2", DeviceLevel: 3}); err != nil {
		t.Fatalf("upsert app-v2: %v", err)
	}

	appDevice, err := shard.GetDevice(ctx, "u1", 1)
	if err != nil {
		t.Fatalf("get app device: %v", err)
	}
	webDevice, err := shard.GetDevice(ctx, "u1", 2)
	if err != nil {
		t.Fatalf("get web device: %v", err)
	}

	if appDevice.Token != "app-v2" || appDevice.DeviceLevel != 3 {
		t.Fatalf("updated app device = %#v", appDevice)
	}
	if webDevice.Token != "web-v1" || webDevice.DeviceLevel != 0 {
		t.Fatalf("untouched web device = %#v", webDevice)
	}
}

func TestGetDeviceHonorsCanceledContext(t *testing.T) {
	db := openTestDB(t)
	shard := db.ForSlot(1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := shard.GetDevice(ctx, "u1", 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
