package meta

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestVerifyBackupSnapshotRejectsRuntimeOnlyRegisteredSpans(t *testing.T) {
	payload, _ := encodeSlotSnapshotPayload([]uint16{5}, []snapshotEntry{{
		Key:   encodeChannelRuntimeMetaRowKey(5, "room", 1, channelRuntimeMetaPrimaryFamilyID),
		Value: []byte("runtime ownership must remain local"),
	}})

	_, err := VerifyBackupHashSlotSnapshotReader(
		context.Background(), []uint16{5}, bytes.NewReader(payload), int64(len(payload)),
	)
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("VerifyBackupHashSlotSnapshotReader() error = %v, want semantic-span rejection", err)
	}
}

func TestVerifyBackupSnapshotRejectsNonCanonicalRegisteredSpanOrder(t *testing.T) {
	payload, _ := encodeSlotSnapshotPayload([]uint16{5}, []snapshotEntry{
		{
			Key:   encodeChannelRowKey(5, "room", 1, channelPrimaryFamilyID),
			Value: []byte("channel"),
		},
		{
			Key:   encodeUserRowKey(5, "user", userPrimaryFamilyID),
			Value: []byte("user"),
		},
	})

	_, err := VerifyBackupHashSlotSnapshotReader(
		context.Background(), []uint16{5}, bytes.NewReader(payload), int64(len(payload)),
	)
	if !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("VerifyBackupHashSlotSnapshotReader() error = %v, want non-canonical order rejection", err)
	}
}

func TestVerifyBackupSnapshotRejectsDuplicateKey(t *testing.T) {
	key := encodeUserRowKey(5, "user", userPrimaryFamilyID)
	payload, _ := encodeSlotSnapshotPayload([]uint16{5}, []snapshotEntry{
		{Key: key, Value: encodeUserValue(User{UID: "user", Token: "first"})},
		{Key: key, Value: encodeUserValue(User{UID: "user", Token: "second"})},
	})

	_, err := VerifyBackupHashSlotSnapshotReader(
		context.Background(), []uint16{5}, bytes.NewReader(payload), int64(len(payload)),
	)
	if !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("VerifyBackupHashSlotSnapshotReader() error = %v, want duplicate-key rejection", err)
	}
}

func TestBackupSnapshotPinsCutAndRestoresSemanticState(t *testing.T) {
	ctx := context.Background()
	source := openTestMetaStore(t)
	defer source.close(t)
	shard := source.db.HashSlot(5)
	if err := shard.CreateUser(ctx, User{UID: "user", Token: "before"}); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	reader, err := source.db.OpenBackupHashSlotSnapshot(ctx, []uint16{5})
	if err != nil {
		t.Fatalf("OpenBackupHashSlotSnapshot(): %v", err)
	}
	if err := shard.UpsertUser(ctx, User{UID: "user", Token: "after"}); err != nil {
		t.Fatalf("UpsertUser(): %v", err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(snapshot): %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close(snapshot): %v", err)
	}

	stats, err := VerifyBackupHashSlotSnapshotReader(
		ctx, []uint16{5}, bytes.NewReader(payload), int64(len(payload)),
	)
	if err != nil || stats.EntryCount == 0 {
		t.Fatalf("VerifyBackupHashSlotSnapshotReader() stats = %+v, error = %v", stats, err)
	}
	target := openTestMetaStore(t)
	defer target.close(t)
	if err := target.db.ImportHashSlotSnapshotReaderForRestore(
		ctx, []uint16{5}, bytes.NewReader(payload), int64(len(payload)), false,
	); err != nil {
		t.Fatalf("ImportHashSlotSnapshotReaderForRestore(): %v", err)
	}
	restored, ok, err := target.db.HashSlot(5).GetUser(ctx, "user")
	if err != nil || !ok || restored.Token != "before" {
		t.Fatalf("GetUser() = %+v, %v, %v, want pinned token before", restored, ok, err)
	}
}

func TestReplayBackupSnapshotIntoRestoreWriterPreservesCanonicalState(t *testing.T) {
	ctx := context.Background()
	source := openTestMetaStore(t)
	defer source.close(t)
	shard := source.db.HashSlot(5)
	if err := shard.CreateUser(ctx, User{UID: "user", Token: "user-token", DeviceFlag: 1, DeviceLevel: 2}); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	if err := shard.UpsertDevice(ctx, Device{UID: "user", DeviceFlag: 1, Token: "device-token", DeviceLevel: 3}); err != nil {
		t.Fatalf("UpsertDevice(): %v", err)
	}
	reader, err := source.db.OpenBackupHashSlotSnapshot(ctx, []uint16{5})
	if err != nil {
		t.Fatalf("OpenBackupHashSlotSnapshot(): %v", err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(snapshot): %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close(snapshot): %v", err)
	}

	target := openTestMetaStore(t)
	defer target.close(t)
	writer, err := target.db.NewRestoreSnapshotWriter(ctx, []uint16{5}, true)
	if err != nil {
		t.Fatalf("NewRestoreSnapshotWriter(): %v", err)
	}
	var visited uint64
	slots, stats, err := ReplayBackupHashSlotSnapshot(
		ctx, bytes.NewReader(payload), int64(len(payload)),
		func(entry BackupSnapshotEntry) error {
			visited++
			return writer.Put(ctx, entry.Key, entry.Value)
		},
	)
	if err != nil {
		_ = writer.Close()
		t.Fatalf("ReplayBackupHashSlotSnapshot(): %v", err)
	}
	if len(slots) != 1 || slots[0] != 5 || stats.EntryCount != visited || visited != 2 {
		_ = writer.Close()
		t.Fatalf("replay slots = %v, stats = %+v, visited = %d", slots, stats, visited)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("RestoreSnapshotWriter.Close(): %v", err)
	}

	restoredUser, ok, err := target.db.HashSlot(5).GetUser(ctx, "user")
	if err != nil || !ok || restoredUser.Token != "" || restoredUser.DeviceLevel != 2 {
		t.Fatalf("GetUser() = %+v, %v, %v, want invalidated token and preserved level", restoredUser, ok, err)
	}
	restoredDevice, ok, err := target.db.HashSlot(5).GetDevice(ctx, "user", 1)
	if err != nil || !ok || restoredDevice.Token != "" || restoredDevice.DeviceLevel != 3 {
		t.Fatalf("GetDevice() = %+v, %v, %v, want invalidated token and preserved level", restoredDevice, ok, err)
	}
}

func TestRestoreSnapshotValidatesDigestAndOrderBeforeMutation(t *testing.T) {
	valid, _ := encodeSlotSnapshotPayload([]uint16{5}, []snapshotEntry{{
		Key:   encodeUserRowKey(5, "incoming", userPrimaryFamilyID),
		Value: encodeUserValue(User{UID: "incoming", Token: "incoming"}),
	}})
	badDigest := append([]byte(nil), valid...)
	badDigest[len(badDigest)-1] ^= 0xff
	badOrder, _ := encodeSlotSnapshotPayload([]uint16{5}, []snapshotEntry{
		{
			Key:   encodeChannelRowKey(5, "room", 1, channelPrimaryFamilyID),
			Value: encodeChannelValue(Channel{ChannelID: "room", ChannelType: 1}),
		},
		{
			Key:   encodeUserRowKey(5, "incoming", userPrimaryFamilyID),
			Value: encodeUserValue(User{UID: "incoming", Token: "incoming"}),
		},
	})

	for _, testCase := range []struct {
		name    string
		payload []byte
		wantErr error
	}{
		{name: "digest", payload: badDigest, wantErr: dberrors.ErrChecksumMismatch},
		{name: "registered span order", payload: badOrder, wantErr: dberrors.ErrCorruptValue},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			target := openTestMetaStore(t)
			defer target.close(t)
			if err := target.db.HashSlot(5).CreateUser(ctx, User{UID: "survivor", Token: "unchanged"}); err != nil {
				t.Fatalf("CreateUser(): %v", err)
			}

			err := target.db.ImportHashSlotSnapshotReaderForRestore(
				ctx, []uint16{5}, bytes.NewReader(testCase.payload), int64(len(testCase.payload)), false,
			)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("ImportHashSlotSnapshotReaderForRestore() error = %v, want %v", err, testCase.wantErr)
			}
			survivor, ok, getErr := target.db.HashSlot(5).GetUser(ctx, "survivor")
			if getErr != nil || !ok || survivor.Token != "unchanged" {
				t.Fatalf("GetUser(survivor) = %+v, %v, %v, want unchanged durable row", survivor, ok, getErr)
			}
		})
	}
}
