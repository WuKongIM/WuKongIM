package message

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestNormalizeBackupChannelCutsSortsWithoutMutatingAndRejectsAmbiguity(t *testing.T) {
	original := []BackupChannelCut{
		{Key: "z:1", ID: ChannelID{ID: "z", Type: 1}, Checkpoint: Checkpoint{Epoch: 2, HW: 4}},
		{Key: "a:1", ID: ChannelID{ID: "a", Type: 1}, Checkpoint: Checkpoint{Epoch: 1, HW: 2}},
	}
	normalized, err := normalizeBackupChannelCuts(original)
	if err != nil {
		t.Fatalf("normalizeBackupChannelCuts(): %v", err)
	}
	if normalized[0].Key != "a:1" || normalized[1].Key != "z:1" {
		t.Fatalf("normalized cuts = %+v", normalized)
	}
	if original[0].Key != "z:1" || original[1].Key != "a:1" {
		t.Fatalf("normalization mutated caller order: %+v", original)
	}

	tests := []struct {
		name string
		cuts []BackupChannelCut
		want error
	}{
		{name: "missing key", cuts: []BackupChannelCut{{ID: ChannelID{ID: "room", Type: 1}}}, want: dberrors.ErrInvalidArgument},
		{name: "missing id", cuts: []BackupChannelCut{{Key: "room:1"}}, want: dberrors.ErrInvalidArgument},
		{name: "invalid checkpoint", cuts: []BackupChannelCut{{Key: "room:1", ID: ChannelID{ID: "room", Type: 1}, Checkpoint: Checkpoint{LogStartOffset: 2, HW: 1}}}, want: dberrors.ErrCorruptState},
		{name: "duplicate key", cuts: []BackupChannelCut{
			{Key: "room:1", ID: ChannelID{ID: "room", Type: 1}},
			{Key: "room:1", ID: ChannelID{ID: "room", Type: 1}},
		}, want: dberrors.ErrInvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizeBackupChannelCuts(test.cuts); !errors.Is(err, test.want) {
				t.Fatalf("normalizeBackupChannelCuts() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestImportBackupSnapshotReaderRejectsTargetConflictsBeforeReplacingData(t *testing.T) {
	ctx := context.Background()
	source := openTestMessageStore(t)
	defer source.close(t)
	sourceLog := mustAcquireChannel(t, source.db, "import-conflict:1", ChannelID{ID: "import-conflict", Type: 1})
	defer sourceLog.Close()
	if _, err := sourceLog.Append(ctx, []Record{{ID: 101, Payload: []byte("source")}}, AppendOptions{}); err != nil {
		t.Fatalf("source Append(): %v", err)
	}
	if err := sourceLog.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 1}); err != nil {
		t.Fatalf("source StoreCheckpoint(): %v", err)
	}
	body := readBackupSnapshot(t, source.db, BackupSnapshotRequest{
		HashSlot: 11,
		Channels: []BackupChannelCut{{
			Key: "import-conflict:1", ID: ChannelID{ID: "import-conflict", Type: 1},
			Checkpoint: Checkpoint{Epoch: 1, HW: 1},
		}},
	})

	t.Run("catalog identity", func(t *testing.T) {
		target := openTestMessageStore(t)
		defer target.close(t)
		existing := mustAcquireChannel(t, target.db, "import-conflict:1", ChannelID{ID: "other", Type: 1})
		defer existing.Close()
		if _, err := existing.Append(ctx, []Record{{ID: 201, Payload: []byte("target")}}, AppendOptions{}); err != nil {
			t.Fatalf("target Append(): %v", err)
		}
		if _, err := target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body))); !errors.Is(err, dberrors.ErrConflict) {
			t.Fatalf("ImportBackupSnapshotReader() error = %v, want conflict", err)
		}
		messages, err := existing.Read(ctx, 1, ReadOptions{})
		if err != nil || len(messages) != 1 || messages[0].MessageID != 201 {
			t.Fatalf("existing data after rejected import = (%+v, %v)", messages, err)
		}
	})

	t.Run("checkpoint revision", func(t *testing.T) {
		target := openTestMessageStore(t)
		defer target.close(t)
		existing := mustAcquireChannel(t, target.db, "import-conflict:1", ChannelID{ID: "import-conflict", Type: 1})
		defer existing.Close()
		if _, err := existing.Append(ctx, []Record{{ID: 301, Payload: []byte("target")}}, AppendOptions{}); err != nil {
			t.Fatalf("target Append(): %v", err)
		}
		if err := existing.StoreCheckpoint(ctx, Checkpoint{Epoch: 2, HW: 1}); err != nil {
			t.Fatalf("target StoreCheckpoint(): %v", err)
		}
		if _, err := target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body))); !errors.Is(err, dberrors.ErrConflict) {
			t.Fatalf("ImportBackupSnapshotReader() error = %v, want conflict", err)
		}
		checkpoint, ok, err := existing.LoadCheckpoint(ctx)
		if err != nil || !ok || checkpoint != (Checkpoint{Epoch: 2, HW: 1}) {
			t.Fatalf("checkpoint after rejected import = (%+v, %v, %v)", checkpoint, ok, err)
		}
	})

	t.Run("canceled preflight", func(t *testing.T) {
		target := openTestMessageStore(t)
		defer target.close(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := target.db.ImportBackupSnapshotReader(canceled, bytes.NewReader(body), int64(len(body))); !errors.Is(err, context.Canceled) {
			t.Fatalf("ImportBackupSnapshotReader() error = %v, want canceled", err)
		}
		entries, err := target.db.ListChannels(ctx)
		if err != nil || len(entries) != 0 {
			t.Fatalf("target catalog after canceled import = (%+v, %v)", entries, err)
		}
	})
}
