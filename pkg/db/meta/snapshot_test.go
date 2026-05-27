package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestSnapshotReplaceSpansUseRegistryPolicies(t *testing.T) {
	spans := hashSlotSnapshotReplaceSpans(9, true)
	for _, span := range spans {
		if bytesInSpan(encodeHashSlotMigrationStateKey(9), span) {
			t.Fatalf("preserving replace spans include hash-slot migration key span: %#v", span)
		}
	}

	foundUser := false
	userKey := encodeUserRowKey(9, "u1", userPrimaryFamilyID)
	for _, span := range spans {
		if bytesInSpan(userKey, span) {
			foundUser = true
			break
		}
	}
	if !foundUser {
		t.Fatalf("preserving replace spans did not include user row span")
	}
}

func TestSnapshotHashSlotRoundTripAndDeleteHashSlotData(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	left := store.db.HashSlot(1)
	right := store.db.HashSlot(2)

	if err := left.CreateUser(ctx, User{UID: "u1", Token: "left"}); err != nil {
		t.Fatalf("left CreateUser(): %v", err)
	}
	if err := left.CreateChannel(ctx, Channel{ChannelID: "c1", ChannelType: 1, Ban: 1}); err != nil {
		t.Fatalf("left CreateChannel(): %v", err)
	}
	if err := left.BindPluginUser(ctx, PluginUserBinding{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 1, UpdatedAtMS: 2}); err != nil {
		t.Fatalf("BindPluginUser(): %v", err)
	}
	if err := right.CreateUser(ctx, User{UID: "u1", Token: "right"}); err != nil {
		t.Fatalf("right CreateUser(): %v", err)
	}
	if _, ok, err := left.GetChannel(ctx, "c1", 1); err != nil || !ok {
		t.Fatalf("warm channel cache ok=%v err=%v", ok, err)
	}
	if store.db.channelCacheSize() != 1 {
		t.Fatalf("channel cache size = %d, want 1", store.db.channelCacheSize())
	}

	snap, err := store.db.ExportHashSlotSnapshot(ctx, []uint16{1})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}
	if len(snap.HashSlots) != 1 || snap.HashSlots[0] != 1 || snap.Stats.EntryCount == 0 || len(snap.Data) == 0 {
		t.Fatalf("snapshot = %+v", snap)
	}

	if err := store.db.DeleteHashSlotData(ctx, 1); err != nil {
		t.Fatalf("DeleteHashSlotData(): %v", err)
	}
	if store.db.channelCacheSize() != 0 {
		t.Fatalf("channel cache size after delete = %d, want 0", store.db.channelCacheSize())
	}
	if _, ok, err := left.GetUser(ctx, "u1"); err != nil || ok {
		t.Fatalf("left GetUser(after delete) ok=%v err=%v, want missing", ok, err)
	}
	if user, ok, err := right.GetUser(ctx, "u1"); err != nil || !ok || user.Token != "right" {
		t.Fatalf("right user after delete = %+v ok=%v err=%v, want right", user, ok, err)
	}

	if err := store.db.ImportHashSlotSnapshot(ctx, snap); err != nil {
		t.Fatalf("ImportHashSlotSnapshot(): %v", err)
	}
	if user, ok, err := left.GetUser(ctx, "u1"); err != nil || !ok || user.Token != "left" {
		t.Fatalf("restored user = %+v ok=%v err=%v, want left", user, ok, err)
	}
	if channel, ok, err := left.GetChannel(ctx, "c1", 1); err != nil || !ok || channel.Ban != 1 {
		t.Fatalf("restored channel = %+v ok=%v err=%v, want ban 1", channel, ok, err)
	}
	bindings, err := left.ListPluginBindingsByUID(ctx, "u1")
	if err != nil || len(bindings) != 1 || bindings[0].PluginNo != "bot-a" {
		t.Fatalf("restored plugin bindings = %+v err=%v", bindings, err)
	}
}

func TestSnapshotMultiHashSlotAndMigrationPreserve(t *testing.T) {
	source := openTestMetaStore(t)
	defer source.close(t)
	ctx := context.Background()

	if err := source.db.HashSlot(3).CreateUser(ctx, User{UID: "u-left", Token: "left"}); err != nil {
		t.Fatalf("left CreateUser(): %v", err)
	}
	if err := source.db.HashSlot(11).CreateUser(ctx, User{UID: "u-right", Token: "right"}); err != nil {
		t.Fatalf("right CreateUser(): %v", err)
	}
	incomingMigration := HashSlotMigrationState{HashSlot: 3, SourceSlot: 1, TargetSlot: 2}
	if err := source.db.HashSlot(3).UpsertHashSlotMigrationState(ctx, incomingMigration); err != nil {
		t.Fatalf("source UpsertHashSlotMigrationState(): %v", err)
	}
	incomingApplied := AppliedHashSlotDelta{HashSlot: 3, SourceSlot: 1, SourceIndex: 100}
	if err := source.db.HashSlot(3).MarkAppliedHashSlotDelta(ctx, incomingApplied); err != nil {
		t.Fatalf("source MarkAppliedHashSlotDelta(): %v", err)
	}
	incomingOutboxOverlap := HashSlotMigrationOutboxRow{HashSlot: 3, SourceSlot: 1, TargetSlot: 2, SourceIndex: 100, Data: []byte("incoming-overlap")}
	incomingOutboxNew := HashSlotMigrationOutboxRow{HashSlot: 3, SourceSlot: 1, TargetSlot: 2, SourceIndex: 101, Data: []byte("incoming-new")}
	for _, row := range []HashSlotMigrationOutboxRow{incomingOutboxOverlap, incomingOutboxNew} {
		if err := source.db.HashSlot(3).UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
			t.Fatalf("source UpsertHashSlotMigrationOutbox(%+v): %v", row, err)
		}
	}
	snap, err := source.db.ExportHashSlotSnapshot(ctx, []uint16{3, 11})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}

	target := openTestMetaStore(t)
	defer target.close(t)
	localMigration := HashSlotMigrationState{HashSlot: 3, SourceSlot: 10, TargetSlot: 20}
	if err := target.db.HashSlot(3).UpsertHashSlotMigrationState(ctx, localMigration); err != nil {
		t.Fatalf("target UpsertHashSlotMigrationState(): %v", err)
	}
	localApplied := AppliedHashSlotDelta{HashSlot: 3, SourceSlot: 10, SourceIndex: 200}
	if err := target.db.HashSlot(3).MarkAppliedHashSlotDelta(ctx, localApplied); err != nil {
		t.Fatalf("target MarkAppliedHashSlotDelta(): %v", err)
	}
	localOutboxOverlap := incomingOutboxOverlap
	localOutboxOverlap.Data = []byte("local-overlap")
	localOutboxDistinct := HashSlotMigrationOutboxRow{HashSlot: 3, SourceSlot: 10, TargetSlot: 20, SourceIndex: 200, Data: []byte("local-distinct")}
	for _, row := range []HashSlotMigrationOutboxRow{localOutboxOverlap, localOutboxDistinct} {
		if err := target.db.HashSlot(3).UpsertHashSlotMigrationOutbox(ctx, row); err != nil {
			t.Fatalf("target UpsertHashSlotMigrationOutbox(%+v): %v", row, err)
		}
	}
	if err := target.db.ImportHashSlotSnapshotPreservingMigrationMeta(ctx, snap); err != nil {
		t.Fatalf("ImportHashSlotSnapshotPreservingMigrationMeta(): %v", err)
	}
	if user, ok, err := target.db.HashSlot(3).GetUser(ctx, "u-left"); err != nil || !ok || user.Token != "left" {
		t.Fatalf("restored left user = %+v ok=%v err=%v, want left", user, ok, err)
	}
	if user, ok, err := target.db.HashSlot(11).GetUser(ctx, "u-right"); err != nil || !ok || user.Token != "right" {
		t.Fatalf("restored right user = %+v ok=%v err=%v, want right", user, ok, err)
	}
	gotMigration, ok, err := target.db.HashSlot(3).LoadHashSlotMigrationState(ctx)
	if err != nil || !ok || gotMigration != localMigration {
		t.Fatalf("migration state = %+v ok=%v err=%v, want local %+v", gotMigration, ok, err, localMigration)
	}
	for _, delta := range []AppliedHashSlotDelta{localApplied, incomingApplied} {
		exists, err := target.db.HashSlot(3).HasAppliedHashSlotDelta(ctx, delta)
		if err != nil || !exists {
			t.Fatalf("applied delta %+v exists=%v err=%v, want true", delta, exists, err)
		}
	}
	gotOutbox, ok, err := target.db.HashSlot(3).LoadHashSlotMigrationOutbox(ctx, localOutboxOverlap.SourceSlot, localOutboxOverlap.TargetSlot, localOutboxOverlap.SourceIndex)
	if err != nil || !ok || !equalHashSlotMigrationOutboxRow(gotOutbox, localOutboxOverlap) {
		t.Fatalf("overlap outbox = %+v ok=%v err=%v, want local %+v", gotOutbox, ok, err, localOutboxOverlap)
	}
	for _, want := range []HashSlotMigrationOutboxRow{localOutboxDistinct, incomingOutboxNew} {
		got, ok, err := target.db.HashSlot(3).LoadHashSlotMigrationOutbox(ctx, want.SourceSlot, want.TargetSlot, want.SourceIndex)
		if err != nil || !ok || !equalHashSlotMigrationOutboxRow(got, want) {
			t.Fatalf("outbox row = %+v ok=%v err=%v, want %+v", got, ok, err, want)
		}
	}
}

func TestSnapshotRejectsCorruptAndMismatchedPayload(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	if err := store.db.HashSlot(9).CreateUser(ctx, User{UID: "u1", Token: "t1"}); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	snap, err := store.db.ExportHashSlotSnapshot(ctx, []uint16{9})
	if err != nil {
		t.Fatalf("ExportHashSlotSnapshot(): %v", err)
	}

	mismatched := snap
	mismatched.HashSlots = []uint16{10}
	if err := store.db.ImportHashSlotSnapshot(ctx, mismatched); !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("ImportHashSlotSnapshot(mismatch) err = %v, want invalid argument", err)
	}

	corrupt := snap
	corrupt.Data = append([]byte(nil), snap.Data...)
	corrupt.Data[len(corrupt.Data)-1] ^= 0xff
	if err := store.db.ImportHashSlotSnapshot(ctx, corrupt); !errors.Is(err, dberrors.ErrChecksumMismatch) {
		t.Fatalf("ImportHashSlotSnapshot(corrupt) err = %v, want checksum mismatch", err)
	}
}
