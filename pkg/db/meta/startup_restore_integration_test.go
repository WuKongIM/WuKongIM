//go:build integration

package meta

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
)

// Failure matrix: reject corrupt input before deleting; fence an interrupted
// install; retry from the immutable snapshot; publish its exact watermark;
// preserve unowned hash slots. This tests persistence, not timing.
func TestStartupRestoreRejectsCorruptionAndRetriesInterruptedInstall(t *testing.T) {
	source := openTestMetaStore(t)
	defer source.close(t)
	target := openTestMetaStore(t)
	defer target.close(t)
	ctx := context.Background()
	batch := source.db.NewBatch()
	for i := 0; i < 70000; i++ {
		if err := batch.UpsertUser(5, User{UID: fmt.Sprintf("u%06d", i)}); err != nil {
			t.Fatal(err)
		}
		if (i+1)%1000 == 0 {
			if err := batch.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			batch = source.db.NewBatch()
		}
	}
	if err := batch.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	snap, err := source.db.ExportHashSlotSnapshot(ctx, []uint16{5})
	if err != nil {
		t.Fatal(err)
	}
	for _, hs := range []HashSlot{5, 6} {
		if err := target.db.HashSlot(hs).UpsertUser(ctx, User{UID: "sentinel"}); err != nil {
			t.Fatal(err)
		}
	}
	corrupt := append([]byte(nil), snap.Data...)
	corrupt[len(corrupt)-1] ^= 1
	if err := target.db.RestoreStartupSnapshot(ctx, 1, 9, []uint16{5}, bytes.NewReader(corrupt), int64(len(corrupt)), nil); err == nil {
		t.Fatal("corrupt snapshot accepted")
	}
	if _, ok, _ := target.db.HashSlot(5).GetUser(ctx, "sentinel"); !ok {
		t.Fatal("corruption deleted existing state")
	}
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	err = target.db.RestoreStartupSnapshot(canceled, 1, 9, []uint16{5}, bytes.NewReader(snap.Data), int64(len(snap.Data)), func(p SnapshotRestoreProgress) {
		if p.Stage == "snapshot_install" && p.Entries > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interruption = %v", err)
	}
	if _, err := target.db.SlotAppliedIndex(ctx, 1); !errors.Is(err, ErrRestoreIncomplete) {
		t.Fatalf("partial state accepted: %v", err)
	}
	if err := target.db.RestoreStartupSnapshot(ctx, 1, 9, []uint16{5}, bytes.NewReader(snap.Data), int64(len(snap.Data)), nil); err != nil {
		t.Fatal(err)
	}
	if index, err := target.db.SlotAppliedIndex(ctx, 1); err != nil || index != 9 {
		t.Fatalf("watermark=%d err=%v", index, err)
	}
	if _, ok, _ := target.db.HashSlot(5).GetUser(ctx, "sentinel"); ok {
		t.Fatal("old row survived")
	}
	if _, ok, _ := target.db.HashSlot(6).GetUser(ctx, "sentinel"); !ok {
		t.Fatal("unowned row deleted")
	}
	for _, i := range []int{0, 65535, 69999} {
		if _, ok, e := target.db.HashSlot(5).GetUser(ctx, fmt.Sprintf("u%06d", i)); e != nil || !ok {
			t.Fatalf("missing %d: %v", i, e)
		}
	}
}
