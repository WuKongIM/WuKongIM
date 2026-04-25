package meta

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestDeleteHashSlotDataRemovesOnlyTargetHashSlot(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	left := db.ForHashSlot(1)
	right := db.ForHashSlot(2)

	if err := left.CreateUser(ctx, User{UID: "u1", Token: "left"}); err != nil {
		t.Fatalf("left create user: %v", err)
	}
	if err := right.CreateUser(ctx, User{UID: "u1", Token: "right"}); err != nil {
		t.Fatalf("right create user: %v", err)
	}
	if err := left.CreateChannel(ctx, Channel{ChannelID: "c1", ChannelType: 1, Ban: 1}); err != nil {
		t.Fatalf("left create channel: %v", err)
	}
	if err := right.CreateChannel(ctx, Channel{ChannelID: "c1", ChannelType: 1, Ban: 2}); err != nil {
		t.Fatalf("right create channel: %v", err)
	}

	if err := db.DeleteHashSlotData(ctx, 1); err != nil {
		t.Fatalf("DeleteHashSlotData(1): %v", err)
	}

	if _, err := left.GetUser(ctx, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("left GetUser() err = %v, want ErrNotFound", err)
	}
	if _, err := left.GetChannel(ctx, "c1", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("left GetChannel() err = %v, want ErrNotFound", err)
	}

	user, err := right.GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("right GetUser(): %v", err)
	}
	if user.Token != "right" {
		t.Fatalf("right user token = %q", user.Token)
	}

	channel, err := right.GetChannel(ctx, "c1", 1)
	if err != nil {
		t.Fatalf("right GetChannel(): %v", err)
	}
	if channel.Ban != 2 {
		t.Fatalf("right channel ban = %d", channel.Ban)
	}
}

func TestHashSlotSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForHashSlot(9)

	originalUser := User{UID: "u1", Token: "t1", DeviceFlag: 1, DeviceLevel: 2}
	originalChannel := Channel{ChannelID: "c1", ChannelType: 1, Ban: 1}
	if err := shard.CreateUser(ctx, originalUser); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	if err := shard.CreateChannel(ctx, originalChannel); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}

	snap, err := db.ExportHashSlotSnapshot(ctx, []uint16{9})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}
	if len(snap.HashSlots) != 1 || snap.HashSlots[0] != 9 || len(snap.Data) == 0 {
		t.Fatalf("snapshot = %#v", snap)
	}

	if err := db.DeleteHashSlotData(ctx, 9); err != nil {
		t.Fatalf("DeleteHashSlotData(): %v", err)
	}
	if _, err := shard.GetUser(ctx, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUser() after delete err = %v, want ErrNotFound", err)
	}

	if err := db.ImportHashSlotSnapshot(ctx, snap); err != nil {
		t.Fatalf("ImportHashSlotSnapshot(): %v", err)
	}

	gotUser, err := shard.GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() after restore: %v", err)
	}
	if gotUser.Token != originalUser.Token || gotUser.DeviceLevel != originalUser.DeviceLevel {
		t.Fatalf("restored user = %#v", gotUser)
	}

	gotChannel, err := shard.GetChannel(ctx, "c1", 1)
	if err != nil {
		t.Fatalf("GetChannel() after restore: %v", err)
	}
	if gotChannel.Ban != originalChannel.Ban {
		t.Fatalf("restored channel = %#v", gotChannel)
	}
}

func TestHashSlotSnapshotRoundTripAcrossMultipleHashSlots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	left := db.ForHashSlot(3)
	right := db.ForHashSlot(11)

	if err := left.CreateUser(ctx, User{UID: "u-left", Token: "left"}); err != nil {
		t.Fatalf("left CreateUser(): %v", err)
	}
	if err := right.CreateUser(ctx, User{UID: "u-right", Token: "right"}); err != nil {
		t.Fatalf("right CreateUser(): %v", err)
	}

	snap, err := db.ExportHashSlotSnapshot(ctx, []uint16{3, 11})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}
	if !reflect.DeepEqual(snap.HashSlots, []uint16{3, 11}) {
		t.Fatalf("snapshot HashSlots = %#v, want %#v", snap.HashSlots, []uint16{3, 11})
	}

	if err := db.DeleteHashSlotData(ctx, 3); err != nil {
		t.Fatalf("DeleteHashSlotData(3): %v", err)
	}
	if err := db.DeleteHashSlotData(ctx, 11); err != nil {
		t.Fatalf("DeleteHashSlotData(11): %v", err)
	}

	if err := db.ImportHashSlotSnapshot(ctx, snap); err != nil {
		t.Fatalf("ImportHashSlotSnapshot(): %v", err)
	}

	gotLeft, err := left.GetUser(ctx, "u-left")
	if err != nil {
		t.Fatalf("left GetUser(): %v", err)
	}
	if gotLeft.Token != "left" {
		t.Fatalf("left restored user = %#v", gotLeft)
	}

	gotRight, err := right.GetUser(ctx, "u-right")
	if err != nil {
		t.Fatalf("right GetUser(): %v", err)
	}
	if gotRight.Token != "right" {
		t.Fatalf("right restored user = %#v", gotRight)
	}
}

func TestImportHashSlotSnapshotRejectsWrongHashSlots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := db.ForHashSlot(9).CreateUser(ctx, User{UID: "u1", Token: "t1"}); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	snap, err := db.ExportHashSlotSnapshot(ctx, []uint16{9})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}
	snap.HashSlots = []uint16{10}

	if err := db.ImportHashSlotSnapshot(ctx, snap); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ImportHashSlotSnapshot() err = %v, want ErrInvalidArgument", err)
	}
}

func TestImportHashSlotSnapshotRejectsChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := db.ForHashSlot(9).CreateUser(ctx, User{UID: "u1", Token: "t1"}); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	snap, err := db.ExportHashSlotSnapshot(ctx, []uint16{9})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}
	snap.Data[len(snap.Data)-1] ^= 0xff

	if err := db.ImportHashSlotSnapshot(ctx, snap); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("ImportHashSlotSnapshot() err = %v, want ErrChecksumMismatch", err)
	}
}

func TestDeleteSlotDataRejectsOutOfRangeLegacySlotID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := db.DeleteSlotData(ctx, uint64(math.MaxUint16)+1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("DeleteSlotData() err = %v, want ErrInvalidArgument", err)
	}
}

func TestExportSlotSnapshotRejectsOutOfRangeLegacySlotID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := db.ExportSlotSnapshot(ctx, uint64(math.MaxUint16)+1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ExportSlotSnapshot() err = %v, want ErrInvalidArgument", err)
	}
}

func TestImportSlotSnapshotRejectsOutOfRangeLegacySlotID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	err := db.ImportSlotSnapshot(ctx, SlotSnapshot{
		SlotID: uint64(math.MaxUint16) + 1,
		Data:   []byte("ignored"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ImportSlotSnapshot() err = %v, want ErrInvalidArgument", err)
	}
}

func TestImportHashSlotSnapshotCanBeRetried(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForHashSlot(9)

	if err := shard.CreateUser(ctx, User{UID: "u1", Token: "old"}); err != nil {
		t.Fatalf("CreateUser(old): %v", err)
	}

	restoreDB := openTestDB(t)
	restoreShard := restoreDB.ForHashSlot(9)
	if err := restoreShard.CreateUser(ctx, User{UID: "u1", Token: "stale"}); err != nil {
		t.Fatalf("CreateUser(stale): %v", err)
	}

	snap, err := db.ExportHashSlotSnapshot(ctx, []uint16{9})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}

	injectedErr := errors.New("injected import failure")
	restoreDB.testHooks.beforeImportCommit = func() error {
		restoreDB.testHooks.beforeImportCommit = nil
		return injectedErr
	}

	if err := restoreDB.ImportHashSlotSnapshot(ctx, snap); !errors.Is(err, injectedErr) {
		t.Fatalf("first ImportHashSlotSnapshot() err = %v, want %v", err, injectedErr)
	}
	if _, err := restoreShard.GetUser(ctx, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUser() after failed import err = %v, want ErrNotFound", err)
	}

	if err := restoreDB.ImportHashSlotSnapshot(ctx, snap); err != nil {
		t.Fatalf("second ImportHashSlotSnapshot(): %v", err)
	}

	gotUser, err := restoreShard.GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() after retry: %v", err)
	}
	if gotUser.Token != "old" {
		t.Fatalf("restored token = %q", gotUser.Token)
	}
}

func TestVisitSlotSnapshotPayloadMatchesDecodedEntries(t *testing.T) {
	entries := []snapshotEntry{
		{Key: []byte("k1"), Value: []byte("v1")},
		{Key: []byte("k2"), Value: []byte("value-2")},
	}
	data, wantStats := encodeSlotSnapshotPayload([]uint16{7}, entries)

	var got []snapshotEntry
	meta, err := visitSlotSnapshotPayload(data, func(key, value []byte) error {
		got = append(got, snapshotEntry{
			Key:   append([]byte(nil), key...),
			Value: append([]byte(nil), value...),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("visitSlotSnapshotPayload(): %v", err)
	}

	if !reflect.DeepEqual(meta.HashSlots, []uint16{7}) {
		t.Fatalf("meta.HashSlots = %#v, want %#v", meta.HashSlots, []uint16{7})
	}
	if meta.Stats != wantStats {
		t.Fatalf("meta.Stats = %#v, want %#v", meta.Stats, wantStats)
	}
	if !reflect.DeepEqual(got, entries) {
		t.Fatalf("visited entries = %#v, want %#v", got, entries)
	}
}

func TestVisitSlotSnapshotPayloadReturnsCallbackError(t *testing.T) {
	data, _ := encodeSlotSnapshotPayload([]uint16{7}, []snapshotEntry{
		{Key: []byte("k1"), Value: []byte("v1")},
	})

	injectedErr := errors.New("stop visiting")
	_, err := visitSlotSnapshotPayload(data, func(key, value []byte) error {
		return injectedErr
	})
	if !errors.Is(err, injectedErr) {
		t.Fatalf("visitSlotSnapshotPayload() err = %v, want %v", err, injectedErr)
	}
}

func TestHashSlotAllDataSpansAreOrderedAndDisjoint(t *testing.T) {
	spans := hashSlotAllDataSpans(7)
	if len(spans) != 3 {
		t.Fatalf("hashSlotAllDataSpans(7) len = %d", len(spans))
	}

	for i, span := range spans {
		if len(span.Start) == 0 || len(span.End) == 0 {
			t.Fatalf("span %d is empty: %#v", i, span)
		}
		if bytes.Compare(span.Start, span.End) >= 0 {
			t.Fatalf("span %d start >= end: %#v", i, span)
		}
		if i > 0 && bytes.Compare(spans[i-1].End, span.Start) > 0 {
			t.Fatalf("spans overlap: %#v then %#v", spans[i-1], span)
		}
	}

	assertKeyInSpan(t, encodeUserPrimaryKey(7, "u1", userPrimaryFamilyID), spans[0])
	assertKeyInSpan(t, encodeChannelPrimaryKey(7, "c1", 1, channelPrimaryFamilyID), spans[0])
	assertKeyInSpan(t, encodeChannelIDIndexKey(7, "c1", 1), spans[1])
	assertKeyInSpan(t, encodeMetaPrefix(7), spans[2])
}

func assertKeyInSpan(t *testing.T, key []byte, span Span) {
	t.Helper()

	if bytes.Compare(key, span.Start) < 0 || bytes.Compare(key, span.End) >= 0 {
		t.Fatalf("key %x not in span [%x, %x)", key, span.Start, span.End)
	}
}
