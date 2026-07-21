package message

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestOpenBackupSnapshotPinsCommittedChannelCuts(t *testing.T) {
	ctx := context.Background()
	source := openTestMessageStore(t)
	defer source.close(t)
	alpha := mustAcquireChannel(t, source.db, ChannelKey("1:alpha"), ChannelID{ID: "alpha", Type: 1})
	defer alpha.Close()
	beta := mustAcquireChannel(t, source.db, ChannelKey("1:beta"), ChannelID{ID: "beta", Type: 1})
	defer beta.Close()

	if _, err := alpha.Append(ctx, []Record{
		{ID: 101, FromUID: "u1", ClientMsgNo: "c1", Payload: []byte("alpha-1")},
		{ID: 102, FromUID: "u1", ClientMsgNo: "c2", Payload: []byte("alpha-uncommitted")},
	}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("append alpha: %v", err)
	}
	if _, err := beta.Append(ctx, []Record{{ID: 201, Payload: []byte("beta")}}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("append beta: %v", err)
	}
	if err := alpha.StoreCheckpoint(ctx, Checkpoint{Epoch: 3, HW: 1}); err != nil {
		t.Fatalf("store checkpoint: %v", err)
	}
	reader, err := source.db.OpenBackupSnapshot(ctx, BackupSnapshotRequest{
		HashSlot: 7,
		Channels: []BackupChannelCut{{
			Key:        ChannelKey("1:alpha"),
			ID:         ChannelID{ID: "alpha", Type: 1},
			Checkpoint: Checkpoint{Epoch: 3, HW: 1},
		}},
	})
	if err != nil {
		t.Fatalf("OpenBackupSnapshot(): %v", err)
	}

	if _, err := alpha.Append(ctx, []Record{{ID: 103, Payload: []byte("alpha-after-open")}}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("append after open: %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(snapshot): %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close(snapshot): %v", err)
	}

	target := openTestMessageStore(t)
	defer target.close(t)
	stats, err := target.db.ImportBackupSnapshot(ctx, body)
	if err != nil {
		t.Fatalf("ImportBackupSnapshot(): %v", err)
	}
	if stats.HashSlot != 7 || stats.ChannelCount != 1 || stats.MessageCount != 1 {
		t.Fatalf("stats = %+v, want slot=7 channels=1 messages=1", stats)
	}

	restoredAlpha := mustAcquireChannel(t, target.db, ChannelKey("1:alpha"), ChannelID{ID: "alpha", Type: 1})
	defer restoredAlpha.Close()
	messages, err := restoredAlpha.Read(ctx, 1, ReadOptions{})
	if err != nil {
		t.Fatalf("read restored alpha: %v", err)
	}
	if len(messages) != 1 || messages[0].MessageID != 101 || string(messages[0].Payload) != "alpha-1" {
		t.Fatalf("restored messages = %+v", messages)
	}
	checkpoint, ok, err := restoredAlpha.LoadCheckpoint(ctx)
	if err != nil || !ok || checkpoint != (Checkpoint{Epoch: 3, HW: 1}) {
		t.Fatalf("restored checkpoint = %+v ok=%v err=%v", checkpoint, ok, err)
	}
	if _, ok, err := restoredAlpha.LookupIdempotency(ctx, IdempotencyKey{FromUID: "u1", ClientMsgNo: "c1"}); err != nil || !ok {
		t.Fatalf("restored idempotency ok=%v err=%v", ok, err)
	}

	entries, err := target.db.ListChannels(ctx)
	if err != nil {
		t.Fatalf("ListChannels(): %v", err)
	}
	if len(entries) != 1 || entries[0].Key != ChannelKey("1:alpha") {
		t.Fatalf("restored catalog = %+v, want alpha only", entries)
	}
}

func TestOpenBackupSnapshotRejectsCutAbovePinnedLEO(t *testing.T) {
	ctx := context.Background()
	store := openTestMessageStore(t)
	defer store.close(t)
	log := mustAcquireChannel(t, store.db, ChannelKey("1:alpha"), ChannelID{ID: "alpha", Type: 1})
	defer log.Close()
	if _, err := log.Append(ctx, []Record{{ID: 1, Payload: []byte("one")}}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("Append(): %v", err)
	}
	reader, err := store.db.OpenBackupSnapshot(ctx, BackupSnapshotRequest{
		HashSlot: 1,
		Channels: []BackupChannelCut{{
			Key:        ChannelKey("1:alpha"),
			ID:         ChannelID{ID: "alpha", Type: 1},
			Checkpoint: Checkpoint{Epoch: 1, HW: 2},
		}},
	})
	if err != nil {
		t.Fatalf("OpenBackupSnapshot(): %v", err)
	}
	defer reader.Close()
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("ReadAll() error = nil, want cut above pinned LEO rejection")
	}
}

func TestBackupSnapshotAppliesCommittedDeltaAfterExactBase(t *testing.T) {
	ctx := context.Background()
	source := openTestMessageStore(t)
	defer source.close(t)
	log := mustAcquireChannel(t, source.db, ChannelKey("1:alpha"), ChannelID{ID: "alpha", Type: 1})
	defer log.Close()
	if _, err := log.Append(ctx, []Record{{ID: 1, Payload: []byte("one")}}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("append base: %v", err)
	}
	if err := log.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 1}); err != nil {
		t.Fatalf("store base checkpoint: %v", err)
	}
	base := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 3, Channels: []BackupChannelCut{{
		Key: ChannelKey("1:alpha"), ID: ChannelID{ID: "alpha", Type: 1}, Checkpoint: Checkpoint{Epoch: 1, HW: 1},
	}}})
	if _, err := log.Append(ctx, []Record{{ID: 2, Payload: []byte("two")}}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("append delta: %v", err)
	}
	if err := log.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 2}); err != nil {
		t.Fatalf("store delta checkpoint: %v", err)
	}
	delta := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 3, Channels: []BackupChannelCut{{
		Key: ChannelKey("1:alpha"), ID: ChannelID{ID: "alpha", Type: 1}, FromExclusive: 1, Checkpoint: Checkpoint{Epoch: 1, HW: 2},
	}}})

	target := openTestMessageStore(t)
	defer target.close(t)
	if stats, err := target.db.ImportBackupSnapshot(ctx, base); err != nil || stats.MessageCount != 1 {
		t.Fatalf("import base stats=%+v err=%v", stats, err)
	}
	if stats, err := target.db.ImportBackupSnapshot(ctx, delta); err != nil || stats.MessageCount != 1 {
		t.Fatalf("import delta stats=%+v err=%v", stats, err)
	}
	restored := mustAcquireChannel(t, target.db, ChannelKey("1:alpha"), ChannelID{ID: "alpha", Type: 1})
	defer restored.Close()
	messages, err := restored.Read(ctx, 1, ReadOptions{})
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if len(messages) != 2 || messages[0].MessageID != 1 || messages[1].MessageID != 2 {
		t.Fatalf("restored messages = %+v", messages)
	}
}

func TestImportBackupSnapshotRejectsDeltaWithoutExactBase(t *testing.T) {
	ctx := context.Background()
	source := openTestMessageStore(t)
	defer source.close(t)
	log := mustAcquireChannel(t, source.db, ChannelKey("1:alpha"), ChannelID{ID: "alpha", Type: 1})
	defer log.Close()
	if _, err := log.Append(ctx, []Record{{ID: 1}, {ID: 2}}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := log.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 2}); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	delta := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 3, Channels: []BackupChannelCut{{
		Key: ChannelKey("1:alpha"), ID: ChannelID{ID: "alpha", Type: 1}, FromExclusive: 1, Checkpoint: Checkpoint{Epoch: 1, HW: 2},
	}}})
	target := openTestMessageStore(t)
	defer target.close(t)
	if _, err := target.db.ImportBackupSnapshot(ctx, delta); err == nil {
		t.Fatal("ImportBackupSnapshot(delta without base) error = nil")
	}
}

func TestImportBackupSnapshotReaderAppliesBaseAndDelta(t *testing.T) {
	ctx := context.Background()
	source := openTestMessageStore(t)
	defer source.close(t)
	target := openTestMessageStore(t)
	defer target.close(t)
	id := ChannelID{ID: "stream-room", Type: 2}
	log := mustAcquireChannel(t, source.db, ChannelKey("stream-room-key"), id)
	defer log.Close()
	if _, err := log.Append(ctx, []Record{{ID: 1, Payload: []byte("one")}}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("Append(base): %v", err)
	}
	if err := log.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 1}); err != nil {
		t.Fatalf("StoreCheckpoint(base): %v", err)
	}
	base := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 7, Channels: []BackupChannelCut{{Key: "stream-room-key", ID: id, Checkpoint: Checkpoint{Epoch: 1, HW: 1}}}})
	if _, err := log.Append(ctx, []Record{{ID: 2, Payload: []byte("two")}}, AppendOptions{Mode: AppendStrict}); err != nil {
		t.Fatalf("Append(delta): %v", err)
	}
	if err := log.StoreCheckpoint(ctx, Checkpoint{Epoch: 1, HW: 2}); err != nil {
		t.Fatalf("StoreCheckpoint(delta): %v", err)
	}
	delta := readBackupSnapshot(t, source.db, BackupSnapshotRequest{HashSlot: 7, Channels: []BackupChannelCut{{Key: "stream-room-key", ID: id, FromExclusive: 1, Checkpoint: Checkpoint{Epoch: 1, HW: 2}}}})
	for _, body := range [][]byte{base, delta} {
		if _, err := target.db.ImportBackupSnapshotReader(ctx, bytes.NewReader(body), int64(len(body))); err != nil {
			t.Fatalf("ImportBackupSnapshotReader(): %v", err)
		}
	}
	restored := mustAcquireChannel(t, target.db, ChannelKey("stream-room-key"), id)
	defer restored.Close()
	messages, err := restored.Read(ctx, 1, ReadOptions{})
	if err != nil || len(messages) != 2 {
		t.Fatalf("ReadCommitted() messages=%#v err=%v", messages, err)
	}
}

func readBackupSnapshot(t *testing.T, db *MessageDB, request BackupSnapshotRequest) []byte {
	t.Helper()
	reader, err := db.OpenBackupSnapshot(context.Background(), request)
	if err != nil {
		t.Fatalf("OpenBackupSnapshot(): %v", err)
	}
	body, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		t.Fatalf("read backup snapshot: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close backup snapshot: %v", closeErr)
	}
	return body
}
