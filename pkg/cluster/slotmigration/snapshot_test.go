package slotmigration

import (
	"context"
	"path/filepath"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

func openTestDB(tb testing.TB) *metadb.DB {
	tb.Helper()

	db, err := metadb.Open(filepath.Join(tb.TempDir(), "db"))
	if err != nil {
		tb.Fatalf("Open() error = %v", err)
	}
	tb.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestExportImportSingleHashSlot(t *testing.T) {
	ctx := context.Background()
	source := openTestDB(t)

	requireNoError := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}

	requireNoError(source.ForHashSlot(5).UpsertChannel(ctx, metadb.Channel{
		ChannelID:   "ch1",
		ChannelType: 1,
	}))
	requireNoError(source.ForHashSlot(5).UpsertUser(ctx, metadb.User{
		UID:   "u1",
		Token: "t1",
	}))

	snap, err := ExportHashSlot(source, 5)
	if err != nil {
		t.Fatalf("ExportHashSlot() error = %v", err)
	}
	if snap.Stats.EntryCount == 0 {
		t.Fatal("ExportHashSlot() returned empty snapshot")
	}

	target := openTestDB(t)
	if err := ImportHashSlot(target, snap); err != nil {
		t.Fatalf("ImportHashSlot() error = %v", err)
	}

	ch, err := target.ForHashSlot(5).GetChannel(ctx, "ch1", 1)
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if ch.ChannelID != "ch1" {
		t.Fatalf("GetChannel().ChannelID = %q, want ch1", ch.ChannelID)
	}

	user, err := target.ForHashSlot(5).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if user.Token != "t1" {
		t.Fatalf("GetUser().Token = %q, want t1", user.Token)
	}
}
