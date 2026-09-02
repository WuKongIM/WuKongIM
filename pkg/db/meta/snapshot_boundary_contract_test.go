package meta

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestRestoreSnapshotWriterRejectsRowsOutsideAuthenticatedSemanticScope(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	writer, err := store.db.NewRestoreSnapshotWriter(ctx, []uint16{5}, true)
	if err != nil {
		t.Fatalf("NewRestoreSnapshotWriter(): %v", err)
	}

	userKey := encodeUserRowKey(5, "restored", userPrimaryFamilyID)
	userValue := encodeUserValue(User{UID: "restored", Token: "secret", DeviceFlag: 1, DeviceLevel: 2})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := writer.Put(canceled, userKey, userValue); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put(canceled) error = %v, want context canceled", err)
	}
	if err := writer.Put(ctx, encodeUserRowKey(6, "foreign", userPrimaryFamilyID), userValue); !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("Put(foreign hash slot) error = %v, want invalid argument", err)
	}
	runtimeKey := encodeChannelRuntimeMetaRowKey(5, "room", 2, channelRuntimeMetaPrimaryFamilyID)
	if err := writer.Put(ctx, runtimeKey, []byte("target-local runtime")); !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("Put(runtime metadata) error = %v, want semantic-scope rejection", err)
	}
	if err := writer.Put(ctx, userKey, []byte{0xff}); !errors.Is(err, dberrors.ErrCorruptValue) {
		t.Fatalf("Put(corrupt authentication row) error = %v, want corrupt value", err)
	}
	if err := writer.Put(ctx, userKey, userValue); err != nil {
		t.Fatalf("Put(valid user): %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if err := writer.Put(ctx, userKey, userValue); !errors.Is(err, dberrors.ErrClosed) {
		t.Fatalf("Put(after close) error = %v, want closed", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(idempotent): %v", err)
	}
	restored, ok, err := store.db.HashSlot(5).GetUser(ctx, "restored")
	if err != nil || !ok || restored.Token != "" || restored.DeviceFlag != 1 || restored.DeviceLevel != 2 {
		t.Fatalf("restored user = (%+v, %v, %v), want token invalidated", restored, ok, err)
	}
}

func TestBackupSnapshotHeaderFailsClosedForMalformedUntrustedStreams(t *testing.T) {
	if _, _, err := InspectBackupHashSlotSnapshotHeader(nil); !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("nil reader error = %v, want invalid argument", err)
	}
	cases := []struct {
		name string
		body []byte
	}{
		{name: "truncated magic", body: []byte("WK")},
		{name: "wrong magic", body: append([]byte("NOPE"), 0, 1, 0, 1)},
		{name: "wrong version", body: snapshotHeaderForTest(2, []uint16{5}, 1)},
		{name: "zero hash slots", body: snapshotHeaderForTest(slotSnapshotVersion, nil, 1)},
		{name: "truncated hash slots", body: append([]byte{'W', 'K', 'D', 'B', 0, 1, 0, 2}, 0, 5)},
		{name: "impossible entry count", body: snapshotHeaderForTest(slotSnapshotVersion, []uint16{5}, math.MaxUint64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := InspectBackupHashSlotSnapshotHeader(io.NopCloser(bytes.NewReader(tc.body)))
			if !errors.Is(err, dberrors.ErrCorruptValue) {
				t.Fatalf("error = %v, want corrupt value", err)
			}
		})
	}
}

func TestReplayBackupSnapshotRejectsMissingVisitorBeforeReading(t *testing.T) {
	_, _, err := ReplayBackupHashSlotSnapshot(context.Background(), nil, 0, nil)
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("ReplayBackupHashSlotSnapshot(nil visitor) error = %v, want invalid argument", err)
	}
}

func snapshotHeaderForTest(version uint16, hashSlots []uint16, entryCount uint64) []byte {
	body := append([]byte(nil), slotSnapshotMagic[:]...)
	body = binary.BigEndian.AppendUint16(body, version)
	body = binary.BigEndian.AppendUint16(body, uint16(len(hashSlots)))
	for _, hashSlot := range hashSlots {
		body = binary.BigEndian.AppendUint16(body, hashSlot)
	}
	return binary.BigEndian.AppendUint64(body, entryCount)
}
