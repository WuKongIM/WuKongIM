//go:build integration

package meta

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A real abrupt subprocess exit models losing the process after a durable
// delete, an intermediate batch, or the final data batch before publication.
func TestStartupRestoreCrashRequiresCompleteReinstall(t *testing.T) {
	ctx := context.Background()
	source := openTestMetaStore(t)
	defer source.close(t)
	for base := 0; base < 3000; base += 100 {
		batch := source.db.NewBatch()
		for i := base; i < base+100; i++ {
			if err := batch.UpsertUser(5, User{UID: fmt.Sprintf("u%06d", i), Token: strings.Repeat("x", 5000)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := batch.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := source.db.ExportHashSlotSnapshot(ctx, []uint16{5})
	if err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(t.TempDir(), "snapshot.bin")
	if err := os.WriteFile(snapshotPath, snap.Data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"delete", "batch", "publication"} {
		t.Run(phase, func(t *testing.T) {
			targetPath := filepath.Join(t.TempDir(), "meta")
			command := exec.Command(os.Args[0], "-test.run=^TestStartupRestoreCrashHelper$")
			command.Env = append(os.Environ(), "WK_TEST_RESTORE_PHASE="+phase, "WK_TEST_RESTORE_PATH="+targetPath, "WK_TEST_RESTORE_SNAPSHOT="+snapshotPath)
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("child did not interrupt at %s: %v %s", phase, err, output)
			}
			target, err := Open(targetPath)
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if _, err := target.SlotAppliedIndex(ctx, 1); !errors.Is(err, ErrRestoreIncomplete) {
				t.Fatalf("partial checkpoint trusted: %v", err)
			}
			if err := target.MetaDB().RestoreStartupSnapshot(ctx, 1, 9, []uint16{5}, bytes.NewReader(snap.Data), int64(len(snap.Data)), nil); err != nil {
				t.Fatal(err)
			}
			got, err := target.MetaDB().ExportHashSlotSnapshot(ctx, []uint16{5})
			if err != nil {
				t.Fatal(err)
			}
			if sha256.Sum256(got.Data) != sha256.Sum256(snap.Data) {
				t.Fatal("reinstalled full-state digest differs")
			}
			if index, err := target.SlotAppliedIndex(ctx, 1); err != nil || index != 9 {
				t.Fatalf("completed watermark=%d err=%v", index, err)
			}
			t.Logf("phase=%s restored_entries=%d sha256=%x", phase, got.Stats.EntryCount, sha256.Sum256(got.Data))
		})
	}
}

func TestStartupRestoreCrashHelper(t *testing.T) {
	phase := os.Getenv("WK_TEST_RESTORE_PHASE")
	if phase == "" {
		t.Skip("subprocess only")
	}
	db, err := Open(os.Getenv("WK_TEST_RESTORE_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reader, err := os.Open(os.Getenv("WK_TEST_RESTORE_SNAPSHOT"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	stat, err := reader.Stat()
	if err != nil {
		t.Fatal(err)
	}
	err = db.MetaDB().RestoreStartupSnapshot(context.Background(), 1, 9, []uint16{5}, reader, stat.Size(), func(p SnapshotRestoreProgress) {
		if p.Stage != "snapshot_install" {
			return
		}
		if (phase == "delete" && p.Entries == 0) || (phase == "batch" && p.Entries > 0 && p.Entries < p.TotalEntries) || (phase == "publication" && p.Entries == p.TotalEntries) {
			os.Exit(73)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("requested crash boundary not reached")
}
