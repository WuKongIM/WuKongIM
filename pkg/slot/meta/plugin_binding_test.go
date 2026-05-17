package meta

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestPluginBindingPrimaryIndexAndIdempotency(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForHashSlot(7)

	first := PluginUserBinding{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 100, UpdatedAtMS: 100}
	if err := shard.BindPluginUser(ctx, first); err != nil {
		t.Fatalf("BindPluginUser(first): %v", err)
	}
	if err := shard.BindPluginUser(ctx, PluginUserBinding{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 200, UpdatedAtMS: 250}); err != nil {
		t.Fatalf("BindPluginUser(second): %v", err)
	}

	bindings, err := shard.ListPluginBindingsByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListPluginBindingsByUID(): %v", err)
	}
	want := []PluginUserBinding{{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 100, UpdatedAtMS: 250}}
	if !reflect.DeepEqual(bindings, want) {
		t.Fatalf("bindings = %#v, want %#v", bindings, want)
	}

	exists, err := shard.ExistPluginBindingByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ExistPluginBindingByUID(): %v", err)
	}
	if !exists {
		t.Fatal("ExistPluginBindingByUID() = false, want true")
	}
}

func TestPluginBindingListByUIDAndScanByPluginNo(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForHashSlot(3)
	seed := []PluginUserBinding{
		{UID: "u1", PluginNo: "bot-b", CreatedAtMS: 100, UpdatedAtMS: 101},
		{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 110, UpdatedAtMS: 111},
		{UID: "u2", PluginNo: "bot-a", CreatedAtMS: 120, UpdatedAtMS: 121},
		{UID: "u0", PluginNo: "bot-a", CreatedAtMS: 130, UpdatedAtMS: 131},
	}
	for _, binding := range seed {
		if err := shard.BindPluginUser(ctx, binding); err != nil {
			t.Fatalf("BindPluginUser(%#v): %v", binding, err)
		}
	}

	byUID, err := shard.ListPluginBindingsByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListPluginBindingsByUID(): %v", err)
	}
	wantUID := []PluginUserBinding{
		{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 110, UpdatedAtMS: 111},
		{UID: "u1", PluginNo: "bot-b", CreatedAtMS: 100, UpdatedAtMS: 101},
	}
	if !reflect.DeepEqual(byUID, wantUID) {
		t.Fatalf("byUID = %#v, want %#v", byUID, wantUID)
	}

	page, cursor, hasMore, err := shard.ScanPluginBindingsByPluginNo(ctx, "bot-a", PluginUserBindingCursor{}, 2)
	if err != nil {
		t.Fatalf("ScanPluginBindingsByPluginNo(page1): %v", err)
	}
	wantPage1 := []PluginUserBinding{
		{UID: "u0", PluginNo: "bot-a", CreatedAtMS: 130, UpdatedAtMS: 131},
		{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 110, UpdatedAtMS: 111},
	}
	if !reflect.DeepEqual(page, wantPage1) || !hasMore || cursor.UID != "u1" || cursor.PluginNo != "bot-a" {
		t.Fatalf("page1=(%#v,%#v,%v), want (%#v,{bot-a u1},true)", page, cursor, hasMore, wantPage1)
	}

	page, cursor, hasMore, err = shard.ScanPluginBindingsByPluginNo(ctx, "bot-a", cursor, 2)
	if err != nil {
		t.Fatalf("ScanPluginBindingsByPluginNo(page2): %v", err)
	}
	wantPage2 := []PluginUserBinding{{UID: "u2", PluginNo: "bot-a", CreatedAtMS: 120, UpdatedAtMS: 121}}
	if !reflect.DeepEqual(page, wantPage2) || hasMore || cursor.UID != "u2" || cursor.PluginNo != "bot-a" {
		t.Fatalf("page2=(%#v,%#v,%v), want (%#v,{bot-a u2},false)", page, cursor, hasMore, wantPage2)
	}
}

func TestPluginBindingUnbindIsIdempotentAndRemovesIndexes(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForHashSlot(5)
	binding := PluginUserBinding{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 100, UpdatedAtMS: 100}
	if err := shard.BindPluginUser(ctx, binding); err != nil {
		t.Fatalf("BindPluginUser(): %v", err)
	}

	if err := shard.UnbindPluginUser(ctx, "u1", "bot-a"); err != nil {
		t.Fatalf("UnbindPluginUser(existing): %v", err)
	}
	if err := shard.UnbindPluginUser(ctx, "u1", "bot-a"); err != nil {
		t.Fatalf("UnbindPluginUser(missing): %v", err)
	}

	bindings, err := shard.ListPluginBindingsByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListPluginBindingsByUID(): %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("bindings after unbind = %#v, want empty", bindings)
	}
	page, _, _, err := shard.ScanPluginBindingsByPluginNo(ctx, "bot-a", PluginUserBindingCursor{}, 10)
	if err != nil {
		t.Fatalf("ScanPluginBindingsByPluginNo(): %v", err)
	}
	if len(page) != 0 {
		t.Fatalf("plugin index after unbind = %#v, want empty", page)
	}
	exists, err := shard.ExistPluginBindingByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ExistPluginBindingByUID(): %v", err)
	}
	if exists {
		t.Fatal("ExistPluginBindingByUID() = true, want false")
	}
}

func TestPluginBindingWriteBatchStagesOperations(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	batch := db.NewWriteBatch()
	defer batch.Close()
	if err := batch.BindPluginUser(9, PluginUserBinding{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatalf("batch BindPluginUser(): %v", err)
	}
	if err := batch.BindPluginUser(9, PluginUserBinding{UID: "u1", PluginNo: "bot-b", CreatedAtMS: 2, UpdatedAtMS: 2}); err != nil {
		t.Fatalf("batch BindPluginUser(bot-b): %v", err)
	}
	if err := batch.UnbindPluginUser(9, "u1", "bot-b"); err != nil {
		t.Fatalf("batch UnbindPluginUser(): %v", err)
	}
	if err := batch.Commit(); err != nil {
		t.Fatalf("batch Commit(): %v", err)
	}

	bindings, err := db.ForHashSlot(9).ListPluginBindingsByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListPluginBindingsByUID(): %v", err)
	}
	want := []PluginUserBinding{{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 1, UpdatedAtMS: 1}}
	if !reflect.DeepEqual(bindings, want) {
		t.Fatalf("bindings = %#v, want %#v", bindings, want)
	}
}

func TestPluginBindingRejectsInvalidArguments(t *testing.T) {
	ctx := context.Background()
	shard := openTestDB(t).ForHashSlot(1)
	if err := shard.BindPluginUser(ctx, PluginUserBinding{UID: "", PluginNo: "bot"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("BindPluginUser(empty uid) err = %v, want ErrInvalidArgument", err)
	}
	if err := shard.BindPluginUser(ctx, PluginUserBinding{UID: "u", PluginNo: ""}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("BindPluginUser(empty plugin) err = %v, want ErrInvalidArgument", err)
	}
	if _, _, _, err := shard.ScanPluginBindingsByPluginNo(ctx, "bot", PluginUserBindingCursor{PluginNo: "other", UID: "u"}, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ScanPluginBindingsByPluginNo(cursor mismatch) err = %v, want ErrInvalidArgument", err)
	}
}
