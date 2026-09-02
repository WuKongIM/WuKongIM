package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestPluginBindingListScanAndIdempotency(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(5)
	ctx := context.Background()

	seed := []PluginUserBinding{
		{UID: "u1", PluginNo: "bot-b", CreatedAtMS: 100, UpdatedAtMS: 101},
		{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 110, UpdatedAtMS: 111},
		{UID: "u2", PluginNo: "bot-a", CreatedAtMS: 120, UpdatedAtMS: 121},
		{UID: "u0", PluginNo: "bot-a", CreatedAtMS: 130, UpdatedAtMS: 131},
	}
	for _, binding := range seed {
		if err := shard.BindPluginUser(ctx, binding); err != nil {
			t.Fatalf("BindPluginUser(%+v): %v", binding, err)
		}
	}
	if err := shard.BindPluginUser(ctx, PluginUserBinding{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 200, UpdatedAtMS: 250}); err != nil {
		t.Fatalf("BindPluginUser(update): %v", err)
	}

	byUID, err := shard.ListPluginBindingsByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListPluginBindingsByUID(): %v", err)
	}
	wantUID := []PluginUserBinding{
		{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 110, UpdatedAtMS: 250},
		{UID: "u1", PluginNo: "bot-b", CreatedAtMS: 100, UpdatedAtMS: 101},
	}
	if !equalPluginBindings(byUID, wantUID) {
		t.Fatalf("bindings by uid = %+v, want %+v", byUID, wantUID)
	}

	page, cursor, done, err := shard.ScanPluginBindingsByPluginNo(ctx, "bot-a", PluginUserBindingCursor{}, 2)
	if err != nil {
		t.Fatalf("ScanPluginBindingsByPluginNo(page1): %v", err)
	}
	wantPage1 := []PluginUserBinding{
		{UID: "u0", PluginNo: "bot-a", CreatedAtMS: 130, UpdatedAtMS: 131},
		{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 110, UpdatedAtMS: 250},
	}
	if done || cursor != (PluginUserBindingCursor{PluginNo: "bot-a", UID: "u1"}) || !equalPluginBindings(page, wantPage1) {
		t.Fatalf("page1=%+v cursor=%+v done=%v, want first two bot-a and more", page, cursor, done)
	}

	page, cursor, done, err = shard.ScanPluginBindingsByPluginNo(ctx, "bot-a", cursor, 2)
	if err != nil {
		t.Fatalf("ScanPluginBindingsByPluginNo(page2): %v", err)
	}
	wantPage2 := []PluginUserBinding{{UID: "u2", PluginNo: "bot-a", CreatedAtMS: 120, UpdatedAtMS: 121}}
	if !done || cursor != (PluginUserBindingCursor{PluginNo: "bot-a", UID: "u2"}) || !equalPluginBindings(page, wantPage2) {
		t.Fatalf("page2=%+v cursor=%+v done=%v, want final u2", page, cursor, done)
	}

	exists, err := shard.ExistPluginBindingByUID(ctx, "u1")
	if err != nil || !exists {
		t.Fatalf("ExistPluginBindingByUID() = %v err %v, want true", exists, err)
	}
}

func TestPluginBindingUnbindAndValidation(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(5)
	ctx := context.Background()

	if err := shard.BindPluginUser(ctx, PluginUserBinding{UID: "u1", PluginNo: "bot-a", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
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
		t.Fatalf("bindings after unbind = %+v, want empty", bindings)
	}
	exists, err := shard.ExistPluginBindingByUID(ctx, "u1")
	if err != nil || exists {
		t.Fatalf("ExistPluginBindingByUID(after unbind) = %v err %v, want false", exists, err)
	}

	if err := shard.BindPluginUser(ctx, PluginUserBinding{UID: "", PluginNo: "bot-a"}); !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("BindPluginUser(empty uid) err = %v, want invalid argument", err)
	}
	if _, _, _, err := shard.ScanPluginBindingsByPluginNo(ctx, "bot-a", PluginUserBindingCursor{PluginNo: "other", UID: "u1"}, 10); !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("ScanPluginBindingsByPluginNo(cursor mismatch) err = %v, want invalid argument", err)
	}
}

func TestPluginBindingTableRuntimeDescriptor(t *testing.T) {
	if pluginBindingTable.Schema().ID != TableIDPluginBinding {
		t.Fatalf("plugin binding table id = %d, want %d", pluginBindingTable.Schema().ID, TableIDPluginBinding)
	}
	if len(pluginBindingTable.Schema().Indexes) != 1 || pluginBindingTable.Schema().Indexes[0].Name != "idx_plugin_binding_plugin_no" {
		t.Fatalf("plugin binding indexes = %#v", pluginBindingTable.Schema().Indexes)
	}
}

func TestPluginBindingScanSkipsStaleIndex(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	ctx := context.Background()
	shard := store.db.HashSlot(22)

	if err := shard.BindPluginUser(ctx, PluginUserBinding{UID: "u1", PluginNo: "p1", CreatedAtMS: 1, UpdatedAtMS: 1}); err != nil {
		t.Fatalf("BindPluginUser: %v", err)
	}
	staleKey := encodePluginBindingPluginIndexKey(22, "stale", "u1")
	batch := store.engine.NewBatch()
	if err := batch.Set(staleKey, nil); err != nil {
		t.Fatalf("set stale index: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		t.Fatalf("commit stale index: %v", err)
	}
	batch.Close()

	rows, _, done, err := shard.ScanPluginBindingsByPluginNo(ctx, "stale", PluginUserBindingCursor{}, 10)
	if err != nil || !done || len(rows) != 0 {
		t.Fatalf("stale scan rows=%#v done=%v err=%v", rows, done, err)
	}
}

func equalPluginBindings(a, b []PluginUserBinding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
